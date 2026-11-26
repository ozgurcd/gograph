package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// gograph hook --agent <claude|codex|opencode|agy> is a PreToolUse hook in
// three layers:
//   - read (per agent): the agent's tool-call JSON -> one hookEvent;
//   - decide (shared): hookEvent -> hookVerdict; it never looks at the agent;
//   - write (per agent): hookVerdict -> the agent's output and exit code.
// Agent formats live only in read and write. A shell or write-capable tool
// whose input cannot be parsed is denied (fail closed); an unknown read-only
// tool is allowed.

type hookToolKind string

const (
	hookKindShell     hookToolKind = "shell"
	hookKindEdit      hookToolKind = "edit"
	hookKindWrite     hookToolKind = "write"
	hookKindMultiEdit hookToolKind = "multi_edit"
	hookKindPatch     hookToolKind = "patch"
	hookKindRead      hookToolKind = "read"
	hookKindOther     hookToolKind = "other"
)

// hookEvent is a tool call in agent-independent form.
type hookEvent struct {
	Agent   string       `json:"agent"`
	Event   string       `json:"event"`
	Kind    hookToolKind `json:"tool_kind"`
	Tool    string       `json:"tool"`
	Command string       `json:"command,omitempty"`
	Paths   []string     `json:"paths,omitempty"`
	CWD     string       `json:"cwd,omitempty"`
	Session string       `json:"session,omitempty"`
	// Unparsed names why a shell or write-capable tool's input could not be
	// read; decide denies such a call.
	Unparsed string `json:"unparsed,omitempty"`
}

// hookVerdict is the shared verdict. UseEditTool asks the write layer to
// point at the agent's own edit tool.
type hookVerdict struct {
	Deny        bool
	Reason      string
	UseEditTool bool
}

type hookAgentSpec struct {
	name     string
	read     func([]byte) (hookEvent, error)
	write    func(hookVerdict, io.Writer, io.Writer) int
	editTool string
	// fixtureBacked is true once recorded inputs of this agent are in
	// testdata/hook-fixtures/<agent>/ (TestHookFixtureBackedMatchesTestdata).
	fixtureBacked bool
}

var hookAgents = map[string]hookAgentSpec{
	"claude":   {name: "claude", read: readHookClaude, write: writeHookClaude, editTool: "the Edit or Write tool"},
	"codex":    {name: "codex", read: readHookCodex, write: writeHookCodex, editTool: "apply_patch"},
	"opencode": {name: "opencode", read: readHookOpencode, write: writeHookDecisionJSON, editTool: "the edit or write tool"},
	"agy":      {name: "agy", read: readHookAgy, write: writeHookDecisionJSON, editTool: "replace_file_content or write_to_file"},
}

