package search

import (
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestImpactAndCallerMermaidNeverFollowConflictingResolvedNames(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example.com/a::Save", Name: "Save", Kind: graph.KindFunction},
		{ID: "example.com/a::Wrap", Name: "Wrap", Kind: graph.KindFunction},
		{ID: "example.com/b::Wrap", Name: "Wrap", Kind: graph.KindFunction},
		{ID: "example.com/b::Unrelated", Name: "Unrelated", Kind: graph.KindFunction},
	}, Calls: []graph.CallEdge{
		{CallerSymbolID: "example.com/a::Wrap", CallerName: "Wrap", CalleeSymbolID: "example.com/a::Save", CalleeRaw: "Save", Resolution: graph.CallResolutionStatic},
		{CallerSymbolID: "example.com/b::Unrelated", CallerName: "Unrelated", CalleeSymbolID: "example.com/b::Wrap", CalleeRaw: "Wrap", Resolution: graph.CallResolutionStatic},
	}}
	for _, diagram := range []string{ImpactToMermaid(g, "example.com/a::Save", true), CallersToMermaid(g, "example.com/a::Save", 3, true, true)} {
		if !strings.Contains(diagram, "Wrap") || strings.Contains(diagram, "Unrelated") {
			t.Fatalf("canonical traversal leaked: %s", diagram)
		}
	}
	impact := ImpactToMermaid(g, "example.com/a::Save", true)
	if !strings.Contains(impact, "example.com/a::Wrap") || strings.Contains(impact, "example.com/b::Wrap") {
		t.Fatalf("same-named nodes were conflated: %s", impact)
	}
}

func TestImpactMermaidPropagatesPossibleAndHonorsExactOnly(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example.com/p::Target", Name: "Target", Kind: graph.KindFunction},
		{ID: "example.com/p::Middle", Name: "Middle", Kind: graph.KindFunction},
		{ID: "example.com/p::Top", Name: "Top", Kind: graph.KindFunction},
	}, Calls: []graph.CallEdge{
		{CallerSymbolID: "example.com/p::Middle", CalleeSymbolID: "example.com/p::Target", Resolution: graph.CallResolutionCHA},
		{CallerSymbolID: "example.com/p::Top", CalleeSymbolID: "example.com/p::Middle", Resolution: graph.CallResolutionStatic},
	}}
	snapshot := NewSnapshot(g)
	possible := snapshot.ImpactMermaid([]string{"Target"}, ImpactOptions{IncludeTests: true})
	if strings.Count(possible, "-.->") != 2 || strings.Contains(possible, " --> ") {
		t.Fatalf("possible path was promoted: %s", possible)
	}
	exact := snapshot.ImpactMermaid([]string{"Target"}, ImpactOptions{IncludeTests: true, ExactOnly: true})
	if strings.Contains(exact, "Middle") || strings.Contains(exact, "Top") {
		t.Fatalf("possible nodes entered exact-only diagram: %s", exact)
	}
}
