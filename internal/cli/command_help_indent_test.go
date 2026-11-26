package cli_test

import (
	"os/exec"
	"strings"
	"testing"
)

// Per-command help keeps the relative indentation of nested option lists
// and splits usage from description when only one space separates them.
func TestCommandHelpKeepsNestedIndentation(t *testing.T) {
	binary := buildTestBinary(t)
	out, err := exec.Command(binary, "hook-guard", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("gograph hook-guard --help: %v\n%s", err, out)
	}
	help := string(out)
	for _, want := range []string{
		"USAGE\n  gograph hook-guard [rules] [--ask]\n\nDESCRIPTION\n  PreToolUse hook for Claude Code, Codex and Antigravity.\n",
		"\n    --deny-go-writes     text tools editing .go files\n                         (sed -i,",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("hook-guard --help lacks %q:\n%s", want, help)
		}
	}
	out, err = exec.Command(binary, "hook", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("gograph hook --help: %v\n%s", err, out)
	}
	if want := "\n    --deny-go-writes   text tools editing .go files\n                       (sed -i,"; !strings.Contains(string(out), want) {
		t.Fatalf("hook --help lacks %q:\n%s", want, out)
	}
}
