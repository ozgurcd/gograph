package cli

import (
	"regexp"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/search"
)

// Agents read long help in chunks; the task index must stay on the first
// screen and point at real commands.
func TestTaskIndexLeadsHelpAndCapabilitiesAndNamesRealCommands(t *testing.T) {
	for _, command := range []string{"help", "capabilities"} {
		stdout, _, code := runCLIParityInDir(t, t.TempDir(), func() int { return Run([]string{command}) })
		if code != 0 {
			t.Fatalf("%s exited %d", command, code)
		}
		lines := strings.Split(stdout, "\n")
		if len(lines) > 30 {
			lines = lines[:30]
		}
		head := strings.Join(lines, "\n")
		if !strings.Contains(head, "choose which tests to run for a change") || !strings.Contains(head, "gograph tests --uncommitted --format go-test") {
			t.Errorf("%s does not show test selection in its first 30 lines:\n%s", command, head)
		}
	}

	commandWord := regexp.MustCompile(`gograph ([a-z][a-z-]*)`)
	seen := map[string]bool{}
	for _, entry := range search.TaskIndex {
		for _, match := range commandWord.FindAllStringSubmatch(entry.CLI, -1) {
			name := match[1]
			if seen[name] {
				continue
			}
			seen[name] = true
			stdout, _, _ := runCLIParityInDir(t, t.TempDir(), func() int { return Run([]string{name, "--help"}) })
			if !strings.HasPrefix(stdout, "USAGE") {
				t.Errorf("task index names %q, which has no command help", name)
			}
		}
	}
	if !seen["tests"] || !seen["uncovered"] {
		t.Fatalf("task index lost tests or uncovered: %v", seen)
	}
}
