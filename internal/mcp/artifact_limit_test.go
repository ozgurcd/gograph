package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestPersistedGraphKeepsFallbackWhenArtifactIsOversized(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".gograph", "graph.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(graph.MaxArtifactBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	fallback := &graph.Graph{Root: root}
	if got := persistedGraph(fallback); got != fallback {
		t.Fatalf("oversized persisted graph replaced fallback: got %p want %p", got, fallback)
	}
}
