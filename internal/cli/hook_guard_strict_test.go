package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/session"
)

// newHookStrictRoot creates an indexed gograph root with a Go file and a sub
// directory, and returns its real path.
func newHookStrictRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".gograph", "sub"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"go.mod":   "module example.test/x\n",
		"a.go":     "package x\n\nfunc Decide() {}\n",
		"sub/b.go": "package sub\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestHookStrictShellDecisions(t *testing.T) {
	root := newHookStrictRoot(t)
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	deny := []struct{ name, command, want string }{
		{"sed in place", `sed -i 's/a/b/' a.go`, ".go write"},
		{"bsd sed in place", `sed -i '' -e 's/a/b/' a.go`, ".go write"},
		{"perl in place", `perl -pi -e 's/a/b/' a.go`, ".go write"},
		{"redirect", `echo hi > a.go`, ".go write"},
		{"append redirect", `printf x >> sub/b.go`, ".go write"},
		{"heredoc into go file", "cat <<'EOF' > sub/c.go\npackage sub\nEOF", ".go write"},
		{"cp onto go", `cp /tmp/x.go sub/b.go`, ".go write"},
		{"tee", `echo x | tee a.go`, ".go write"},
		{"dd", `dd if=/dev/zero of=a.go count=1`, ".go write"},
		{"cat", `cat a.go`, ".go region read"},
		{"sed region", `sed -n '1,80p' a.go`, ".go region read"},
		{"head", `head -20 sub/b.go`, ".go region read"},
		{"awk", `awk 'NR<10' a.go`, ".go region read"},
		{"perl print", `perl -ne 'print if 1..3' a.go`, ".go region read"},
		{"literal without reason", `grep -rn "hello world" --include=*.go .`, "without a reason"},
		{"rg type go literal", `rg -t go 'error: x'`, "without a reason"},
		{"git grep literal", `git grep -n 'error: x'`, "without a reason"},
		{"symbol search", `grep -rn Decide .`, "Go symbol search"},
		{"symbol after cd", `cd sub && grep -rn Decide .`, "Go symbol search"},
		{"symbol over git show", `git show HEAD:a.go | grep -n Decide`, "Go symbol search"},
		{"unknown dir keeps blocking", `cd "$X" && cat a.go`, ".go region read"},
		{"cd dash is unknown", `cd - && cat a.go`, ".go region read"},
		{"second command", `ls && cat a.go`, ".go region read"},
	}
	for _, tc := range deny {
		t.Run("deny/"+tc.name, func(t *testing.T) {
			reason := evaluateHookStrictShell(tc.command, root, hookStrictOptions{})
			if !strings.Contains(reason, tc.want) {
				t.Fatalf("command %q: reason = %q, want it to contain %q", tc.command, reason, tc.want)
			}
		})
	}

	allow := []struct{ name, command, cwd string }{
		{"count", `grep -c Decide a.go`, root},
		{"files with matches", `grep -rl 'error: x' --include=*.go .`, root},
		{"wc sink", `cat a.go | wc -l`, root},
		{"checksum", `cat a.go | sha256sum`, root},
		{"git show region", `git show HEAD:a.go | sed -n '1,80p'`, root},
		{"git diff", `git diff -- a.go`, root},
		{"literal with reason", `grep -rn "hello world" --include=*.go . # gograph-cannot: text inside comments`, root},
		{"markdown search", `grep -rn Decide docs/*.md`, root},
		{"markdown write", `echo hi > notes.md`, root},
		{"makefile read", `cat Makefile`, root},
		{"quoted redirect", `echo 'x > y.go'`, root},
		{"heredoc data names go", "cat <<'EOF'\nsed -i s/a/b/ a.go > a.go\nEOF", root},
		{"input redirect from go into wc", `wc -l < a.go`, root},
		{"listing", `ls *.go && wc -l *.go`, root},
		{"go toolchain", `go test ./... && gofmt -l .`, root},
		{"gograph", `gograph source x.Decide`, root},
		{"outside root read", `cat a.go`, outside},
		{"outside root symbol", `grep -rn Decide .`, outside},
		{"cd outside", "cd " + outside + " && cat a.go", root},
		{"fd copy", `go test ./... 2>&1 | tail -5`, root},
	}
	for _, tc := range allow {
		t.Run("allow/"+tc.name, func(t *testing.T) {
			if reason := evaluateHookStrictShell(tc.command, tc.cwd, hookStrictOptions{}); reason != "" {
				t.Fatalf("command %q denied:\n%s", tc.command, reason)
			}
		})
	}

	t.Run("codex may read regions", func(t *testing.T) {
		if reason := evaluateHookStrictShell(`sed -n '1,80p' a.go`, root, hookStrictOptions{allowRegionReads: true}); reason != "" {
			t.Fatalf("region read denied for an agent without a read tool:\n%s", reason)
		}
	})
}

