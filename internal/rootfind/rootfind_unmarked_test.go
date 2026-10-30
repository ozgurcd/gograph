package rootfind

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func chdirTest(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
}

func realPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// Without .git or go.mod the start directory is the project root for every
// operation; an unrelated ancestor's .gograph state is never used.
func TestUnmarkedDirectoryNeverUsesAncestorState(t *testing.T) {
	outer := t.TempDir()
	mustMkdir(t, filepath.Join(outer, ".gograph", "sessions"))
	mustWrite(t, filepath.Join(outer, ".gograph", "graph.json"), `{"version":"`+graph.Version+`"}`+"\n")
	child := filepath.Join(outer, "child", "pkg")
	mustMkdir(t, child)

	if got, ok := FindRootFrom(child); ok {
		t.Errorf("FindRootFrom(child) = (%q, true), want no indexed root", got)
	}
	if got, ok := FindRepositoryRootFrom(child); ok {
		t.Errorf("FindRepositoryRootFrom(child) = (%q, true), want no project root", got)
	}
	if got := ResolveProjectRoot(child); realPath(t, got) != realPath(t, child) {
		t.Errorf("ResolveProjectRoot(child) = %q, want the child itself", got)
	}
	chdirTest(t, child)
	for name, find := range map[string]func() string{"FindRoot": FindRoot, "FindRepositoryRoot": FindRepositoryRoot} {
		if got := find(); got != "." && realPath(t, got) != realPath(t, child) {
			t.Errorf("%s() = %q, want the child or \".\"", name, got)
		}
	}
}
