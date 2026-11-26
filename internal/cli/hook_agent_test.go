package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ozgurcd/gograph/internal/session"
)

const hookTestPatch = "*** Begin Patch\n*** Update File: a.go\n@@\n-x\n+y\n*** Add File: sub/c.go\n+package sub\n*** Delete File: old.go\n*** Update File: d.txt\n*** Move to: sub/e.go\n*** End Patch\n"

func hookJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// hookCallFor builds one logical tool call in an agent's input format.
func hookCallFor(t *testing.T, agent string, kind hookToolKind, value, cwd string) string {
	t.Helper()
	switch agent {
	case "claude", "codex":
		tool, input := map[hookToolKind]string{hookKindShell: "Bash", hookKindEdit: "Edit", hookKindWrite: "Write", hookKindMultiEdit: "MultiEdit", hookKindRead: "Read"}[kind], map[string]any{"file_path": value}
		if kind == hookKindShell {
			input = map[string]any{"command": value}
		}
		if kind == hookKindPatch {
			tool, input = "apply_patch", map[string]any{"command": value}
		}
		return hookJSON(t, map[string]any{"hook_event_name": "PreToolUse", "session_id": "s1", "cwd": cwd, "tool_name": tool, "tool_input": input})
	case "opencode":
		tool, args := map[hookToolKind]string{hookKindShell: "bash", hookKindEdit: "edit", hookKindWrite: "write", hookKindMultiEdit: "multiedit", hookKindPatch: "apply_patch", hookKindRead: "read"}[kind], map[string]any{"filePath": value}
		switch kind {
		case hookKindShell:
			args = map[string]any{"command": value, "workdir": cwd}
		case hookKindPatch:
			args = map[string]any{"patchText": value}
		}
		return hookJSON(t, map[string]any{"tool": tool, "sessionID": "s1", "cwd": cwd, "args": args})
	case "agy":
		tool, args := map[hookToolKind]string{hookKindShell: "run_command", hookKindEdit: "replace_file_content", hookKindWrite: "write_to_file", hookKindMultiEdit: "multi_replace_file_content"}[kind], map[string]any{"TargetFile": value}
		if kind == hookKindShell {
			args = map[string]any{"CommandLine": value, "Cwd": cwd}
		}
		return hookJSON(t, map[string]any{"hookEventName": "PreToolUse", "cwd": cwd, "conversationId": "s1", "toolCall": map[string]any{"id": "1", "name": tool, "args": args}})
	}
	t.Fatalf("unknown agent %s", agent)
	return ""
}

// hookAgentKinds lists the tool kinds each agent has a tool for.
var hookAgentKinds = map[string][]hookToolKind{
	"claude":   {hookKindShell, hookKindEdit, hookKindWrite, hookKindMultiEdit, hookKindRead},
	"codex":    {hookKindShell, hookKindPatch},
	"opencode": {hookKindShell, hookKindEdit, hookKindWrite, hookKindMultiEdit, hookKindPatch, hookKindRead},
	"agy":      {hookKindShell, hookKindEdit, hookKindWrite, hookKindMultiEdit},
}

func TestHookReadLayer(t *testing.T) {
	for agent, kinds := range hookAgentKinds {
		for _, kind := range kinds {
			value, want := "/w/a.go", []string{"/w/a.go"}
			switch kind {
			case hookKindShell:
				value, want = "cat a.go", nil
			case hookKindPatch:
				value, want = hookTestPatch, []string{"a.go", "sub/c.go", "old.go", "d.txt", "sub/e.go"}
			}
			t.Run(agent+"/"+string(kind), func(t *testing.T) {
				event, err := hookAgents[agent].read([]byte(hookCallFor(t, agent, kind, value, "/w")))
				if err != nil {
					t.Fatal(err)
				}
				if event.Agent != agent || event.Kind != kind || event.CWD != "/w" || event.Session != "s1" || event.Unparsed != "" {
					t.Fatalf("event = %+v", event)
				}
				if kind == hookKindShell && event.Command != value {
					t.Fatalf("command = %q, want %q", event.Command, value)
				}
				if !reflect.DeepEqual(event.Paths, want) {
					t.Fatalf("paths = %q, want %q", event.Paths, want)
				}
			})
		}
	}

	codexArgv := `{"tool_name":"shell","cwd":"/w","tool_input":{"command":["bash","-lc","grep -rn X ."]}}`
	if event, _ := readHookCodex([]byte(codexArgv)); event.Kind != hookKindShell || event.Command != "grep -rn X ." {
		t.Fatalf("codex argv command: %+v", event)
	}
	agyWorkspace := `{"workspacePaths":["/ws"],"toolCall":{"name":"run_command","args":{"CommandLine":"ls"}}}`
	if event, _ := readHookAgy([]byte(agyWorkspace)); event.CWD != "/ws" {
		t.Fatalf("agy workspace cwd: %+v", event)
	}
	if event, _ := readHookClaude([]byte(`{"tool_name":"Glob","tool_input":{"pattern":"*.go"}}`)); event.Kind != hookKindOther || event.Unparsed != "" {
		t.Fatalf("known read-only tool: %+v", event)
	}
}

