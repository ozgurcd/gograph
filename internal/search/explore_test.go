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

func TestExploreModeDefaults(t *testing.T) {
	tests := []struct {
		mode ExploreMode
		want int
	}{
		{mode: ExploreModeStandard, want: DefaultExploreLimit},
		{mode: ExploreModeCompact, want: CompactExploreLimit},
		{mode: ExploreModeDeep, want: DeepExploreLimit},
	}
	for _, test := range tests {
		result := Explore(&graph.Graph{}, t.TempDir(), "missing", ExploreOptions{Mode: test.mode})
		if result.Mode != test.mode || result.Limit != test.want {
			t.Fatalf("mode %q = result mode %q limit %d, want %d", test.mode, result.Mode, result.Limit, test.want)
		}
	}
	if got := DefaultExploreLimitForMode("unknown"); got != DefaultExploreLimit {
		t.Fatalf("unknown mode default = %d, want %d", got, DefaultExploreLimit)
	}
}

func TestExploreCompactKeepsCountsAndOmitsEvidenceBodies(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package sample\n\nfunc Target() {}\n\nfunc Caller() { Target() }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: "example.com/sample::Target", Name: "Target", Kind: graph.KindFunction, PackageName: "sample", File: "sample.go", Line: 3, EndLine: 3},
			{ID: "example.com/sample::Caller", Name: "Caller", Kind: graph.KindFunction, PackageName: "sample", File: "sample.go", Line: 5, EndLine: 5},
		},
		Calls:     []graph.CallEdge{{CallerSymbolID: "example.com/sample::Caller", CallerName: "Caller", CalleeSymbolID: "example.com/sample::Target", CalleeRaw: "Target", File: "sample.go", Line: 5}},
		TestEdges: []graph.TestEdge{{TestFunc: "TestTarget", Target: "Target", TargetSymbolID: "example.com/sample::Target", File: "sample_test.go", Line: 3}},
	}

	result := Explore(g, root, "Target", ExploreOptions{Mode: ExploreModeCompact})
	if result.Mode != ExploreModeCompact || result.Limit != CompactExploreLimit {
		t.Fatalf("compact identity = mode %q limit %d", result.Mode, result.Limit)
	}
	if result.Context == nil || result.Context.Node == nil || result.Context.Source != "" {
		t.Fatalf("compact context = %#v, want node without source", result.Context)
	}
	if len(result.Context.Callers) != 0 || len(result.Context.Callees) != 0 || len(result.Context.TestResults) != 0 || len(result.Impact) != 0 {
		t.Fatalf("compact returned token-heavy bodies: context=%#v impact=%#v", result.Context, result.Impact)
	}
	if result.Totals.Callers != 1 || result.Totals.Tests != 1 || result.Totals.Impact != 1 {
		t.Fatalf("compact totals = %#v, want callers/tests/impact counts", result.Totals)
	}
	for _, section := range []string{"source", "callers", "callees", "tests", "impact"} {
		if !containsExploreSection(result.OmittedSections, section) {
			t.Fatalf("compact omitted_sections = %v, missing %q", result.OmittedSections, section)
		}
	}
	if result.Deep != nil {
		t.Fatalf("compact unexpectedly returned deep payload: %#v", result.Deep)
	}
}

