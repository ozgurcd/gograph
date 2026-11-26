package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/ozgurcd/gograph/internal/search"
)

// commandWarnsWhenStale reports whether a graph-backed command should name a
// stale graph. stale and stats report freshness themselves, and changes
// exists to compare the working tree with the persisted graph.
func commandWarnsWhenStale(command string) bool {
	switch command {
	case "stale", "stats", "changes", "build", "mcp", "hook-guard", "hook", "session", "--session":
		return false
	}
	return true
}

// staleGraphWarning names why a persisted graph no longer matches the
// working tree, or returns "" for a current graph.
func staleGraphWarning(stale search.StaleResult) string {
	if !stale.IsStale {
		return ""
	}
	reason := "the Go build context changed since the last build"
	if count := len(stale.ChangedFiles); count > 0 {
		shown := stale.ChangedFiles
		if len(shown) > 3 {
			shown = shown[:3]
		}
		reason = fmt.Sprintf("%d selected file(s) changed since the last build (for example %s)", count, strings.Join(shown, ", "))
	}
	return "WARNING: the graph is stale: " + reason + "; results can miss or misplace new and edited code. Run `gograph build .` to refresh."
}

// warnIfStaleGraph prints the stale warning on stderr after a text-mode
// command that answered from the persisted graph. JSON output carries the
// same text in its warning field and graph_state.freshness.
func warnIfStaleGraph(command string) {
	if jsonMode || !commandWarnsWhenStale(command) {
		return
	}
	outputGraph.RLock()
	g := outputGraph.graph
	outputGraph.RUnlock()
	if g == nil {
		return
	}
	if warning := staleGraphWarning(loadedGraphStale(g)); warning != "" {
		fmt.Fprintln(os.Stderr, "gograph: "+warning)
	}
}