// TestHookFixtureBackedMatchesTestdata keeps fixtureBacked equal to the
// presence of recorded inputs, and checks every recording reads without an
// error. A recording x.json may carry its expected event in x.event.json.
func TestHookFixtureBackedMatchesTestdata(t *testing.T) {
	for _, agent := range hookAgentNames() {
		recordings, _ := filepath.Glob(filepath.Join("testdata", "hook-fixtures", agent, "*.json"))
		var inputs []string
		for _, recording := range recordings {
			if !strings.HasSuffix(recording, ".event.json") {
				inputs = append(inputs, recording)
			}
		}
		if got := hookAgents[agent].fixtureBacked; got != (len(inputs) > 0) {
			t.Errorf("%s: fixtureBacked = %v with %d recordings", agent, got, len(inputs))
		}
		for _, input := range inputs {
			raw, err := os.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}
			event, err := hookAgents[agent].read(raw)
			if err != nil {
				t.Errorf("%s: %v", input, err)
				continue
			}
			wantRaw, err := os.ReadFile(strings.TrimSuffix(input, ".json") + ".event.json")
			if err != nil {
				continue
			}
			var want hookEvent
			if err := json.Unmarshal(wantRaw, &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(event, want) {
				t.Errorf("%s: event = %+v, want %+v", input, event, want)
			}
		}
	}
}

func TestHookDecideLayer(t *testing.T) {
	root := newHookStrictRoot(t)
	goFile := filepath.Join(root, "a.go")
	editing := hookRules{denyGoWrites: true, requireSession: true}
	cases := []struct {
		name  string
		rules hookRules
		event hookEvent
		want  string // "" allows
		edit  bool
	}{
		{"symbol search", hookRules{}, hookEvent{Kind: hookKindShell, Command: `grep -rn Decide .`}, "Go symbol search", false},
		{"region read", hookRules{}, hookEvent{Kind: hookKindShell, Command: `cat a.go`}, ".go region read", false},
		{"literal without reason", hookRules{}, hookEvent{Kind: hookKindShell, Command: `grep -rn "a b" --include=*.go .`}, "without a reason", false},
		{"count", hookRules{}, hookEvent{Kind: hookKindShell, Command: `grep -c Decide a.go`}, "", false},
		{"literal with reason", hookRules{}, hookEvent{Kind: hookKindShell, Command: `grep -rn "a b" --include=*.go . # gograph-cannot: comments`}, "", false},
		{"unparsed shell", hookRules{}, hookEvent{Kind: hookKindShell, Tool: "X", Unparsed: "no command"}, "cannot read its input", false},
		{"shell write by default", hookRules{}, hookEvent{Kind: hookKindShell, Command: `sed -i 's/a/b/' a.go`}, "", false},
		{"edit by default", hookRules{}, hookEvent{Kind: hookKindEdit, Paths: []string{goFile}}, "", false},
		{"patch by default", hookRules{}, hookEvent{Kind: hookKindPatch, Paths: []string{"a.go"}}, "", false},
		{"unparsed write by default", hookRules{}, hookEvent{Kind: hookKindPatch, Tool: "apply_patch", Unparsed: "bad patch"}, "", false},
		{"read", hookRules{}, hookEvent{Kind: hookKindRead, Paths: []string{goFile}}, "", false},
		{"other", hookRules{}, hookEvent{Kind: hookKindOther}, "", false},
		{"shell write, editing", editing, hookEvent{Kind: hookKindShell, Command: `sed -i 's/a/b/' a.go`}, ".go write", true},
		{"edit without session", editing, hookEvent{Kind: hookKindEdit, Paths: []string{goFile}}, "gograph session create", false},
		{"write without session", editing, hookEvent{Kind: hookKindWrite, Paths: []string{"sub/new.go"}}, "gograph session create", false},
		{"multi edit without session", editing, hookEvent{Kind: hookKindMultiEdit, Paths: []string{goFile}}, "gograph session create", false},
		{"patch with one go path", editing, hookEvent{Kind: hookKindPatch, Paths: []string{"notes.txt", "sub/b.go"}}, "gograph session create", false},
		{"unparsed write, editing", editing, hookEvent{Kind: hookKindPatch, Tool: "apply_patch", Unparsed: "bad patch"}, "cannot read its input", false},
		{"edit of non-go", editing, hookEvent{Kind: hookKindEdit, Paths: []string{filepath.Join(root, "README.md")}}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var first hookVerdict
			for index, agent := range hookAgentNames() {
				event := tc.event
				event.Agent, event.CWD = agent, root
				verdict := decideHookEvent(event, tc.rules)
				if index == 0 {
					first = verdict
				} else if verdict != first {
					t.Fatalf("decide depends on the agent: %s gave %+v, %s gave %+v", agent, verdict, hookAgentNames()[0], first)
				}
			}
			if tc.want == "" && first.Deny || tc.want != "" && !strings.Contains(first.Reason, tc.want) || first.UseEditTool != tc.edit {
				t.Fatalf("verdict = %+v, want reason containing %q, UseEditTool %v", first, tc.want, tc.edit)
			}
		})
	}

	if _, err := session.StartSessionAt(root, ""); err != nil {
		t.Fatal(err)
	}
	if verdict := decideHookEvent(hookEvent{Kind: hookKindPatch, CWD: root, Paths: []string{"a.go"}}, editing); verdict.Deny {
		t.Fatalf("patch with an open session denied: %q", verdict.Reason)
	}
}

