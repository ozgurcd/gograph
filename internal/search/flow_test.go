package search_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func securityFlowGraph(root string) *graph.Graph {
	return &graph.Graph{
		Root: root,
		FlowFunctions: []graph.FlowFunction{
			{
				ID: "example.com/app::Handle", Name: "Handle", File: "handler.go",
				Params: []graph.FlowParameter{{Name: "request", Type: "*http.Request"}},
				Facts: []graph.FlowFact{
					{Kind: "source", Target: "request", SourceKind: "http_request", Detail: "request", Line: 10},
					{Kind: "transfer", Target: "path", Inputs: []string{"request.URL.Path"}, Detail: "path := request.URL.Path", Line: 11},
					{Kind: "call", Target: "$call:12:2", Inputs: []string{"path"}, Arguments: [][]string{{"path"}}, Callee: "writeFile", Detail: "writeFile(path)", Line: 12, Column: 2},
				},
			},
			{
				ID: "example.com/app::writeFile", Name: "writeFile", File: "storage.go",
				Params: []graph.FlowParameter{{Name: "name", Type: "string"}},
				Facts: []graph.FlowFact{
					{Kind: "sink", Inputs: []string{"name"}, Callee: "os.WriteFile", SinkKind: "filesystem", Detail: "os.WriteFile(name, data, 0600)", Line: 20},
				},
			},
			{
				ID: "example.com/app::Run", Name: "Run", File: "run.go",
				Facts: []graph.FlowFact{
					{Kind: "source", Target: "command", SourceKind: "environment", Detail: "os.Getenv(RUN_COMMAND)", Line: 30},
					{Kind: "sink", Inputs: []string{"command"}, Callee: "exec.Command", SinkKind: "process_execution", Detail: "exec.Command(command)", Line: 31},
				},
			},
			{
				ID: "example.com/app::Query", Name: "Query", File: "query.go",
				Facts: []graph.FlowFact{
					{Kind: "source", Target: "payload", SourceKind: "decoded_json", Detail: "json.Unmarshal(body, &payload)", Line: 40},
					{Kind: "sink", Inputs: []string{"payload.SQL"}, Callee: "db.Query", SinkKind: "sql_query", Detail: "db.Query(payload.SQL)", Line: 41},
					{Kind: "sink", Callee: "db.Query", SinkKind: "sql_query", Detail: "db.Query(\"SELECT * FROM users WHERE id = ?\", payload.ID)", Line: 42},
				},
			},
			{
				ID: "example.com/app::TestDanger", Name: "TestDanger", File: "danger_test.go",
				Facts: []graph.FlowFact{
					{Kind: "source", Target: "target", SourceKind: "environment", Detail: "os.Getenv(TARGET)", Line: 5},
					{Kind: "sink", Inputs: []string{"target"}, Callee: "os.Remove", SinkKind: "filesystem", Detail: "os.Remove(target)", Line: 6},
				},
			},
		},
		Calls: []graph.CallEdge{{
			CallerSymbolID: "example.com/app::Handle", CallerName: "Handle",
			CalleeRaw: "writeFile", CalleeSymbolID: "example.com/app::writeFile",
			File: "handler.go", Line: 12,
		}},
	}
}

func TestFlowFindsDirectAndInterproceduralPaths(t *testing.T) {
	results, err := search.Flow(securityFlowGraph(t.TempDir()), search.FlowOptions{})
	if err != nil {
		t.Fatalf("Flow returned error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected three production findings, got %d: %+v", len(results), results)
	}

	bySink := make(map[string]search.FlowResult)
	for _, result := range results {
		bySink[result.Sink.Kind] = result
	}
	filesystem, ok := bySink["filesystem"]
	if !ok {
		t.Fatal("expected filesystem finding")
	}
	if filesystem.Source.Kind != "http_request" || filesystem.Source.Function != "Handle" || filesystem.Sink.Function != "writeFile" {
		t.Errorf("unexpected interprocedural path: %+v", filesystem)
	}
	if filesystem.Confidence != "medium" {
		t.Errorf("expected precise internal path to have medium confidence, got %s", filesystem.Confidence)
	}
	if bySink["process_execution"].Severity != "high" || bySink["sql_query"].Severity != "high" {
		t.Errorf("expected command and SQL findings to be high severity: %+v", bySink)
	}
	for _, result := range results {
		if result.Sink.Line == 42 {
			t.Error("parameterized SQL value argument must not taint the static query text")
		}
	}
}

