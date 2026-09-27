package search

import (
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestPreparedSymbolMatcherPreservesReferenceSemantics(t *testing.T) {
	var symbols []graph.SymbolNode
	queries := []string{"", "missing", "p.Store.Save", "(*Store).Save", "p::Save", "p.Save.more.parts", "Σ", "σ", "K", "k", "İ", "i", "\xff"}
	for _, receiver := range []string{"", "Store", "*Store", "(*Store)", "**Store", "Änderung"} {
		for _, name := range []string{"Save", "SAVE", "Änderung", "Σ", "", "\xff"} {
			for _, pkg := range []string{"p", ""} {
				s := graph.SymbolNode{ID: "example.com/" + pkg + "::" + receiver + "." + name, Name: name, Receiver: receiver, PackageName: pkg}
				symbols = append(symbols, s)
				queries = append(queries, s.ID, strings.ToUpper(s.ID), s.Name, pkg+"."+s.Name, receiver+"."+s.Name, "("+receiver+")."+s.Name)
			}
		}
	}
	// Duplicate records must remain ambiguous, including identical stable IDs.
	symbols = append(symbols, symbols[0], graph.SymbolNode{Name: "Legacy"})
	g := &graph.Graph{Symbols: symbols}
	resolver := newSymbolResolver(symbols)
	for _, query := range queries {
		q := prepareSymbolQuery(query)
		for i, symbol := range symbols {
			if got, want := resolver.prepared[i].matches(q), MatchSymbol(symbol, query); got != want {
				t.Fatalf("prepared/reference mismatch for symbol %#v, query %q", symbol, query)
			}
		}
		matches := FindSymbols(g, query)
		id, unique := resolver.unique(query)
		if unique != (len(matches) == 1) || (unique && id != matches[0].ID) {
			t.Fatalf("unique resolution differs for %q: %q/%v vs %#v", query, id, unique, matches)
		}
	}
	if id, unique := resolver.unique("Legacy"); !unique || id != "" {
		t.Fatal("unique legacy empty ID was lost")
	}
}

func FuzzPreparedSymbolMatcher(f *testing.F) {
	f.Add("example.com/p::(*Store).Save", "Save", "*Store", "p", "p.Store.Save")
	f.Add("", "Änderung", "", "p", "änderung")
	f.Fuzz(func(t *testing.T, id, name, receiver, pkg, query string) {
		symbol := graph.SymbolNode{ID: id, Name: name, Receiver: receiver, PackageName: pkg}
		if prepareSymbol(symbol).matches(prepareSymbolQuery(query)) != MatchSymbol(symbol, query) {
			t.Fatalf("prepared matching differs for %#v, query %q", symbol, query)
		}
	})
}
