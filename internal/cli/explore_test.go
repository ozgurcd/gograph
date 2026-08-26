package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestCLIExploreJSONReturnsNativeComposedPayload(t *testing.T) {
	g := &graph.Graph{
		Packages: []graph.PackageNode{{ID: "sample", Name: "sample", ImportPathBestEffort: "example.com/explore", Dir: ".", Files: []string{"target.go"}}},
		Files:    []graph.FileNode{{ID: "target.go", Path: "target.go", PackageName: "sample"}},
		Symbols: []graph.SymbolNode{
			{ID: "example.com/explore::Target", Name: "Target", Kind: graph.KindFunction, PackageName: "sample", File: "target.go", Line: 3, EndLine: 3},
			{ID: "example.com/explore::Caller", Name: "Caller", Kind: graph.KindFunction, PackageName: "sample", File: "target.go", Line: 5, EndLine: 5},
		},
		Calls: []graph.CallEdge{{
			CallerName: "Caller", CallerSymbolID: "example.com/explore::Caller",
			CalleeRaw: "Target", CalleeSymbolID: "example.com/explore::Target",
			File: "target.go", Line: 5,
		}},
		TestEdges: []graph.TestEdge{{TestFunc: "TestTarget", Target: "Target", File: "target_test.go", Line: 3}},
	}
	root := writeCLIParityGraph(t, g)
	if err := os.WriteFile(filepath.Join(root, "target.go"), []byte("package sample\n\nfunc Target() {}\n\nfunc Caller() { Target() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLIParityInDir(t, root, func() int {
		return Run([]string{"explore", "Target", "--limit", "1", "--json"})
	})
	if code != 0 {
		t.Fatalf("explore --json failed with code %d: %s", code, stderr)
	}
	var envelope struct {
		Command string               `json:"command"`
		Query   string               `json:"query"`
		Count   int                  `json:"count"`
		Results search.ExploreResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("explore returned invalid JSON: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if envelope.Command != "explore" || envelope.Query != "Target" || envelope.Count != 1 {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}
	result := envelope.Results
	if result.SchemaVersion != search.ExploreSchemaVersion || result.SelectedSymbol != "Target" {
		t.Fatalf("unexpected native result identity: %#v", result)
	}
	if result.Context == nil || !strings.Contains(result.Context.Source, "func Target()") {
		t.Fatalf("explore context omitted source: %#v", result.Context)
	}
	if len(result.Context.Callers) != 1 || result.Context.Callers[0].Name != "Caller" {
		t.Fatalf("explore callers = %#v, want Caller", result.Context.Callers)
	}
	if len(result.Context.TestResults) != 1 || result.Context.TestResults[0].Name != "TestTarget" {
		t.Fatalf("explore tests = %#v, want TestTarget", result.Context.TestResults)
	}
	if len(result.Impact) != 1 || result.Impact[0].Name != "Caller" {
		t.Fatalf("explore impact = %#v, want Caller", result.Impact)
	}
}

func TestCLIExploreTextDisclosesLexicalSelectionAndBounds(t *testing.T) {
	g := &graph.Graph{
		Packages: []graph.PackageNode{{ID: "sample", Name: "sample", ImportPathBestEffort: "example.com/explore", Dir: ".", Files: []string{"target.go"}}},
		Files:    []graph.FileNode{{ID: "target.go", Path: "target.go", PackageName: "sample"}},
		Symbols: []graph.SymbolNode{
			{ID: "example.com/explore::Target", Name: "Target", Kind: graph.KindFunction, PackageName: "sample", File: "target.go", Line: 3, EndLine: 3},
		},
	}
	root := writeCLIParityGraph(t, g)
	if err := os.WriteFile(filepath.Join(root, "target.go"), []byte("package sample\n\nfunc Target() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLIParityInDir(t, root, func() int {
		return Run([]string{"explore", "how", "does", "Target", "work?", "--limit", "500"})
	})
	if code != 0 {
		t.Fatalf("explore text failed with code %d: %s", code, stderr)
	}
	for _, want := range []string{
		"=== EXPLORE: how does Target work? ===",
		"SELECTED SYMBOL: Target (ranked_lexical_match)",
		"func Target()",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("explore text omitted %q:\n%s", want, stdout)
		}
	}
}

func TestCLIExploreRejectsMissingQueryAndUnknownFlags(t *testing.T) {
	for _, args := range [][]string{{"explore"}, {"explore", "Target", "--unknown"}} {
		_, stderr, code := captureCLIParityOutput(t, func() int { return Run(args) })
		if code == 0 {
			t.Fatalf("Run(%v) succeeded, stderr=%q", args, stderr)
		}
		if !strings.Contains(stderr, "explore") {
			t.Fatalf("Run(%v) error omitted command name: %q", args, stderr)
		}
	}
}

func TestCLIExploreClampsExplicitLimitToOne(t *testing.T) {
	root := writeCLIParityGraph(t, &graph.Graph{})
	stdout, stderr, code := runCLIParityInDir(t, root, func() int {
		return Run([]string{"explore", "missing", "--limit", "0", "--json"})
	})
	if code != 0 {
		t.Fatalf("explore --limit 0 failed with code %d: %s", code, stderr)
	}
	var envelope struct {
		Results search.ExploreResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("explore returned invalid JSON: %v\n%s", err, stdout)
	}
	if envelope.Results.Limit != 1 {
		t.Fatalf("explicit zero limit = %d, want clamp to 1", envelope.Results.Limit)
	}
}

func TestCLIExploreCompactUsesLowTokenNativeMode(t *testing.T) {
	g := &graph.Graph{
		Packages: []graph.PackageNode{{ID: "sample", Name: "sample", ImportPathBestEffort: "example.com/explore", Dir: ".", Files: []string{"target.go"}}},
		Files:    []graph.FileNode{{ID: "target.go", Path: "target.go", PackageName: "sample"}},
		Symbols: []graph.SymbolNode{
			{ID: "example.com/explore::Target", Name: "Target", Kind: graph.KindFunction, PackageName: "sample", File: "target.go", Line: 3, EndLine: 3},
			{ID: "example.com/explore::Caller", Name: "Caller", Kind: graph.KindFunction, PackageName: "sample", File: "target.go", Line: 5, EndLine: 5},
		},
		Calls: []graph.CallEdge{{CallerName: "Caller", CallerSymbolID: "example.com/explore::Caller", CalleeRaw: "Target", CalleeSymbolID: "example.com/explore::Target", File: "target.go", Line: 5}},
	}
	root := writeCLIParityGraph(t, g)
	if err := os.WriteFile(filepath.Join(root, "target.go"), []byte("package sample\n\nfunc Target() {}\n\nfunc Caller() { Target() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLIParityInDir(t, root, func() int {
		return Run([]string{"explore", "Target", "--compact", "--json"})
	})
	if code != 0 {
		t.Fatalf("explore --compact failed with code %d: %s", code, stderr)
	}
	var envelope struct {
		Results search.ExploreResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("compact explore returned invalid JSON: %v\n%s", err, stdout)
	}
	result := envelope.Results
	if result.Mode != search.ExploreModeCompact || result.Limit != search.CompactExploreLimit {
		t.Fatalf("compact result = mode %q limit %d", result.Mode, result.Limit)
	}
	if result.Context == nil || result.Context.Node == nil || result.Context.Source != "" || len(result.Context.Callers) != 0 || len(result.Impact) != 0 {
		t.Fatalf("compact result returned token-heavy evidence: %#v", result)
	}
	if len(result.OmittedSections) != 5 {
		t.Fatalf("compact omitted sections = %v, want five", result.OmittedSections)
	}
	textOutput, textStderr, textCode := runCLIParityInDir(t, root, func() int {
		return Run([]string{"explore", "Target", "--compact"})
	})
	if textCode != 0 {
		t.Fatalf("text explore --compact failed with code %d: %s", textCode, textStderr)
	}
	if !strings.Contains(textOutput, "mode: compact (limit 5 per section)") ||
		!strings.Contains(textOutput, "available evidence:") ||
		!strings.Contains(textOutput, "omitted sections:") ||
		strings.Contains(textOutput, "--- SOURCE ---") {
		t.Fatalf("compact text output did not expose its response contract:\n%s", textOutput)
	}
}

func TestCLIExploreDeepAddsExpandedEvidenceAndHonorsExplicitLimit(t *testing.T) {
	g := &graph.Graph{
		Packages: []graph.PackageNode{{ID: "sample", Name: "sample", ImportPathBestEffort: "example.com/explore", Dir: ".", Files: []string{"target.go"}}},
		Files:    []graph.FileNode{{ID: "target.go", Path: "target.go", PackageName: "sample"}},
		Symbols: []graph.SymbolNode{
			{ID: "example.com/explore::Target", Name: "Target", Kind: graph.KindFunction, PackageName: "sample", File: "target.go", Line: 3, EndLine: 3},
			{ID: "example.com/explore::Caller", Name: "Caller", Kind: graph.KindFunction, PackageName: "sample", File: "target.go", Line: 5, EndLine: 5},
		},
		Calls: []graph.CallEdge{{CallerName: "Caller", CallerSymbolID: "example.com/explore::Caller", CalleeRaw: "Target", CalleeSymbolID: "example.com/explore::Target", File: "target.go", Line: 5, Resolution: graph.CallResolutionStatic}},
	}
	root := writeCLIParityGraph(t, g)
	if err := os.WriteFile(filepath.Join(root, "target.go"), []byte("package sample\n\nfunc Target() {}\n\nfunc Caller() { Target() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLIParityInDir(t, root, func() int {
		return Run([]string{"explore", "Target", "--deep", "--limit", "1", "--json"})
	})
	if code != 0 {
		t.Fatalf("explore --deep failed with code %d: %s", code, stderr)
	}
	var envelope struct {
		Results search.ExploreResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("deep explore returned invalid JSON: %v\n%s", err, stdout)
	}
	result := envelope.Results
	if result.Mode != search.ExploreModeDeep || result.Limit != 1 || result.Deep == nil || result.Deep.Explanation == nil {
		t.Fatalf("deep result = %#v", result)
	}
	textOutput, textStderr, textCode := runCLIParityInDir(t, root, func() int {
		return Run([]string{"explore", "Target", "--deep"})
	})
	if textCode != 0 {
		t.Fatalf("text explore --deep failed with code %d: %s", textCode, textStderr)
	}
	for _, marker := range []string{"mode: deep (limit 25 per section)", "--- DEEP EXPLANATION ---", "--- DEEP CALLERS (showing 1 of 1) ---", "--- PACKAGE CONTEXT (showing 5 of 5) ---"} {
		if !strings.Contains(textOutput, marker) {
			t.Fatalf("deep text output missing %q:\n%s", marker, textOutput)
		}
	}
}

func TestCLIExploreRejectsConflictingResponseModes(t *testing.T) {
	_, stderr, code := captureCLIParityOutput(t, func() int {
		return Run([]string{"explore", "Target", "--compact", "--deep", "--intention", "test conflicting explore modes"})
	})
	if code == 0 || !strings.Contains(stderr, "mutually exclusive") {
		t.Fatalf("conflicting explore modes = code %d stderr %q", code, stderr)
	}
}
