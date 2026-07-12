package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
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
	// Only intercept grep / rg
	if !regexp.MustCompile(`^\s*(grep|rg)\b`).MatchString(command) {
		return 0
	}

	// Allow if targeting explicit non-Go extensions
	nonGo := regexp.MustCompile(`\.(yaml|yml|json|md|sh|toml|env|sql|txt|html|css|js|ts|py|rb|java|c|cpp|h|rs)`)
	if nonGo.MatchString(command) {
		return 0
	}

	// Allow comment/doc searches
	if regexp.MustCompile(`(?i)(TODO|FIXME|HACK|XXX|NOTE|BUG|DEPRECATED)`).MatchString(command) {
		return 0
	}

	// Allow explicit non-code directory searches
	if regexp.MustCompile(`(docs/|\.github/|testdata/|migrations/)`).MatchString(command) {
		return 0
	}

	// Must target Go files or be a broad recursive search to proceed
	goTarget := regexp.MustCompile(`(--include=.*\.go|\.go\b)`)
	broadSearch := regexp.MustCompile(`(grep\s+-[a-zA-Z]*r|^rg\b|\brg\s)`)
	if !goTarget.MatchString(command) && !broadSearch.MatchString(command) {
		return 0
	}

	pattern := extractGrepPattern(command)
	if pattern == "" {
		return 0
	}

	// Only block if pattern looks like a Go identifier (3+ alphanum chars)
	if !regexp.MustCompile(`^[A-Za-z_][a-zA-Z0-9_]{2,}$`).MatchString(pattern) {
		return 0
	}

	fmt.Printf(`gograph-guard: blocked grep — this looks like a Go symbol search.
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

// extractGrepPattern pulls the search pattern out of a grep/rg command string.
func extractGrepPattern(command string) string {
	s := regexp.MustCompile(`^\s*(grep|rg)\s+`).ReplaceAllString(command, "")
	s = regexp.MustCompile(`--\S+\s*`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`-[a-zA-Z]+\s*`).ReplaceAllString(s, "")
	if idx := strings.IndexAny(s, "|>"); idx != -1 {
		s = s[:idx]
	}
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return ""
	}
	pattern := strings.Trim(parts[0], `"'`)
	if strings.HasPrefix(pattern, "/") || strings.HasPrefix(pattern, ".") {
		return ""
	}
	return pattern
}
