package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// An explicit MCP or build path inside a project resolves to that project's
// root before guards, graph loading and session storage use it.
func TestExplicitSubdirectoryPathsUseTheProjectRoot(t *testing.T) {
	repo := t.TempDir()
	for name, data := range map[string]string{
		"go.mod":     "module example.test/mcproot\n\ngo 1.26\n",
		"main.go":    "package main\n\nfunc main() {}\n",
		"sub/sub.go": "package sub\n\nfunc Sub() {}\n",
		".git/HEAD":  "ref: refs/heads/main\n",
	} {
		path := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sub := filepath.Join(repo, "sub")
	want, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	same := func(label, got string) {
		t.Helper()
		resolved, err := filepath.EvalSymlinks(got)
		if err != nil || resolved != want {
			t.Errorf("%s = %q, want project root %q", label, got, want)
		}
	}

	for _, command := range []string{"mcp", "build"} {
		same(command+" guard root", callerCommandRoot([]string{command, sub}))
	}
	options, err := parseMCPArgs([]string{sub})
	if err != nil {
		t.Fatal(err)
	}
	_, absRoot, _, err := prepareMCPGraphWithState(options)
	if err != nil {
		t.Fatalf("prepare MCP graph: %v", err)
	}
	same("MCP graph root", absRoot)
}
