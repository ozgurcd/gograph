package search

import (
	"sort"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

// astCallGraph mirrors an AST build: no call edge carries a CalleeSymbolID.
func astCallGraph() *graph.Graph {
	return &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: "example.com/app/search::Callers", Kind: graph.KindFunction, Name: "Callers", PackageName: "search", File: "search/search.go", Line: 3},
			{ID: "example.com/app/search::CallersDepth", Kind: graph.KindFunction, Name: "CallersDepth", PackageName: "search", File: "search/search.go", Line: 9},
			{ID: "example.com/app/search::Untested", Kind: graph.KindFunction, Name: "Untested", PackageName: "search", File: "search/search.go", Line: 12},
			{ID: "example.com/app/search::(*Snapshot).Untested", Kind: graph.KindMethod, Name: "Untested", Receiver: "*Snapshot", PackageName: "search", File: "search/search.go", Line: 15},
			{ID: "example.com/app/search::(*Snapshot).UntestedWith", Kind: graph.KindMethod, Name: "UntestedWith", Receiver: "*Snapshot", PackageName: "search", File: "search/search.go", Line: 18},
			{ID: "example.com/app/search::Plan", Kind: graph.KindFunction, Name: "Plan", PackageName: "search", File: "search/plan.go", Line: 3},
			{ID: "example.com/app/cli::run", Kind: graph.KindFunction, Name: "run", PackageName: "cli", File: "cli/cli.go", Line: 3},
			{ID: "example.com/app/cli::alias", Kind: graph.KindFunction, Name: "alias", PackageName: "cli", File: "cli/alias.go", Line: 3},
			{ID: "example.com/app/other::Callers", Kind: graph.KindFunction, Name: "Callers", PackageName: "other", File: "other/other.go", Line: 3},
		},
		Imports: []graph.ImportEdge{
			{FromFile: "cli/cli.go", ImportPath: "example.com/app/search"},
			{FromFile: "cli/alias.go", ImportPath: "example.com/app/search", Alias: "s"},
			{FromFile: "cli/alias.go", ImportPath: "example.com/app/other"},
			{FromFile: "cli/cli_test.go", ImportPath: "example.com/app/search"},
		},
		Calls: []graph.CallEdge{
			{CallerSymbolID: "example.com/app/search::Plan", CallerName: "Plan", CalleeRaw: "Callers", File: "search/plan.go", Line: 4},
			{CallerSymbolID: "example.com/app/cli::run", CallerName: "run", CalleeRaw: "search.Callers", File: "cli/cli.go", Line: 4},
			{CallerSymbolID: "example.com/app/cli::run", CallerName: "run", CalleeRaw: "search.CallersDepth", File: "cli/cli.go", Line: 5},
			{CallerSymbolID: "example.com/app/cli::run", CallerName: "run", CalleeRaw: "search.Untested", File: "cli/cli.go", Line: 6},
			{CallerSymbolID: "example.com/app/cli::run", CallerName: "run", CalleeRaw: "snapshot.Untested", File: "cli/cli.go", Line: 7},
			{CallerSymbolID: "example.com/app/cli::run", CallerName: "run", CalleeRaw: "snapshot.UntestedWith", File: "cli/cli.go", Line: 8},
			{CallerSymbolID: "example.com/app/cli::alias", CallerName: "alias", CalleeRaw: "s.Callers", File: "cli/alias.go", Line: 4},
			{CallerSymbolID: "example.com/app/cli::alias", CallerName: "alias", CalleeRaw: "other.Callers", File: "cli/alias.go", Line: 5},
		},
		TestEdges: []graph.TestEdge{
			{TestFunc: "TestRun", Target: "search.Callers", File: "cli/cli_test.go", Line: 3},
			{TestFunc: "TestSnapshot", Target: "snapshot.Untested", File: "search/snapshot_test.go", Line: 3},
		},
	}
}

