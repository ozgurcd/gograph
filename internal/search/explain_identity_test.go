package search

import (
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestExplainReportsEveryAmbiguousIdentity(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example/b::Save", Name: "Save", Kind: graph.KindFunction, File: "b.go"},
		{ID: "example/a::Save", Name: "Save", Kind: graph.KindFunction, File: "a.go"},
	}}
	r := Explain(g, "Save")
	if r == nil || r.Status != "ambiguous" || r.Symbol != "" || len(r.Candidates) != 2 {
		t.Fatalf("ambiguous query silently selected a symbol: %+v", r)
	}
	if r.Candidates[0].StableID != "example/a::Save" {
		t.Fatalf("unordered candidates: %+v", r.Candidates)
	}
	r = Explain(g, "example/b::Save")
	if r == nil || r.Status != "ok" || r.Symbol != "example/b::Save" {
		t.Fatalf("qualified query failed: %+v", r)
	}
}

func TestExplainEvidenceDoesNotLeakAcrossNamesakes(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example/a::Save", Name: "Save", Kind: graph.KindFunction, PackageName: "a", File: "a/save.go", Line: 10, EndLine: 30},
		{ID: "example/b::Save", Name: "Save", Kind: graph.KindFunction, PackageName: "b", File: "b/save.go", Line: 10, EndLine: 30},
		{ID: "example/a::Write", Name: "Write", Kind: graph.KindFunction, PackageName: "a", File: "a/save.go", Line: 40, EndLine: 50},
		{ID: "example/b::Write", Name: "Write", Kind: graph.KindFunction, PackageName: "b", File: "b/save.go", Line: 40, EndLine: 50},
		{ID: "example/a::Run", Name: "Run", Kind: graph.KindFunction, PackageName: "a", File: "a/run.go"},
		{ID: "example/a::TestRun", Name: "TestRun", Kind: graph.KindFunction, PackageName: "a", File: "a/run_test.go"},
	}, Calls: []graph.CallEdge{
		{CallerSymbolID: "example/a::Save", CallerName: "Save", CalleeSymbolID: "example/a::Write", CalleeRaw: "Write", File: "a/save.go", Line: 12},
		{CallerSymbolID: "example/b::Save", CallerName: "Save", CalleeSymbolID: "example/b::Write", CalleeRaw: "Write", File: "b/save.go", Line: 12},
		{CallerSymbolID: "example/a::Run", CallerName: "Run", CalleeSymbolID: "example/a::Save", CalleeRaw: "Save", File: "a/run.go", Line: 1},
		{CallerSymbolID: "example/a::TestRun", CallerName: "Run", CalleeSymbolID: "example/a::Save", CalleeRaw: "Save", File: "a/run_test.go", Line: 1},
		// A conflicting resolved target must never use the raw fallback.
		{CallerSymbolID: "example/a::Run", CallerName: "Run", CalleeSymbolID: "example/b::Save", CalleeRaw: "example/a::Save", File: "a/run.go", Line: 2},
	}, SQLs: []graph.SQLEdge{
		{Function: "Save", File: "a/save.go", Line: 11},
		{Function: "Save", File: "b/save.go", Line: 11},
		{Function: "Write", File: "a/save.go", Line: 41},
		{Function: "Write", File: "b/save.go", Line: 41},
		{Function: "Save", File: "a/save.go", Line: 80},
	}, EnvReads: []graph.EnvRead{
		{Function: "Save", File: "a/save.go", Line: 13, Key: "A"},
		{Function: "Save", File: "b/save.go", Line: 13, Key: "B"},
	}, Routes: []graph.HTTPRoute{
		{Handler: "Save", File: "a/routes.go", Method: "GET", Path: "/a"},
		{Handler: "Save", File: "b/routes.go", Method: "GET", Path: "/b"},
		{Handler: "Save", File: "a/routes.go", Method: "GET", Path: "/factory", DynamicHandler: true},
	}, Concurrency: []graph.ConcurrencyNode{
		{Function: "Save", File: "a/save.go", Line: 14, Kind: "go"},
		{Function: "Save", File: "b/save.go", Line: 14, Kind: "channel"},
	}, TestEdges: []graph.TestEdge{
		{TestFunc: "TestRun", Target: "Save", TargetSymbolID: "example/a::Save", File: "a/run_test.go", Line: 1},
		{TestFunc: "TestRun", Target: "Save", TargetSymbolID: "example/a::Save", File: "a/run_test.go", Line: 2},
		{TestFunc: "TestRun", Target: "Save", TargetSymbolID: "example/b::Save", File: "b/run_test.go", Line: 1},
	}}
	r := Explain(g, "example/a::Save")
	if r.SQLDirect != 1 || r.SQLCallees != 1 || len(r.EnvKeys) != 1 || r.EnvKeys[0] != "A" || len(r.ConcurrencyKinds) != 1 || r.ConcurrencyKinds[0] != "go" {
		t.Fatalf("namesake evidence leaked: %+v", r)
	}
	if len(r.Routes) != 1 || r.Routes[0] != "GET /a" || r.DirectTestCount != 1 || r.CallerCount != 2 || len(r.ProdCallers) != 1 || len(r.TestCallers) != 1 {
		t.Fatalf("route/test/caller identity lost: %+v", r)
	}
}

func TestExplainTargetUsesImportAliasAndRejectsAmbiguity(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example/a::Save", Name: "Save", PackageName: "a", Kind: graph.KindFunction, File: "a/save.go"},
		{ID: "example/b::Save", Name: "Save", PackageName: "b", Kind: graph.KindFunction, File: "b/save.go"},
	}, Imports: []graph.ImportEdge{{FromFile: "router/main.go", ImportPath: "example/a", Alias: "storage"}}}
	if s := explanationTarget(g, "", "storage.Save", "router/main.go"); s == nil || s.ID != "example/a::Save" {
		t.Fatalf("import alias lost: %+v", s)
	}
	for _, raw := range []string{"Save", "unknown.Save"} {
		if s := explanationTarget(g, "", raw, "router/main.go"); s != nil {
			t.Fatalf("unscoped %q resolved to %+v", raw, s)
		}
	}
	if s := explanationTarget(g, "missing::Save", "storage.Save", "router/main.go"); s != nil {
		t.Fatalf("resolved identity fell back to raw spelling: %+v", s)
	}
}

func TestExplainFactMatchesReceiverRangeAndIdentifierCase(t *testing.T) {
	symbol := &graph.SymbolNode{ID: "example/p::(*Store).Save", Name: "Save", Receiver: "*Store", PackageName: "p", File: "store.go", Line: 10, EndLine: 20}
	for _, spelling := range []string{"Save", "Store.Save", "(*Store).Save", "p.Store.Save", symbol.ID} {
		if !explanationFactMatches(spelling, "store.go", 12, symbol) {
			t.Errorf("valid method fact %q rejected", spelling)
		}
	}
	for _, spelling := range []string{"save", "Other.Save", "(*store).Save", "other/p::(*Store).Save"} {
		if explanationFactMatches(spelling, "store.go", 12, symbol) {
			t.Errorf("conflicting method fact %q accepted", spelling)
		}
	}
	if explanationFactMatches("Save", "store.go", 25, symbol) {
		t.Fatal("another declaration's fact accepted")
	}
}
