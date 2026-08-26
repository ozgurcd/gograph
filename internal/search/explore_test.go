package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestExploreCombinesRankedSearchContextTestsAndImpact(t *testing.T) {
	root := t.TempDir()
	source := "package sample\n\nfunc Target() {}\n\nfunc Caller() { Target() }\n"
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: "example.com/sample::Target", Name: "Target", Kind: graph.KindFunction, File: "sample.go", Line: 3, EndLine: 3},
			{ID: "example.com/sample::Caller", Name: "Caller", Kind: graph.KindFunction, File: "sample.go", Line: 5, EndLine: 5},
		},
		Calls: []graph.CallEdge{
			{
				CallerName:     "Caller",
				CallerSymbolID: "example.com/sample::Caller",
				CalleeRaw:      "Target",
				CalleeSymbolID: "example.com/sample::Target",
				File:           "sample.go",
				Line:           5,
			},
		},
		TestEdges: []graph.TestEdge{
			{TestFunc: "TestTarget", Target: "Target", File: "sample_test.go", Line: 3},
		},
	}

	result := Explore(g, root, "Target", ExploreOptions{Limit: 1})
	if result.SchemaVersion != ExploreSchemaVersion {
		t.Fatalf("schema_version = %q, want %q", result.SchemaVersion, ExploreSchemaVersion)
	}
	if result.SelectedSymbol != "Target" || result.SelectionBasis != "direct_symbol_match" {
		t.Fatalf("selection = %q (%s), want direct Target", result.SelectedSymbol, result.SelectionBasis)
	}
	if result.Ambiguous {
		t.Fatal("unambiguous Target was reported ambiguous")
	}
	if result.Context == nil {
		t.Fatal("context is nil")
	}
	if !strings.Contains(result.Context.Source, "func Target()") {
		t.Fatalf("source = %q, want Target body", result.Context.Source)
	}
	if result.Totals.Callers != 1 || len(result.Context.Callers) != 1 || result.Context.Callers[0].Name != "Caller" {
		t.Fatalf("callers = total %d %#v, want Caller", result.Totals.Callers, result.Context.Callers)
	}
	if result.Totals.Tests != 1 || len(result.Context.TestResults) != 1 || result.Context.TestResults[0].Name != "TestTarget" {
		t.Fatalf("tests = total %d %#v, want TestTarget", result.Totals.Tests, result.Context.TestResults)
	}
	if result.Totals.Impact != 1 || len(result.Impact) != 1 || result.Impact[0].Name != "Caller" {
		t.Fatalf("impact = total %d %#v, want Caller", result.Totals.Impact, result.Impact)
	}
	if result.Totals.Matches <= result.Count || !containsExploreSection(result.TruncatedSections, "matches") {
		t.Fatalf("matches = returned %d total %d truncated=%v, want bounded matches", result.Count, result.Totals.Matches, result.TruncatedSections)
	}
}

func TestExploreDisclosesRankedLexicalSelectionForQuestionLikeInput(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example.com/sample::Target", Name: "Target", Kind: graph.KindFunction, File: "sample.go", Line: 3, EndLine: 3},
		{ID: "example.com/sample::WorkQueue", Name: "WorkQueue", Kind: graph.KindFunction, File: "sample.go", Line: 5, EndLine: 5},
	}}

	result := Explore(g, t.TempDir(), "how does Target work?", ExploreOptions{})
	if result.SelectedSymbol != "Target" {
		t.Fatalf("selected_symbol = %q, want Target", result.SelectedSymbol)
	}
	if result.SelectionBasis != "ranked_lexical_match" {
		t.Fatalf("selection_basis = %q, want ranked_lexical_match", result.SelectionBasis)
	}
	if result.Context == nil || len(result.Context.Nodes) != 1 {
		t.Fatalf("context = %#v, want one selected node", result.Context)
	}
	for _, match := range result.Matches {
		if match.Name == "WorkQueue" {
			t.Fatalf("question boilerplate introduced unrelated match: %#v", result.Matches)
		}
	}
}

func TestExploreExactDoesNotPromoteFuzzySearchMatch(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example.com/sample::TargetHelper", Name: "TargetHelper", Kind: graph.KindFunction, File: "sample.go", Line: 3, EndLine: 3},
	}}

	result := Explore(g, t.TempDir(), "Target", ExploreOptions{Exact: true})
	if result.Context != nil || result.SelectedSymbol != "" || result.SelectionBasis != "none" {
		t.Fatalf("exact fuzzy result selected context: %#v", result)
	}
	if result.Count == 0 {
		t.Fatal("broad lexical matches should remain visible when exact context resolution fails")
	}
}

