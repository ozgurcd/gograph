package search_test

import (
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

// buildUntestedGraph builds a minimal, controlled graph for Untested tests.
//
// Symbols:
//   - foo   (function, production) — called by bar (production), NO test edge  → UNTESTED
//   - bar   (function, production) — called by main (production), HAS test edge → tested
//   - baz   (function, production) — zero callers                              → orphan (not untested)
//   - qux   (function, production) — called only from test file                → excluded (test-only caller)
//   - main  (function, production) — always skipped by convention
//   - init  (function, production) — always skipped by convention
//   - setup (function, test file)  — skipped (test file symbol)
func buildUntestedGraph() *graph.Graph {
	return &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: "pkg::foo", Name: "foo", Kind: graph.KindFunction, PackageName: "pkg", File: "pkg/a.go", Line: 10},
			{ID: "pkg::bar", Name: "bar", Kind: graph.KindFunction, PackageName: "pkg", File: "pkg/a.go", Line: 20},
			{ID: "pkg::baz", Name: "baz", Kind: graph.KindFunction, PackageName: "pkg", File: "pkg/a.go", Line: 30},
			{ID: "pkg::qux", Name: "qux", Kind: graph.KindFunction, PackageName: "pkg", File: "pkg/a.go", Line: 40},
			{ID: "pkg::main", Name: "main", Kind: graph.KindFunction, PackageName: "pkg", File: "pkg/main.go", Line: 1},
			{ID: "pkg::init", Name: "init", Kind: graph.KindFunction, PackageName: "pkg", File: "pkg/a.go", Line: 5},
			{ID: "pkg::setup", Name: "setup", Kind: graph.KindFunction, PackageName: "pkg", File: "pkg/a_test.go", Line: 5},
		},
		Calls: []graph.CallEdge{
			// foo is called by bar (production file) — qualifies as "has caller"
			{File: "pkg/a.go", CalleeSymbolID: "pkg::foo"},
			// bar is called by main (production file)
			{File: "pkg/main.go", CalleeSymbolID: "pkg::bar"},
			// qux is called ONLY from a test file — should not count as a production caller
			{File: "pkg/a_test.go", CalleeSymbolID: "pkg::qux"},
			// baz has zero callers (orphan)
		},
		TestEdges: []graph.TestEdge{
			// bar has a test edge → it is "tested"
			{Target: "pkg::bar"},
		},
	}
}

// TestUntestedBasic verifies the core: foo is returned, bar is not.
func TestUntestedBasic(t *testing.T) {
	g := buildUntestedGraph()
	results := search.Untested(g)

	found := make(map[string]search.UntestedResult)
	for _, r := range results {
		found[r.Name] = r
	}

	// foo: production caller + no test edge → must appear
	if _, ok := found["foo"]; !ok {
		t.Error("expected 'foo' in Untested results (has production caller, no test edge)")
	}

	// bar: has a test edge → must NOT appear
	if _, ok := found["bar"]; ok {
		t.Error("'bar' should NOT be in Untested results (it has a test edge)")
	}
}

// TestUntestedExcludesOrphans verifies symbols with zero callers are not reported.
func TestUntestedExcludesOrphans(t *testing.T) {
	g := buildUntestedGraph()
	results := search.Untested(g)

	for _, r := range results {
		if r.Name == "baz" {
			t.Error("'baz' is an orphan (zero callers) and should NOT appear in Untested results")
		}
	}
}

// TestUntestedExcludesTestOnlyCallers verifies that symbols called only from test
// files are not counted as "having a production caller".
func TestUntestedExcludesTestOnlyCallers(t *testing.T) {
	g := buildUntestedGraph()
	results := search.Untested(g)

	for _, r := range results {
		if r.Name == "qux" {
			t.Errorf("'qux' has only test-file callers and should NOT appear in Untested results")
		}
	}
}

// TestUntestedExcludesConventionSymbols verifies main and init are never reported.
func TestUntestedExcludesConventionSymbols(t *testing.T) {
	g := buildUntestedGraph()
	results := search.Untested(g)

	for _, r := range results {
		if r.Name == "main" || r.Name == "init" {
			t.Errorf("'%s' is a convention entry point and should NOT appear in Untested results", r.Name)
		}
	}
}

// TestUntestedExcludesTestFileSymbols verifies symbols defined in *_test.go are excluded.
func TestUntestedExcludesTestFileSymbols(t *testing.T) {
	g := buildUntestedGraph()
	results := search.Untested(g)

	for _, r := range results {
		if r.Name == "setup" {
			t.Error("'setup' is in a test file and should NOT appear in Untested results")
		}
	}
}

