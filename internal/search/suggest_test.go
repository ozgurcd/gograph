package search_test

import (
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestNotFoundHintSuggestsCloseDeclarations(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "m/app::classify", Kind: graph.KindFunction, Name: "classify", PackageName: "app", File: "app/classify.go", Line: 3},
		{ID: "m/app::(*Manager).Run", Kind: graph.KindMethod, Name: "Run", Receiver: "*Manager", PackageName: "app", File: "app/manager.go", Line: 5},
		{ID: "m/app::validateRun", Kind: graph.KindFunction, Name: "validateRun", PackageName: "app", File: "app/run.go", Line: 7},
		{ID: "m/app::Source", Kind: graph.KindFunction, Name: "Source", PackageName: "app", File: "app/source.go", Line: 9},
	}}
	cases := map[string]string{
		"(*Manager).classify": "app/classify.go:classify",
		"Classify":            "app/classify.go:classify",
		"validateRunn":        "app/run.go:validateRun",
		"app/x.go:Manager.Rn": "app/manager.go:Manager.Run",
	}
	for query, want := range cases {
		if hint := search.NotFoundHint(g, query); !strings.Contains(hint, want) {
			t.Errorf("NotFoundHint(%q) = %q, want it to suggest %s", query, hint, want)
		}
	}
	if hint := search.NotFoundHint(g, "sourceTargetsPlan"); hint != "" {
		t.Errorf("a name that only contains a short declaration name must not suggest it: %q", hint)
	}
	if hint := search.NotFoundHint(g, "nothingLikeThis"); hint != "" {
		t.Errorf("unrelated names get no suggestion: %q", hint)
	}
}
