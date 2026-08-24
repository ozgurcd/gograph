package precise

import (
	"reflect"
	"sort"
	"testing"

	"github.com/ozgurcd/gograph/internal/buildctx"
	"github.com/ozgurcd/gograph/internal/graph"
)

func TestLowMemoryEnrichmentMatchesStandard(t *testing.T) {
	root := fixtureDir(t)
	config, err := buildctx.Resolve(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	standard := emptyGraph()
	low := emptyGraph()
	requirePreciseEnrich(t, EnrichWithOptions(root, standard, config, Options{}))
	requirePreciseEnrich(t, EnrichWithOptions(root, low, config, Options{LowMemory: true}))
	for _, candidate := range []*graph.Graph{standard, low} {
		sort.Slice(candidate.Calls, func(i, j int) bool {
			left, right := candidate.Calls[i], candidate.Calls[j]
			return left.File < right.File || left.File == right.File && (left.Line < right.Line || left.Line == right.Line && (left.Column < right.Column || left.Column == right.Column && left.CalleeSymbolID < right.CalleeSymbolID))
		})
	}
	if !reflect.DeepEqual(low, standard) {
		t.Fatal("low-memory precise enrichment changed graph results")
	}
}
