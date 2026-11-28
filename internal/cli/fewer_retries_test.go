package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

func writeFewerRetriesFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/retries\n\ngo 1.22\n",
		"a/a.go": "package a\n\n// State is shared.\ntype State struct {\n\t// Deadlines lists due times.\n\tDeadlines []int // sorted\n\tName      string\n}\n\nfunc Load() int { return helper() }\n\nfunc helper() int { return 1 }\n",
		"b/b.go": "package b\n\ntype State struct {\n\tDeadlines []string\n}\n\nfunc Load() int { return 2 }\n\nfunc Use() int { return Load() }\n",
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) }); code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}
	return root
}

func TestAmbiguousNamesPrintCopyReadySelectors(t *testing.T) {
	root := writeFewerRetriesFixture(t)
	run := func(args ...string) (string, string, int) {
		return runCLIParityInDir(t, root, func() int { return Run(args) })
	}
	for _, args := range [][]string{{"identity", "Load"}, {"explain", "Load"}, {"tests", "Load", "--format", "go-test"}, {"source", "Load"}} {
		out, stderr, _ := run(args...)
		answer := out + stderr
		for _, selector := range []string{"a/a.go:Load", "b/b.go:Load"} {
			if !strings.Contains(answer, selector) {
				t.Errorf("%v must name selector %s: %s", args, selector, answer)
			}
		}
	}
	for _, command := range []string{"callers", "callees", "impact"} {
		_, stderr, code := run(command, "Load")
		if code != 0 || !strings.Contains(stderr, `"Load" matches 2 declarations`) || !strings.Contains(stderr, "a/a.go:Load | b/b.go:Load") {
			t.Errorf("%s must note the merged matches with selectors: code=%d err=%s", command, code, stderr)
		}
	}
	if _, stderr, _ := run("callees", "a/a.go:Load"); strings.Contains(stderr, "matches") {
		t.Errorf("an exact selector must not be noted as merged: %s", stderr)
	}
}

func TestSourceFileOutlineFieldSelectorsAndSeveralRanges(t *testing.T) {
	root := writeFewerRetriesFixture(t)
	run := func(args ...string) (string, string, int) {
		return runCLIParityInDir(t, root, func() int { return Run(args) })
	}
	out, stderr, code := run("source", "a/a.go")
	if code != 0 || !strings.Contains(out, "a/a.go: package a, 12 lines") || !strings.Contains(out, "10-10       func Load() (int)") || !strings.Contains(out, "struct State {Deadlines, Name}") {
		t.Fatalf("a file selector without --lines prints the outline: code=%d out=%s err=%s", code, out, stderr)
	}
	out, stderr, code = run("source", "a/a.go:State.Deadlines")
	if code != 0 || !strings.Contains(out, "example.com/retries/a::State.Deadlines (a/a.go:5-6)") || !strings.Contains(out, "// sorted") {
		t.Fatalf("Type.Field prints the field with its comments: code=%d out=%s err=%s", code, out, stderr)
	}
	if _, stderr, code = run("source", "State.Deadlines"); code == 0 || !strings.Contains(stderr, "a/a.go:State.Deadlines") || !strings.Contains(stderr, "b/b.go:State.Deadlines") {
		t.Fatalf("an ambiguous field names each selector: code=%d err=%s", code, stderr)
	}
	if out, stderr, code = run("source", "a.State.Name"); code != 0 || !strings.Contains(out, "Name      string") {
		t.Fatalf("package-qualified field: code=%d out=%s err=%s", code, out, stderr)
	}
	if _, _, code = run("source", "a.State.Missing"); code == 0 {
		t.Fatal("an absent field must fail")
	}
	out, stderr, code = run("source", "a/a.go:1-2", "b/b.go:3-4")
	if code != 0 || !strings.Contains(out, "(a/a.go:1-2)") || !strings.Contains(out, "(b/b.go:3-4)") {
		t.Fatalf("several file ranges: code=%d out=%s err=%s", code, out, stderr)
	}
	out, stderr, code = run("source", "a/a.go:helper", "b/b.go:Use", "--lines", "1-100")
	if code != 0 || !strings.Contains(out, "(a/a.go:12-12)") || !strings.Contains(out, "(b/b.go:9-9)") {
		t.Fatalf("--lines applies to several selectors, clamped to each: code=%d out=%s err=%s", code, out, stderr)
	}
}

