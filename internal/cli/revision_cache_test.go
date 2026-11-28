package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRevisionCacheKeepsUnderItsCapByEvictingLeastRecentlyUsedTrees(t *testing.T) {
	root := t.TempDir()
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
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write("go.mod", "module example.com/revcap\n\ngo 1.22\n")
	var commits []string
	for _, body := range []string{"1", "2", "3"} {
		write("app/a.go", "package app\n\nfunc V() int { return "+body+" }\n")
		git("add", "-A")
		git("commit", "-qm", "c"+body)
		commits = append(commits, git("rev-parse", "HEAD"))
	}
	run := func(args ...string) (string, string, int) {
		return runCLIParityInDir(t, root, func() int { return Run(args) })
	}
	if _, stderr, code := run("build", "."); code != 0 {
		t.Fatalf("build: %s", stderr)
	}
	cache := filepath.Join(root, ".gograph", "revs")
	// Read commit 0, then 1; measure one tree to size the cap for two trees.
	for _, commit := range commits[:2] {
		if _, stderr, code := run("--rev", commit, "source", "V"); code != 0 {
			t.Fatalf("--rev %s: %s", commit, stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	one := revisionTreeSize(cache, commits[0], false)
	if one <= 0 {
		t.Fatalf("tree size must be recorded: %d", one)
	}
	t.Setenv(revisionCacheMaxEnv, strconv.FormatInt(one*2+one/2, 10))
	// Touch commit 0 so commit 1 is the least recently used.
	if _, _, code := run("--rev", commits[0], "source", "V"); code != 0 {
		t.Fatal("reuse commit 0")
	}
	time.Sleep(20 * time.Millisecond)
	_, stderr, code := run("--rev", commits[2], "source", "V")
	if code != 0 || !strings.Contains(stderr, "removed the least recently used revision tree "+commits[1]) {
		t.Fatalf("a third tree over the cap evicts the LRU one: code=%d err=%s", code, stderr)
	}
	for commit, want := range map[string]bool{commits[0]: true, commits[1]: false, commits[2]: true} {
		_, err := os.Stat(filepath.Join(cache, commit))
		if (err == nil) != want {
			t.Errorf("tree %s present=%v, want %v", commit, err == nil, want)
		}
	}
}