// TestUntestedSortOrder verifies results are sorted by CallerCount descending.
func TestUntestedSortOrder(t *testing.T) {
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: "pkg::alpha", Name: "alpha", Kind: graph.KindFunction, PackageName: "pkg", File: "pkg/a.go"},
			{ID: "pkg::beta", Name: "beta", Kind: graph.KindFunction, PackageName: "pkg", File: "pkg/a.go"},
			{ID: "pkg::gamma", Name: "gamma", Kind: graph.KindFunction, PackageName: "pkg", File: "pkg/a.go"},
		},
		Calls: []graph.CallEdge{
			// beta has 3 callers
			{File: "pkg/a.go", CalleeSymbolID: "pkg::beta"},
			{File: "pkg/a.go", CalleeSymbolID: "pkg::beta"},
			{File: "pkg/a.go", CalleeSymbolID: "pkg::beta"},
			// gamma has 2 callers
			{File: "pkg/a.go", CalleeSymbolID: "pkg::gamma"},
			{File: "pkg/a.go", CalleeSymbolID: "pkg::gamma"},
			// alpha has 1 caller
			{File: "pkg/a.go", CalleeSymbolID: "pkg::alpha"},
		},
		TestEdges: nil, // none tested
	}

	results := search.Untested(g)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].Name != "beta" {
		t.Errorf("expected beta first (3 callers), got %s", results[0].Name)
	}
	if results[1].Name != "gamma" {
		t.Errorf("expected gamma second (2 callers), got %s", results[1].Name)
	}
	if results[2].Name != "alpha" {
		t.Errorf("expected alpha third (1 caller), got %s", results[2].Name)
	}
}

// TestUntestedCallerCountAccurate verifies the CallerCount field is correct.
func TestUntestedCallerCountAccurate(t *testing.T) {
	g := buildUntestedGraph()
	results := search.Untested(g)

	for _, r := range results {
		if r.Name == "foo" {
			if r.CallerCount != 1 {
				t.Errorf("expected foo.CallerCount=1, got %d", r.CallerCount)
			}
		}
	}
}

// TestUntestedEmptyGraph returns empty slice — no panic on empty graph.
func TestUntestedEmptyGraph(t *testing.T) {
	g := &graph.Graph{}
	results := search.Untested(g)
	if len(results) != 0 {
		t.Errorf("expected empty result for empty graph, got %d items", len(results))
	}
}

// TestUntestedShortNameFallback verifies the short-name test edge lookup works
// when TestEdge.Target is a plain name (not a fully-qualified ID).
func TestUntestedShortNameFallback(t *testing.T) {
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: "pkg::handler", Name: "handler", Kind: graph.KindFunction, PackageName: "pkg", File: "pkg/a.go"},
		},
		Calls: []graph.CallEdge{
			{File: "pkg/a.go", CalleeSymbolID: "pkg::handler"},
		},
		TestEdges: []graph.TestEdge{
			// Target is a short name, not a FQ ID — must still match.
			{Target: "handler"},
		},
	}

	results := search.Untested(g)
	for _, r := range results {
		if r.Name == "handler" {
			t.Error("'handler' has a short-name test edge and should NOT appear in Untested results")
		}
	}
}

func TestUntestedUsesTypedTestIdentityWithoutConflatingReceivers(t *testing.T) {
	const (
		testedID   = "example.com/pkg::(*OAuthClientAuthService).Authenticate"
		untestedID = "example.com/pkg::(*DCRService).Authenticate"
	)
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: testedID, Name: "Authenticate", Kind: graph.KindMethod, Receiver: "*OAuthClientAuthService", PackageName: "pkg", File: "oauth.go"},
			{ID: untestedID, Name: "Authenticate", Kind: graph.KindMethod, Receiver: "*DCRService", PackageName: "pkg", File: "dcr.go"},
		},
		Calls: []graph.CallEdge{
			{File: "caller.go", CalleeSymbolID: testedID},
			{File: "caller.go", CalleeSymbolID: untestedID},
		},
		TestEdges: []graph.TestEdge{{
			TestFunc:       "TestClientAuth",
			Target:         "auth.Authenticate",
			TargetSymbolID: testedID,
			Resolution:     graph.CallResolutionStatic,
		}},
	}

	results := search.Untested(g)
	if len(results) != 1 || results[0].File != "dcr.go" || results[0].TestResolution != "none" {
		t.Fatalf("typed test identity did not isolate the tested receiver: %#v", results)
	}
}