func TestHookWriteLayer(t *testing.T) {
	deny := hookVerdict{Deny: true, Reason: "why"}
	cases := []struct {
		agent          string
		verdict        hookVerdict
		code           int
		stdout, stderr string
	}{
		{"claude", hookVerdict{}, 0, "", ""},
		{"claude", deny, 0, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"why"}}`, ""},
		{"codex", hookVerdict{}, 0, "", ""},
		{"codex", deny, 2, "", "why"},
		{"opencode", hookVerdict{}, 0, `{"decision":"allow"}`, ""},
		{"opencode", deny, 0, `{"decision":"deny","reason":"why"}`, ""},
		{"agy", hookVerdict{}, 0, `{"decision":"allow"}`, ""},
		{"agy", deny, 0, `{"decision":"deny","reason":"why"}`, ""},
	}
	for _, tc := range cases {
		var stdout, stderr bytes.Buffer
		code := hookAgents[tc.agent].write(tc.verdict, &stdout, &stderr)
		if code != tc.code || strings.TrimSpace(stdout.String()) != tc.stdout || strings.TrimSpace(stderr.String()) != tc.stderr {
			t.Errorf("%s %+v: code=%d stdout=%q stderr=%q", tc.agent, tc.verdict, code, stdout.String(), stderr.String())
		}
	}

	spec := hookAgents["codex"]
	reason := hookReasonFor(spec, hookVerdict{Deny: true, Reason: "r", UseEditTool: true})
	if !strings.Contains(reason, "Use apply_patch instead.") || !strings.Contains(reason, "no fixture-backed format for codex") {
		t.Fatalf("codex reason = %q", reason)
	}
}

// hookRun runs the command with input and returns whether it denied.
func hookRun(t *testing.T, agent, input string, flags ...string) (bool, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runHookWith(append([]string{"--agent", agent}, flags...), strings.NewReader(input), &stdout, &stderr)
	output := stdout.String() + stderr.String()
	denied := code == 2 || strings.Contains(output, `"deny"`)
	if !denied && code != 0 {
		t.Fatalf("%s: exit %d: %s", agent, code, output)
	}
	return denied, output
}

func TestHookFailsClosedForEveryAgent(t *testing.T) {
	shell := map[string]string{
		"claude":   `{"tool_name":"Bash","tool_input":{}}`,
		"codex":    `{"tool_name":"run_terminal","tool_input":{}}`,
		"opencode": `{"tool":"bash","args":{}}`,
		"agy":      `{"toolCall":{"name":"run_command","args":{}}}`,
	}
	write := map[string]map[string]string{
		"claude":   {"unknown write tool": `{"tool_name":"FileWriter","tool_input":{}}`},
		"codex":    {"unknown write tool": `{"tool_name":"write_file","tool_input":{}}`, "malformed patch": `{"tool_name":"apply_patch","tool_input":{"command":"*** Update File: a.go"}}`},
		"opencode": {"unknown write tool": `{"tool":"file_write","args":{}}`, "malformed patch": `{"tool":"apply_patch","args":{"patchText":"nonsense"}}`},
		"agy":      {"unknown write tool": `{"toolCall":{"name":"create_file","args":{}}}`, "edit without target": `{"toolCall":{"name":"write_to_file","args":{}}}`},
	}
	for _, agent := range hookAgentNames() {
		for name, input := range map[string]string{"unreadable shell call": shell[agent], "not json": "{", "no tool name": `{}`} {
			if denied, output := hookRun(t, agent, input); !denied || !strings.Contains(output, "no fixture-backed format for "+agent) {
				t.Errorf("%s %s: denied=%v output=%s", agent, name, denied, output)
			}
		}
		for name, input := range write[agent] {
			if denied, output := hookRun(t, agent, input); denied {
				t.Errorf("%s %s denied without an editing rule: %s", agent, name, output)
			}
			if denied, output := hookRun(t, agent, input, "--require-session"); !denied {
				t.Errorf("%s %s allowed with --require-session: %s", agent, name, output)
			}
		}
	}
	for agent, input := range map[string]string{
		"claude":   `{"tool_name":"SomeViewer","tool_input":{}}`,
		"codex":    `{"tool_name":"view_image","tool_input":{}}`,
		"opencode": `{"tool":"lsp_hover","args":{}}`,
		"agy":      `{"toolCall":{"name":"view_file","args":{}}}`,
	} {
		if denied, output := hookRun(t, agent, input, "--require-session", "--deny-go-writes"); denied {
			t.Errorf("%s unknown read-only tool denied: %s", agent, output)
		}
	}
}

// TestHookCrossAgentParity sends the same logical call in each agent's
// format and requires the same verdict, with the default (navigation) rules
// and with both editing rules.
func TestHookCrossAgentParity(t *testing.T) {
	root := newHookStrictRoot(t)
	t.Chdir(root)
	editing := []string{"--deny-go-writes", "--require-session"}
	cases := []struct {
		name                    string
		kind                    hookToolKind
		value                   string
		denied, deniedIfEditing bool
	}{
		{"symbol search", hookKindShell, `grep -rn Decide .`, true, true},
		{"region read", hookKindShell, `sed -n '1,20p' a.go`, true, true},
		{"literal without reason", hookKindShell, `rg -t go 'error: x'`, true, true},
		{"literal with reason", hookKindShell, `rg -t go 'error: x' # gograph-cannot: comment text`, false, false},
		{"text-tool write", hookKindShell, `echo x > a.go`, false, true},
		{"move", hookKindShell, `mv a.go sub/c.go`, false, false},
		{"git diff", hookKindShell, `git diff -- a.go`, false, false},
		{"edit .go", hookKindEdit, filepath.Join(root, "a.go"), false, true},
		{"write new .go", hookKindWrite, filepath.Join(root, "sub", "n.go"), false, true},
		{"multi edit .go", hookKindMultiEdit, filepath.Join(root, "a.go"), false, true},
		{"edit .md", hookKindEdit, filepath.Join(root, "README.md"), false, false},
		{"patch .go", hookKindPatch, hookTestPatch, false, true},
		{"read .go", hookKindRead, filepath.Join(root, "a.go"), false, false},
	}
	for _, tc := range cases {
		for _, agent := range hookAgentNames() {
			if !hookAgentHasKind(agent, tc.kind) {
				continue
			}
			input := hookCallFor(t, agent, tc.kind, tc.value, root)
			if denied, output := hookRun(t, agent, input); denied != tc.denied {
				t.Errorf("%s / %s: denied=%v, want %v: %s", tc.name, agent, denied, tc.denied, output)
			}
			if denied, output := hookRun(t, agent, input, editing...); denied != tc.deniedIfEditing {
				t.Errorf("%s / %s with editing rules: denied=%v, want %v: %s", tc.name, agent, denied, tc.deniedIfEditing, output)
			}
		}
	}
}

