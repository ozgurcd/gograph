package search

import (
	"fmt"
	"strings"
)

// TaskIndexEntry maps one agent task to the command that answers it.
type TaskIndexEntry struct {
	Task string `json:"task"`
	CLI  string `json:"cli"`
	MCP  string `json:"mcp"`
}

// TaskIndex is the short "what do I run for this job" table printed at the
// top of `help` and `capabilities` and returned by gograph_capabilities.
// Full help is hundreds of lines; agents that read it in chunks missed
// commands listed past the first chunk (for example `tests`).
var TaskIndex = []TaskIndexEntry{
	{"orient in an unfamiliar repository", "gograph summary; gograph explore <term>", "gograph_summary, gograph_explore"},
	{"find a symbol and read its code", "gograph query <term>; gograph source <symbol>", "gograph_query, gograph_source"},
	{"who calls it / what it calls", "gograph callers <symbol>; gograph callees <symbol>", "gograph_callers, gograph_callees"},
	{"plan an edit (callers, tests, risk)", "gograph plan <symbol>", "gograph_plan"},
	{"blast radius of a change", "gograph impact <symbol>|--uncommitted|--since <ref>", "gograph_impact"},
	{"choose which tests to run for a change", "gograph tests --uncommitted --format go-test", "gograph_tests uncommitted=true"},
	{"choose which tests to run for a symbol", "gograph tests <symbol> --format go-test --max-depth 3", "gograph_tests select=true"},
	{"list tests that reach a symbol", "gograph tests <symbol> --transitive", "gograph_tests transitive=true"},
	{"what one test exercises (static)", "gograph coverage <test>", "gograph_coverage"},
	{"find untested code", "gograph untested [--coverprofile cover.out]", "gograph_untested"},
	{"changed lines no test executed", "gograph uncovered --uncommitted --coverprofile cover.out", "gograph_uncovered"},
	{"review a change before committing", "gograph changes; gograph review <symbol>", "gograph_changes, gograph_review"},
	{"security: untrusted data to sinks", "gograph flow --no-tests", "gograph_flow"},
	{"enforce architecture rules", "gograph boundaries; gograph check; gograph validate", "gograph_boundaries, gograph_check"},
	{"complexity hot spots", "gograph complexity --no-tests --top 20; gograph hotspot", "gograph_complexity, gograph_hotspot"},
	{"HTTP routes and SQL", "gograph routes; gograph sql", "gograph_routes, gograph_sql"},
	{"is the index fresh? rebuild it", "gograph stale; gograph build . --precise", "gograph_stale"},
	{"what changed in this gograph release", "gograph whatsnew [--since vX.Y.Z]", "gograph_capabilities (version)"},
	{"details for one command", "gograph <command> --help", "tools/list descriptions"},
}

// TaskIndexText renders TaskIndex for terminal output.
func TaskIndexText() string {
	var sb strings.Builder
	sb.WriteString("━━━ TASK INDEX: what to run for a job ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	for _, entry := range TaskIndex {
		fmt.Fprintf(&sb, "  %-40s %s\n", entry.Task, entry.CLI)
	}
	sb.WriteString("Full reference below; `gograph <command> --help` shows one command.\n\n")
	return sb.String()
}
