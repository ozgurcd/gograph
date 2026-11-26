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
var hookAllRules = hookGuardOptions{denyGoWrites: true, denyRegionReads: true, requireReason: true, requireSession: true}

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
		{"tee", `echo x | tee a.go`, ".go write"},
		{"bare redirect", `> a.go`, ".go write"},
		{"awk in place", `gawk -i inplace '{print}' a.go`, ".go write"},
		{"sed redirect", `sed 's/a/b/' a.go > sub/b.go`, ".go write"},
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
		{"grep in a for loop", `for f in *.go; do grep -n Decide $f; done`, "Go symbol search"},
		{"grep in an if", `if grep -q Decide a.go; then echo y; fi`, "Go symbol search"},
		{"bash -c", `bash -c 'grep -n Decide a.go'`, "Go symbol search"},
		{"eval", `eval "grep -n Decide a.go"`, "Go symbol search"},
		{"shell heredoc", "bash <<'SH'\ngrep -n Decide a.go\nSH", "Go symbol search"},
		{"find piped to xargs", `find . -name '*.go' | xargs grep -n needle`, "Go symbol search"},
		{"rg --files piped to xargs", `rg --files | xargs grep needle`, "Go symbol search"},
		{"find -exec", `find . -name '*.go' -exec grep -n needle {} +`, "Go symbol search"},
		{"python subprocess list", "python3 - <<'PY'\nimport subprocess\nsubprocess.run([\"grep\", \"Decide\", \"a.go\"])\nPY", "Go symbol search"},
		{"python shell string", "python3 -c 'import os; os.system(\"grep -n Decide a.go\")'", "Go symbol search"},
		{"perl backtick", "perl - <<PL\nmy $o = `grep -n Decide a.go`;\nPL", "Go symbol search"},
		{"ag --go", `ag --go Decide`, "Go symbol search"},
		{"strings piped to grep", `strings a.go | grep Decide`, "Go symbol search"},
		{"call site with a reason", `grep -rn 'Decide(' --include=*.go . # gograph-cannot: just because`, "Go symbol search"},
		{"declaration with a reason", `grep -rn 'func Decide' . # gograph-cannot: just because`, "Go symbol search"},
		{"unbalanced quote", `echo "a; grep -n Decide a.go`, "Go symbol search"},
		{"snake_case without a reason", `grep -n upgrade_carried_disabled a.go`, "without a reason"},
	}
	for _, tc := range deny {
		t.Run("deny/"+tc.name, func(t *testing.T) {
			reason := evaluateHookStrictShell(tc.command, root, hookAllRules)
			if !strings.Contains(reason, tc.want) {
				t.Fatalf("command %q: reason = %q, want it to contain %q", tc.command, reason, tc.want)
			}
		})
	}

	allow := []struct{ name, command, cwd string }{
		{"count", `grep -c Decide a.go`, root},
		{"move", `mv a.go sub/c.go`, root},
		{"filter over go test output", `go test ./... | rg Decide`, root},
		{"filter over git status", `git status | rg Decide`, root},
		{"git log of a .go piped to grep", `git log --oneline -- a.go | grep Decide`, root},
		{"git blame piped to grep", `git blame a.go | grep TODO`, root},
		{"region read with a reason", "# gograph-cannot: inspect a named file\ncat a.go", root},
		{"snake_case with a reason", `grep -n upgrade_carried_disabled a.go # gograph-cannot: an SQL table name`, root},
		{"find -exec over markdown", `find . -name '*.md' -exec grep -n Decide {} +`, root},
		{"bash -c over markdown", `bash -c 'grep -n Decide notes.md'`, root},
		{"copy", `cp /tmp/x.go sub/b.go`, root},
		{"dd", `dd if=/dev/zero of=a.go count=1`, root},
		{"generator output", `go run ./gen > sub/zz_gen.go`, root},
		{"generator through tee", `go run ./gen | tee sub/zz_gen.go`, root},
		{"restore from git", `git show HEAD:a.go > a.go`, root},
		{"text edit with reason", `sed -i 's/Old/New/' a.go sub/b.go # gograph-cannot: one rename across files`, root},
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
			if reason := evaluateHookStrictShell(tc.command, tc.cwd, hookAllRules); reason != "" {
				t.Fatalf("command %q denied:\n%s", tc.command, reason)
			}
		})
	}

	t.Run("codex may read regions", func(t *testing.T) {
		if reason := evaluateHookStrictShell(`sed -n '1,80p' a.go`, root, hookGuardOptions{denyGoWrites: true, denyRegionReads: true, requireReason: true, allowRegionReads: true}); reason != "" {
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
	if code := emitHookVerdict(hookAgentClaude, "nope", false, &stdout, &stderr); code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "nope") {
		t.Fatalf("claude deny: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := emitHookVerdict(hookAgentCodex, "", false, &stdout, &stderr); code != 0 || stdout.Len()+stderr.Len() != 0 {
		t.Fatalf("codex allow wrote output: code=%d", code)
	}
	for _, tc := range []struct{ reason, want string }{
		{"", `{"decision":"allow"}`},
		{"nope", `{"decision":"deny","reason":"nope"}`},
	} {
		stdout.Reset()
		if code := emitHookVerdict(hookAgentAntigravity, tc.reason, false, &stdout, &stderr); code != 0 || strings.TrimSpace(stdout.String()) != tc.want {
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

func TestHookRulesAreIndependent(t *testing.T) {
	root := newHookStrictRoot(t)
	cases := []struct {
		name    string
		options hookGuardOptions
		command string
		denied  bool
	}{
		{"writes only: write denied", hookGuardOptions{denyGoWrites: true}, `echo x > a.go`, true},
		{"writes only: read allowed", hookGuardOptions{denyGoWrites: true}, `cat a.go`, false},
		{"writes only: literal allowed", hookGuardOptions{denyGoWrites: true}, `grep -rn "a b" --include=*.go .`, false},
		{"reads only: read denied", hookGuardOptions{denyRegionReads: true}, `cat a.go`, true},
		{"reads only: write allowed", hookGuardOptions{denyRegionReads: true}, `echo x > a.go`, false},
		{"reason only: literal denied", hookGuardOptions{requireReason: true}, `grep -rn "a b" --include=*.go .`, true},
		{"reason only: read allowed", hookGuardOptions{requireReason: true}, `cat a.go`, false},
		{"any rule still blocks symbols", hookGuardOptions{denyGoWrites: true}, `grep -rn Decide .`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := evaluateHookStrictShell(tc.command, root, tc.options)
			if (reason != "") != tc.denied {
				t.Fatalf("command %q with %+v: reason = %q, denied want %v", tc.command, tc.options, reason, tc.denied)
			}
		})
	}

	edit := hookCall{agent: hookAgentClaude, cwd: root, editPaths: []string{"a.go"}}
	if reason := evaluateHookCall(edit, hookGuardOptions{denyGoWrites: true}); reason != "" {
		t.Fatalf("edit denied without --require-session: %q", reason)
	}
	if reason := evaluateHookCall(edit, hookGuardOptions{requireSession: true}); reason == "" {
		t.Fatal("edit allowed with --require-session and no session")
	}
	plain := hookCall{agent: hookAgentClaude, cwd: root, shell: `cat a.go`}
	if reason := evaluateHookCall(plain, hookGuardOptions{}); reason != "" {
		t.Fatalf("default mode denied a region read: %q", reason)
	}
}

func TestEmitHookVerdictAsk(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := emitHookVerdict(hookAgentClaude, "why", true, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), `"permissionDecision":"ask"`) || !strings.Contains(stdout.String(), `"permissionDecisionReason":"why"`) {
		t.Fatalf("claude ask: code=%d stdout=%q", code, stdout.String())
	}
	stdout.Reset()
	if code := emitHookVerdict(hookAgentAntigravity, "why", true, &stdout, &stderr); code != 0 || strings.TrimSpace(stdout.String()) != `{"decision":"ask","reason":"why"}` {
		t.Fatalf("antigravity ask: code=%d stdout=%q", code, stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := emitHookVerdict(hookAgentCodex, "why", true, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "why") {
		t.Fatalf("codex cannot ask, must deny: code=%d stderr=%q", code, stderr.String())
	}
}