func hookAgentHasKind(agent string, kind hookToolKind) bool {
	for _, have := range hookAgentKinds[agent] {
		if have == kind {
			return true
		}
	}
	return false
}

func TestHookArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runHookWith([]string{"--agent", "vim"}, strings.NewReader("{}"), &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), `unknown --agent "vim"`) {
		t.Fatalf("unknown agent: code=%d stderr=%q", code, stderr.String())
	}
	stderr.Reset()
	if code := runHookWith([]string{"--agent"}, strings.NewReader("{}"), &stdout, &stderr); code != 2 {
		t.Fatalf("missing value: code=%d", code)
	}
	stdout.Reset()
	if code := runHookWith(nil, strings.NewReader(`{"tool_name":"Glob","tool_input":{}}`), &stdout, &stderr); code != 0 || stdout.Len() != 0 {
		t.Fatalf("default agent claude allow: code=%d stdout=%q", code, stdout.String())
	}
}

func TestHookRecord(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rec")
	big := strings.Repeat("x", 10000)
	input := hookJSON(t, map[string]any{"tool_name": "Write", "tool_input": map[string]any{"file_path": "/w/a.go", "content": big}})
	var stdout, stderr bytes.Buffer
	if code := runHookWith([]string{"--agent=claude", "--record", dir}, strings.NewReader(input), &stdout, &stderr); code != 0 || stdout.Len() != 0 {
		t.Fatalf("record must allow: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("dir: %v %v", info, err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "claude-*.json"))
	if len(files) != 1 {
		t.Fatalf("recordings = %v", files)
	}
	info, _ = os.Stat(files[0])
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v", info.Mode())
	}
	raw, _ := os.ReadFile(files[0])
	var recorded struct {
		ToolInput struct {
			FilePath string `json:"file_path"`
			Content  string `json:"content"`
		} `json:"tool_input"`
	}
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.ToolInput.FilePath != "/w/a.go" || !strings.HasPrefix(recorded.ToolInput.Content, strings.Repeat("x", hookRecordLimit)) || len(recorded.ToolInput.Content) > hookRecordLimit+32 {
		t.Fatalf("recorded content length %d, path %q", len(recorded.ToolInput.Content), recorded.ToolInput.FilePath)
	}
	if err := recordHookInput(dir, "codex", []byte("not json"), time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
}

func TestHookSelftest(t *testing.T) {
	root := newHookStrictRoot(t)
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("claude/grep.json", hookCallFor(t, "claude", hookKindShell, "grep -rn Decide .", root))
	write("codex/ls.json", hookCallFor(t, "codex", hookKindShell, "ls", root))
	write("verdicts.tsv", "# fixture\tverdict\treason\nclaude/grep.json\tdeny\tGo symbol search\ncodex/ls.json\tallow\n")
	var stdout, stderr bytes.Buffer
	if code := runHookWith([]string{"--selftest", dir}, nil, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "2 rows, 0 failures") {
		t.Fatalf("selftest pass: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	write("agy/x.json", hookCallFor(t, "agy", hookKindShell, "ls", root))
	write("verdicts.tsv", "claude/grep.json\tallow\ncodex/ls.json\tallow\n")
	stdout.Reset()
	if code := runHookWith([]string{"--selftest", dir}, nil, &stdout, &stderr); code != 1 ||
		!strings.Contains(stdout.String(), "FAIL\tclaude/grep.json") || !strings.Contains(stdout.String(), "FAIL\tagy/x.json\tno row") {
		t.Fatalf("selftest failures: code=%d stdout=%s", code, stdout.String())
	}
}

func TestInstallHookRegistersGographHookAndRemovesLegacyScript(t *testing.T) {
	home := t.TempDir()
	hooksDir := filepath.Join(home, ".claude", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(hooksDir, "gograph-guard.sh")
	if err := os.WriteFile(legacy, []byte(legacyHookScript), 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	before := map[string]any{
		"model": "keep-me",
		"hooks": map[string]any{"PreToolUse": []any{
			map[string]any{"matcher": "Bash", "hooks": []any{
				map[string]any{"type": "command", "command": legacy},
				map[string]any{"type": "command", "command": "other-hook"},
			}},
			map[string]any{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": legacy}}},
			map[string]any{"matcher": "Edit|Write|MultiEdit|NotebookEdit|Read", "hooks": []any{map[string]any{"type": "command", "command": claudeHookCommand}}},
		}},
	}
	if err := os.WriteFile(settingsPath, []byte(hookJSON(t, before)), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 { // idempotent
		if err := installHook(home); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var after struct {
		Model string `json:"model"`
		Hooks struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, group := range after.Hooks.PreToolUse {
		for _, hook := range group.Hooks {
			got = append(got, group.Matcher+" -> "+hook.Command)
		}
	}
	want := []string{"Bash -> other-hook", "Bash -> gograph hook --agent claude"}
	if after.Model != "keep-me" || !reflect.DeepEqual(got, want) {
		t.Fatalf("settings: model=%q hooks=%q, want %q", after.Model, got, want)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("unchanged legacy script not removed: %v", err)
	}

	edited := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(filepath.Join(edited, ".claude", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(edited, ".claude", "hooks", "gograph-guard.sh")
	if err := os.WriteFile(custom, []byte("#!/bin/sh\nexec my-own-check\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installHook(edited); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(custom); err != nil {
		t.Fatalf("an edited legacy script must be kept: %v", err)
	}
}
