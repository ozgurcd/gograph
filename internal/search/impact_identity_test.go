package search

import (
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestImpactNeverWidensResolvedCallerIdentity(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example/a::Save", Name: "Save", Kind: graph.KindFunction},
		{ID: "example/a::Wrap", Name: "Wrap", Kind: graph.KindFunction, File: "a.go"},
		{ID: "example/b::Wrap", Name: "Wrap", Kind: graph.KindFunction, File: "b.go"},
		{ID: "example::Unrelated", Name: "Unrelated", Kind: graph.KindFunction, File: "main.go"},
	}, Calls: []graph.CallEdge{
		{CallerSymbolID: "example/a::Wrap", CalleeSymbolID: "example/a::Save", CalleeRaw: "Save", Resolution: graph.CallResolutionStatic},
		{CallerSymbolID: "example::Unrelated", CalleeSymbolID: "example/b::Wrap", CalleeRaw: "b.Wrap", Resolution: graph.CallResolutionStatic},
	}}
	got := Impact(g, "example/a::Save", true)
	if len(got) != 1 || got[0].Name != "Wrap" {
		t.Fatalf("identity escaped into unrelated package: %+v", got)
	}
}

func TestImpactUncertaintyPropagatesAndExactPathWins(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example::Target", Name: "Target", Kind: graph.KindFunction},
		{ID: "example::Middle", Name: "Middle", Kind: graph.KindFunction},
		{ID: "example::Top", Name: "Top", Kind: graph.KindFunction},
	}, Calls: []graph.CallEdge{
		{CallerSymbolID: "example::Middle", CalleeSymbolID: "example::Target", Resolution: graph.CallResolutionCHA},
		{CallerSymbolID: "example::Top", CalleeSymbolID: "example::Middle", Resolution: graph.CallResolutionStatic},
	}}
	got := Impact(g, "example::Target", true)
	if len(got) != 2 {
		t.Fatalf("lost possible impact: %+v", got)
	}
	for _, row := range got {
		if row.ResolutionStatus != "possible" {
			t.Fatalf("uncertainty failed to propagate: %+v", got)
		}
	}
	if exact := ImpactWithOptions(g, []string{"example::Target"}, "", ImpactOptions{IncludeTests: true, ExactOnly: true}); len(exact) != 0 {
		t.Fatalf("possible path passed exact-only: %+v", exact)
	}
	g.Calls = append(g.Calls, graph.CallEdge{CallerSymbolID: "example::Middle", CalleeSymbolID: "example::Target", Resolution: graph.CallResolutionStatic})
	got = Impact(g, "example::Target", true)
	for _, row := range got {
		if row.ResolutionStatus != "exact" {
			t.Fatalf("exact path did not dominate: %+v", got)
		}
	}
}
