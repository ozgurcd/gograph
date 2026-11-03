package search

import (
	"fmt"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// EmptyAnswerWarning keeps missing AST facts distinct from proved absence.
// It adds no result rows and leaves precise answers unchanged.
func EmptyAnswerWarning(command, precision string, count int) string {
	if count != 0 || (precision != "ast" && precision != "precise_fallback") {
		return ""
	}
	switch command {
	case "tests", "envs", "usages":
		return "This AST-only answer may be incomplete; run gograph build . --precise and repeat the query."
	default:
		return ""
	}
}

// EmptyResultContext explains an empty answer with the graph it came from, so
// that an empty list is never read as proof that nothing exists. It names the
// analyzed root and indexed totals; a wrong root or an unindexed area is then
// visible to the caller.
func EmptyResultContext(g *graph.Graph, command string) string {
	if g == nil {
		return ""
	}
	totals := fmt.Sprintf("%d files, %d symbols, %d routes", len(g.Files), len(g.Symbols), len(g.Routes))
	generated := ""
	if !g.GeneratedAt.IsZero() {
		generated = ", generated " + g.GeneratedAt.UTC().Format("2006-01-02 15:04:05 UTC")
	}
	return fmt.Sprintf("No %s rows matched in the graph of %s (%s%s). An empty answer is not proof of absence: confirm this is the repository you meant, and that the graph is current, before concluding nothing exists.",
		command, g.Root, totals, generated)
}

// JoinWarnings joins non-empty warnings with a space.
func JoinWarnings(warnings ...string) string {
	var kept []string
	for _, warning := range warnings {
		if strings.TrimSpace(warning) != "" {
			kept = append(kept, warning)
		}
	}
	return strings.Join(kept, " ")
}
