package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ozgurcd/gograph/internal/graph"
)

func chdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
}

func setupGraphDir(t *testing.T, g *graph.Graph) {
	t.Helper()
	currentPolicyGraph(g)
	if err := os.MkdirAll(".gograph", 0750); err != nil {
		t.Fatalf("mkdir .gograph: %v", err)
	}
	if err := writeJSON(".gograph/graph.json", g); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
}

func TestSnapshotSaveAndList(t *testing.T) {
	tmpDir := t.TempDir()
	origWd, _ := os.Getwd()
	chdir(t, tmpDir)
	defer func() { chdir(t, origWd) }()

	g := &graph.Graph{
		Version:     graph.Version,
		GeneratedAt: time.Now(),
		Symbols: []graph.SymbolNode{
			{Name: "Foo"},
		},
	}
	setupGraphDir(t, g)

	// Save
	if code := runSnapshot([]string{"save", "v1"}); code != 0 {
		t.Fatalf("expected snapshot save to succeed, got %d", code)
	}

	// List
	if code := runSnapshot([]string{"list"}); code != 0 {
		t.Fatalf("expected snapshot list to succeed, got %d", code)
	}
}

func TestSnapshotDiffImproved(t *testing.T) {
	tmpDir := t.TempDir()
	origWd, _ := os.Getwd()
	chdir(t, tmpDir)
	defer func() { chdir(t, origWd) }()

	// Old graph (lots of imports)
	gOld := &graph.Graph{
		Version:     graph.Version,
		GeneratedAt: time.Now(),
		Imports:     make([]graph.ImportEdge, 100),
	}
	setupGraphDir(t, gOld)
	runSnapshot([]string{"save", "base"})

	// New graph (fewer imports -> improved coupling edges)
	gNew := &graph.Graph{
		Version:     graph.Version,
		GeneratedAt: time.Now(),
		Imports:     make([]graph.ImportEdge, 50),
	}
	setupGraphDir(t, gNew)

	if code := runSnapshot([]string{"diff", "base"}); code != 0 {
		t.Fatalf("expected snapshot diff to succeed, got %d", code)
	}
}

func TestSnapshotDiffWorse(t *testing.T) {
	tmpDir := t.TempDir()
	origWd, _ := os.Getwd()
	chdir(t, tmpDir)
	defer func() { chdir(t, origWd) }()

	gOld := &graph.Graph{
		Version:     graph.Version,
		GeneratedAt: time.Now(),
		Imports:     make([]graph.ImportEdge, 10),
	}
	setupGraphDir(t, gOld)
	runSnapshot([]string{"save", "base"})

	// New graph (more imports -> worse coupling edges)
	gNew := &graph.Graph{
		Version:     graph.Version,
		GeneratedAt: time.Now(),
		Imports:     make([]graph.ImportEdge, 50),
	}
	setupGraphDir(t, gNew)

	if code := runSnapshot([]string{"diff", "base"}); code != 0 {
		t.Fatalf("expected snapshot diff to succeed, got %d", code)
	}
}

func TestSnapshotDrop(t *testing.T) {
	tmpDir := t.TempDir()
	origWd, _ := os.Getwd()
	chdir(t, tmpDir)
	defer func() { chdir(t, origWd) }()

	g := &graph.Graph{
		Version:     graph.Version,
		GeneratedAt: time.Now(),
	}
	setupGraphDir(t, g)
	runSnapshot([]string{"save", "v1"})

	// Drop existing
	if code := runSnapshot([]string{"drop", "v1"}); code != 0 {
		t.Fatalf("expected snapshot drop to succeed, got %d", code)
	}

	// Drop missing
	if code := runSnapshot([]string{"drop", "v1"}); code != 1 {
		t.Fatalf("expected snapshot drop missing to fail, got %d", code)
	}
}

func TestSnapshotValidation(t *testing.T) {
	tmpDir := t.TempDir()
	origWd, _ := os.Getwd()
	chdir(t, tmpDir)
	defer func() { chdir(t, origWd) }()

	g := &graph.Graph{
		Version:     graph.Version,
		GeneratedAt: time.Now(),
	}
	setupGraphDir(t, g)

	invalidNames := []string{
		"../../traversal",
		"v1.json",
		"invalid/name",
		"name\\with\\backslashes",
		"invalid$",
	}

	for _, name := range invalidNames {
		if code := runSnapshot([]string{"save", name}); code != 1 {
			t.Errorf("expected save with invalid name %q to fail, got %d", name, code)
		}
		if code := runSnapshot([]string{"diff", name}); code != 1 {
			t.Errorf("expected diff with invalid name %q to fail, got %d", name, code)
		}
		if code := runSnapshot([]string{"drop", name}); code != 1 {
			t.Errorf("expected drop with invalid name %q to fail, got %d", name, code)
		}
	}

	validNames := []string{
		"v1",
		"base",
		"my-snapshot_123",
	}

	for _, name := range validNames {
		if code := runSnapshot([]string{"save", name}); code != 0 {
			t.Errorf("expected save with valid name %q to succeed, got %d", name, code)
		}
	}
}

func TestSnapshotCommandsRejectLinkedDirectory(t *testing.T) {
	root := t.TempDir()
	origWd, _ := os.Getwd()
	chdir(t, root)
	defer func() { chdir(t, origWd) }()
	setupGraphDir(t, &graph.Graph{Version: graph.Version, GeneratedAt: time.Now()})

	outside := t.TempDir()
	outsideSnapshot := filepath.Join(outside, "guarded.json")
	if err := os.WriteFile(outsideSnapshot, []byte("KEEP"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".gograph", "snapshots")); err != nil {
		t.Skipf("create snapshots symlink: %v", err)
	}
	if code := runSnapshot([]string{"save", "created"}); code != 1 {
		t.Fatalf("snapshot save through linked directory exit = %d, want 1", code)
	}
	if code := runSnapshot([]string{"drop", "guarded"}); code != 1 {
		t.Fatalf("snapshot drop through linked directory exit = %d, want 1", code)
	}
	data, err := os.ReadFile(outsideSnapshot)
	if err != nil || string(data) != "KEEP" {
		t.Fatalf("outside snapshot = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "created.json")); !os.IsNotExist(err) {
		t.Fatalf("outside snapshot was created: %v", err)
	}
}
