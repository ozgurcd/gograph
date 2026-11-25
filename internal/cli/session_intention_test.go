package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// With a session open, a query without --intention uses the session's
// default intention, and still fails when the session has none.
func TestSessionDefaultIntentionCoversQueries(t *testing.T) {
	t.Setenv("GOGRAPH_SESSION", "")
	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":   "module example.com/intent\n\ngo 1.22\n",
		"app/a.go": "package app\n\nfunc A() int { return 1 }\n",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) (string, string, int) {
		return runCLIParityInDir(t, root, func() int { return Run(args) })
	}
	if _, stderr, code := run("build", "."); code != 0 {
		t.Fatalf("build: %s", stderr)
	}
	if _, stderr, code := run("session", "create", "plain"); code != 0 {
		t.Fatalf("create: %s", stderr)
	}
	if _, stderr, code := run("source", "A"); code == 0 || !strings.Contains(stderr, "requires an intention") {
		t.Fatalf("a session without a default must still require -i: code=%d %s", code, stderr)
	}
	if _, stderr, code := run("session", "end"); code != 0 {
		t.Fatalf("end: %s", stderr)
	}
	stdout, stderr, code := run("session", "create", "task", "-i", "fix A")
	if code != 0 || !strings.Contains(stdout, `default: "fix A"`) {
		t.Fatalf("create with -i: code=%d\n%s\n%s", code, stdout, stderr)
	}
	if stdout, stderr, code := run("source", "A"); code != 0 || !strings.Contains(stdout, "func A()") {
		t.Fatalf("query without -i in a session with a default: code=%d\n%s\n%s", code, stdout, stderr)
	}
	if _, stderr, code := run("session", "end"); code != 0 {
		t.Fatalf("end: %s", stderr)
	}
}