func TestExploreDeepAddsBoundedExactCallAndPackageContext(t *testing.T) {
	g := deepExploreGraph()
	result := Explore(g, t.TempDir(), "Target", ExploreOptions{Mode: ExploreModeDeep, Limit: 1})
	if result.Mode != ExploreModeDeep || result.Deep == nil {
		t.Fatalf("deep result = mode %q payload %#v", result.Mode, result.Deep)
	}
	deep := result.Deep
	if deep.Depth != ExploreDeepDepth || deep.Totals.Callers != 2 || deep.Totals.Callees != 2 {
		t.Fatalf("deep traversal totals = %#v depth=%d, want two exact callers/callees at depth 3", deep.Totals, deep.Depth)
	}
	if len(deep.Callers) != 1 || len(deep.Callees) != 1 || deep.Explanation == nil {
		t.Fatalf("bounded deep payload = %#v", deep)
	}
	if deep.Totals.PackageContext <= len(deep.PackageContext) || len(deep.PackageContext) != 1 {
		t.Fatalf("deep package context = total %d returned %d, want bounded", deep.Totals.PackageContext, len(deep.PackageContext))
	}
	for _, section := range []string{"deep.callers", "deep.callees", "deep.package_context"} {
		if !containsExploreSection(result.TruncatedSections, section) {
			t.Fatalf("deep truncated_sections = %v, missing %q", result.TruncatedSections, section)
		}
	}
	for _, row := range append(append([]Result{}, deep.Callers...), deep.Callees...) {
		if row.Name == "PossibleCaller" || row.Name == "PossibleCallee" {
			t.Fatalf("deep exact traversal included possible dispatch: %#v", row)
		}
		if row.StableID == "" {
			t.Fatalf("deep exact traversal omitted stable identity: %#v", row)
		}
	}
	if got := deep.Callees[0]; got.StableID != "example.com/sample::CalleeOne" || got.File != "sample.go" || got.Line != 4 || got.CallSiteLine != 30 {
		t.Fatalf("deep callee provenance = %#v, want declared target plus source call site", got)
	}
}

func TestExploreDeepPackageContextUsesSelectedPackageInstance(t *testing.T) {
	g := deepExploreGraph()
	g.Packages = append(g.Packages, graph.PackageNode{ID: "decoy", Name: "sample", ImportPathBestEffort: "example.com/decoy/sample", Dir: "decoy", Files: []string{"decoy.go"}})
	g.Files = append(g.Files, graph.FileNode{ID: "decoy.go", Path: "decoy.go", PackageName: "sample"})
	g.Symbols = append(g.Symbols, graph.SymbolNode{ID: "example.com/decoy/sample::Decoy", Name: "Decoy", Kind: graph.KindFunction, PackageName: "sample", File: "decoy.go", Line: 1})

	result := Explore(g, t.TempDir(), "example.com/sample::Target", ExploreOptions{Mode: ExploreModeDeep, Limit: 100})
	if result.Deep == nil {
		t.Fatal("deep result omitted package context")
	}
	for _, row := range result.Deep.PackageContext {
		if row.File == "decoy.go" || row.Name == "Decoy" {
			t.Fatalf("selected package context included same-named decoy package: %#v", row)
		}
	}
}

func TestExploreDeepBoundsExplanationListsAndNarrative(t *testing.T) {
	g := deepExploreGraph()
	g.EnvReads = []graph.EnvRead{
		{Key: "FIRST_KEY", Function: "Target"},
		{Key: "SECOND_KEY", Function: "Target"},
	}
	result := Explore(g, t.TempDir(), "Target", ExploreOptions{Mode: ExploreModeDeep, Limit: 1})
	if result.Deep == nil || result.Deep.Explanation == nil {
		t.Fatalf("deep explanation = %#v", result.Deep)
	}
	explanation := result.Deep.Explanation
	if len(explanation.EnvKeys) != 1 || explanation.EnvKeys[0] != "FIRST_KEY" {
		t.Fatalf("bounded env keys = %v", explanation.EnvKeys)
	}
	if strings.Contains(explanation.Narrative, "SECOND_KEY") {
		t.Fatalf("bounded narrative leaked an omitted explanation item: %q", explanation.Narrative)
	}
	if !containsExploreSection(result.TruncatedSections, "deep.explanation") || !containsExploreSection(result.Deep.ExplanationTruncation, "env_keys") {
		t.Fatalf("explanation truncation not disclosed: top=%v fields=%v", result.TruncatedSections, result.Deep.ExplanationTruncation)
	}
}