func TestExploreFullyQualifiedIDRetainsResolvedNodeAsMatch(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example.com/sample::Target", Name: "Target", Kind: graph.KindFunction, File: "sample.go", Line: 3, EndLine: 3},
	}}

	result := Explore(g, t.TempDir(), "example.com/sample::Target", ExploreOptions{Exact: true})
	if result.Context == nil || result.SelectedSymbol != "example.com/sample::Target" {
		t.Fatalf("fully-qualified selection failed: %#v", result)
	}
	if result.Count != 1 || result.Totals.Matches != 1 || len(result.Matches) != 1 {
		t.Fatalf("resolved-node matches = returned %d total %d %#v, want one", result.Count, result.Totals.Matches, result.Matches)
	}
}

func TestExploreImpactDoesNotUseShortNameFallback(t *testing.T) {
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: "example.com/sample::Target", Name: "Target", Kind: graph.KindFunction, File: "sample.go", Line: 3},
			{ID: "example.com/sample::Caller", Name: "Caller", Kind: graph.KindFunction, File: "sample.go", Line: 5},
			{ID: "example.com/other::Unrelated", Name: "Unrelated", Kind: graph.KindFunction, File: "other.go", Line: 3},
		},
		Calls: []graph.CallEdge{
			{CallerSymbolID: "example.com/sample::Caller", CallerName: "Caller", CalleeSymbolID: "example.com/sample::Target", CalleeRaw: "Target"},
			{CallerSymbolID: "example.com/other::Unrelated", CallerName: "Unrelated", CalleeSymbolID: "example.com/sample::Target", CalleeRaw: "Target", Resolution: graph.CallResolutionCHA},
		},
	}

	result := Explore(g, t.TempDir(), "Target", ExploreOptions{})
	if result.Totals.Impact != 1 || len(result.Impact) != 1 || result.Impact[0].Name != "Caller" {
		t.Fatalf("identity impact = total %d %#v, want only Caller", result.Totals.Impact, result.Impact)
	}
}

func TestExploreImpactTraversesSyntheticForwardersWithoutReportingThem(t *testing.T) {
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: "example.com/sample::Target", Name: "Target", Kind: graph.KindMethod, File: "sample.go", Line: 3},
			{ID: "example.com/sample::Caller", Name: "Caller", Kind: graph.KindFunction, File: "sample.go", Line: 5},
		},
		Calls: []graph.CallEdge{
			{CallerSymbolID: "example.com/sample::wrapper", CalleeSymbolID: "example.com/sample::Target", Synthetic: true, Resolution: graph.CallResolutionSynthetic},
			{CallerSymbolID: "example.com/sample::Caller", CallerName: "Caller", CalleeSymbolID: "example.com/sample::wrapper", Resolution: graph.CallResolutionStatic},
		},
	}

	result := Explore(g, t.TempDir(), "Target", ExploreOptions{})
	if result.Totals.Impact != 1 || len(result.Impact) != 1 || result.Impact[0].Name != "Caller" {
		t.Fatalf("synthetic forwarding impact = total %d %#v, want only Caller", result.Totals.Impact, result.Impact)
	}
}

func TestExplorePartialSymbolNameUsesRankedSelection(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example.com/sample::TargetHelper", Name: "TargetHelper", Kind: graph.KindFunction, File: "sample.go", Line: 3},
	}}

	result := Explore(g, t.TempDir(), "Target", ExploreOptions{})
	if result.SelectedSymbol != "TargetHelper" || result.SelectionBasis != "ranked_lexical_match" {
		t.Fatalf("partial selection = %q (%s), want ranked TargetHelper", result.SelectedSymbol, result.SelectionBasis)
	}
}

func TestNormalizeExploreLimit(t *testing.T) {
	if got := NormalizeExploreLimit(0); got != DefaultExploreLimit {
		t.Fatalf("zero limit = %d, want %d", got, DefaultExploreLimit)
	}
	if got := NormalizeExploreLimit(MaxExploreLimit + 1); got != MaxExploreLimit {
		t.Fatalf("large limit = %d, want %d", got, MaxExploreLimit)
	}
	if got := NormalizeExploreLimit(7); got != 7 {
		t.Fatalf("explicit limit = %d, want 7", got)
	}
}

func containsExploreSection(sections []string, want string) bool {
	for _, section := range sections {
		if section == want {
			return true
		}
	}
	return false
}
