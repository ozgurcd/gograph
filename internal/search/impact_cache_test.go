package search

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

// This reference deliberately retains the pre-cache algorithm, including its
// ambiguity and empty-ID behavior, as an oracle for the optimized index.
func uncachedImpactIndex(g *graph.Graph) impactIndex {
	symbols := make(map[string]graph.SymbolNode, len(g.Symbols))
	for _, symbol := range g.Symbols {
		symbols[symbol.ID] = symbol
	}
	incoming := make(map[string][]graph.CallEdge)
	var unresolved []graph.CallEdge
	for _, call := range g.Calls {
		if call.CalleeSymbolID == "" {
			candidates := FindSymbols(g, call.CalleeRaw)
			if len(candidates) != 1 {
				unresolved = append(unresolved, call)
				continue
			}
			call.CalleeSymbolID = candidates[0].ID
			call.Resolution = ""
		}
		incoming[call.CalleeSymbolID] = append(incoming[call.CalleeSymbolID], call)
	}
	return impactIndex{symbols: symbols, incoming: incoming, unresolved: unresolved}
}

func TestImpactIndexMatchesUncachedResolution(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example.com/a::Target", Name: "Target", PackageName: "a"},
		{ID: "example.com/b::Target", Name: "Target", PackageName: "b"},
		{ID: "example.com/a::(*Store).Save", Name: "Save", Receiver: "*Store", PackageName: "a"},
		{ID: "example.com/a::Änderung", Name: "Änderung", PackageName: "a"},
		{ID: "", Name: "Legacy"},
	}}
	for repeat := range 3 {
		for _, raw := range []string{"Target", "a.Target", "EXAMPLE.COM/A::TARGET", "Store.Save", "a.Store.Save", "(*Store).Save", "änderung", "Legacy", "external.Missing", ""} {
			g.Calls = append(g.Calls, graph.CallEdge{CallerSymbolID: "caller", CalleeRaw: raw, File: "caller.go", Line: repeat + 1, Resolution: graph.CallResolutionStatic})
		}
		// A resolved edge must never be redirected by a conflicting raw name.
		g.Calls = append(g.Calls, graph.CallEdge{CallerSymbolID: "caller", CalleeRaw: "Store.Save", CalleeSymbolID: "example.com/a::Target", Resolution: graph.CallResolutionCHA})
	}
	before := slices.Clone(g.Calls)
	if got, want := buildImpactIndex(g), uncachedImpactIndex(g); !reflect.DeepEqual(got, want) {
		t.Fatal("cached index changed resolution, ambiguity, ordering, or certainty")
	}
	if !reflect.DeepEqual(g.Calls, before) {
		t.Fatal("building the index mutated the graph")
	}
}

func TestImpactIndexRepeatedSpellingsAvoidRepeatedResolution(t *testing.T) {
	g := &graph.Graph{}
	for i := range 128 {
		g.Symbols = append(g.Symbols, graph.SymbolNode{ID: fmt.Sprintf("example.com/pkg::Symbol%03d", i), Name: fmt.Sprintf("Symbol%03d", i)})
	}
	g.Calls = []graph.CallEdge{{CallerSymbolID: "caller", CalleeRaw: "external.Missing"}}
	single := testing.AllocsPerRun(3, func() { buildImpactIndex(g) })
	g.Calls = slices.Repeat(g.Calls, 128)
	repeated := testing.AllocsPerRun(3, func() { buildImpactIndex(g) })
	t.Logf("one spelling, one call: %.0f allocations; one spelling, 128 calls: %.0f", single, repeated)
	// Allow index storage to grow, but not 128 repetitions of symbol matching.
	if repeated > single*8+512 {
		t.Fatalf("repeated raw spellings repeat expensive resolution: %.0f > %.0f", repeated, single*8+512)
	}
}

func BenchmarkImpactIndexRepeatedSpellings(b *testing.B) {
	g := &graph.Graph{}
	for i := range 256 {
		g.Symbols = append(g.Symbols, graph.SymbolNode{ID: fmt.Sprintf("example.com/pkg::Symbol%03d", i), Name: fmt.Sprintf("Symbol%03d", i)})
	}
	for i := range 2048 {
		g.Calls = append(g.Calls, graph.CallEdge{CallerSymbolID: "caller", CalleeRaw: fmt.Sprintf("external.Missing%d", i%8)})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		buildImpactIndex(g)
	}
}