func hookAgentNames() []string {
	names := make([]string, 0, len(hookAgents))
	for name := range hookAgents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ---------------------------------------------------------------- read

type hookJSONInput struct {
	HookEventName string          `json:"hook_event_name"`
	SessionID     string          `json:"session_id"`
	CWD           string          `json:"cwd"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
}

func decodeHookObject(raw json.RawMessage) map[string]any {
	var object map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &object)
	}
	return object
}

func hookString(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := args[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

// hookPathEvent fills Paths from the first non-empty key, or marks the call
// unparsed.
func hookPathEvent(event hookEvent, args map[string]any, keys ...string) hookEvent {
	if path := hookString(args, keys...); path != "" {
		event.Paths = []string{path}
		return event
	}
	if event.Kind == hookKindRead {
		return event
	}
	event.Unparsed = "no " + strings.Join(keys, "/") + " in its input"
	return event
}

func hookShellEvent(event hookEvent, command string) hookEvent {
	if strings.TrimSpace(command) == "" {
		event.Unparsed = "no command in its input"
		return event
	}
	event.Command = command
	return event
}

func hookPatchEvent(event hookEvent, patch string) hookEvent {
	paths, err := parseHookPatch(patch)
	if err != nil {
		event.Unparsed = err.Error()
		return event
	}
	event.Paths = paths
	return event
}

// hookUnknownTool classifies a tool the read layer does not know: a name
// that suggests it runs commands or writes files is denied as unparsed,
// any other name is treated as read-only.
func hookUnknownTool(event hookEvent) hookEvent {
	name := strings.ToLower(event.Tool)
	for _, word := range []string{"bash", "shell", "exec", "command", "run", "terminal", "write", "edit", "patch", "replace", "create", "delete", "remove", "move", "rename"} {
		if strings.Contains(name, word) {
			event.Unparsed = "unknown tool that may run commands or write files"
			return event
		}
	}
	event.Kind = hookKindOther
	return event
}

func readHookJSONEnvelope(agent string, raw []byte) (hookEvent, map[string]any, error) {
	var input hookJSONInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return hookEvent{}, nil, fmt.Errorf("input is not JSON: %w", err)
	}
	event := hookEvent{Agent: agent, Event: input.HookEventName, Tool: input.ToolName, CWD: input.CWD, Session: input.SessionID}
	if event.Event == "" {
		event.Event = "PreToolUse"
	}
	return event, decodeHookObject(input.ToolInput), nil
}

func readHookClaude(raw []byte) (hookEvent, error) {
	event, args, err := readHookJSONEnvelope("claude", raw)
	if err != nil {
		return event, err
	}
	switch event.Tool {
	case "Bash":
		event.Kind = hookKindShell
		return hookShellEvent(event, hookString(args, "command")), nil
	case "Edit":
		event.Kind = hookKindEdit
		return hookPathEvent(event, args, "file_path"), nil
	case "Write":
		event.Kind = hookKindWrite
		return hookPathEvent(event, args, "file_path"), nil
	case "MultiEdit":
		event.Kind = hookKindMultiEdit
		return hookPathEvent(event, args, "file_path"), nil
	case "NotebookEdit":
		event.Kind = hookKindEdit
		return hookPathEvent(event, args, "notebook_path"), nil
	case "Read":
		event.Kind = hookKindRead
		return hookPathEvent(event, args, "file_path"), nil
	case "Glob", "Grep", "LS", "WebFetch", "WebSearch", "Task", "TodoWrite", "ToolSearch":
		event.Kind = hookKindOther
		return event, nil
	}
	return hookUnknownTool(event), nil
}

func readHookCodex(raw []byte) (hookEvent, error) {
	event, args, err := readHookJSONEnvelope("codex", raw)
	if err != nil {
		return event, err
	}
	switch event.Tool {
	case "Bash", "shell", "local_shell", "exec_command", "container.exec":
		event.Kind = hookKindShell
		return hookShellEvent(event, hookCodexCommand(args)), nil
	case "apply_patch":
		event.Kind = hookKindPatch
		patch := hookString(args, "command", "input", "patch")
		if patch == "" {
			patch = hookCodexCommand(args)
		}
		return hookPatchEvent(event, patch), nil
	}
	return hookUnknownTool(event), nil
}

// hookCodexCommand reads a command given as a string or an argv array;
// ["bash", "-lc", script] yields the script.
func hookCodexCommand(args map[string]any) string {
	if command := hookString(args, "command", "cmd"); command != "" {
		return command
	}
	list, _ := args["command"].([]any)
	argv := make([]string, 0, len(list))
	for _, item := range list {
		word, ok := item.(string)
		if !ok {
			return ""
		}
		argv = append(argv, word)
	}
	if len(argv) == 3 && (argv[1] == "-lc" || argv[1] == "-c") {
		switch filepath.Base(argv[0]) {
		case "bash", "sh", "zsh":
			return argv[2]
		}
	}
	if len(argv) >= 2 && argv[0] == "apply_patch" {
		return argv[len(argv)-1]
	}
	quoted := make([]string, len(argv))
	for index, word := range argv {
		quoted[index] = hookShellQuote(word)
	}
	return strings.Join(quoted, " ")
}

func hookShellQuote(word string) string {
	if word != "" && !strings.ContainsAny(word, " \t\n'\"\\$`|&;<>()*?[]{}~#!") {
		return word
	}
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}

