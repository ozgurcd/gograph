package search

import (
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func composedSnapshotFixture() *graph.Graph {
	return &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: "Handler", Name: "Handler", Kind: graph.KindFunction, File: "api.go", Line: 1},
			{ID: "Target", Name: "Target", Kind: graph.KindFunction, File: "service.go", Line: 1},
			{ID: "worker", Name: "worker", Kind: graph.KindFunction, File: "service.go", Line: 2},
		},
		Calls: []graph.CallEdge{
			{CallerSymbolID: "Handler", CallerName: "Handler", CalleeRaw: "Target", File: "api.go", Line: 2},
			{CallerSymbolID: "Target", CallerName: "Target", CalleeRaw: "worker", File: "service.go", Line: 3},
		},
		Routes:    []graph.HTTPRoute{{Method: "GET", Path: "/resource", Handler: "Handler"}},
		EnvReads:  []graph.EnvRead{{Key: "EXAMPLE_SETTING", Function: "worker"}},
		SQLs:      []graph.SQLEdge{{Query: "SELECT 1", Function: "worker"}},
		TestEdges: []graph.TestEdge{{TestFunc: "TestTarget", Target: "Target", File: "service_test.go", Line: 1}},
		Errors:    []graph.ErrorEdge{{Message: "example failure", Function: "worker"}},
	}
}

func TestComposedSnapshotConcurrentEquivalentAndOwnsResults(t *testing.T) {
	g := composedSnapshotFixture()
	snapshot := NewSnapshot(g)
	// Populate an independent oracle with the original index algorithm.
	oracle := NewSnapshot(g)
	oracle.impactOnce.Do(func() { oracle.impact = uncachedImpactIndex(g) })
	for _, names := range [][]string{{"Target"}, {"Target", "worker"}, {"missing"}, nil} {
		wantPlan := oracle.Plan(names, "plan")
		wantReview := oracle.Review(names, "review")
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				if got := snapshot.Plan(names, "plan"); !reflect.DeepEqual(got, wantPlan) {
					t.Error("cached plan differs from uncached index results")
				}
				if got := snapshot.Review(names, "review"); !reflect.DeepEqual(got, wantReview) {
					t.Error("cached review differs from uncached index results")
				}
			})
		}
		wg.Wait()
	}
	if snapshot.impact.incoming == nil {
		t.Fatal("composed operations bypassed the snapshot's impact cache")
	}
	plan := snapshot.Plan([]string{"Target"}, "plan")
	review := snapshot.Review([]string{"Target"}, "review")
	if plan.PublicAPI != "yes" || plan.TouchesSQL != "yes" || review.TouchesSQL != "yes" ||
		!slices.Equal(plan.Routes, []string{"GET /resource"}) || !slices.Equal(review.Envs, []string{"EXAMPLE_SETTING"}) ||
		!slices.Equal(review.Errors, []string{"example failure"}) || !slices.Equal(review.Tests, []string{"service_test.go"}) {
		t.Fatal("composed results lost public API, routes, environment, SQL, errors, or tests")
	}
	plan.ReadFirst[0].Name = "changed"
	plan.Tests[0] = "changed"
	plan.Routes[0] = "changed"
	review.Changes[0].Name = "changed"
	review.Envs[0] = "changed"
	review.Errors[0] = "changed"
	if !reflect.DeepEqual(snapshot.Plan([]string{"Target"}, "plan"), oracle.Plan([]string{"Target"}, "plan")) ||
		!reflect.DeepEqual(snapshot.Review([]string{"Target"}, "review"), oracle.Review([]string{"Target"}, "review")) {
		t.Fatal("mutating a returned result poisoned the shared cache")
	}
}

func TestComposedSnapshotsDoNotShareResolutionAcrossGraphs(t *testing.T) {
	oldGraph := composedSnapshotFixture()
	old := NewSnapshot(oldGraph)
	if got := old.Plan([]string{"Target"}, "old"); len(got.Routes) != 1 {
		t.Fatal("old snapshot did not resolve the unique target")
	}
	nextGraph := composedSnapshotFixture()
	nextGraph.Symbols = append(nextGraph.Symbols, graph.SymbolNode{ID: "other::Target", Name: "Target", Kind: graph.KindFunction})
	next := NewSnapshot(nextGraph)
	if got := next.Plan([]string{"Target"}, "next"); len(got.Routes) != 0 {
		t.Fatal("fresh ambiguous graph reused the old unique resolution")
	}
	if got := old.Review([]string{"Target"}, "old"); len(got.Routes) != 1 {
		t.Fatal("refresh invalidated an in-flight old snapshot")
	}
}
