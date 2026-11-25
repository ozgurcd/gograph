package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/session"
)

// A graph query on a stale graph rebuilds it first with the recorded build
// settings, unless --no-refresh, GOGRAPH_NO_REFRESH, a changed build context
// or another caller's session prevents it.
func TestCLIQueriesRefreshStaleGraphWithRecordedSettings(t *testing.T) {
	t.Setenv("GOGRAPH_NO_REFRESH", "")
	t.Setenv("GOFLAGS", "")
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/refresh\n\ngo 1.22\n")
	write("app/a.go", "package app\n\nfunc A() int { return 1 }\n")
	write("app/i.go", "//go:build integration\n\npackage app\n\nfunc I() int { return A() }\n")
	write("skip/s.go", "package skip\n\nfunc S() {}\n")
	run := func(args ...string) (string, string, int) {
		return runCLIParityInDir(t, root, func() int { return Run(args) })
	}
	if _, stderr, code := run("build", ".", "--tags=integration", "--exclude-dirs=skip"); code != 0 {
		t.Fatalf("build: %s", stderr)
	}
	recorded := func() *graph.BuildMetadata {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, graphFile))
		if err != nil {
			t.Fatal(err)
		}
		var g graph.Graph
		if err := json.Unmarshal(data, &g); err != nil {
			t.Fatal(err)
		}
		return g.Build
	}

	write("app/a.go", "package app\n\nfunc A() int { return B() }\n\nfunc B() int { return 2 }\n")
	stdout, stderr, code := run("source", "B")
	if code != 0 || !strings.Contains(stdout, "func B() int") || !strings.Contains(stderr, "rebuilt the stale graph") {
		t.Fatalf("source B after edit: code=%d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "gograph build") || strings.Contains(stderr, "WARNING: the graph is stale") || strings.Contains(stderr, "found ") {
		t.Fatalf("refresh leaked build output or a stale warning:\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	if build := recorded(); strings.Join(build.Selection.BuildTags, ",") != "integration" || strings.Join(build.Selection.ExcludeDirs, ",") != "skip" {
		t.Fatalf("refresh changed the recorded selection: %+v", build.Selection)
	}

	write("app/a.go", "package app\n\nfunc A() int { return B() }\n\nfunc B() int { return 2 }\n\nfunc C() {}\n")
	stdout, stderr, code = run("--json", "query", "C")
	var envelope struct {
		Count   int    `json:"count"`
		Warning string `json:"warning"`
	}
	if code != 0 || json.Unmarshal([]byte(stdout), &envelope) != nil || envelope.Count == 0 || stderr != "" || !strings.Contains(envelope.Warning, "rebuilt the stale graph") {
		t.Fatalf("--json query after edit: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	write("app/a.go", "package app\n\nfunc A() int { return 1 }\n\nfunc D() {}\n")
	for _, args := range [][]string{{"--no-refresh", "source", "D"}} {
		if _, stderr, _ := run(args...); !strings.Contains(stderr, "WARNING: the graph is stale") || strings.Contains(stderr, "rebuilt") {
			t.Fatalf("%v must not refresh:\n%s", args, stderr)
		}
	}
	t.Setenv("GOGRAPH_NO_REFRESH", "1")
	if _, stderr, _ := run("source", "D"); strings.Contains(stderr, "rebuilt") {
		t.Fatalf("GOGRAPH_NO_REFRESH=1 must not refresh:\n%s", stderr)
	}
	t.Setenv("GOGRAPH_NO_REFRESH", "")
	if _, stderr, _ := run("source", "D", "--tags", "other"); strings.Contains(stderr, "rebuilt") {
		t.Fatalf("a different build context must not refresh:\n%s", stderr)
	}
	if _, stderr, code := run("gate", "init"); code != 0 {
		t.Fatalf("gate init: %s", stderr)
	}
	for _, command := range []string{"stale", "gate"} {
		if _, stderr, _ := run(command); strings.Contains(stderr, "rebuilt") {
			t.Fatalf("%s must not refresh:\n%s", command, stderr)
		}
	}
	if _, stderr, code := run("gate"); code == 0 || !strings.Contains(stderr, "stale") {
		t.Fatalf("gate must keep failing closed on a stale graph: code=%d\n%s", code, stderr)
	}

	first, err := session.StartCallerSessionAt(root, "first", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.StartCallerSessionAt(root, "second", ""); err != nil {
		t.Fatal(err)
	}
	if _, stderr, _ := run("--session-id", first, "-i", "refresh check", "source", "D"); strings.Contains(stderr, "rebuilt") || !strings.Contains(stderr, "WARNING: the graph is stale") {
		t.Fatalf("another caller's session must block the refresh:\n%s", stderr)
	}
}