func TestFlowFiltersAndTestInclusion(t *testing.T) {
	graph := securityFlowGraph(t.TempDir())
	results, err := search.Flow(graph, search.FlowOptions{Source: "environment", Sink: "filesystem", IncludeTests: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Sink.File != "danger_test.go" {
		t.Fatalf("expected only test filesystem path, got %+v", results)
	}

	results, err = search.Flow(graph, search.FlowOptions{Term: "storage.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Sink.Kind != "filesystem" {
		t.Fatalf("expected term-filtered filesystem result, got %+v", results)
	}
}

func TestFlowSanitizerIsSinkScoped(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".gograph"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := `{"sanitizers":[{"function":"security.Clean","for":["filesystem"]}]}`
	if err := os.WriteFile(filepath.Join(root, ".gograph", "flow.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	g := &graph.Graph{Root: root, FlowFunctions: []graph.FlowFunction{{
		ID: "example.com/app::Handle", Name: "Handle", File: "handler.go",
		Facts: []graph.FlowFact{
			{Kind: "source", Target: "request", SourceKind: "http_request", Detail: "request", Line: 1},
			{Kind: "call", Target: "$call:2:1", Inputs: []string{"request"}, Arguments: [][]string{{"request"}}, Callee: "security.Clean", Detail: "security.Clean(request)", Line: 2},
			{Kind: "transfer", Target: "clean", Inputs: []string{"$call:2:1"}, Detail: "clean := security.Clean(request)", Line: 2},
			{Kind: "sink", Inputs: []string{"clean"}, Callee: "os.WriteFile", SinkKind: "filesystem", Detail: "os.WriteFile(clean, data, 0600)", Line: 3},
			{Kind: "sink", Inputs: []string{"clean"}, Callee: "http.Get", SinkKind: "outbound_http", Detail: "http.Get(clean)", Line: 4},
		},
	}}}

	results, err := search.Flow(g, search.FlowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Sink.Kind != "outbound_http" {
		t.Fatalf("expected only unsanitized outbound HTTP path, got %+v", results)
	}
}

func TestFlowResolvesImportedFunctionsWithoutPreciseGraph(t *testing.T) {
	g := &graph.Graph{
		Root: t.TempDir(),
		Imports: []graph.ImportEdge{{
			FromFile: "api/handler.go", ImportPath: "example.com/app/storage", Alias: "storage",
		}},
		FlowFunctions: []graph.FlowFunction{
			{
				ID: "example.com/app/api::Handle", Name: "Handle", File: "api/handler.go",
				Facts: []graph.FlowFact{
					{Kind: "source", Target: "request", SourceKind: "http_request", Detail: "request", Line: 4},
					{Kind: "call", Target: "$call:5:1", Inputs: []string{"request.URL.Path"}, Arguments: [][]string{{"request.URL.Path"}}, Callee: "storage.Save", Detail: "storage.Save(request.URL.Path)", Line: 5},
				},
			},
			{
				ID: "example.com/app/storage::Save", Name: "Save", File: "storage/save.go",
				Params: []graph.FlowParameter{{Name: "path", Type: "string"}},
				Facts:  []graph.FlowFact{{Kind: "sink", Inputs: []string{"path"}, Callee: "os.WriteFile", SinkKind: "filesystem", Detail: "os.WriteFile(path, nil, 0600)", Line: 8}},
			},
			{
				ID: "example.com/app/other::Save", Name: "Save", File: "other/save.go",
				Params: []graph.FlowParameter{{Name: "path", Type: "string"}},
			},
		},
	}

	results, err := search.Flow(g, search.FlowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Sink.File != "storage/save.go" {
		t.Fatalf("expected imported storage.Save path, got %+v", results)
	}
	if results[0].Confidence != "medium" {
		t.Fatalf("expected import-resolved path to retain medium confidence, got %s", results[0].Confidence)
	}
}

func TestFlowLowersConfidenceAcrossUnresolvedExternalCall(t *testing.T) {
	g := &graph.Graph{Root: t.TempDir(), FlowFunctions: []graph.FlowFunction{{
		ID: "example.com/app::Handle", Name: "Handle", File: "handler.go",
		Facts: []graph.FlowFact{
			{Kind: "source", Target: "request", SourceKind: "http_request", Detail: "request", Line: 1},
			{Kind: "call", Target: "$call:2:1", Inputs: []string{"request.URL.Path"}, Arguments: [][]string{{"request.URL.Path"}}, Callee: "strings.TrimSpace", Detail: "strings.TrimSpace(request.URL.Path)", Line: 2},
			{Kind: "transfer", Target: "path", Inputs: []string{"$call:2:1"}, Detail: "path := strings.TrimSpace(request.URL.Path)", Line: 2},
			{Kind: "sink", Inputs: []string{"path"}, Callee: "os.ReadFile", SinkKind: "filesystem", Detail: "os.ReadFile(path)", Line: 3},
		},
	}}}

	results, err := search.Flow(g, search.FlowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Confidence != "low" {
		t.Fatalf("expected one low-confidence finding, got %+v", results)
	}
}

func TestFlowDoesNotResolveExternalCallToSameNamedLocalFunction(t *testing.T) {
	g := &graph.Graph{
		Root:    t.TempDir(),
		Imports: []graph.ImportEdge{{FromFile: "handler.go", ImportPath: "strings"}},
		FlowFunctions: []graph.FlowFunction{
			{
				ID: "example.com/app::Handle", Name: "Handle", File: "handler.go",
				Facts: []graph.FlowFact{
					{Kind: "source", Target: "request", SourceKind: "http_request", Detail: "request", Line: 1},
					{Kind: "call", Target: "$call:2:1", Inputs: []string{"request.URL.Path"}, Arguments: [][]string{{"request.URL.Path"}}, Callee: "strings.Fields", Detail: "strings.Fields(request.URL.Path)", Line: 2},
				},
			},
			{
				ID: "example.com/app::Fields", Name: "Fields", File: "fields.go",
				Params: []graph.FlowParameter{{Name: "path", Type: "string"}},
				Facts:  []graph.FlowFact{{Kind: "sink", Inputs: []string{"path"}, Callee: "os.ReadFile", SinkKind: "filesystem", Detail: "os.ReadFile(path)", Line: 8}},
			},
		},
	}

	results, err := search.Flow(g, search.FlowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("external strings.Fields must not enter local Fields: %+v", results)
	}
}

func TestFlowKeepsMultipleReturnValuesSeparate(t *testing.T) {
	g := &graph.Graph{Root: t.TempDir(), FlowFunctions: []graph.FlowFunction{
		{
			ID: "example.com/app::Handle", Name: "Handle", File: "handler.go",
			Facts: []graph.FlowFact{
				{Kind: "source", Target: "request", SourceKind: "http_request", Detail: "request", Line: 1},
				{Kind: "call", Target: "$call:2:1", Inputs: []string{"request.URL.Path"}, Arguments: [][]string{{"request.URL.Path"}}, Callee: "parse", Detail: "parse(request.URL.Path)", Line: 2},
				{Kind: "transfer", Target: "value", Inputs: []string{"$call:2:1:0"}, Detail: "value, err := parse(request.URL.Path)", Line: 2},
				{Kind: "transfer", Target: "err", Inputs: []string{"$call:2:1:1"}, Detail: "value, err := parse(request.URL.Path)", Line: 2},
				{Kind: "sink", Inputs: []string{"value"}, Callee: "os.ReadFile", SinkKind: "filesystem", Detail: "os.ReadFile(value)", Line: 3},
				{Kind: "sink", Inputs: []string{"err"}, Callee: "exec.Command", SinkKind: "process_execution", Detail: "exec.Command(err.Error())", Line: 4},
			},
		},
		{
			ID: "example.com/app::parse", Name: "parse", File: "parse.go",
			Params: []graph.FlowParameter{{Name: "path", Type: "string"}},
			Facts:  []graph.FlowFact{{Kind: "return", Target: "$return:1", Inputs: []string{"path"}, Detail: "return value, errorFor(path)", Line: 8}},
		},
	}}

	results, err := search.Flow(g, search.FlowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Sink.Kind != "process_execution" {
		t.Fatalf("expected taint only through the second return value, got %+v", results)
	}
}

func TestFlowDoesNotReturnTaintToUnrelatedCaller(t *testing.T) {
	g := &graph.Graph{
		Root: t.TempDir(),
		FlowFunctions: []graph.FlowFunction{
			{
				ID: "example.com/app::TaintedCaller", Name: "TaintedCaller", File: "tainted.go",
				Facts: []graph.FlowFact{
					{Kind: "source", Target: "request", SourceKind: "http_request", Detail: "request", Line: 1},
					{Kind: "call", Target: "$call:2:1", Inputs: []string{"request.URL.Path"}, Arguments: [][]string{{"request.URL.Path"}}, Callee: "identity", Detail: "identity(request.URL.Path)", Line: 2, Column: 1},
				},
			},
			{
				ID: "example.com/app::SafeCaller", Name: "SafeCaller", File: "safe.go",
				Facts: []graph.FlowFact{
					{Kind: "call", Target: "$call:4:1", Callee: "identity", Detail: "identity(\"safe.txt\")", Line: 4, Column: 1},
					{Kind: "transfer", Target: "path", Inputs: []string{"$call:4:1"}, Detail: "path := identity(\"safe.txt\")", Line: 4},
					{Kind: "sink", Inputs: []string{"path"}, Callee: "os.ReadFile", SinkKind: "filesystem", Detail: "os.ReadFile(path)", Line: 5},
				},
			},
			{
				ID: "example.com/app::identity", Name: "identity", File: "identity.go",
				Params: []graph.FlowParameter{{Name: "value", Type: "string"}},
				Facts:  []graph.FlowFact{{Kind: "return", Target: "$return:0", Inputs: []string{"value"}, Detail: "return value", Line: 8}},
			},
		},
		Calls: []graph.CallEdge{
			{CallerSymbolID: "example.com/app::TaintedCaller", CalleeRaw: "identity", CalleeSymbolID: "example.com/app::identity", File: "tainted.go", Line: 2},
			{CallerSymbolID: "example.com/app::SafeCaller", CalleeRaw: "identity", CalleeSymbolID: "example.com/app::identity", File: "safe.go", Line: 4},
		},
	}

	results, err := search.Flow(g, search.FlowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("taint from one identity call leaked into another caller: %+v", results)
	}
}

func TestFlowRejectsInvalidConfigurationAndFilters(t *testing.T) {
	g := securityFlowGraph(t.TempDir())
	if _, err := search.Flow(g, search.FlowOptions{ConfigPath: "../flow.json"}); err == nil || !strings.Contains(err.Error(), "path traversal") {
		t.Fatalf("expected path traversal error, got %v", err)
	}
	if _, err := search.Flow(g, search.FlowOptions{Source: "cookies"}); err == nil {
		t.Fatal("expected invalid source error")
	}
	if _, err := search.Flow(g, search.FlowOptions{Sink: "logs"}); err == nil {
		t.Fatal("expected invalid sink error")
	}
}

func TestFlowRejectsConfigSymlinkOutsideGraphRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".gograph"), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "flow.json")
	if err := os.WriteFile(outside, []byte(`{"sanitizers":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".gograph", "linked-flow.json")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	g := securityFlowGraph(root)
	if _, err := search.Flow(g, search.FlowOptions{ConfigPath: ".gograph/linked-flow.json"}); err == nil || !strings.Contains(err.Error(), "symlink target") {
		t.Fatalf("expected symlink containment error, got %v", err)
	}
}

func TestFlowValidatesSanitizerConfiguration(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".gograph"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".gograph", "policy.json")
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "invalid JSON", content: `{`, want: "invalid JSON"},
		{name: "missing function", content: `{"sanitizers":[{"for":["filesystem"]}]}`, want: "function is required"},
		{name: "invalid sink", content: `{"sanitizers":[{"function":"Clean","for":["logs"]}]}`, want: "unsupported sink"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := search.Flow(securityFlowGraph(root), search.FlowOptions{ConfigPath: ".gograph/policy.json"})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
		})
	}
}
