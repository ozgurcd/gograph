package rootfind

import (
	"os"
	"path/filepath"
	"testing"
)

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, data string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// An ancestor .gograph above a Git boundary belongs to another project, for
// example a non-repository workspace that holds many linked worktrees.
func TestFindRootFromStopsAtGitBoundary(t *testing.T) {
	outer := t.TempDir()
	mustMkdir(t, filepath.Join(outer, ".gograph", "sessions"))
	repo := filepath.Join(outer, "repo")
	mustMkdir(t, filepath.Join(repo, ".git"))
	worktree := filepath.Join(outer, "worktree")
	mustWrite(t, filepath.Join(worktree, ".git"), "gitdir: "+filepath.Join(repo, ".git", "worktrees", "wt")+"\n")

	for _, start := range []string{repo, filepath.Join(repo, "internal", "pkg"), worktree, filepath.Join(worktree, "cmd")} {
		if got, ok := FindRootFrom(start); ok {
			t.Errorf("FindRootFrom(%q) = (%q, true), want no root above the Git boundary", start, got)
		}
	}

	mustMkdir(t, filepath.Join(repo, ".gograph"))
	if got, ok := FindRootFrom(filepath.Join(repo, "internal")); !ok || got != repo {
		t.Errorf("FindRootFrom(repo/internal) = (%q, %v), want (%q, true)", got, ok, repo)
	}
}

// A nested go.mod is its own project, even below an indexed repository.
func TestProjectRootIsNearestGitOrGoMod(t *testing.T) {
	repo := t.TempDir()
	mustMkdir(t, filepath.Join(repo, ".git"))
	mustMkdir(t, filepath.Join(repo, ".gograph"))
	nested := filepath.Join(repo, "tools", "menubar")
	mustWrite(t, filepath.Join(nested, "go.mod"), "module example.test/menubar\n")
	start := filepath.Join(nested, "internal")
	for name, find := range map[string]func(string) (string, bool){"ProjectRootFrom": ProjectRootFrom, "FindRepositoryRootFrom": FindRepositoryRootFrom} {
		if got, ok := find(start); !ok || got != nested {
			t.Errorf("%s(nested) = (%q, %v), want (%q, true)", name, got, ok, nested)
		}
	}
	if got, ok := FindRootFrom(start); ok {
		t.Errorf("FindRootFrom(nested) = (%q, true), want no root above the nested go.mod", got)
	}
	if got, ok := ProjectRootFrom(filepath.Join(repo, "cmd")); !ok || got != repo {
		t.Errorf("ProjectRootFrom(repo/cmd) = (%q, %v), want (%q, true)", got, ok, repo)
	}
}

// Sessions, queries and cleanup resolve the same repository root from a
// subdirectory of an unindexed checkout, never an ancestor workspace.
func TestFindRootUsesRepositoryRootInsideUnindexedCheckout(t *testing.T) {
	outer := t.TempDir()
	mustMkdir(t, filepath.Join(outer, ".gograph", "sessions"))
	worktree := filepath.Join(outer, "worktree")
	mustWrite(t, filepath.Join(worktree, ".git"), "gitdir: elsewhere\n")
	mustWrite(t, filepath.Join(worktree, "go.mod"), "module example.test/wt\n")
	sub := filepath.Join(worktree, "internal", "pkg")
	mustMkdir(t, sub)

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	got, err := filepath.EvalSymlinks(FindRoot())
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("FindRoot() = %q, want worktree root %q", got, want)
	}
	repoRoot, err := filepath.EvalSymlinks(FindRepositoryRoot())
	if err != nil || repoRoot != got {
		t.Fatalf("FindRepositoryRoot() = %q (%v), want the session root %q", repoRoot, err, got)
	}
}
