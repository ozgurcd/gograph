package cli

import (
	"fmt"
	"os"

	"github.com/ozgurcd/gograph/internal/session"
)

// runSession manages the `--session` / `session` CLI subcommands.
func runSession(args []string) int {
	if len(args) == 0 {
		printSessionHelp()
		return 1
	}

	switch args[0] {
	case "create":
		customWord := ""
		if len(args) >= 2 {
			customWord = args[1]
		}
		sessionID, err := session.StartCallerSessionAt("", customWord, callerSessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error starting session: %v\n", err)
			return 1
		}
		fmt.Printf("Session %q successfully created and activated.\n", sessionID)
		fmt.Printf("Use --session-id %s or GOGRAPH_SESSION=%s on this caller's commands.\n", sessionID, sessionID)
		return 0

	case "end":
		sessionID, err := session.EndCallerSessionAt("", callerSessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error ending session: %v\n", err)
			return 1
		}
		fmt.Printf("Session %q successfully ended.\n", sessionID)
		return 0

	case "audit":
		sessionID := callerSessionID
		if len(args) >= 2 {
			sessionID = args[1]
		}
		return session.RunAudit(sessionID, jsonMode)

	case "cleanup":
		report, err := session.CleanupCallerSessionsReportAt("", callerSessionID)
		for _, line := range report.Lines() {
			fmt.Println(line)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error cleaning up sessions: %v\n", err)
			return 1
		}
		if report.ArchiveDir == "" {
			fmt.Println("Nothing to archive.")
		}
		return 0

	default:
		printSessionHelp()
		return 1
	}
}

func printSessionHelp() {
	fmt.Println("Usage:")
	fmt.Println("  Caller selection: --session-id ID or GOGRAPH_SESSION=ID; use the ID printed by create.")
	fmt.Println("  gograph session create [unique_identifier_word]  - Starts a new audit session")
	fmt.Println("  gograph session end                              - Ends the active audit session")
	fmt.Println("  gograph session audit [session_id]               - Audits and scores agent compliance & success")
	fmt.Println("  gograph session cleanup                          - Ends stale sessions, then moves inactive logs into .gograph/_to_delete/")
}

func printSessionVerbHelp(verb string) bool {
	var usage, description, options string
	switch verb {
	case "create":
		usage = "create [unique_identifier_word]"
		description = "Start an independent caller audit session. Returns an ID to pass as --session-id or GOGRAPH_SESSION. Other callers may create concurrently."
	case "end":
		usage = "end"
		description = "End only the selected caller's session; refuses unselected callers while another session is active."
	case "audit":
		usage = "audit [session_id] [--json]"
		description = "Audit and score the selected session, or the latest session when omitted."
		options = "  --json        Print the native structured audit result.\n"
	case "cleanup":
		usage = "cleanup"
		description = "Never deletes: moves files into .gograph/_to_delete/<UTC time>_<suffix>/. First ends other callers' CLI-owned sessions idle over 24h or MCP-owned sessions whose server process exited on this host (owners without metadata or on another host are kept), printing each, and archives an unselected legacy active_session.json; then archives ended logs. Preserves the selected session and refuses while any other caller is still live."
	default:
		return false
	}
	fmt.Printf("USAGE\n  gograph session %s\n\nDESCRIPTION\n  %s\n\nOPTIONS\n%s  --session-id ID  Select this caller's session (overrides GOGRAPH_SESSION).\n  --help, -h    Show this help without changing session state.\n", usage, description, options)
	return true
}