func TestExploreDeepOmitsExpansionForAmbiguousSelection(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example.com/a::Target", Name: "Target", Kind: graph.KindFunction, File: "a.go", Line: 3},
		{ID: "example.com/b::Target", Name: "Target", Kind: graph.KindFunction, File: "b.go", Line: 3},
	}}
	result := Explore(g, t.TempDir(), "Target", ExploreOptions{Mode: ExploreModeDeep})
	if !result.Ambiguous || result.Deep != nil || !containsExploreSection(result.OmittedSections, "deep") {
		t.Fatalf("ambiguous deep result = %#v", result)
	}
}

func deepExploreGraph() *graph.Graph {
	return &graph.Graph{
		Packages: []graph.PackageNode{{ID: "sample", Name: "sample", ImportPathBestEffort: "example.com/sample", Dir: ".", Files: []string{"sample.go"}}},
		Files:    []graph.FileNode{{ID: "sample.go", Path: "sample.go", PackageName: "sample"}},
		Symbols: []graph.SymbolNode{
			{ID: "example.com/sample::CallerTwo", Name: "CallerTwo", Kind: graph.KindFunction, PackageName: "sample", File: "sample.go", Line: 1},
			{ID: "example.com/sample::CallerOne", Name: "CallerOne", Kind: graph.KindFunction, PackageName: "sample", File: "sample.go", Line: 2},
			{ID: "example.com/sample::Target", Name: "Target", Kind: graph.KindFunction, PackageName: "sample", File: "sample.go", Line: 3},
			{ID: "example.com/sample::CalleeOne", Name: "CalleeOne", Kind: graph.KindFunction, PackageName: "sample", File: "sample.go", Line: 4},
			{ID: "example.com/sample::CalleeTwo", Name: "CalleeTwo", Kind: graph.KindFunction, PackageName: "sample", File: "sample.go", Line: 5},
			{ID: "example.com/sample::PossibleCaller", Name: "PossibleCaller", Kind: graph.KindFunction, PackageName: "sample", File: "sample.go", Line: 6},
			{ID: "example.com/sample::PossibleCallee", Name: "PossibleCallee", Kind: graph.KindFunction, PackageName: "sample", File: "sample.go", Line: 7},
		},
		Calls: []graph.CallEdge{
			{CallerSymbolID: "example.com/sample::CallerOne", CallerName: "CallerOne", CalleeSymbolID: "example.com/sample::targetWrapper", CalleeRaw: "Target", File: "sample.go", Line: 20, Resolution: graph.CallResolutionStatic},
			{CallerSymbolID: "example.com/sample::targetWrapper", CalleeSymbolID: "example.com/sample::Target", Synthetic: true, Resolution: graph.CallResolutionSynthetic},
			{CallerSymbolID: "example.com/sample::CallerTwo", CallerName: "CallerTwo", CalleeSymbolID: "example.com/sample::CallerOne", CalleeRaw: "CallerOne", Resolution: graph.CallResolutionStatic},
			{CallerSymbolID: "example.com/sample::PossibleCaller", CallerName: "PossibleCaller", CalleeSymbolID: "example.com/sample::Target", CalleeRaw: "Target", Resolution: graph.CallResolutionCHA},
			{CallerSymbolID: "example.com/sample::Target", CallerName: "Target", CalleeSymbolID: "example.com/sample::calleeWrapper", CalleeRaw: "CalleeOne", File: "sample.go", Line: 30, Resolution: graph.CallResolutionStatic},
			{CallerSymbolID: "example.com/sample::calleeWrapper", CalleeSymbolID: "example.com/sample::CalleeOne", Synthetic: true, Resolution: graph.CallResolutionSynthetic},
			{CallerSymbolID: "example.com/sample::CalleeOne", CallerName: "CalleeOne", CalleeSymbolID: "example.com/sample::CalleeTwo", CalleeRaw: "CalleeTwo", Resolution: graph.CallResolutionStatic},
			{CallerSymbolID: "example.com/sample::Target", CallerName: "Target", CalleeSymbolID: "example.com/sample::PossibleCallee", CalleeRaw: "PossibleCallee", Resolution: graph.CallResolutionCHA},
		},
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
