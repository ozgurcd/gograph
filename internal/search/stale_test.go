package search_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

// makeGoFile writes a minimal Go file in dir and sets its mtime to t.
func makeGoFile(t *testing.T, dir, name string, mtime time.Time) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("package x\n"), 0600); err != nil {
		t.Fatalf("makeGoFile: %v", err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatalf("makeGoFile Chtimes: %v", err)
	}
	return p
}

func graphWithTime(t time.Time) *graph.Graph {
	return &graph.Graph{GeneratedAt: t}
}

// ---------------------------------------------------------------------------
// TestStale_NotStale: all source files are older than the graph → is_stale false
// ---------------------------------------------------------------------------

func TestStale_NotStale(t *testing.T) {
	dir := t.TempDir()
	graphTime := time.Now()

	// Put a source file that is 2 minutes OLDER than the graph.
	makeGoFile(t, dir, "old.go", graphTime.Add(-2*time.Minute))

	g := graphWithTime(graphTime)
	sr := search.Stale(g, dir)

	if sr.IsStale {
		t.Error("expected is_stale=false when all source files are older than the graph")
	}
	if len(sr.ChangedFiles) != 0 {
		t.Errorf("expected no changed_files, got %v", sr.ChangedFiles)
	}
	if sr.GraphAge == "" {
		t.Error("GraphAge must always be set")
	}
}

// ---------------------------------------------------------------------------
// TestStale_Stale: at least one source file is newer than the graph → is_stale true
// ---------------------------------------------------------------------------

func TestStale_Stale(t *testing.T) {
	dir := t.TempDir()
	graphTime := time.Now().Add(-5 * time.Minute)

	// Write a file newer than the graph.
	makeGoFile(t, dir, "new.go", time.Now())
	// Write a file older than the graph (should not appear in changed_files).
	makeGoFile(t, dir, "old.go", graphTime.Add(-1*time.Minute))

	g := graphWithTime(graphTime)
	sr := search.Stale(g, dir)

	if !sr.IsStale {
		t.Error("expected is_stale=true when a source file is newer than the graph")
	}
	if len(sr.ChangedFiles) != 1 {
		t.Errorf("expected exactly 1 changed file, got %v", sr.ChangedFiles)
	}
	if !strings.Contains(sr.ChangedFiles[0], "new.go") {
		t.Errorf("expected new.go in changed_files, got %v", sr.ChangedFiles)
	}
}

// ---------------------------------------------------------------------------
// TestStale_NewestSourceFields: NewestSourceMtime and NewestSourceFile are populated
// even when is_stale=false.
// ---------------------------------------------------------------------------

func TestStale_NewestSourceFields(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	graphTime := now.Add(1 * time.Hour) // graph is from the future → not stale

	older := now.Add(-10 * time.Minute)
	newer := now.Add(-2 * time.Minute)

	makeGoFile(t, dir, "a.go", older)
	makeGoFile(t, dir, "b.go", newer) // b.go is the newest source file

	g := graphWithTime(graphTime)
	sr := search.Stale(g, dir)

	if sr.IsStale {
		t.Error("expected is_stale=false (graph is in the future)")
	}
	if sr.NewestSourceMtime == "" {
		t.Error("NewestSourceMtime must be set when Go source files exist")
	}
	if sr.NewestSourceFile == "" {
		t.Error("NewestSourceFile must be set when Go source files exist")
	}
	if !strings.Contains(sr.NewestSourceFile, "b.go") {
		t.Errorf("expected NewestSourceFile to be b.go (the newest), got %q", sr.NewestSourceFile)
	}
}

// ---------------------------------------------------------------------------
// TestStale_EmptyDir: no source files → is_stale false, no newest fields
// ---------------------------------------------------------------------------

func TestStale_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	g := graphWithTime(time.Now())
	sr := search.Stale(g, dir)

	if sr.IsStale {
		t.Error("expected is_stale=false for empty directory")
	}
	if sr.NewestSourceMtime != "" {
		t.Errorf("expected no NewestSourceMtime for empty dir, got %q", sr.NewestSourceMtime)
	}
	if sr.NewestSourceFile != "" {
		t.Errorf("expected no NewestSourceFile for empty dir, got %q", sr.NewestSourceFile)
	}
}

// ---------------------------------------------------------------------------
// TestStale_GraphAgeAlwaysSet
// ---------------------------------------------------------------------------

func TestStale_GraphAgeAlwaysSet(t *testing.T) {
	dir := t.TempDir()
	ref := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	g := graphWithTime(ref)

	sr := search.Stale(g, dir)

	if sr.GraphAge == "" {
		t.Error("GraphAge must be non-empty")
	}
	// Should contain the date in the expected format.
	if !strings.Contains(sr.GraphAge, "2026-01-15") {
		t.Errorf("GraphAge should contain the build date, got %q", sr.GraphAge)
	}
}

// ---------------------------------------------------------------------------
// TestStale_SkipsGographDir: files under .gograph/ must not affect staleness
// ---------------------------------------------------------------------------

func TestStale_SkipsGographDir(t *testing.T) {
	dir := t.TempDir()
	graphTime := time.Now().Add(-5 * time.Minute)

	// Create a .gograph dir with a stale-looking .go file.
	gographDir := filepath.Join(dir, ".gograph")
	if err := os.MkdirAll(gographDir, 0755); err != nil {
		t.Fatal(err)
	}
	// This file is newer but should be skipped.
	if err := os.WriteFile(filepath.Join(gographDir, "fake.go"), []byte("package x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(gographDir, "fake.go"), time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}

	g := graphWithTime(graphTime)
	sr := search.Stale(g, dir)

	// Should be NOT stale because the only .go file is under .gograph/ (skipped).
	if sr.IsStale {
		t.Errorf("expected is_stale=false — files under .gograph/ must be skipped, got changed_files=%v", sr.ChangedFiles)
	}
}

func TestStale_SkipsGeneratedFiles(t *testing.T) {
	dir := t.TempDir()
	graphTime := time.Now().Add(-5 * time.Minute)
	makeGoFile(t, dir, "api_generated.go", time.Now())

	sr := search.Stale(graphWithTime(graphTime), dir)
	if sr.IsStale {
		t.Fatalf("generated files must not make the graph stale: %v", sr.ChangedFiles)
	}
}
