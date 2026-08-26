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