func callerSites(results []Result) []string {
	var sites []string
	for _, result := range results {
		sites = append(sites, result.Name+"@"+result.CallSiteFile+":"+string(rune('0'+result.CallSiteLine)))
	}
	sort.Strings(sites)
	return sites
}

func TestCallersExactSelectorsFindCallersInASTGraphs(t *testing.T) {
	g := astCallGraph()
	want := []string{"Plan@search/plan.go:4", "alias@cli/alias.go:4", "run@cli/cli.go:4"}
	for _, query := range []string{"search/search.go:Callers", "example.com/app/search::Callers", "search.Callers"} {
		got := callerSites(Callers(g, query, true, false))
		if len(got) != len(want) {
			t.Fatalf("%s: callers = %v, want %v", query, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: callers = %v, want %v", query, got, want)
			}
		}
	}
}

func TestCallersNeverMatchLongerNamesOrConflateMethods(t *testing.T) {
	g := astCallGraph()
	for query, want := range map[string][]string{
		"Snapshot.Untested":                  {"run@cli/cli.go:7"},
		"search/search.go:Snapshot.Untested": {"run@cli/cli.go:7"},
		"search.Untested":                    {"run@cli/cli.go:6"},
		"search/search.go:Untested":          {"run@cli/cli.go:6"},
		"other.Callers":                      {"alias@cli/alias.go:5"},
	} {
		got := callerSites(Callers(g, query, true, false))
		if len(got) != len(want) || got[0] != want[0] {
			t.Errorf("%s: callers = %v, want %v", query, got, want)
		}
	}
}

func TestRawCalleeMatchesWholeIdentifiers(t *testing.T) {
	for _, testCase := range []struct {
		raw, query string
		want       bool
	}{
		{"json.Marshal", "json.Marshal", true},
		{"json.Marshal", "Marshal", true},
		{"json.MarshalIndent", "json.Marshal", false},
		{"json.MarshalIndent", "Marshal", false},
		{"encoding/json.Marshal", "json.Marshal", true},
		{"x.json.Marshal", "json.Marshal", true},
		{"xjson.Marshal", "json.Marshal", false},
	} {
		if got := rawCalleeMatchesName(testCase.raw, testCase.query, false); got != testCase.want {
			t.Errorf("rawCalleeMatchesName(%q, %q) = %v", testCase.raw, testCase.query, got)
		}
	}
}

func TestTestsFileSelectorMatchesUnresolvedTestEdges(t *testing.T) {
	g := astCallGraph()
	for query, want := range map[string]string{
		"search/search.go:Callers":           "TestRun",
		"search/search.go:Snapshot.Untested": "TestSnapshot",
	} {
		results := Tests(g, query)
		if len(results) != 1 || results[0].Name != want {
			t.Errorf("Tests(%q) = %+v, want %s", query, results, want)
		}
	}
}

func TestCallersDepthFollowsUnresolvedEdgesFromExactSelectors(t *testing.T) {
	g := astCallGraph()
	g.Symbols = append(g.Symbols, graph.SymbolNode{ID: "example.com/app/search::Driver", Kind: graph.KindFunction, Name: "Driver", PackageName: "search", File: "search/driver.go", Line: 3})
	g.Calls = append(g.Calls, graph.CallEdge{CallerSymbolID: "example.com/app/search::Driver", CallerName: "Driver", CalleeRaw: "Plan", File: "search/driver.go", Line: 4})
	for _, query := range []string{"search/search.go:Callers", "search.Callers"} {
		names := map[string]bool{}
		for _, result := range CallersDepth(g, query, 2, true, false) {
			names[result.Name] = true
		}
		for _, want := range []string{"Plan", "run", "alias", "Driver"} {
			if !names[want] {
				t.Errorf("%s depth 2 lacks %s: %v", query, want, names)
			}
		}
		if len(names) != 4 {
			t.Errorf("%s depth 2 = %v", query, names)
		}
	}
}
