package search_test

import (
	"reflect"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func attributionGraph() *graph.Graph {
	return &graph.Graph{
		Build: &graph.BuildMetadata{Precision: graph.PrecisionPrecise, TestCallResolution: graph.TestCallResolutionTyped},
		Symbols: []graph.SymbolNode{
			{ID: "example.com/app/service::TestFlow", Kind: graph.KindFunction, Name: "TestFlow", PackageName: "service", File: "service/flow_test.go", Line: 10},
			{ID: "example.com/app/other::TestFlow", Kind: graph.KindFunction, Name: "TestFlow", PackageName: "other", File: "other/flow_test.go", Line: 7},
			{ID: "example.com/app/service::Start", Kind: graph.KindFunction, Name: "Start", PackageName: "service", File: "service/flow.go", Line: 10},
			{ID: "example.com/app/service::Exact", Kind: graph.KindFunction, Name: "Exact", PackageName: "service", File: "service/flow.go", Line: 20},
			{ID: "example.com/app/service::(*Impl).Run", Kind: graph.KindMethod, Name: "Run", Receiver: "*Impl", PackageName: "service", File: "service/impl.go", Line: 30},
			{ID: "example.com/app/service::Leaf", Kind: graph.KindFunction, Name: "Leaf", PackageName: "service", File: "service/leaf.go", Line: 40},
		},
		TestEdges: []graph.TestEdge{
			{TestFunc: "TestFlow", Target: "Start", TargetSymbolID: "example.com/app/service::Start", Resolution: graph.CallResolutionStatic, File: "service/flow_test.go", Line: 11},
		},
		Calls: []graph.CallEdge{
			{CallerSymbolID: "example.com/app/service::Start", CalleeSymbolID: "example.com/app/service::Exact", Resolution: graph.CallResolutionStatic},
			{CallerSymbolID: "example.com/app/service::Exact", CalleeSymbolID: "example.com/app/service::(*Impl).Run", Resolution: graph.CallResolutionCHA},
			{CallerSymbolID: "example.com/app/service::(*Impl).Run", CalleeSymbolID: "example.com/app/service::Leaf", Resolution: graph.CallResolutionStatic},
		},
	}
}

func TestIdentityResolvesStableIDAndReportsAmbiguity(t *testing.T) {
	g := attributionGraph()
	exact := search.Identity(g, "example.com/app/service::(*Impl).Run")
	if exact.Status != "exact" || len(exact.Matches) != 1 || exact.Matches[0].File != "service/impl.go" {
		t.Fatalf("exact identity = %#v", exact)
	}
	ambiguous := search.Identity(g, "TestFlow")
	if ambiguous.Status != "ambiguous" || len(ambiguous.Matches) != 2 {
		t.Fatalf("ambiguous identity = %#v", ambiguous)
	}
	missing := search.Identity(g, "example.com/app/service::Missing")
	if missing.Status != "not_found" || missing.Matches == nil {
		t.Fatalf("missing identity = %#v", missing)
	}
}

func TestPackageQualifierDisambiguatesCollidingExternalTestIdentity(t *testing.T) {
	g := attributionGraph()
	g.Symbols = append(g.Symbols,
		graph.SymbolNode{ID: "example.com/app/service::TestCollision", Kind: graph.KindFunction, Name: "TestCollision", PackageName: "service", File: "service/internal_test.go"},
		graph.SymbolNode{ID: "example.com/app/service::TestCollision", Kind: graph.KindFunction, Name: "TestCollision", PackageName: "service_test", File: "service/external_test.go"},
	)
	g.TestEdges = append(g.TestEdges,
		graph.TestEdge{TestFunc: "TestCollision", TargetSymbolID: "example.com/app/service::Start", Resolution: graph.CallResolutionStatic, File: "service/internal_test.go"},
		graph.TestEdge{TestFunc: "TestCollision", TargetSymbolID: "example.com/app/service::Exact", Resolution: graph.CallResolutionStatic, File: "service/external_test.go"},
	)
	if report := search.Identity(g, "example.com/app/service::TestCollision"); report.Status != "ambiguous" {
		t.Fatalf("unqualified identity = %#v", report)
	}
	identity := search.IdentityInPackage(g, "example.com/app/service::TestCollision", "service_test")
	if identity.Status != "exact" || identity.Package != "service_test" || len(identity.Matches) != 1 || identity.Matches[0].File != "service/external_test.go" {
		t.Fatalf("qualified identity = %#v", identity)
	}
	coverage := search.CoverageInPackage(g, "example.com/app/service::TestCollision", "service_test", true)
	if coverage.Status != "exact" || coverage.Package != "service_test" || len(coverage.Symbols) == 0 || coverage.Symbols[0].StableID != "example.com/app/service::Exact" {
		t.Fatalf("qualified coverage = %#v", coverage)
	}
}

func TestCoveragePropagatesPossibleTransitively(t *testing.T) {
	report := search.Coverage(attributionGraph(), "example.com/app/service::TestFlow", false)
	if report.Status != "exact" || report.AnalysisPrecision != graph.PrecisionPrecise || report.TestCallResolution != graph.TestCallResolutionTyped {
		t.Fatalf("coverage metadata = %#v", report)
	}
	want := map[string]string{
		"example.com/app/service::Start":       "exact",
		"example.com/app/service::Exact":       "exact",
		"example.com/app/service::(*Impl).Run": "possible",
		"example.com/app/service::Leaf":        "possible",
	}
	if len(report.Symbols) != len(want) {
		t.Fatalf("symbols = %#v", report.Symbols)
	}
	for _, symbol := range report.Symbols {
		if want[symbol.StableID] != symbol.Resolution {
			t.Errorf("%s resolution = %q, want %q", symbol.StableID, symbol.Resolution, want[symbol.StableID])
		}
		if len(symbol.Path) != symbol.Depth+1 || symbol.Path[0] != "example.com/app/service::TestFlow" {
			t.Errorf("%s path/depth = %#v/%d", symbol.StableID, symbol.Path, symbol.Depth)
		}
	}
}

func TestCoverageExactOnlyAndAmbiguousTest(t *testing.T) {
	exact := search.Coverage(attributionGraph(), "example.com/app/service::TestFlow", true)
	if len(exact.Symbols) != 2 {
		t.Fatalf("exact-only symbols = %#v", exact.Symbols)
	}
	ambiguous := search.Coverage(attributionGraph(), "TestFlow", false)
	if ambiguous.Status != "ambiguous" || len(ambiguous.MatchedTests) != 2 || len(ambiguous.Symbols) != 0 {
		t.Fatalf("ambiguous coverage = %#v", ambiguous)
	}
}

func TestCoverageExactPathOverridesEarlierPossiblePath(t *testing.T) {
	g := attributionGraph()
	g.TestEdges = []graph.TestEdge{
		{TestFunc: "TestFlow", TargetSymbolID: "example.com/app/service::Start", Resolution: graph.CallResolutionCHA, File: "service/flow_test.go"},
		{TestFunc: "TestFlow", TargetSymbolID: "example.com/app/service::Exact", Resolution: graph.CallResolutionStatic, File: "service/flow_test.go"},
	}
	g.Calls = []graph.CallEdge{
		{CallerSymbolID: "example.com/app/service::Start", CalleeSymbolID: "example.com/app/service::(*Impl).Run", Resolution: graph.CallResolutionStatic},
		{CallerSymbolID: "example.com/app/service::Exact", CalleeSymbolID: "example.com/app/service::(*Impl).Run", Resolution: graph.CallResolutionStatic},
		{CallerSymbolID: "example.com/app/service::(*Impl).Run", CalleeSymbolID: "example.com/app/service::Leaf", Resolution: graph.CallResolutionStatic},
	}
	report := search.Coverage(g, "example.com/app/service::TestFlow", false)
	byID := make(map[string]search.CoverageSymbol)
	for _, symbol := range report.Symbols {
		byID[symbol.StableID] = symbol
	}
	for _, id := range []string{"example.com/app/service::(*Impl).Run", "example.com/app/service::Leaf"} {
		symbol := byID[id]
		if symbol.Resolution != "exact" || len(symbol.Path) != symbol.Depth+1 {
			t.Fatalf("%s did not upgrade to a coherent exact path: %#v", id, symbol)
		}
	}
}

func TestCoverageParserTargetsArePossibleAndPackageBounded(t *testing.T) {
	g := attributionGraph()
	g.TestEdges = []graph.TestEdge{{TestFunc: "TestFlow", Target: "Start", File: "service/flow_test.go", Line: 11}}
	report := search.Coverage(g, "example.com/app/service::TestFlow", false)
	if len(report.Symbols) == 0 || report.Symbols[0].StableID != "example.com/app/service::Start" || report.Symbols[0].Resolution != "possible" {
		t.Fatalf("parser coverage = %#v", report.Symbols)
	}
}

func TestTransitiveTestsFindsExactAndPossibleReversePaths(t *testing.T) {
	g := attributionGraph()
	exact := search.TransitiveTests(g, "example.com/app/service::Exact", false)
	if exact.Status != "exact" || exact.SchemaVersion != search.TestsSchemaVersion || len(exact.Tests) != 1 {
		t.Fatalf("exact reverse attribution = %#v", exact)
	}
	if test := exact.Tests[0]; test.StableID != "example.com/app/service::TestFlow" || test.Resolution != "exact" || test.Depth != 2 || !reflect.DeepEqual(test.Path, []string{
		"example.com/app/service::TestFlow",
		"example.com/app/service::Start",
		"example.com/app/service::Exact",
	}) {
		t.Fatalf("exact reverse path = %#v", test)
	}

	possible := search.TransitiveTests(g, "example.com/app/service::Leaf", false)
	if len(possible.Tests) != 1 || possible.Tests[0].Resolution != "possible" || possible.Tests[0].Depth != 4 {
		t.Fatalf("possible reverse attribution = %#v", possible)
	}
	if exactOnly := search.TransitiveTests(g, "example.com/app/service::Leaf", true); len(exactOnly.Tests) != 0 {
		t.Fatalf("exact-only reverse attribution retained possible path: %#v", exactOnly)
	}
}

func TestTransitiveTestsUsesGoTestEntryNamingRules(t *testing.T) {
	const targetID = "example.com/app/service::Target"
	g := &graph.Graph{
		Build: &graph.BuildMetadata{},
		Symbols: []graph.SymbolNode{
			{ID: targetID, Kind: graph.KindFunction, Name: "Target", PackageName: "service", File: "service/target.go"},
			{ID: "example.com/app/service::TestingHelper", Kind: graph.KindFunction, Name: "TestingHelper", PackageName: "service", File: "service/target_test.go"},
			{ID: "example.com/app/service::FuzzTarget", Kind: graph.KindFunction, Name: "FuzzTarget", PackageName: "service", File: "service/target_test.go"},
		},
		Calls: []graph.CallEdge{
			{CallerSymbolID: "example.com/app/service::TestingHelper", CallerName: "TestingHelper", CalleeSymbolID: targetID, Resolution: graph.CallResolutionStatic, File: "service/target_test.go"},
			{CallerSymbolID: "example.com/app/service::FuzzTarget", CallerName: "FuzzTarget", CalleeSymbolID: targetID, Resolution: graph.CallResolutionStatic, File: "service/target_test.go"},
		},
	}

	report := search.TransitiveTests(g, targetID, false)
	if len(report.Tests) != 1 || report.Tests[0].Name != "FuzzTarget" {
		t.Fatalf("test entry roots = %#v, want only FuzzTarget", report.Tests)
	}
}

func TestTransitiveTestsDoesNotMergeAmbiguousProductSymbols(t *testing.T) {
	g := attributionGraph()
	g.Symbols = append(g.Symbols, graph.SymbolNode{
		ID: "example.com/app/other::Exact", Kind: graph.KindFunction, Name: "Exact", PackageName: "other", File: "other/exact.go",
	})
	report := search.TransitiveTests(g, "Exact", false)
	if report.Status != "ambiguous" || len(report.MatchedSymbols) != 2 || len(report.Tests) != 0 {
		t.Fatalf("ambiguous reverse attribution = %#v", report)
	}
}

func TestTransitiveTestsKeepsCollidingInternalAndExternalTestsDistinct(t *testing.T) {
	g := attributionGraph()
	g.Symbols = append(g.Symbols,
		graph.SymbolNode{ID: "example.com/app/service::TestCollision", Kind: graph.KindFunction, Name: "TestCollision", PackageName: "service", File: "service/internal_test.go"},
		graph.SymbolNode{ID: "example.com/app/service::TestCollision", Kind: graph.KindFunction, Name: "TestCollision", PackageName: "service_test", File: "service/external_test.go"},
	)
	g.TestEdges = append(g.TestEdges,
		graph.TestEdge{TestFunc: "TestCollision", File: "service/internal_test.go", TargetSymbolID: "example.com/app/service::Exact", Resolution: graph.CallResolutionStatic},
		graph.TestEdge{TestFunc: "TestCollision", File: "service/external_test.go", TargetSymbolID: "example.com/app/service::Exact", Resolution: graph.CallResolutionStatic},
	)
	report := search.TransitiveTests(g, "example.com/app/service::Exact", true)
	if len(report.Tests) != 3 {
		t.Fatalf("colliding test attribution = %#v", report.Tests)
	}
	packages := make(map[string]bool)
	for _, test := range report.Tests {
		if test.Name == "TestCollision" {
			packages[test.Package] = true
		}
	}
	if !packages["service"] || !packages["service_test"] {
		t.Fatalf("colliding test packages were merged: %#v", report.Tests)
	}
}

func TestFilterUntestedUsesRepositoryRelativeGlobs(t *testing.T) {
	input := []search.UntestedResult{
		{Name: "A", File: "tools/a.go"},
		{Name: "B", File: "internal/mw/b.go"},
		{Name: "C", File: "internal/mw/deep/c.go"},
		{Name: "D", File: "internal/core/d.go"},
	}
	got, err := search.FilterUntested(input, []string{"tools/**", "internal/mw/*"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []search.UntestedResult{{Name: "C", File: "internal/mw/deep/c.go"}, {Name: "D", File: "internal/core/d.go"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered = %#v, want %#v", got, want)
	}
	if _, err := search.FilterUntested(input, []string{"["}); err == nil {
		t.Fatal("expected malformed glob error")
	}
}
