package mcp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// These tests use real git. The fixture is built before any GIT_* variable is
// set, with a clean environment of its own.

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			env = append(env, entry)
		}
	}
	cmd.Env = append(env, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// repoWithWorktree makes <base>/<name> with a worktree <base>/<name>-wt.
func repoWithWorktree(t *testing.T, base, name string) (repo, worktree string) {
	t.Helper()
	repo, worktree = filepath.Join(base, name), filepath.Join(base, name+"-wt")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "init")
	runGit(t, repo, "worktree", "add", "-q", "-b", name+"-b", worktree)
	return repo, worktree
}

func canon(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestGitWorktreeRootsIgnoresRepositorySelectingEnvironment(t *testing.T) {
	base := canon(t, t.TempDir())
	own, ownWt := repoWithWorktree(t, base, "own")
	foreign, foreignWt := repoWithWorktree(t, base, "foreign")
	for name, poison := range map[string][]string{
		"GIT_DIR":          {"GIT_DIR=" + filepath.Join(foreign, ".git")},
		"GIT_WORK_TREE":    {"GIT_DIR=" + filepath.Join(foreign, ".git"), "GIT_WORK_TREE=" + foreign},
		"GIT_COMMON_DIR":   {"GIT_COMMON_DIR=" + filepath.Join(foreign, ".git")},
		"GIT_CONFIG_COUNT": {"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.worktree", "GIT_CONFIG_VALUE_0=" + foreign},
	} {
		t.Run(name, func(t *testing.T) {
			for _, entry := range poison {
				key, value, _ := strings.Cut(entry, "=")
				t.Setenv(key, value)
			}
			roots, err := gitWorktreeRoots(context.Background(), own)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(roots, ownWt) {
				t.Fatalf("own worktree %s missing from %v", ownWt, roots)
			}
			if slices.Contains(roots, foreignWt) || slices.Contains(roots, foreign) {
				t.Fatalf("foreign checkout leaked into %v", roots)
			}
		})
	}
}

func TestScopeRouterWithPoisonedGitEnvironmentAnswersOnlyOwnWorktrees(t *testing.T) {
	base := canon(t, t.TempDir())
	own, ownWt := repoWithWorktree(t, base, "own")
	_, foreignWt := repoWithWorktree(t, base, "foreign")
	t.Setenv("GIT_DIR", filepath.Join(base, "foreign", ".git"))
	t.Setenv("GIT_WORK_TREE", filepath.Join(base, "foreign"))
	router, err := NewScopeRouter(ScopeConfig{Root: own, Load: func(string) (ScopeHandlers, error) { return ScopeHandlers{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if sel, err := router.Resolve(context.Background(), ownWt); err != nil || sel.Root != ownWt {
		t.Fatalf("own worktree must resolve: %+v, %v", sel, err)
	}
	var scopeErr *OutOfScopeError
	if _, err := router.Resolve(context.Background(), foreignWt); !errors.As(err, &scopeErr) {
		t.Fatalf("a foreign repository's worktree must be refused, got %v", err)
	}
}

func TestGitWorktreeRootsRejectsListedPathOfAnotherRepository(t *testing.T) {
	base := canon(t, t.TempDir())
	own, ownWt := repoWithWorktree(t, base, "own")
	foreign, _ := repoWithWorktree(t, base, "foreign")
	// Worktree metadata lives inside .git and names any path: forge an entry in
	// own's repository that points at the foreign checkout.
	evil := filepath.Join(own, ".git", "worktrees", "evil")
	if err := os.MkdirAll(evil, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, content := range map[string]string{
		"gitdir":    filepath.Join(foreign, ".git") + "\n",
		"HEAD":      "ref: refs/heads/main\n",
		"commondir": "../..\n",
	} {
		if err := os.WriteFile(filepath.Join(evil, file), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := exec.Command("git", "-C", own, "worktree", "list", "--porcelain").Output()
	if err != nil || !strings.Contains(string(out), "worktree "+foreign+"\n") {
		t.Fatalf("precondition: git must list the forged entry (%v):\n%s", err, out)
	}
	roots, err := gitWorktreeRoots(context.Background(), own)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(roots, foreign) {
		t.Fatalf("a listed checkout of another repository must be dropped: %v", roots)
	}
	if !slices.Contains(roots, ownWt) {
		t.Fatalf("the real worktree must stay: %v", roots)
	}
}

func TestGitWorktreeRootsOnNonGitDirectoryHasNoWorktrees(t *testing.T) {
	roots, err := gitWorktreeRoots(context.Background(), t.TempDir())
	if err != nil || len(roots) != 0 {
		t.Fatalf("got %v, %v", roots, err)
	}
}

func TestRefusalNamesOnlyTheTwoRootsAndOneWording(t *testing.T) {
	h := newScopeHarness(t, 0, 0, nil, nil)
	for name, asked := range map[string]string{
		"existing outside": h.other,
		"missing":          filepath.Join(h.other, "nope"),
	} {
		_, err := h.router.Resolve(context.Background(), asked)
		if err == nil {
			t.Fatalf("%s: want refusal", name)
		}
		text := err.Error()
		if strings.Contains(text, h.wt1) || strings.Contains(text, h.wt2) {
			t.Errorf("%s: refusal leaks another worktree path: %s", name, text)
		}
		if strings.Contains(text, "not an existing directory") {
			t.Errorf("%s: refusal is an existence oracle: %s", name, text)
		}
	}
	_, e1 := h.router.Resolve(context.Background(), h.other)
	_, e2 := h.router.Resolve(context.Background(), filepath.Join(h.other, "nope"))
	if strings.ReplaceAll(e1.Error(), h.other, "X") != strings.ReplaceAll(strings.ReplaceAll(e2.Error(), filepath.Join(h.other, "nope"), "X"), h.other, "X") {
		t.Errorf("wording differs between an existing and a missing path:\n%s\n%s", e1, e2)
	}
}

func TestClientRootsNamingSeveralCheckoutsAreRefused(t *testing.T) {
	h := newScopeHarness(t, 0, 0, nil, nil)
	for name, roots := range map[string][]string{
		"two worktrees":        {h.wt1, h.wt2},
		"primary and worktree": {h.primary, h.wt1},
	} {
		_, err := h.router.ResolveClientRoots(context.Background(), roots)
		var scopeErr *OutOfScopeError
		if !errors.As(err, &scopeErr) || !strings.Contains(err.Error(), "pass root") {
			t.Errorf("%s: want an ambiguity refusal, got %v", name, err)
		}
	}
	// the same checkout twice (a root and its subdirectory) is not ambiguous
	sel, err := h.router.ResolveClientRoots(context.Background(), []string{h.wt1, filepath.Join(h.wt1, "sub")})
	if err != nil || sel.Root != h.wt1 {
		t.Fatalf("got %+v, %v", sel, err)
	}
}
