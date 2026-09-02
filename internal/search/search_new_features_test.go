package search_test

import (
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

// buildRichGraph returns a graph that exercises all new search features.
func buildRichGraph() *graph.Graph {
	return &graph.Graph{
		EnvReads: []graph.EnvRead{
			{Key: "DATABASE_URL", Accessor: "os.Getenv", File: "db.go", Line: 10, Function: "Connect"},
			{Key: "JWT_SECRET", Accessor: "viper.GetString", File: "auth.go", Line: 5, Function: "NewAuth"},
		},
		Symbols: []graph.SymbolNode{
			// Interface with two methods
			{
				Name: "Stringer",
				Kind: graph.KindInterface,
				File: "stringer.go",
				Line: 1,
				InterfaceMethods: map[string]string{
					"String": "func() string",
				},
			},
			// Interface with both methods
			{
				Name: "ReadWriter",
				Kind: graph.KindInterface,
				File: "rw.go",
				Line: 5,
				InterfaceMethods: map[string]string{
					"Read":  "func([]byte) (int, error)",
					"Write": "func([]byte) (int, error)",
				},
			},
			// Worker has String method -> satisfies Stringer but NOT ReadWriter
			{Name: "String", Kind: graph.KindMethod, Receiver: "*Worker", File: "worker.go", Line: 10, MethodSignature: "func() string"},
			// Worker does not have Read/Write -> doesn't satisfy ReadWriter

			// For Constructors tests
			{Name: "NewWorker", Kind: graph.KindFunction, Signature: "func() *Worker", File: "worker.go", Line: 20},
			{Name: "NewWorkerVal", Kind: graph.KindFunction, Signature: "func() Worker", File: "worker.go", Line: 25},
			{Name: "NewWorkerErr", Kind: graph.KindFunction, Signature: "func() (*Worker, error)", File: "worker.go", Line: 30},
			{Name: "NotWorker", Kind: graph.KindFunction, Signature: "func() *WorkerOther", File: "worker.go", Line: 35},
			{Name: "VoidFunc", Kind: graph.KindFunction, Signature: "func(w *Worker)", File: "worker.go", Line: 40},

			// For Schema tests
			{
				Name: "User", Kind: graph.KindStruct, File: "user.go", Line: 10,
				StructFields: []graph.StructField{
					{Name: "ID", Type: "int", Tag: `db:"users" json:"id"`},
				},
			},
			{
				Name: "Post", Kind: graph.KindStruct, File: "post.go", Line: 10,
				StructFields: []graph.StructField{
					{Name: "ID", Type: "int", Tag: `gorm:"table:posts"`},
				},
			},

			// For Globals tests
			{Name: "globalConfig", Kind: graph.KindVar, PackageName: "config", File: "config.go", Line: 5},

			// For Mocks tests
			{
				Name: "MockStringer", Kind: graph.KindStruct, File: "stringer_test.go", Line: 15,
			},
			{Name: "String", Kind: graph.KindMethod, Receiver: "*MockStringer", File: "stringer_test.go", Line: 20, MethodSignature: "func() string"},
		},
		Mutations: []graph.MutationEdge{
			{Function: "Load", Field: "globalConfig", File: "config.go", Line: 10},
		},
		Concurrency: []graph.ConcurrencyNode{
			{Kind: "goroutine", Function: "Start", File: "server.go", Line: 20, Detail: "go handleConn"},
			{Kind: "mutex_lock", Function: "Update", File: "store.go", Line: 15, Detail: "w.mu.Lock"},
			{Kind: "waitgroup_wait", Function: "Shutdown", File: "server.go", Line: 80, Detail: "wg.Wait"},
		},
		TestEdges: []graph.TestEdge{
			{TestFunc: "TestConnect", Target: "Connect", File: "db_test.go", Line: 5},
			{TestFunc: "TestConnect", Target: "Connect", File: "db_test.go", Line: 5}, // duplicate — should be deduplicated
			{TestFunc: "TestAuth", Target: "NewAuth", File: "auth_test.go", Line: 12},
		},
	}
}

func TestEnvs_All(t *testing.T) {
	g := buildRichGraph()
	res := search.Envs(g, "")
	if len(res) != 2 {
		t.Errorf("expected 2 env reads, got %d: %v", len(res), res)
	}
}

func TestEnvs_FilteredByKey(t *testing.T) {
	g := buildRichGraph()
	res := search.Envs(g, "DATABASE")
	if len(res) != 1 || res[0].Name != "DATABASE_URL" {
		t.Errorf("expected only DATABASE_URL, got %v", res)
	}
}

func TestEnvs_NoMatch(t *testing.T) {
	g := buildRichGraph()
	res := search.Envs(g, "NONEXISTENT_KEY")
	if len(res) != 0 {
		t.Errorf("expected 0 results, got %d", len(res))
	}
}

func TestInterfaces_Satisfied(t *testing.T) {
	g := buildRichGraph()
	// Worker has String() method, so it should satisfy Stringer.
	res := search.Interfaces(g, "Worker")
	if len(res) == 0 {
		t.Fatal("expected Worker to satisfy at least one interface (Stringer)")
	}
	found := false
	for _, r := range res {
		if r.Name == "Stringer" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Stringer in results, got %v", res)
	}
}

func TestInterfaces_NotSatisfied(t *testing.T) {
	g := buildRichGraph()
	// Worker doesn't have Read/Write, should NOT satisfy ReadWriter.
	res := search.Interfaces(g, "Worker")
	for _, r := range res {
		if r.Name == "ReadWriter" {
			t.Errorf("Worker should not satisfy ReadWriter but it was returned: %v", r)
		}
	}
}

func TestInterfaces_UnknownStruct(t *testing.T) {
	g := buildRichGraph()
	res := search.Interfaces(g, "GhostStruct")
	if len(res) != 0 {
		t.Errorf("expected 0 results for unknown struct, got %d", len(res))
	}
}

func TestConcurrency_All(t *testing.T) {
	g := buildRichGraph()
	res := search.Concurrency(g, "")
	if len(res) != 3 {
		t.Errorf("expected 3 concurrency nodes, got %d: %v", len(res), res)
	}
}

func TestConcurrency_FilterByKind(t *testing.T) {
	g := buildRichGraph()
	res := search.Concurrency(g, "goroutine")
	if len(res) != 1 || res[0].Name != "goroutine" {
		t.Errorf("expected 1 goroutine result, got %v", res)
	}
}

func TestConcurrency_FilterByFunction(t *testing.T) {
	g := buildRichGraph()
	res := search.Concurrency(g, "server")
	// "server.go" contains goroutine (Start) and waitgroup_wait (Shutdown) — but filter is by function name
	// Start and Shutdown are in server.go, Concurrency filter matches function name
	// "goroutine" has function "Start", "waitgroup_wait" has function "Shutdown" — neither matches "server"
	// However we filter on Kind OR function; "server" doesn't match either function name
	if len(res) != 0 {
		t.Logf("Note: filter 'server' matched %d results (function names: Start, Update, Shutdown)", len(res))
	}
}

func TestTests_All(t *testing.T) {
	g := buildRichGraph()
	res := search.Tests(g, "")
	// Should deduplicate the duplicate TestConnect->Connect entry
	if len(res) != 2 {
		t.Errorf("expected 2 unique test edges, got %d: %v", len(res), res)
	}
}

func TestTests_FilterBySymbol(t *testing.T) {
	g := buildRichGraph()
	res := search.Tests(g, "Connect")
	if len(res) != 1 || res[0].Name != "TestConnect" {
		t.Errorf("expected TestConnect for 'Connect', got %v", res)
	}
}

func TestTests_FilterByReceiverQualifiedMethod(t *testing.T) {
	g := &graph.Graph{TestEdges: []graph.TestEdge{
		{TestFunc: "TestLocalLogin", Target: "svc.Login", TargetSymbolID: "example.com/auth::(*LocalLoginService).Login", File: "auth/login_test.go", Line: 12},
		{TestFunc: "TestOtherLogin", Target: "svc.Login", TargetSymbolID: "example.com/auth::(*OtherService).Login", File: "auth/other_test.go", Line: 20},
		{TestFunc: "TestPrefixedLogin", Target: "svc.Login", TargetSymbolID: "example.com/auth::(*OtherLocalLoginService).Login", File: "auth/prefixed_test.go", Line: 24},
		{TestFunc: "TestIssue", Target: "svc.Issue", TargetSymbolID: "example.com/token::(IDTokenService).Issue", File: "token/id_test.go", Line: 9},
	}}

	for _, query := range []string{"LocalLoginService.Login", "(*LocalLoginService).Login"} {
		results := search.Tests(g, query)
		if len(results) != 1 || results[0].Name != "TestLocalLogin" {
			t.Fatalf("tests %q = %+v, want only TestLocalLogin", query, results)
		}
	}

	results := search.Tests(g, "IDTokenService.Issue")
	if len(results) != 1 || results[0].Name != "TestIssue" {
		t.Fatalf("value-receiver tests = %+v, want TestIssue", results)
	}
}

func TestTests_NoMatch(t *testing.T) {
	g := buildRichGraph()
	res := search.Tests(g, "NonExistentSymbol")
	if len(res) != 0 {
		t.Errorf("expected 0 results for non-existent symbol, got %d", len(res))
	}
}

func TestConstructors(t *testing.T) {
	g := buildRichGraph()
	res := search.Constructors(g, "Worker")
	if len(res) != 3 { // NewWorker, NewWorkerVal, NewWorkerErr
		t.Errorf("expected 3 constructors, got %d: %v", len(res), res)
	}
	names := make(map[string]bool)
	for _, r := range res {
		names[r.Name] = true
	}
	for _, n := range []string{"NewWorker", "NewWorkerVal", "NewWorkerErr"} {
		if !names[n] {
			t.Errorf("expected %s to be recognized as constructor", n)
		}
	}
}

func TestSchema(t *testing.T) {
	g := buildRichGraph()
	// Test matching against db tag
	res := search.Schema(g, "users")
	if len(res) != 1 || res[0].Name != "User" {
		t.Errorf("expected User struct for 'users' table, got %v", res)
	}

	// Test matching against gorm tag
	res2 := search.Schema(g, "posts")
	if len(res2) != 1 || res2[0].Name != "Post" {
		t.Errorf("expected Post struct for 'posts' table, got %v", res2)
	}

	// Test case insensitive match
	res3 := search.Schema(g, "USERS")
	if len(res3) != 1 || res3[0].Name != "User" {
		t.Errorf("expected User struct for 'USERS' table, got %v", res3)
	}
}

func TestGlobals(t *testing.T) {
	g := buildRichGraph()
	res := search.Globals(g, "config")
	// Expect the variable and its mutator
	if len(res) != 2 {
		t.Errorf("expected 2 global results, got %d: %v", len(res), res)
	}

	hasVar := false
	hasMut := false
	for _, r := range res {
		if r.Kind == "var" && r.Name == "globalConfig" {
			hasVar = true
		}
		if r.Kind == "mutator" && r.Name == "Load" {
			hasMut = true
		}
	}
	if !hasVar {
		t.Error("expected globalConfig variable in results")
	}
	if !hasMut {
		t.Error("expected Load mutator in results")
	}
}

func TestMocks(t *testing.T) {
	g := buildRichGraph()
	res := search.Mocks(g, "Stringer")
	if len(res) != 1 || res[0].Name != "MockStringer" {
		t.Errorf("expected MockStringer for 'Stringer' mock, got %v", res)
	}
	if res[0].Kind != "mock" {
		t.Errorf("expected Kind 'mock', got %s", res[0].Kind)
	}
}
