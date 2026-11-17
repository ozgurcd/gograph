package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func sameNameFunctionAndMethodGraph() *graph.Graph {
	return &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example.com/app/search::Untested", Kind: graph.KindFunction, Name: "Untested", PackageName: "search", File: "search/search.go", Line: 5, EndLine: 5},
		{ID: "example.com/app/search::(*Snapshot).Untested", Kind: graph.KindMethod, Name: "Untested", Receiver: "*Snapshot", PackageName: "search", File: "search/search.go", Line: 7, EndLine: 7},
		{ID: "example.com/app/search::(*Snapshot).reach", Kind: graph.KindMethod, Name: "reach", Receiver: "*Snapshot", PackageName: "search", File: "search/search.go", Line: 9, EndLine: 9},
		{ID: "example.com/app/search::SymbolIdentity", Kind: graph.KindStruct, Name: "SymbolIdentity", PackageName: "search", File: "search/search.go", Line: 11, EndLine: 11},
		{ID: "example.com/app/search::symbolIdentity", Kind: graph.KindFunction, Name: "symbolIdentity", PackageName: "search", File: "search/search.go", Line: 13, EndLine: 13},
	}}
}

func TestFileSelectorBareNameNeverMatchesMethod(t *testing.T) {
	g := sameNameFunctionAndMethodGraph()
	for _, testCase := range []struct {
		selector string
		want     string
	}{
		{"search/search.go:Untested", "example.com/app/search::Untested"},
		{"search/search.go:Snapshot.Untested", "example.com/app/search::(*Snapshot).Untested"},
		{"example.com/app/search/search.go:Untested", "example.com/app/search::Untested"},
	} {
		var matched []string
		for _, symbol := range g.Symbols {
			if matchSymbolExact(symbol, testCase.selector) {
				matched = append(matched, symbol.ID)
			}
			if MatchSymbol(symbol, testCase.selector) != matchSymbolExact(symbol, testCase.selector) {
				t.Errorf("MatchSymbol and matchSymbolExact disagree on %s for %q", symbol.ID, testCase.selector)
			}
		}
		if len(matched) != 1 || matched[0] != testCase.want {
			t.Errorf("%q matched %v, want [%s]", testCase.selector, matched, testCase.want)
		}
		target, ok := qualifiedTarget(g, testCase.selector)
		if !ok || target.ID != testCase.want {
			t.Errorf("qualifiedTarget(%q) = %s, %v", testCase.selector, target.ID, ok)
		}
	}
}

func TestEveryPrintedFileSelectorIsUnique(t *testing.T) {
	g := sameNameFunctionAndMethodGraph()
	for _, symbol := range g.Symbols {
		selector := fileSelector(symbol)
		target, ok := qualifiedTarget(g, selector)
		if !ok || target.ID != symbol.ID {
			t.Errorf("printed selector %q resolves to %q (%v), want %q", selector, target.ID, ok, symbol.ID)
		}
	}
}

func TestFileSelectorHintNamesReceiverSelector(t *testing.T) {
	g := sameNameFunctionAndMethodGraph()
	if hint := FileSelectorHint(g, "search/search.go:reach"); !strings.Contains(hint, "search/search.go:Snapshot.reach") {
		t.Fatalf("hint = %q", hint)
	}
	for _, query := range []string{"search/search.go:Snapshot.reach", "reach", "search/other.go:reach"} {
		if hint := FileSelectorHint(g, query); hint != "" {
			t.Errorf("unexpected hint for %q: %q", query, hint)
		}
	}
}

func TestSourcePrefersCaseSensitiveNameAndResolvesFileSelectors(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 14)
	lines[4] = "func Untested() {}"
	lines[6] = "func (s *Snapshot) Untested() {}"
	lines[8] = "func (s *Snapshot) reach() {}"
	lines[10] = "type SymbolIdentity struct{}"
	lines[12] = "func symbolIdentity() {}"
	lines[0] = "package search"
	if err := os.MkdirAll(filepath.Join(root, "search"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "search", "search.go"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := sameNameFunctionAndMethodGraph()
	for query, want := range map[string]string{
		"search/search.go:Untested":          "func Untested() {}",
		"search/search.go:Snapshot.Untested": "func (s *Snapshot) Untested() {}",
		"SymbolIdentity":                     "type SymbolIdentity struct{}",
		"symbolIdentity":                     "func symbolIdentity() {}",
	} {
		source, err := Source(g, root, query)
		if err != nil || !strings.Contains(source, want) {
			t.Errorf("Source(%q) = %q, %v; want %q", query, source, err, want)
		}
	}
	if _, err := Source(g, root, "search/search.go:reach"); err == nil || !strings.Contains(err.Error(), "search/search.go:Snapshot.reach") {
		t.Errorf("Source of a bare method file selector should hint the receiver selector, got %v", err)
	}
}
