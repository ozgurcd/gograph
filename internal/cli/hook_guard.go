package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ozgurcd/gograph/internal/rootfind"
)

// hookGuardInput is the PreToolUse JSON Claude Code and Codex send to hooks.
// Codex adds turn_id; Antigravity sends toolCall instead (hookAgyInput).
type hookGuardInput struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
	CWD       string         `json:"cwd"`
	TurnID    string         `json:"turn_id"`
	ToolCall  *hookAgyCall   `json:"toolCall"`
	// Antigravity common fields.
	WorkspacePaths []string `json:"workspacePaths"`
}

type hookAgyCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type hookAgent uint8

const (
	hookAgentClaude hookAgent = iota
	hookAgentCodex
	hookAgentAntigravity
)

// hookCall is a tool call normalized across agents.
type hookCall struct {
	agent     hookAgent
	shell     string   // shell command line, when the call runs one
	editPaths []string // files the call edits or creates
	cwd       string
}

func normalizeHookCall(input hookGuardInput) hookCall {
	if input.ToolCall != nil {
		call := hookCall{agent: hookAgentAntigravity}
		if len(input.WorkspacePaths) > 0 {
			call.cwd = input.WorkspacePaths[0]
		}
		args := input.ToolCall.Args
		switch input.ToolCall.Name {
		case "run_command":
			call.shell, _ = args["CommandLine"].(string)
			if cwd, _ := args["Cwd"].(string); cwd != "" {
				call.cwd = cwd
			}
		case "write_to_file", "replace_file_content", "multi_replace_file_content":
			if target, _ := args["TargetFile"].(string); target != "" {
				call.editPaths = []string{target}
			}
		}
		return call
	}

	call := hookCall{agent: hookAgentClaude, cwd: input.CWD}
	if input.TurnID != "" {
		call.agent = hookAgentCodex
	}
	switch input.ToolName {
	case "Bash":
		call.shell, _ = input.ToolInput["command"].(string)
	case "apply_patch":
		patch, _ := input.ToolInput["command"].(string)
		call.editPaths = hookApplyPatchPaths(patch)
	case "Edit", "Write", "MultiEdit":
		if path, _ := input.ToolInput["file_path"].(string); path != "" {
			call.editPaths = []string{path}
		}
	}
	return call
}

// runHookGuard reads a PreToolUse tool call from stdin and allows or denies
// it. Claude Code and Codex: exit 0 allows; exit 2 denies with the reason on
// stderr. Antigravity: always exit 0 with {"decision":"allow"|"deny"} on
// stdout, since it treats any other output as allow. With --ask, Claude Code
// and Antigravity ask the user instead; Codex cannot ask, so it still denies.
func runHookGuard(args []string) int {
	var options hookGuardOptions
	for _, arg := range args {
		switch arg {
		case "--strict":
			options.denyGoWrites, options.denyRegionReads = true, true
			options.requireReason, options.requireSession = true, true
		case "--deny-go-writes":
			options.denyGoWrites = true
		case "--deny-region-reads":
			options.denyRegionReads = true
		case "--require-reason":
			options.requireReason = true
		case "--require-session":
			options.requireSession = true
		case "--ask":
			options.ask = true
		default:
			fmt.Fprintf(os.Stderr, "gograph-guard: unknown flag %q; ignoring it\n", arg)
		}
	}
	var input hookGuardInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return 0 // can't parse — allow through
	}
	call := normalizeHookCall(input)
	if call.cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			call.cwd = wd
		}
	}
	reason := evaluateHookCall(call, options)
	return emitHookVerdict(call.agent, reason, options.ask, os.Stdout, os.Stderr)
}

func evaluateHookCall(call hookCall, options hookGuardOptions) string {
	if len(call.editPaths) > 0 {
		if options.requireSession {
			return evaluateHookStrictEdit(call.editPaths, call.cwd)
		}
		return ""
	}
	if call.shell == "" || call.cwd == "" {
		return ""
	}
	if options.shellRules() {
		options.allowRegionReads = call.agent == hookAgentCodex
		return evaluateHookStrictShell(call.shell, call.cwd, options)
	}
	return hookSymbolSearchReason(call.shell, call.cwd)
}