func TestBuildLockMakesQueriesAndBuildsWaitForARunningBuild(t *testing.T) {
	root := writeFewerRetriesFixture(t)
	t.Setenv(buildWaitEnv, "20s")
	lock := flock.New(buildLockPath(root))
	if locked, err := lock.TryLock(); err != nil || !locked {
		t.Fatalf("lock: %v %v", locked, err)
	}
	graphPath := filepath.Join(root, graphFile)
	if err := os.Remove(graphPath); err != nil {
		t.Fatal(err)
	}
	unlockAfter := func(publish bool) {
		time.Sleep(300 * time.Millisecond)
		if publish {
			heldBuildLocks[root]++ // this process plays the running build
			_, _, _ = runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) })
			delete(heldBuildLocks, root)
		}
		_ = lock.Unlock()
	}
	go unlockAfter(true)
	out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"callers", "a/a.go:helper"}) })
	if code != 0 || !strings.Contains(stderr, "waiting up to 20s") || !strings.Contains(out, "Load") {
		t.Fatalf("a query without a graph must wait for the running build: code=%d out=%s err=%s", code, out, stderr)
	}

	if locked, err := lock.TryLock(); err != nil || !locked {
		t.Fatalf("relock: %v %v", locked, err)
	}
	go unlockAfter(false)
	out, stderr, code = runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) })
	if code != 0 || !strings.Contains(stderr, "waiting up to 20s for the build already running") || !strings.Contains(out, "reused the graph the concurrent build just published") {
		t.Fatalf("a second build must wait and reuse a fitting graph: code=%d out=%s err=%s", code, out, stderr)
	}

	t.Setenv(buildWaitEnv, "0")
	if locked, err := lock.TryLock(); err != nil || !locked {
		t.Fatalf("relock: %v %v", locked, err)
	}
	defer func() { _ = lock.Unlock() }()
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) }); code == 0 || !strings.Contains(stderr, "another gograph build is running") {
		t.Fatalf("GOGRAPH_BUILD_WAIT=0 must fail at once: code=%d err=%s", code, stderr)
	}
}

func TestDoctorJudgesTheGraphWithItsRecordedBuildTags(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod": "module example.com/doctortags\n\ngo 1.22\n",
		"a.go":   "package doctortags\n\nfunc A() {}\n",
		"t.go":   "//go:build integration\n\npackage doctortags\n\nfunc T() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) (string, string, int) {
		return runCLIParityInDir(t, root, func() int { return Run(args) })
	}
	if _, stderr, code := run("build", ".", "--tags=integration"); code != 0 {
		t.Fatal(stderr)
	}
	out, stderr, _ := run("doctor", "--json")
	if !strings.Contains(out, `"build_tags":["integration"]`) || !strings.Contains(out, `"freshness":"current"`) {
		t.Fatalf("doctor must use the graph's tags: out=%s err=%s", out, stderr)
	}
	// Run resets the global query tags on entry only; keep later direct
	// callers of doctor helpers in this test binary unaffected.
	t.Cleanup(func() { queryBuildTags = nil })
	out, _, _ = run("--tags", "other", "doctor", "--json")
	if !strings.Contains(out, `"freshness":"stale"`) {
		t.Fatalf("an explicit --tags still selects another context: %s", out)
	}

	t.Setenv("GOFLAGS", "")
	adopted := adoptRecordedMCPSelection(mcpOptions{Root: root})
	if strings.Join(adopted.Tags, ",") != "integration" {
		t.Fatalf("MCP without --tags must adopt the graph's tags: %v", adopted.Tags)
	}
	if kept := adoptRecordedMCPSelection(mcpOptions{Root: root, Tags: []string{"other"}}); strings.Join(kept.Tags, ",") != "other" {
		t.Fatalf("explicit MCP --tags win: %v", kept.Tags)
	}
	t.Setenv("GOFLAGS", "-tags=other")
	if viaFlags := adoptRecordedMCPSelection(mcpOptions{Root: root}); len(viaFlags.Tags) != 0 {
		t.Fatalf("GOFLAGS -tags wins over recorded tags: %v", viaFlags.Tags)
	}
}