func TestUntestedDoesNotTreatAmbiguousParserNameAsExact(t *testing.T) {
	const (
		firstID  = "example.com/pkg::(*First).Authenticate"
		secondID = "example.com/pkg::(*Second).Authenticate"
		testID   = "example.com/pkg::TestClientAuth"
	)
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: firstID, Name: "Authenticate", Kind: graph.KindMethod, Receiver: "*First", PackageName: "pkg", File: "first.go"},
			{ID: secondID, Name: "Authenticate", Kind: graph.KindMethod, Receiver: "*Second", PackageName: "pkg", File: "second.go"},
			{ID: testID, Name: "TestClientAuth", Kind: graph.KindFunction, PackageName: "pkg", File: "auth_test.go"},
		},
		Calls: []graph.CallEdge{
			{File: "caller.go", CalleeSymbolID: firstID},
			{File: "caller.go", CalleeSymbolID: secondID},
		},
		TestEdges: []graph.TestEdge{{TestFunc: "TestClientAuth", Target: "Authenticate", File: "auth_test.go"}},
	}

	results := search.Untested(g)
	if len(results) != 2 {
		t.Fatalf("ambiguous parser target hid same-named symbols: %#v", results)
	}
	for _, result := range results {
		if result.TestResolution != "possible" || result.PossibleTestCount != 1 {
			t.Fatalf("ambiguous parser target = %#v, want one possible test", result)
		}
	}
}

func TestUntestedRetainsPossibleDispatchWithExplicitResolution(t *testing.T) {
	const targetID = "example.com/pkg::(*MemoryStore).Delete"
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{{ID: targetID, Name: "Delete", Kind: graph.KindMethod, Receiver: "*MemoryStore", PackageName: "pkg", File: "store.go"}},
		Calls:   []graph.CallEdge{{File: "caller.go", CalleeSymbolID: targetID}},
		TestEdges: []graph.TestEdge{
			{TestFunc: "TestDeleteA", Target: "store.Delete", TargetSymbolID: targetID, Resolution: graph.CallResolutionCHA},
			{TestFunc: "TestDeleteB", Target: "store.Delete", TargetSymbolID: targetID, Resolution: graph.CallResolutionCHA},
		},
	}

	results := search.Untested(g)
	if len(results) != 1 || results[0].TestResolution != "possible" || results[0].PossibleTestCount != 2 {
		t.Fatalf("possible dispatch must remain visible and qualified: %#v", results)
	}
}

func TestUntestedUsesTransitiveExactAttributionAndKeepsPossiblePaths(t *testing.T) {
	const (
		testID     = "example.com/pkg::TestRouter"
		routerID   = "example.com/pkg::Router"
		handlerID  = "example.com/pkg::HandleRevoke"
		possibleID = "example.com/pkg::PossibleHandler"
	)
	g := &graph.Graph{
		Build: &graph.BuildMetadata{Precision: graph.PrecisionPrecise, TestCallResolution: graph.TestCallResolutionTyped},
		Symbols: []graph.SymbolNode{
			{ID: testID, Name: "TestRouter", Kind: graph.KindFunction, PackageName: "pkg", File: "router_test.go"},
			{ID: routerID, Name: "Router", Kind: graph.KindFunction, PackageName: "pkg", File: "router.go"},
			{ID: handlerID, Name: "HandleRevoke", Kind: graph.KindFunction, PackageName: "pkg", File: "handler.go"},
			{ID: possibleID, Name: "PossibleHandler", Kind: graph.KindFunction, PackageName: "pkg", File: "possible.go"},
		},
		TestEdges: []graph.TestEdge{{TestFunc: "TestRouter", File: "router_test.go", TargetSymbolID: routerID, Resolution: graph.CallResolutionStatic}},
		Calls: []graph.CallEdge{
			{CallerSymbolID: routerID, CalleeSymbolID: handlerID, File: "router.go", Resolution: graph.CallResolutionStatic},
			{CallerSymbolID: routerID, CalleeSymbolID: possibleID, File: "router.go", Resolution: graph.CallResolutionCHA},
			{CallerSymbolID: "example.com/pkg::Production", CalleeSymbolID: handlerID, File: "production.go", Resolution: graph.CallResolutionStatic},
			{CallerSymbolID: "example.com/pkg::Production", CalleeSymbolID: possibleID, File: "production.go", Resolution: graph.CallResolutionStatic},
		},
	}
	results := search.Untested(g)
	if len(results) != 1 || results[0].StableID != possibleID || results[0].TestResolution != "possible" || results[0].PossibleTestCount != 1 {
		t.Fatalf("transitive untested results = %#v", results)
	}
}

func TestUntestedIncludesStableIdentity(t *testing.T) {
	results := search.Untested(buildUntestedGraph())
	for _, result := range results {
		if result.Name == "foo" && result.StableID != "pkg::foo" {
			t.Fatalf("foo stable ID = %q", result.StableID)
		}
	}
}
