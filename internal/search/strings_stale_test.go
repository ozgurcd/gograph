package search

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestStringLiteralsSearchesAddedFilesAndExplainsMissingOnes(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("kept.go", "package app\n\nconst kept = \"marker-kept\"\n")
	write("added.go", "package app\n\nconst added = \"marker-added\"\n")
	g := &graph.Graph{Root: root, Files: []graph.FileNode{{Path: "kept.go"}, {Path: "gone.go"}}}

	results, diagnostic, err := StringLiteralsIn(g, []string{"marker"}, true, []string{"added.go", "kept.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].File != "added.go" || results[1].File != "kept.go" {
		t.Fatalf("results = %+v", results)
	}
	if !strings.Contains(results[0].Detail, "added since the last build") {
		t.Errorf("added-file detail = %q", results[0].Detail)
	}
	for _, want := range []string{"1 indexed file(s) no longer exist", "gone.go", "gograph build ."} {
		if !strings.Contains(diagnostic, want) {
			t.Errorf("diagnostic %q lacks %q", diagnostic, want)
		}
	}
	if results, _, _ := StringLiterals(g, []string{"marker-added"}, true); len(results) != 0 {
		t.Errorf("StringLiterals searched a file outside the graph: %+v", results)
	}
}

func TestStringSearchDiagnosticNamesUnreadableReason(t *testing.T) {
	if got := stringSearchDiagnostic(nil, nil, nil); got != "" {
		t.Fatalf("diagnostic without problems = %q", got)
	}
	got := stringSearchDiagnostic(nil, []string{"a.go"}, errors.New("not a regular file"))
	if !strings.Contains(got, "could not be read safely (for example a.go; not a regular file)") {
		t.Fatalf("diagnostic = %q", got)
	}
}
