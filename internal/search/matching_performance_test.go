package search

import (
	"fmt"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestImpactIndexDistinctSpellingsDoNotRenormalizeSymbols(t *testing.T) {
	g := &graph.Graph{}
	for i := range 256 {
		g.Symbols = append(g.Symbols, graph.SymbolNode{ID: fmt.Sprintf("example.com/pkg::Symbol%03d", i), Name: fmt.Sprintf("Symbol%03d", i)})
	}
	for i := range 64 {
		g.Calls = append(g.Calls, graph.CallEdge{CallerSymbolID: "caller", CalleeRaw: fmt.Sprintf("external.Missing%d", i)})
	}
	allocations := testing.AllocsPerRun(1, func() { buildImpactIndex(g) })
	t.Logf("distinct-spelling impact build: %.0f allocations", allocations)
	if allocations > 6000 {
		t.Fatalf("repeated query/symbol normalization: %.0f allocations exceed 6000", allocations)
	}
}
