package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// hookGuardInput is the JSON structure Claude Code sends to PreToolUse hooks.
type hookGuardInput struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
}

// runHookGuard reads a JSON tool call from stdin and decides whether to allow or block it.
// Exit 0 = allow, exit 2 = block (Claude sees the message and tries differently).
func runHookGuard() int {
	var input hookGuardInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return 0 // can't parse — allow through
	}
	if input.ToolName != "Bash" {
		return 0
	}
	command, _ := input.ToolInput["command"].(string)
	if command == "" {
		return 0
	}
	return evaluateHookCommand(command)
}

// evaluateHookCommand returns 2 (block) or 0 (allow).
func evaluateHookCommand(command string) int {
	return evaluateHookCommandTo(command, os.Stdout)
}

func evaluateHookCommandTo(command string, output io.Writer) int {
	decision := classifyHookCommand(command)
	if !decision.block {
		return 0
	}
	pattern := decision.symbols[0]

	_, _ = fmt.Fprintf(output, `gograph-guard: blocked grep — this looks like a Go symbol search.
  Blocked:  %s
  Start with gograph's AST-derived structural tools:
    gograph_query "%s"          search symbols, files, packages
    gograph_context "%s"        node + source + callers + callees + tests
    gograph_callers "%s"        who calls this symbol
    gograph_impact "%s"         downstream blast radius

  If precision is ast/precise_fallback, a result is ambiguous, or a known
  source call is missing, verify with gopls or a targeted source/text search.
  For raw text search (comments, strings) target files explicitly:
    grep -r "..." --include="*.md"
    grep -r "..." --include="*.yaml"
`, command, pattern, pattern, pattern, pattern)
	return 2
}
