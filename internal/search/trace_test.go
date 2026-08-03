package search_test

import (
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestTraceFallbackDeduplicatesParallelTargetEdges(t *testing.T) {
	g := &graph.Graph{
		Errors: []graph.ErrorEdge{{Message: "boom", Function: "Target", File: "target.go", Line: 4}},
		Calls: []graph.CallEdge{
			{CallerSymbolID: "example::Run", CallerName: "Run", CalleeRaw: "Target", CalleeSymbolID: "example::(*Memory).Target", File: "run.go", Line: 12, Column: 8},
			{CallerSymbolID: "example::Run", CallerName: "Run", CalleeRaw: "Target", CalleeSymbolID: "example::(*SQL).Target", File: "run.go", Line: 12, Column: 8},
		},
	}

	results := search.Trace(g, "boom", false)
	if len(results) != 1 || len(results[0].Path) != 1 || results[0].Path[0].Name != "Run" {
		t.Fatalf("parallel target edges produced duplicate trace fallback rows: %#v", results)
	}
}