func TestHookStrictEditNeedsSession(t *testing.T) {
	root := newHookStrictRoot(t)
	goFile := filepath.Join(root, "a.go")

	if reason := evaluateHookStrictEdit([]string{goFile}, root); !strings.Contains(reason, "gograph session create") {
		t.Fatalf("edit without session: reason = %q", reason)
	}
	if reason := evaluateHookStrictEdit([]string{filepath.Join(root, "README.md")}, root); reason != "" {
		t.Fatalf("non-Go edit denied: %q", reason)
	}
	outside := t.TempDir()
	if reason := evaluateHookStrictEdit([]string{filepath.Join(outside, "x.go")}, outside); reason != "" {
		t.Fatalf("edit outside a gograph root denied: %q", reason)
	}

	if _, err := session.StartSessionAt(root, ""); err != nil {
		t.Fatalf("start session: %v", err)
	}
	if reason := evaluateHookStrictEdit([]string{"a.go"}, root); reason != "" {
		t.Fatalf("edit with an open session denied: %q", reason)
	}
}

func TestHookCallNormalization(t *testing.T) {
	decode := func(t *testing.T, payload string) hookCall {
		t.Helper()
		var input hookGuardInput
		if err := json.Unmarshal([]byte(payload), &input); err != nil {
			t.Fatal(err)
		}
		return normalizeHookCall(input)
	}

	claude := decode(t, `{"tool_name":"Bash","cwd":"/w","tool_input":{"command":"ls"}}`)
	if claude.agent != hookAgentClaude || claude.shell != "ls" || claude.cwd != "/w" {
		t.Fatalf("claude bash: %+v", claude)
	}
	edit := decode(t, `{"tool_name":"Edit","cwd":"/w","tool_input":{"file_path":"/w/a.go"}}`)
	if len(edit.editPaths) != 1 || edit.editPaths[0] != "/w/a.go" {
		t.Fatalf("claude edit: %+v", edit)
	}
	codex := decode(t, `{"tool_name":"apply_patch","turn_id":"t1","cwd":"/w","tool_input":{"command":"*** Begin Patch\n*** Update File: a.go\n@@\n*** Add File: b/c.go\n+package b\n*** End Patch\n"}}`)
	if codex.agent != hookAgentCodex || strings.Join(codex.editPaths, ",") != "a.go,b/c.go" {
		t.Fatalf("codex apply_patch: %+v", codex)
	}
	agy := decode(t, `{"toolCall":{"name":"run_command","args":{"CommandLine":"cat a.go","Cwd":"/p"}},"workspacePaths":["/w"]}`)
	if agy.agent != hookAgentAntigravity || agy.shell != "cat a.go" || agy.cwd != "/p" {
		t.Fatalf("antigravity run_command: %+v", agy)
	}
	agyEdit := decode(t, `{"toolCall":{"name":"replace_file_content","args":{"TargetFile":"/w/a.go"}},"workspacePaths":["/w"]}`)
	if len(agyEdit.editPaths) != 1 || agyEdit.cwd != "/w" {
		t.Fatalf("antigravity edit: %+v", agyEdit)
	}
}

func TestEmitHookVerdict(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := emitHookVerdict(hookAgentClaude, "nope", &stdout, &stderr); code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "nope") {
		t.Fatalf("claude deny: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := emitHookVerdict(hookAgentCodex, "", &stdout, &stderr); code != 0 || stdout.Len()+stderr.Len() != 0 {
		t.Fatalf("codex allow wrote output: code=%d", code)
	}
	for _, tc := range []struct{ reason, want string }{
		{"", `{"decision":"allow"}`},
		{"nope", `{"decision":"deny","reason":"nope"}`},
	} {
		stdout.Reset()
		if code := emitHookVerdict(hookAgentAntigravity, tc.reason, &stdout, &stderr); code != 0 || strings.TrimSpace(stdout.String()) != tc.want {
			t.Fatalf("antigravity verdict: code=%d stdout=%q, want %s", code, stdout.String(), tc.want)
		}
	}
}

func TestSplitHookShell(t *testing.T) {
	commands, ok := splitHookShell("cd a && grep -n 'x | y' f.go 2>&1 | wc -l; echo \"$(date)\" > out.txt &")
	if !ok || len(commands) != 4 {
		t.Fatalf("split: ok=%v commands=%+v", ok, commands)
	}
	if commands[1].argv[2] != "x | y" || !commands[1].pipeOut || !commands[2].pipeIn {
		t.Fatalf("pipeline: %+v %+v", commands[1], commands[2])
	}
	if len(commands[1].redirects) != 0 {
		t.Fatalf("2>&1 must not be a file redirect: %+v", commands[1].redirects)
	}
	last := commands[3]
	if !last.dynamic || !last.background || len(last.redirects) != 1 || last.redirects[0].target != "out.txt" {
		t.Fatalf("last command: %+v", last)
	}
	if _, ok := splitHookShell(`echo 'unbalanced`); ok {
		t.Fatal("unbalanced quote should not split")
	}
}
