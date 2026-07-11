package search_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestSourceReturnsErrorWhenEveryMatchIsUnreadable(t *testing.T) {
	root := t.TempDir()
	g := &graph.Graph{Symbols: []graph.SymbolNode{{
		ID:      "example.com/project::Missing",
		Name:    "Missing",
		Kind:    graph.KindFunction,
		File:    "missing.go",
		Line:    1,
		EndLine: 2,
	}}}

	source, err := search.Source(g, root, "Missing")
	if err == nil {
		t.Fatalf("expected an unreadable source error, got source %q", source)
	}
	if !strings.Contains(err.Error(), filepath.Join("missing.go")) {
		t.Fatalf("error should identify the unreadable file: %v", err)
	}
}
