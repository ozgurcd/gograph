package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRevQueriesReadTheTreeOfAGitRef(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "svc")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=/dev/null", "-c", "user.name=Gograph Test", "-c", "user.email=gograph-test@example.invalid"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(project, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write("go.mod", "module example.com/svc\n\ngo 1.22\n")
	write("app/a.go", "package app\n\nfunc Old() int { return helper() }\n\nfunc helper() int { return 1 }\n")
	write("app/tagged.go", "//go:build integration\n\npackage app\n\nfunc Tagged() int { return helper() }\n")
	git("add", "-A")
	git("commit", "-qm", "one")
	first := git("rev-parse", "HEAD")
	write("app/a.go", "package app\n\nfunc New() int { return helper() + 1 }\n\nfunc helper() int { return 2 }\n")
	git("commit", "-qam", "two")

	run := func(args ...string) (string, string, int) {
		return runCLIParityInDir(t, project, func() int { return Run(args) })
	}
	if _, stderr, code := run("build", ".", "--tags", "integration"); code != 0 {
		t.Fatalf("build: %s", stderr)
	}

	out, stderr, code := run("--rev", "HEAD~1", "callers", "helper")
	if code != 0 || !strings.Contains(out, "Old") || !strings.Contains(out, "Tagged") || strings.Contains(out, "New") {
		t.Fatalf("callers at HEAD~1 (working graph's tags): code=%d out=%s err=%s", code, out, stderr)
	}
	tree := filepath.Join(project, ".gograph", "revs", first)
	if _, err := os.Stat(filepath.Join(tree, "svc", "app", "a.go")); err != nil {
		t.Fatalf("tree is cached per commit: %v", err)
	}
	if out, _, code := run("source", "helper", "--rev="+first[:8]); code != 0 || !strings.Contains(out, "return 1") {
		t.Fatalf("source at a short commit ID: %s", out)
	}
	if out, _, code := run("--rev", "HEAD~1", "--tags", "none", "callers", "helper"); code != 0 || strings.Contains(out, "Tagged") {
		t.Fatalf("--tags rebuilds the revision graph: %s", out)
	}
	if out, _, code := run("callers", "helper"); code != 0 || !strings.Contains(out, "New") || strings.Contains(out, "Old") {
		t.Fatalf("without --rev the working tree is read: %s", out)
	}
	if strings.Contains(git("status", "--porcelain"), ".gograph") {
		t.Fatal(".gograph must stay ignored")
	}

	for _, args := range [][]string{
		{"--rev", "nosuch", "callers", "helper"},
		{"--rev", "a;b", "callers", "helper"},
		{"--rev", "HEAD~1", "tests", "--uncommitted"},
		{"--rev", "HEAD~1", "review", "--since", "HEAD"},
		{"--rev", "HEAD~1", "build", "."},
		{"callers", "helper", "--rev"},
	} {
		if _, stderr, code := run(args...); code == 0 || !strings.Contains(stderr+"", "rev") {
			t.Errorf("%v must fail naming --rev: code=%d err=%s", args, code, stderr)
		}
	}
}
