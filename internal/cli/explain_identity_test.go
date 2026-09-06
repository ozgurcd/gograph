package cli

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestCLIExplainAmbiguityMatchesSharedNativeResult(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example/a::Save", Name: "Save", Kind: graph.KindFunction, File: "a.go"},
		{ID: "example/b::Save", Name: "Save", Kind: graph.KindFunction, File: "b.go"},
	}}
	root := writeCLIParityGraph(t, g)
	out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"explain", "Save", "--json"}) })
	if code != 0 {
		t.Fatalf("explain: %s", stderr)
	}
	var envelope struct {
		Status  string
		Count   int
		Results search.ExplainResult
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Status != "ambiguous" || envelope.Count != 2 || !reflect.DeepEqual(&envelope.Results, search.Explain(g, "Save")) {
		t.Fatalf("CLI ambiguity diverged: %+v", envelope)
	}
}
