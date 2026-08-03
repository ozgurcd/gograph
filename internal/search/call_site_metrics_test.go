package search_test

import (
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestSourceSiteMetricsDeduplicateParallelTargetsAndSkipSynthetic(t *testing.T) {
	const (
		serviceMethodID = "example.com/app/internal/service::(Service).Run"
		storeAWrapperID = "example.com/app/internal/store::(CompositeStore).Delete"
		storeADeleteID  = "example.com/app/internal/store::(StoreA).Delete"
		storeBDeleteID  = "example.com/app/internal/store::(StoreB).Delete"
	)
	g := &graph.Graph{
		Packages: []graph.PackageNode{{
			ID:    "internal/service",
			Name:  "service",
			Dir:   "internal/service",
			Files: []string{"internal/service/service.go"},
		}},
		Files: []graph.FileNode{{Path: "internal/service/service.go", PackageName: "service"}},
		Symbols: []graph.SymbolNode{
			{ID: "example.com/app/internal/service::Service", Kind: graph.KindStruct, Name: "Service", PackageName: "service", File: "internal/service/service.go"},
			{ID: serviceMethodID, Kind: graph.KindMethod, Name: "Run", Receiver: "Service", PackageName: "service", File: "internal/service/service.go"},
			{ID: storeADeleteID, Kind: graph.KindMethod, Name: "Delete", Receiver: "StoreA", PackageName: "store", File: "internal/store/a.go"},
			{ID: storeBDeleteID, Kind: graph.KindMethod, Name: "Delete", Receiver: "StoreB", PackageName: "store", File: "internal/store/b.go"},
		},
	}
	for _, line := range []int{10, 11} {
		// The direct StoreA target and CompositeStore wrapper both forward to
		// the same declared method body; that body still has one incoming source
		// expression per line, not two dispatch-path records.
		for _, targetID := range []string{storeAWrapperID, storeADeleteID, storeBDeleteID} {
			g.Calls = append(g.Calls, graph.CallEdge{
				CallerSymbolID: serviceMethodID,
				CallerName:     "(Service).Run",
				CalleeRaw:      "Delete",
				CalleeSymbolID: targetID,
				File:           "internal/service/service.go",
				Line:           line,
				Column:         12,
			})
		}
	}
	// A traversal-only wrapper edge must not contribute to any source metric,
	// even if it happens to carry package-local provenance.
	g.Calls = append(g.Calls, graph.CallEdge{
		CallerSymbolID: storeAWrapperID,
		CallerName:     "(CompositeStore).Delete",
		CalleeRaw:      "Delete",
		CalleeSymbolID: storeADeleteID,
		File:           "internal/service/service.go",
		Line:           12,
		Column:         12,
		Synthetic:      true,
	})

	focus := search.Focus(g, "service")
	foundFocusCount := false
	for _, result := range focus {
		if result.Kind == "callee" && result.Name == "Delete" {
			foundFocusCount = true
			if !strings.HasPrefix(result.Detail, "2 calls,") {
				t.Fatalf("Focus counted graph targets instead of source sites: %#v", result)
			}
		}
	}
	if !foundFocusCount {
		t.Fatalf("Focus omitted Delete: %#v", focus)
	}

	objects := search.GodObjects(g, search.GodObjectParams{MinMethods: 100, MinFields: 100, MinCalls: 1, Top: 10})
	if len(objects) != 1 || objects[0].Name != "Service" || objects[0].OutgoingCalls != 2 {
		t.Fatalf("GodObjects outgoing calls were not source-site normalized: %#v", objects)
	}

	hotspots := search.Hotspot(g, 0, false)
	deleteHotspots := 0
	for _, hotspot := range hotspots {
		if hotspot.Name == "(StoreA).Delete" || hotspot.Name == "(StoreB).Delete" {
			deleteHotspots++
			if hotspot.IncomingCalls != 2 {
				t.Fatalf("Hotspot counted graph targets instead of source sites: %#v", hotspot)
			}
		}
	}
	if deleteHotspots != 2 {
		t.Fatalf("expected both Delete hotspots, got %#v", hotspots)
	}

	untested := search.Untested(g)
	deleteUntested := 0
	for _, result := range untested {
		if result.Name != "Delete" {
			continue
		}
		deleteUntested++
		if result.CallerCount != 2 {
			t.Fatalf("Untested did not normalize/forward target call sites: %#v", result)
		}
	}
	if deleteUntested != 2 {
		t.Fatalf("expected both Delete implementations in Untested, got %#v", untested)
	}

	orphans := search.ReachableOrphans(g)
	deleteOrphans := 0
	for _, orphan := range orphans {
		if orphan.Name != storeADeleteID && orphan.Name != storeBDeleteID {
			continue
		}
		deleteOrphans++
		if !strings.Contains(orphan.Detail, "incoming calls: 2") {
			t.Fatalf("orphan incoming detail counted graph targets instead of source sites: %#v", orphan)
		}
	}
	if deleteOrphans != 2 {
		t.Fatalf("expected both unreachable Delete implementations, got %#v", orphans)
	}
}