// opencode has no command hooks; a plugin calls the hook with
// {"tool", "args", "cwd", "sessionID"} built from tool.execute.before.
type hookOpencodeInput struct {
	Tool      string          `json:"tool"`
	Args      json.RawMessage `json:"args"`
	CWD       string          `json:"cwd"`
	SessionID string          `json:"sessionID"`
}

func readHookOpencode(raw []byte) (hookEvent, error) {
	var input hookOpencodeInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return hookEvent{}, fmt.Errorf("input is not JSON: %w", err)
	}
	event := hookEvent{Agent: "opencode", Event: "tool.execute.before", Tool: input.Tool, CWD: input.CWD, Session: input.SessionID}
	args := decodeHookObject(input.Args)
	switch input.Tool {
	case "bash":
		event.Kind = hookKindShell
		if workdir := hookString(args, "workdir"); workdir != "" {
			event.CWD = workdir
		}
		return hookShellEvent(event, hookString(args, "command")), nil
	case "edit":
		event.Kind = hookKindEdit
		return hookPathEvent(event, args, "filePath"), nil
	case "write":
		event.Kind = hookKindWrite
		return hookPathEvent(event, args, "filePath"), nil
	case "multiedit":
		event.Kind = hookKindMultiEdit
		return hookPathEvent(event, args, "filePath"), nil
	case "apply_patch", "patch":
		event.Kind = hookKindPatch
		return hookPatchEvent(event, hookString(args, "patchText")), nil
	case "read":
		event.Kind = hookKindRead
		return hookPathEvent(event, args, "filePath"), nil
	case "glob", "grep", "list", "webfetch", "websearch", "todowrite", "todoread", "task", "skill":
		event.Kind = hookKindOther
		return event, nil
	}
	return hookUnknownTool(event), nil
}

