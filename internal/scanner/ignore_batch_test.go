package scanner

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixtureGit(t testing.TB, root string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestIgnoreBatchMatchesGitAndReapsItsProcess(t *testing.T) {
	root := ignoreFixture(t)
	writeIgnoreFixture(t, root, ".gitignore", "*.skip.go\n!kept.skip.go\nignored/\ntracked.go\nnested/*.go\n")
	writeIgnoreFixture(t, root, "nested/.gitignore", "!kept.go\n")
	writeIgnoreFixture(t, root, "global-ignore", "global.go\n")
	fixtureGit(t, root, "config", "core.excludesFile", filepath.Join(root, "global-ignore"))
	names := []string{"keep.go", "drop.skip.go", "kept.skip.go", "tracked.go", "nested/drop.go", "nested/kept.go", "ignored/keep.go", "global.go", "space name.go", "line\nbreak.skip.go", "-dash.skip.go"}
	for _, name := range names {
		writeIgnoreFixture(t, root, name, "package fixture\n")
	}
	fixtureGit(t, root, "add", "-f", "tracked.go")
	names = append(names, "ignored", "nested", ".")
	// A selected subdirectory still uses the containing repository's rules.
	g := newGitIgnoreChecker(filepath.Join(root, "nested"))
	t.Cleanup(func() { g.stopBatch(false) })
	var process *exec.Cmd
	ignoredCount := 0
	for _, name := range names {
		path := filepath.Join(root, name)
		cmd := exec.Command("git", "-C", root, "check-ignore", "--quiet", path)
		err := cmd.Run()
		if err != nil && cmd.ProcessState.ExitCode() != 1 {
			t.Fatal(err)
		}
		want := err == nil
		if got := g.isIgnored(path); got != want {
			t.Fatalf("%q: batch=%v, per-path=%v", name, got, want)
		}
		if want {
			ignoredCount++
		}
		if g.cmd == nil {
			t.Fatal("batch unexpectedly fell back to per-path processes")
		}
		if process == nil {
			process = g.cmd
		} else if g.cmd != process {
			t.Fatal("batch process changed during a scan")
		}
	}
	if ignoredCount == 0 || ignoredCount == len(names) {
		t.Fatal("fixture must exercise both ignored and selected paths")
	}
	if g.isIgnored(filepath.Join(root, "keep.go") + "\x00injected.skip.go") {
		t.Fatal("NUL-containing path entered the batch protocol")
	}
	if !g.isIgnored(filepath.Join(root, "drop.skip.go")) {
		t.Fatal("invalid path desynchronized the next request")
	}
	g.stopBatch(false)
	if process.ProcessState == nil || g.cmd != nil {
		t.Fatal("batch subprocess was not reaped")
	}
}

func TestIgnoreBatchLinkedWorktreeAndUnavailableGit(t *testing.T) {
	root := ignoreFixture(t)
	fixtureGit(t, root, "-c", "core.hooksPath=/dev/null", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "--allow-empty", "-m", "fixture")
	worktree := filepath.Join(t.TempDir(), "linked-worktree")
	fixtureGit(t, root, "worktree", "add", "--quiet", "--detach", worktree)
	writeIgnoreFixture(t, worktree, ".gitignore", "ignored.go\n")
	g := newGitIgnoreChecker(worktree)
	if !g.isIgnored(filepath.Join(worktree, "ignored.go")) || g.cmd == nil {
		g.stopBatch(false)
		t.Fatal("linked worktree ignore rules were not applied")
	}
	g.stopBatch(false)
	t.Setenv("PATH", t.TempDir())
	missing := newGitIgnoreChecker(root)
	defer missing.stopBatch(false)
	if missing.isIgnored(filepath.Join(root, "ignored.go")) || missing.hasGit {
		t.Fatal("unavailable Git changed the original fail-open behavior")
	}
}

func TestIgnoreBatchFailureFallsBackAndNextScanReadsIndex(t *testing.T) {
	root := ignoreFixture(t)
	writeIgnoreFixture(t, root, ".gitignore", "tracked.go\n")
	writeIgnoreFixture(t, root, "tracked.go", "package fixture\n")
	path := filepath.Join(root, "tracked.go")
	g := newGitIgnoreChecker(root)
	t.Cleanup(func() { g.stopBatch(false) })
	if !g.isIgnored(path) || g.cmd == nil {
		t.Fatal("expected ignored file through batch")
	}
	process := g.cmd
	if err := process.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if !g.isIgnored(path) || g.cmd != nil || process.ProcessState == nil {
		t.Fatal("broken batch did not reap and fall back to the original check")
	}
	fixtureGit(t, root, "add", "-f", "tracked.go")
	next := newGitIgnoreChecker(root)
	t.Cleanup(func() { next.stopBatch(false) })
	if next.isIgnored(path) {
		t.Fatal("new scan reused stale index state")
	}
}

func TestIgnoreProtocolBoundsAndPathIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		ignored, bad   bool
	}{
		{"ignored", "rules\x001\x00*.go\x00a.go\x00", true, false},
		{"negated", "rules\x001\x00!a.go\x00a.go\x00", false, false},
		{"unmatched", "\x00\x00\x00a.go\x00", false, false},
		{"wrong-path", "\x00\x00\x00b.go\x00", false, true},
		{"truncated", "\x00\x00\x00a.go", false, true},
		{"oversized", strings.Repeat("x", 256) + "\x00\x00\x00a.go\x00", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readIgnoreResponse(bufio.NewReaderSize(strings.NewReader(tc.response), 64), "a.go")
			if (err != nil) != tc.bad || (!tc.bad && got != tc.ignored) {
				t.Fatalf("ignored=%v, error=%v", got, err)
			}
		})
	}
}

