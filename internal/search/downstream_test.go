package search

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func uncachedDownstream(g *graph.Graph, names []string) map[string]bool {
	seen := make(map[string]bool)
	queue := append([]string(nil), names...)
	for _, name := range names {
		seen[name] = true
	}
	for index := 0; index < len(queue); index++ {
		for _, call := range g.Calls {
			if call.CallerName == queue[index] && !seen[call.CalleeRaw] {
				seen[call.CalleeRaw] = true
				queue = append(queue, call.CalleeRaw)
			}
		}
	}
	return seen
}

func TestDownstreamIndexPreservesTraversalAndSnapshotOwnership(t *testing.T) {
	g := &graph.Graph{Calls: []graph.CallEdge{
		{CallerName: "A", CalleeRaw: "B"}, {CallerName: "B", CalleeRaw: "A"},
		{CallerName: "A", CalleeRaw: "B"}, {CallerName: "A", CalleeRaw: ""},
		{CallerName: "", CalleeRaw: "C"}, {CallerName: "A", CalleeRaw: "D", CallerSymbolID: "other::A"},
		{CallerName: "other::A", CalleeRaw: "E"},
	}}
	snapshot := NewSnapshot(g)
	if len(snapshot.downstream(nil)) != 0 || snapshot.downstreamCalls != nil {
		t.Fatal("empty seeds must not construct an unused index")
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for _, names := range [][]string{{"A"}, {"other::A"}, {""}, nil, {"missing"}, {"A", "A", "B"}} {
				got := snapshot.downstream(names)
				if !reflect.DeepEqual(got, uncachedDownstream(g, names)) {
					t.Error("indexed traversal differs from the original traversal")
				}
				got["request-local mutation"] = true
			}
		})
	}
	workers.Wait()
	next := NewSnapshot(&graph.Graph{Calls: []graph.CallEdge{{CallerName: "A", CalleeRaw: "new"}}})
	if snapshot.downstream([]string{"A"})["new"] || !next.downstream([]string{"A"})["new"] {
		t.Fatal("adjacency leaked across snapshots")
	}
}

func BenchmarkDownstreamTraversal(b *testing.B) {
	g := &graph.Graph{}
	for i := range 500 {
		g.Calls = append(g.Calls, graph.CallEdge{CallerName: fmt.Sprint(i), CalleeRaw: fmt.Sprint(i + 1)})
	}
	snapshot := NewSnapshot(g)
	names := []string{"0"}
	snapshot.downstream(names)
	b.Run("full_scan", func(b *testing.B) {
		for b.Loop() {
			uncachedDownstream(g, names)
		}
	})
	b.Run("indexed", func(b *testing.B) {
		for b.Loop() {
			snapshot.downstream(names)
		}
	})
}