type hookAgyInput struct {
	HookEventName  string   `json:"hookEventName"`
	CWD            string   `json:"cwd"`
	WorkspacePaths []string `json:"workspacePaths"`
	ConversationID string   `json:"conversationId"`
	ToolCall       *struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"toolCall"`
}

// readHookAgy reads Antigravity's hooks.json input. Tool names and argument
// keys come from hook-guard's existing Antigravity support and are not yet
// fixture-backed.
func readHookAgy(raw []byte) (hookEvent, error) {
	var input hookAgyInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return hookEvent{}, fmt.Errorf("input is not JSON: %w", err)
	}
	if input.ToolCall == nil {
		return hookEvent{}, errors.New("input has no toolCall")
	}
	event := hookEvent{Agent: "agy", Event: input.HookEventName, Tool: input.ToolCall.Name, CWD: input.CWD, Session: input.ConversationID}
	if event.CWD == "" && len(input.WorkspacePaths) > 0 {
		event.CWD = input.WorkspacePaths[0]
	}
	args := decodeHookObject(input.ToolCall.Args)
	switch input.ToolCall.Name {
	case "run_command":
		event.Kind = hookKindShell
		if cwd := hookString(args, "Cwd"); cwd != "" {
			event.CWD = cwd
		}
		return hookShellEvent(event, hookString(args, "CommandLine")), nil
	case "write_to_file":
		event.Kind = hookKindWrite
		return hookPathEvent(event, args, "TargetFile"), nil
	case "replace_file_content":
		event.Kind = hookKindEdit
		return hookPathEvent(event, args, "TargetFile"), nil
	case "multi_replace_file_content":
		event.Kind = hookKindMultiEdit
		return hookPathEvent(event, args, "TargetFile"), nil
	}
	return hookUnknownTool(event), nil
}

// parseHookPatch returns every path an apply_patch body touches. A body
// without the patch envelope or without a file header is an error.
func parseHookPatch(patch string) ([]string, error) {
	if strings.TrimSpace(patch) == "" {
		return nil, errors.New("empty patch")
	}
	var paths []string
	begun, ended := false, false
	for _, line := range strings.Split(patch, "\n") {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case "*** Begin Patch":
			begun = true
			continue
		case "*** End Patch":
			ended = true
			continue
		}
		for _, prefix := range []string{"*** Add File:", "*** Update File:", "*** Delete File:", "*** Move to:"} {
			if path, ok := strings.CutPrefix(trimmed, prefix); ok {
				path = strings.TrimSpace(path)
				if path == "" {
					return nil, fmt.Errorf("patch header %q names no file", trimmed)
				}
				paths = append(paths, path)
			}
		}
	}
	if !begun || !ended {
		return nil, errors.New("patch lacks *** Begin Patch / *** End Patch")
	}
	if len(paths) == 0 {
		return nil, errors.New("patch names no file")
	}
	return paths, nil
}

// ---------------------------------------------------------------- decide

var hookAllStrictRules = hookGuardOptions{denyGoWrites: true, denyRegionReads: true, requireReason: true, requireSession: true}

// decideHookEvent applies the gograph-first rules. It never reads
// event.Agent.
func decideHookEvent(event hookEvent) hookVerdict {
	if event.Unparsed != "" {
		return hookVerdict{Deny: true, Reason: fmt.Sprintf("%s %s call — cannot read its input (%s). A tool that can run commands or write files is denied when its input cannot be checked.", hookStrictPrefix, event.Tool, event.Unparsed)}
	}
	cwd := event.CWD
	switch event.Kind {
	case hookKindShell:
		reason := evaluateHookStrictShell(event.Command, cwd, hookAllStrictRules)
		return hookVerdict{Deny: reason != "", Reason: reason, UseEditTool: strings.HasPrefix(reason, hookStrictPrefix+" .go write")}
	case hookKindEdit, hookKindWrite, hookKindMultiEdit, hookKindPatch:
		reason := evaluateHookStrictEdit(event.Paths, cwd)
		return hookVerdict{Deny: reason != "", Reason: reason}
	}
	return hookVerdict{}
}

// ---------------------------------------------------------------- write

// hookReasonFor adds the agent-specific parts of a denial.
func hookReasonFor(spec hookAgentSpec, decision hookVerdict) string {
	reason := decision.Reason
	if decision.UseEditTool {
		reason += "\n  Use " + spec.editTool + " instead."
	}
	if !spec.fixtureBacked {
		reason += "\n  (no fixture-backed format for " + spec.name + ")"
	}
	return reason
}

// writeHookClaude: allow is exit 0 with no output, so Claude Code's own
// permission rules still apply; deny is a PreToolUse permissionDecision.
func writeHookClaude(decision hookVerdict, stdout, _ io.Writer) int {
	if !decision.Deny {
		return 0
	}
	return writeHookJSON(stdout, map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName":            "PreToolUse",
		"permissionDecision":       "deny",
		"permissionDecisionReason": decision.Reason,
	}})
}

// writeHookCodex: exit 0 allows; exit 2 denies with the reason on stderr.
func writeHookCodex(decision hookVerdict, _, stderr io.Writer) int {
	if !decision.Deny {
		return 0
	}
	_, _ = fmt.Fprintln(stderr, decision.Reason)
	return 2
}

// writeHookDecisionJSON (opencode, agy): exit 0 with
// {"decision":"allow"} or {"decision":"deny","reason":...} on stdout.
func writeHookDecisionJSON(decision hookVerdict, stdout, _ io.Writer) int {
	if !decision.Deny {
		return writeHookJSON(stdout, map[string]string{"decision": "allow"})
	}
	return writeHookJSON(stdout, map[string]string{"decision": "deny", "reason": decision.Reason})
}

func writeHookJSON(stdout io.Writer, value any) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return 0
}

// ---------------------------------------------------------------- command

func runHook(args []string) int {
	return runHookWith(args, os.Stdin, os.Stdout, os.Stderr)
}

func runHookWith(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	agentName, recordDir, selftestDir := "claude", "", ""
	for index := 0; index < len(args); index++ {
		arg := args[index]
		name, value, hasValue := strings.Cut(arg, "=")
		switch name {
		case "--agent", "--record", "--selftest":
		default:
			_, _ = fmt.Fprintf(stderr, "gograph hook: unknown argument %q\n", arg)
			return 2
		}
		if !hasValue {
			if index+1 >= len(args) {
				_, _ = fmt.Fprintf(stderr, "gograph hook: %s requires a value\n", name)
				return 2
			}
			index++
			value = args[index]
		}
		switch name {
		case "--agent":
			agentName = value
		case "--record":
			recordDir = value
		case "--selftest":
			selftestDir = value
		}
	}
	spec, ok := hookAgents[agentName]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "gograph hook: unknown --agent %q (want %s)\n", agentName, strings.Join(hookAgentNames(), ", "))
		return 2
	}
	if selftestDir != "" {
		return runHookSelftest(selftestDir, stdout, stderr)
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, 64<<20))
	if err != nil {
		return spec.write(hookVerdict{Deny: true, Reason: hookReasonFor(spec, hookVerdict{Reason: hookStrictPrefix + " — cannot read the hook input: " + err.Error()})}, stdout, stderr)
	}
	if recordDir != "" {
		if err := recordHookInput(recordDir, spec.name, raw, time.Now()); err != nil {
			_, _ = fmt.Fprintf(stderr, "gograph hook: record: %v\n", err)
		}
		return spec.write(hookVerdict{}, stdout, stderr)
	}
	return spec.write(evaluateHookInput(spec, raw), stdout, stderr)
}

// evaluateHookInput runs read and decide for one agent's raw input.
func evaluateHookInput(spec hookAgentSpec, raw []byte) hookVerdict {
	event, err := spec.read(raw)
	if err != nil {
		event = hookEvent{Agent: spec.name, Tool: "unknown", Unparsed: err.Error()}
	}
	if event.CWD == "" {
		if wd, err := os.Getwd(); err == nil {
			event.CWD = wd
		}
	}
	decision := decideHookEvent(event)
	if decision.Deny {
		decision.Reason = hookReasonFor(spec, decision)
		decision.UseEditTool = false
	}
	return decision
}

// recordHookInput writes raw to dir/<agent>-<time>.json (dir 0700, file
// 0600). JSON string values longer than 4 KiB are cut to 4 KiB.
func recordHookInput(dir, agent string, raw []byte, now time.Time) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	content := raw
	var value any
	if json.Unmarshal(raw, &value) == nil {
		if encoded, err := json.MarshalIndent(truncateHookStrings(value), "", "  "); err == nil {
			content = append(encoded, '\n')
		}
	} else if len(content) > hookRecordLimit {
		content = content[:hookRecordLimit]
	}
	name := filepath.Join(dir, fmt.Sprintf("%s-%s.json", agent, now.UTC().Format("20060102T150405.000000000Z")))
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

const hookRecordLimit = 4096

func truncateHookStrings(value any) any {
	switch typed := value.(type) {
	case string:
		if len(typed) > hookRecordLimit {
			return typed[:hookRecordLimit] + "…[truncated]"
		}
	case map[string]any:
		for key, item := range typed {
			typed[key] = truncateHookStrings(item)
		}
	case []any:
		for index, item := range typed {
			typed[index] = truncateHookStrings(item)
		}
	}
	return value
}

// runHookSelftest replays dir/verdicts.tsv: each line is
// "<agent>/<file>\t<allow|deny>[\t<reason substring>]", the file relative
// to dir. Every *.json under dir/<agent>/ must have a row.
func runHookSelftest(dir string, stdout, stderr io.Writer) int {
	table, err := os.Open(filepath.Join(dir, "verdicts.tsv"))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "gograph hook: selftest: %v\n", err)
		return 2
	}
	defer func() { _ = table.Close() }()
	covered := map[string]bool{}
	rows, failures := 0, 0
	scanner := bufio.NewScanner(table)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimRight(scanner.Text(), "\r")
		if strings.TrimSpace(text) == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Split(text, "\t")
		if len(fields) < 2 || (fields[1] != "allow" && fields[1] != "deny") {
			_, _ = fmt.Fprintf(stderr, "verdicts.tsv:%d: want <agent>/<file>\\t<allow|deny>[\\t<reason substring>]\n", line)
			failures++
			continue
		}
		rows++
		fixture := filepath.ToSlash(filepath.Clean(fields[0]))
		covered[fixture] = true
		agent, _, _ := strings.Cut(fixture, "/")
		spec, ok := hookAgents[agent]
		got, detail := "error", ""
		if !ok {
			detail = "unknown agent " + agent
		} else if raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(fixture))); err != nil {
			detail = err.Error()
		} else {
			decision := evaluateHookInput(spec, raw)
			got, detail = "allow", ""
			if decision.Deny {
				got, detail = "deny", decision.Reason
			}
		}
		pass := got == fields[1] && (len(fields) < 3 || strings.Contains(detail, fields[2]))
		status := "ok"
		if !pass {
			status = "FAIL"
			failures++
		}
		_, _ = fmt.Fprintf(stdout, "%s\t%s\twant %s\tgot %s\n", status, fixture, fields[1], got)
		if !pass && detail != "" {
			_, _ = fmt.Fprintf(stdout, "\t%s\n", strings.ReplaceAll(detail, "\n", "\n\t"))
		}
	}
	if err := scanner.Err(); err != nil {
		_, _ = fmt.Fprintf(stderr, "gograph hook: selftest: %v\n", err)
		return 2
	}
	for _, agent := range hookAgentNames() {
		files, _ := filepath.Glob(filepath.Join(dir, agent, "*.json"))
		for _, file := range files {
			relative := agent + "/" + filepath.Base(file)
			if !covered[relative] {
				_, _ = fmt.Fprintf(stdout, "FAIL\t%s\tno row in verdicts.tsv\n", relative)
				failures++
			}
		}
	}
	_, _ = fmt.Fprintf(stdout, "%d rows, %d failures\n", rows, failures)
	if failures > 0 {
		return 1
	}
	return 0
}

// printHookHelp prints the command help and each agent's registration.
func printHookHelp(stdout io.Writer) {
	printCommandHelp("hook")
	_, _ = fmt.Fprint(stdout, hookRegistrationHelp)
}

const hookRegistrationHelp = `
REGISTRATION
  Claude Code (~/.claude/settings.json):
    {
      "hooks": {
        "PreToolUse": [
          {"matcher": "Bash",
           "hooks": [{"type": "command", "command": "gograph hook --agent claude"}]},
          {"matcher": "Edit|Write|MultiEdit|NotebookEdit|Read",
           "hooks": [{"type": "command", "command": "gograph hook --agent claude"}]}
        ]
      }
    }

  Codex (~/.codex/config.toml):
    [[hooks.PreToolUse]]
    matcher = "^(Bash|shell|local_shell|exec_command|apply_patch)$"
    [[hooks.PreToolUse.hooks]]
    type = "command"
    command = "gograph hook --agent codex"

  opencode (plugin, e.g. ~/.config/opencode/plugin/gograph-hook.js):
    export const GographHook = async ({ $, directory }) => ({
      "tool.execute.before": async (input, output) => {
        // bash, edit, write, multiedit, apply_patch and read are checked;
        // other tools are allowed by the hook.
        const event = JSON.stringify({ tool: input.tool, sessionID: input.sessionID,
                                       cwd: directory, args: output.args });
        const run = await $` + "`" + `gograph hook --agent opencode < ${new Response(event)}` + "`" + `.quiet().nothrow();
        let verdict = { decision: "deny", reason: "gograph hook failed: " + run.stderr };
        if (run.exitCode === 0) { try { verdict = JSON.parse(run.stdout.toString()); } catch {} }
        if (verdict.decision !== "allow") throw new Error(verdict.reason);
      },
    });

  Antigravity (~/.gemini/config/hooks.json):
    {
      "PreToolUse": [
        {"matcher": "run_command|write_to_file|replace_file_content|multi_replace_file_content",
         "hooks": [{"type": "command", "command": "gograph hook --agent agy"}]}
      ]
    }

OUTPUT
  claude: allow = exit 0, no output; deny = exit 0 with
    {"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny",...}}.
  codex: allow = exit 0; deny = exit 2, reason on stderr.
  opencode, agy: exit 0 with {"decision":"allow"} or {"decision":"deny","reason":...}.
`
