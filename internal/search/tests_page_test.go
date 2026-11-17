package search_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

// hubGraph has one product function reached by n tests at depth 1 through a
// direct edge, and by n more at depth 2 through a CHA possible edge.
func hubGraph(n int) *graph.Graph {
	g := &graph.Graph{
		Build: &graph.BuildMetadata{Precision: graph.PrecisionPrecise, TestCallResolution: graph.TestCallResolutionTyped},
		Symbols: []graph.SymbolNode{
			{ID: "example.com/app::Hub", Kind: graph.KindFunction, Name: "Hub", PackageName: "app", File: "hub.go"},
			{ID: "example.com/app::Via", Kind: graph.KindFunction, Name: "Via", PackageName: "app", File: "hub.go"},
		},
		Calls: []graph.CallEdge{{CallerSymbolID: "example.com/app::Via", CalleeSymbolID: "example.com/app::Hub", Resolution: graph.CallResolutionCHA}},
	}
	for i := 0; i < 2*n; i++ {
		name := fmt.Sprintf("TestCase%03d", i)
		target := "example.com/app::Hub"
		if i%2 == 1 {
			target = "example.com/app::Via"
		}
		g.Symbols = append(g.Symbols, graph.SymbolNode{ID: "example.com/app::" + name, Kind: graph.KindFunction, Name: name, PackageName: "app", File: "hub_test.go"})
		g.TestEdges = append(g.TestEdges, graph.TestEdge{TestFunc: name, Target: target, TargetSymbolID: target, Resolution: graph.CallResolutionStatic, File: "hub_test.go"})
	}
	return g
}

func TestTransitiveTestsPageSummarizesSortsAndPages(t *testing.T) {
	snapshot := search.NewSnapshot(hubGraph(60))
	first, err := snapshot.TransitiveTestsPage("example.com/app::Hub", "", false, search.TestsPageOptions{Command: "gograph tests Hub --transitive"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Summary.Total != 120 || first.Summary.Exact != 60 || first.Summary.Possible != 60 {
		t.Fatalf("summary = %+v", first.Summary)
	}
	if len(first.Summary.ByDepth) != 2 || first.Summary.ByDepth[0] != (search.TestDepthCount{Depth: 1, Exact: 60}) || first.Summary.ByDepth[1] != (search.TestDepthCount{Depth: 2, Possible: 60}) {
		t.Fatalf("by depth = %+v", first.Summary.ByDepth)
	}
	if *first.Returned != search.DefaultTestsLimit || !*first.Truncated || *first.Total != 120 || *first.NextCursor == "" {
		t.Fatalf("page returned=%d truncated=%v total=%d", *first.Returned, *first.Truncated, *first.Total)
	}
	if !strings.HasPrefix(first.Summary.NextCommand, "gograph tests Hub --transitive --cursor v1.") {
		t.Fatalf("next command = %q", first.Summary.NextCommand)
	}
	for i, test := range first.Tests {
		if test.Depth != 1 || test.Resolution != "exact" {
			t.Fatalf("test %d = d%d %s, want nearest exact tests first", i, test.Depth, test.Resolution)
		}
	}
	seen := map[string]bool{}
	for _, test := range first.Tests {
		seen[test.StableID] = true
	}
	cursor := *first.NextCursor
	for cursor != "" {
		page, err := snapshot.TransitiveTestsPage("example.com/app::Hub", "", false, search.TestsPageOptions{Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, test := range page.Tests {
			if seen[test.StableID] {
				t.Fatalf("duplicate %s across pages", test.StableID)
			}
			seen[test.StableID] = true
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 120 {
		t.Fatalf("paged %d tests, want 120", len(seen))
	}

	near, err := snapshot.TransitiveTestsPage("example.com/app::Hub", "", false, search.TestsPageOptions{MaxDepth: 1, Limit: -1})
	if err != nil {
		t.Fatal(err)
	}
	if near.Summary.Total != 60 || near.Summary.BeyondDepth != 60 || *near.Returned != 60 || *near.Truncated {
		t.Fatalf("max depth page = summary %+v returned %d", near.Summary, *near.Returned)
	}
	if _, err := snapshot.TransitiveTestsPage("example.com/app::Hub", "", true, search.TestsPageOptions{Cursor: *first.NextCursor}); err == nil {
		t.Fatal("cursor accepted for a different selection (exact_only)")
	}
	if _, err := snapshot.TransitiveTestsPage("example.com/app::Hub", "", false, search.TestsPageOptions{MaxDepth: -1}); err == nil {
		t.Fatal("negative max depth accepted")
	}
}