// emitHookVerdict delivers the decision in the agent's format. ask turns a
// denial into an approval prompt where the agent supports one.
func emitHookVerdict(agent hookAgent, reason string, ask bool, stdout, stderr io.Writer) int {
	if agent == hookAgentAntigravity {
		verdict := map[string]string{"decision": "allow"}
		if reason != "" {
			decision := "deny"
			if ask {
				decision = "ask"
			}
			verdict = map[string]string{"decision": decision, "reason": reason}
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(verdict)
		return 0
	}
	if reason == "" {
		return 0
	}
	if ask && agent == hookAgentClaude {
		output := map[string]any{"hookSpecificOutput": map[string]string{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "ask",
			"permissionDecisionReason": reason,
		}}
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(output)
		return 0
	}
	_, _ = fmt.Fprintln(stderr, reason)
	return 2
}

// evaluateHookCommandAtTo writes the symbol-search block message to output
// and returns 2 (block) or 0 (allow).
func evaluateHookCommandAtTo(command, cwd string, output io.Writer) int {
	reason := hookSymbolSearchReason(command, cwd)
	if reason == "" {
		return 0
	}
	_, _ = fmt.Fprint(output, reason)
	return 2
}

// hookSymbolSearchReason returns the block message for a Go symbol search in
// an indexed repository, or "".
func hookSymbolSearchReason(command, cwd string) string {
	invocation, ok := parseHookCommand(command)
	if !ok {
		return ""
	}
	decision := classifyHookSearchInvocation(invocation)
	if !decision.block || !hookSearchesIndexedRepository(invocation, cwd) {
		return ""
	}
	return hookSymbolSearchMessage(command, decision.symbols[0])
}

func hookSymbolSearchMessage(command, pattern string) string {
	return fmt.Sprintf(`gograph-guard: blocked grep — this looks like a Go symbol search.
  Blocked:  %s
  Start with gograph's AST-derived structural tools (CLI / MCP):
    gograph query "%[2]s"   / gograph_query "%[2]s"     declarations: symbols, files, packages
    gograph callers "%[2]s" / gograph_callers "%[2]s"   call sites
    gograph usages "%[2]s"  / gograph_usages "%[2]s"    every use of the name
    gograph context "%[2]s" / gograph_context "%[2]s"   node + source + callers + callees + tests
    gograph impact "%[2]s"  / gograph_impact "%[2]s"    downstream blast radius

  If precision is ast/precise_fallback, a result is ambiguous, or a known
  source call is missing, verify with gopls or a targeted source/text search.
  For raw text search (comments, strings) target files explicitly:
    grep -r "..." --include="*.md"
    grep -r "..." --include="*.yaml"
`, command, pattern)
}

func hookSearchesIndexedRepository(invocation hookSearchInvocation, cwd string) bool {
	if cwd == "" {
		return false
	}
	if !filepath.IsAbs(cwd) {
		absolute, err := filepath.Abs(cwd)
		if err != nil {
			return false
		}
		cwd = absolute
	}

	for _, target := range hookSearchTargets(invocation) {
		if !filepath.IsAbs(target) {
			target = filepath.Join(cwd, target)
		}
		if _, ok := rootfind.FindRootFrom(target); ok {
			return true
		}
	}
	return false
}

func hookSearchTargets(invocation hookSearchInvocation) []string {
	var targets []string
	if len(invocation.inputPaths) > 0 {
		inputPath := invocation.inputPaths[len(invocation.inputPaths)-1]
		if hookPathCanContainGo(inputPath) &&
			(hookHasStdinPath(invocation.paths) ||
				(len(invocation.paths) == 0 && (invocation.tool != hookToolGrep || !invocation.recursive))) {
			targets = append(targets, inputPath)
		}
	}

	if len(invocation.paths) == 0 {
		if len(targets) == 0 {
			targets = append(targets, ".")
		}
		return targets
	}

	for _, path := range invocation.paths {
		if hookPathCanContainGo(path) {
			targets = append(targets, path)
		}
	}
	return targets
}
