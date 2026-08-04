package cli

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ozgurcd/gograph/internal/session"
)

// failCommand is the single error-output path for commands that support
// structured output. JSON mode writes exactly one standard error envelope to
// stdout; text mode preserves the CLI convention of writing errors to stderr.
func failCommand(command, message string) int {
	if jsonMode {
		return PrintJSON(errEnvelope(command, message))
	}
	fmt.Fprintln(os.Stderr, message)
	return exitError
}

func failCommandf(command, format string, args ...any) int {
	return failCommand(command, fmt.Sprintf(format, args...))
}

func hasSingleTarget(args []string) bool {
	return len(args) == 1 && args[0] != "" && !strings.HasPrefix(args[0], "-")
}

func hasOptionalTarget(args []string) bool {
	return len(args) == 0 || hasSingleTarget(args)
}

func parseIntegerFlag(args []string, index *int) (int, error) {
	flag := args[*index]
	if *index+1 >= len(args) {
		return 0, fmt.Errorf("%s requires a value", flag)
	}
	*index = *index + 1
	value, err := strconv.Atoi(args[*index])
	if err != nil {
		return 0, fmt.Errorf("invalid %s value: %q", flag, args[*index])
	}
	return value, nil
}

// runSessionWithJSONErrors preserves session audit's documented native JSON
// success object while adapting its direct stderr failures to the standard CLI
// error envelope.
func runSessionWithJSONErrors(args []string) int {
	if !jsonMode || len(args) == 0 || args[0] != "audit" {
		return runSession(args)
	}
	if len(args) > 2 {
		return failCommand("session audit", "usage: gograph session audit [session_id]")
	}

	sessionID := ""
	if len(args) == 2 {
		sessionID = args[1]
	}
	var stdout, stderr bytes.Buffer
	if code := session.RunAuditTo(sessionID, true, &stdout, &stderr); code != exitSuccess {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = "session audit failed"
		}
		return failCommand("session audit", message)
	}
	fmt.Print(stdout.String())
	return exitSuccess
}

// commandFromArgs finds the subcommand before global flags are stripped. It is
// used only for failures detected during global parsing, when dispatch has not
// yet selected a handler.
func commandFromArgs(args []string) string {
	for i := 0; i < len(args); i++ {
		switch argument := args[i]; {
		case argument == "--json", argument == "--files-only", argument == "--mermaid":
			continue
		case argument == "-i", argument == "--intention":
			i++
		case strings.HasPrefix(argument, "-i="), strings.HasPrefix(argument, "--intention="):
			continue
		default:
			return argument
		}
	}
	return "gograph"
}

func requestsJSON(args []string) bool {
	for i := 0; i < len(args); i++ {
		argument := args[i]
		if argument == "-i" || argument == "--intention" {
			i++
			continue
		}
		if strings.HasPrefix(argument, "-i=") || strings.HasPrefix(argument, "--intention=") {
			continue
		}
		if argument == "--json" {
			return true
		}
	}
	return false
}
