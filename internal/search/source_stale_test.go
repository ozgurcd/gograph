package search_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestSourceRefusesFileChangedAfterIndexing(t *testing.T) {
	root := t.TempDir()
	indexed := []byte("package app\n\nfunc Work() int {\n\treturn 1\n}\n")
	path := filepath.Join(root, "work.go")
	if err := os.WriteFile(path, indexed, 0o644); err != nil {
		t.Fatal(err)
	}
	g := &graph.Graph{
		Root:    root,
		Files:   []graph.FileNode{{ID: "work.go", Path: "work.go", PackageName: "app", ContentDigest: graph.SourceDigest(indexed)}},
		Symbols: []graph.SymbolNode{{ID: "example.com/app::Work", Name: "Work", Kind: graph.KindFunction, PackageName: "app", File: "work.go", Line: 3, EndLine: 5}},
	}
	code, err := search.Source(g, root, "Work")
	if err != nil || !strings.Contains(code, "return 1") {
		t.Fatalf("current source must be served: %q, %v", code, err)
	}

	if err := os.WriteFile(path, []byte("package app\n\n// moved down\n\nfunc Work() int {\n\treturn 2\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, err := search.Source(g, root, "Work"); err == nil || !strings.Contains(err.Error(), "changed after the graph was built") {
		t.Fatalf("a changed file must not serve its stale line range: %q, %v", code, err)
	}
}
