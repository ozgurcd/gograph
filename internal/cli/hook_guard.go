package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ozgurcd/gograph/internal/rootfind"
)

// hookGuardInput is the JSON structure Claude Code sends to PreToolUse hooks.
type hookGuardInput struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
	CWD       string         `json:"cwd"`
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
	cwd := input.CWD
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return 0
		}
	}
	return evaluateHookCommandAt(command, cwd)
}

// evaluateHookCommandAt returns 2 (block) or 0 (allow).
func evaluateHookCommandAt(command, cwd string) int {
	return evaluateHookCommandAtTo(command, cwd, os.Stdout)
}

func evaluateHookCommandAtTo(command, cwd string, output io.Writer) int {
	invocation, ok := parseHookCommand(command)
	if !ok {
		return 0
	}
	decision := classifyHookSearchInvocation(invocation)
	if decision.block && !hookSearchesIndexedRepository(invocation, cwd) {
		decision = hookDecision{}
	}
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
