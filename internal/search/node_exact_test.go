package search_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestNodePrefersExactCaseSensitiveMatches(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "x::Manager", Kind: graph.KindStruct, Name: "Manager", PackageName: "x", File: "x.go", Line: 3},
		{ID: "x::manager", Kind: graph.KindFunction, Name: "manager", PackageName: "x", File: "x.go", Line: 5},
		{ID: "x::(*Manager).Run", Kind: graph.KindMethod, Name: "Run", Receiver: "*Manager", PackageName: "x", File: "x.go", Line: 7},
		{ID: "x::NewManagerPool", Kind: graph.KindFunction, Name: "NewManagerPool", PackageName: "x", File: "x.go", Line: 9},
	}}
	names := func(query string) string {
		var out []string
		for _, r := range search.Node(g, query) {
			out = append(out, r.Name)
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	cases := map[string]string{
		"Manager":     "Manager",
		"manager":     "manager",
		"Manager.Run": "(*Manager).Run",
		"x.Manager":   "Manager",
		"MANAGER":     "Manager,manager",
		"anagerPool":  "NewManagerPool",
	}
	for query, want := range cases {
		if got := names(query); got != want {
			t.Errorf("Node(%q) = %s, want %s", query, got, want)
		}
	}
}
