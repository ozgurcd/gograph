package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestWriteJSONPreservesExistingFileWhenMarshalFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "graph.json")
	original := []byte(`{"version":"existing"}`)
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}

	if err := writeJSON(path, make(chan int)); err == nil {
		t.Fatal("expected marshal failure")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("existing graph changed after failed write: %q", got)
	}
}

func TestWriteJSONAtomicallyReplacesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "graph.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(path, map[string]string{"version": "new"}); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{\n  \"version\": \"new\"\n}" {
		t.Fatalf("unexpected JSON: %s", data)
	}
	temps, err := filepath.Glob(filepath.Join(dir, ".graph.json-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 0 {
		t.Fatalf("temporary files remain: %v", temps)
	}
}

func TestLoadGraphReanchorsMovedRepository(t *testing.T) {
	root := t.TempDir()
	oldRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(root, ".gograph", "graph.json"), &graph.Graph{Version: graph.Version, Root: oldRoot}); err != nil {
		t.Fatal(err)
	}
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(originalWD) }()

	loaded, err := loadGraph(".")
	if err != nil {
		t.Fatalf("loadGraph: %v", err)
	}
	loadedInfo, err := os.Stat(loaded.Root)
	if err != nil {
		t.Fatal(err)
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(loadedInfo, rootInfo) {
		t.Fatalf("loaded root = %q, want moved repository %q", loaded.Root, root)
	}
}