func ignoreFixture(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "--quiet", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return root
}

func writeIgnoreFixture(t testing.TB, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIgnoreWalkRefreshesRulesAndPreservesUnsafeInputChecks(t *testing.T) {
	root := ignoreFixture(t)
	writeIgnoreFixture(t, root, "go.mod", "module example.com/fixture\n\ngo 1.24\n")
	writeIgnoreFixture(t, root, "keep.go", "package fixture\n")
	writeIgnoreFixture(t, root, "other.go", "package fixture\n")
	writeIgnoreFixture(t, root, ".gitignore", "other.go\n")
	paths, errs := Walk(root)
	if len(errs) != 0 || !reflect.DeepEqual(paths, []string{filepath.Join(root, "keep.go")}) {
		t.Fatalf("first selection: %v, %v", paths, errs)
	}
	writeIgnoreFixture(t, root, ".gitignore", "keep.go\n")
	paths, errs = Walk(root)
	if len(errs) != 0 || !reflect.DeepEqual(paths, []string{filepath.Join(root, "other.go")}) {
		t.Fatalf("selection after ignore edit: %v, %v", paths, errs)
	}
	if err := os.Symlink(filepath.Join(root, "other.go"), filepath.Join(root, "linked.go")); err != nil {
		t.Fatal(err)
	}
	writeIgnoreFixture(t, root, ".gitignore", "keep.go\nlinked.go\n")
	_, errs = Walk(root)
	if len(errs) == 0 {
		t.Fatal("an ignored linked Go input must still be rejected")
	}
}

func BenchmarkGitIgnoreWalk(b *testing.B) {
	root := ignoreFixture(b)
	writeIgnoreFixture(b, root, "go.mod", "module example.com/fixture\n\ngo 1.24\n")
	for i := range 100 {
		writeIgnoreFixture(b, root, fmt.Sprintf("file%03d.go", i), "package fixture\n")
	}
	b.ResetTimer()
	for b.Loop() {
		paths, errs := Walk(root)
		if len(errs) != 0 || len(paths) != 100 {
			b.Fatalf("selection: %d paths, %v", len(paths), errs)
		}
	}
}
