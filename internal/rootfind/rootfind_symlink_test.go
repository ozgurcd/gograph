package rootfind

import (
	"os"
	"path/filepath"
	"testing"
)

// A start directory reached through a symlink walks its real ancestors, so the
// nearest real .git or go.mod decides, not the link's lexical parents.
func TestSymlinkedStartUsesRealAncestors(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	mustMkdir(t, filepath.Join(repo, ".git"))
	sub := filepath.Join(repo, "sub")
	mustMkdir(t, sub)
	link := filepath.Join(t.TempDir(), "link-to-sub")
	if err := os.Symlink(sub, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	want := realPath(t, repo)
	for name, got := range map[string]string{
		"ProjectRootFrom":    func() string { root, _ := ProjectRootFrom(link); return root }(),
		"ResolveProjectRoot": ResolveProjectRoot(link),
		"file below link":    ResolveProjectRoot(filepath.Join(link, "x.go")),
	} {
		if got == "" || realPath(t, got) != want {
			t.Errorf("%s(link) = %q, want real root %q", name, got, want)
		}
	}

	chdirTest(t, link)
	t.Setenv("PWD", link)
	if got := FindRoot(); realPath(t, got) != want {
		t.Errorf("FindRoot() with PWD=%s = %q, want %q", link, got, want)
	}
}

// Without links the lexical spelling of the root is kept, so callers that
// compare against their own paths keep matching.
func TestUnlinkedStartKeepsLexicalRoot(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	mustMkdir(t, filepath.Join(repo, ".git"))
	mustMkdir(t, filepath.Join(repo, "a", "b"))
	if got, ok := ProjectRootFrom(filepath.Join(repo, "a", "b")); !ok || got != repo {
		t.Fatalf("ProjectRootFrom = (%q, %v), want (%q, true)", got, ok, repo)
	}
}
