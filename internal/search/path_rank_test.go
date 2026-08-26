package search

import (
	"slices"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

const pathRankModule = "example.com/pathrank"

func rankedPathGraph(symbols ...string) *graph.Graph {
	g := &graph.Graph{}
	for index, name := range symbols {
		g.Symbols = append(g.Symbols, graph.SymbolNode{
			ID: pathRankModule + "::" + name, Name: name, Kind: graph.KindFunction,
			File: "path.go", Line: index + 1,
		})
	}
	return g
}

func rankedCall(from, to, file string, resolution graph.CallResolution) graph.CallEdge {
	return graph.CallEdge{
		CallerSymbolID: pathRankModule + "::" + from,
		CallerName:     from,
		CalleeSymbolID: pathRankModule + "::" + to,
		CalleeRaw:      to,
		File:           file,
		Resolution:     resolution,
	}
}

func pathNames(path []Result) []string {
	names := make([]string, 0, len(path))
	for _, step := range path {
		names = append(names, step.Name)
	}
	return names
}

func TestPathRanksExactBeforeShorterPossible(t *testing.T) {
	g := rankedPathGraph("Start", "ExactMid", "End")
	g.Calls = []graph.CallEdge{
		rankedCall("Start", "End", "path.go", graph.CallResolutionCHA),
		rankedCall("Start", "ExactMid", "path.go", graph.CallResolutionStatic),
		rankedCall("ExactMid", "End", "path.go", graph.CallResolutionStatic),
	}

	got := pathNames(Path(g, pathRankModule+"::Start", pathRankModule+"::End", true))
	if want := []string{"Start", "ExactMid", "End"}; !slices.Equal(got, want) {
		t.Fatalf("ranked path = %v, want %v", got, want)
	}
}

func TestPathRanksShorterBeforeProduction(t *testing.T) {
	g := rankedPathGraph("Start", "ProductionMid", "End")
	g.Calls = []graph.CallEdge{
		rankedCall("Start", "End", "path_test.go", graph.CallResolutionStatic),
		rankedCall("Start", "ProductionMid", "path.go", graph.CallResolutionStatic),
		rankedCall("ProductionMid", "End", "path.go", graph.CallResolutionStatic),
	}

	got := pathNames(Path(g, pathRankModule+"::Start", pathRankModule+"::End", true))
	if want := []string{"Start", "End"}; !slices.Equal(got, want) {
		t.Fatalf("ranked path = %v, want shorter path %v", got, want)
	}
}

func TestPathRanksProductionBeforeTests(t *testing.T) {
	g := rankedPathGraph("Start", "TestMid", "ProductionMid", "End")
	g.Calls = []graph.CallEdge{
		rankedCall("Start", "TestMid", "path_test.go", graph.CallResolutionStatic),
		rankedCall("TestMid", "End", "path_test.go", graph.CallResolutionStatic),
		rankedCall("Start", "ProductionMid", "path.go", graph.CallResolutionStatic),
		rankedCall("ProductionMid", "End", "path.go", graph.CallResolutionStatic),
	}

	got := pathNames(Path(g, pathRankModule+"::Start", pathRankModule+"::End", true))
	if want := []string{"Start", "ProductionMid", "End"}; !slices.Equal(got, want) {
		t.Fatalf("ranked path = %v, want production path %v", got, want)
	}
}

func TestPathRanksTypedBeforeHeuristic(t *testing.T) {
	g := rankedPathGraph("Start", "HeuristicMid", "TypedMid", "End")
	g.Calls = []graph.CallEdge{
		rankedCall("Start", "HeuristicMid", "path.go", ""),
		rankedCall("HeuristicMid", "End", "path.go", ""),
		rankedCall("Start", "TypedMid", "path.go", graph.CallResolutionStatic),
		rankedCall("TypedMid", "End", "path.go", graph.CallResolutionStatic),
	}

	got := pathNames(Path(g, pathRankModule+"::Start", pathRankModule+"::End", true))
	if want := []string{"Start", "TypedMid", "End"}; !slices.Equal(got, want) {
		t.Fatalf("ranked path = %v, want typed path %v", got, want)
	}
}

func TestPathTieBreakIsIndependentOfEdgeOrder(t *testing.T) {
	calls := []graph.CallEdge{
		rankedCall("Start", "BMid", "path.go", graph.CallResolutionStatic),
		rankedCall("BMid", "End", "path.go", graph.CallResolutionStatic),
		rankedCall("Start", "AMid", "path.go", graph.CallResolutionStatic),
		rankedCall("AMid", "End", "path.go", graph.CallResolutionStatic),
	}
	for _, reverse := range []bool{false, true} {
		g := rankedPathGraph("Start", "AMid", "BMid", "End")
		g.Calls = append([]graph.CallEdge(nil), calls...)
		if reverse {
			slices.Reverse(g.Calls)
		}
		got := pathNames(Path(g, pathRankModule+"::Start", pathRankModule+"::End", true))
		if want := []string{"Start", "AMid", "End"}; !slices.Equal(got, want) {
			t.Fatalf("reverse=%v ranked path = %v, want %v", reverse, got, want)
		}
	}
}

func TestPathKeepsCertaintyClassesUntilTheDestination(t *testing.T) {
	g := rankedPathGraph("Start", "ExactMid", "Join", "End")
	g.Calls = []graph.CallEdge{
		rankedCall("Start", "Join", "path.go", graph.CallResolutionCHA),
		rankedCall("Start", "ExactMid", "path.go", graph.CallResolutionStatic),
		rankedCall("ExactMid", "Join", "path.go", graph.CallResolutionStatic),
		rankedCall("Join", "End", "path.go", graph.CallResolutionCHA),
	}

	got := pathNames(Path(g, pathRankModule+"::Start", pathRankModule+"::End", true))
	if want := []string{"Start", "Join", "End"}; !slices.Equal(got, want) {
		t.Fatalf("ranked path = %v, want shorter path after both routes degrade to possible: %v", got, want)
	}
}
