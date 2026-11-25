package search_test

import (
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestRelativePackagePathSelectors(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example.com/m/internal/client::Run", Kind: graph.KindFunction, Name: "Run", PackageName: "client", File: "internal/client/run.go"},
		{ID: "example.com/m/internal/client::(*Manager).Run", Kind: graph.KindMethod, Name: "Run", Receiver: "*Manager", PackageName: "client", File: "internal/client/manager.go"},
		{ID: "example.com/m/cmd/client::Run", Kind: graph.KindFunction, Name: "Run", PackageName: "client", File: "cmd/client/main.go"},
	}}
	cases := map[string]string{
		"internal/client::Run":               "example.com/m/internal/client::Run",
		"internal/client.Run":                "example.com/m/internal/client::Run",
		"./internal/client::Run":             "example.com/m/internal/client::Run",
		"example.com/m/internal/client::Run": "example.com/m/internal/client::Run",
		"internal/client::Manager.Run":       "example.com/m/internal/client::(*Manager).Run",
		"internal/client::(*Manager).Run":    "example.com/m/internal/client::(*Manager).Run",
		"m/internal/client.Manager.Run":      "example.com/m/internal/client::(*Manager).Run",
		"client::Manager.Run":                "example.com/m/internal/client::(*Manager).Run",
		"cmd/client::Run":                    "example.com/m/cmd/client::Run",
	}
	for query, want := range cases {
		got := search.FindSymbols(g, query)
		if len(got) != 1 || got[0].ID != want {
			t.Errorf("FindSymbols(%q) = %v, want only %s", query, got, want)
		}
	}
	for _, query := range []string{"ernal/client::Run", "other/client::Run"} {
		if got := search.FindSymbols(g, query); len(got) != 0 {
			t.Errorf("FindSymbols(%q) = %v, want no match (path segments must be whole)", query, got)
		}
	}
}
