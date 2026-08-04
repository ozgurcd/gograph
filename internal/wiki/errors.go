package wiki

import (
	"fmt"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// buildErrorsPage produces errors.md — all indexed error and panic sites.
// Returns an empty page if no error edges are in the graph.
func buildErrorsPage(g *graph.Graph) WikiPage {
	if len(g.Errors) == 0 {
		return WikiPage{Filename: "errors.md", Content: ""}
	}

	var b strings.Builder
	b.WriteString("# Error and Panic Sites\n\n")
	b.WriteString("All indexed `errors.New`, `fmt.Errorf`, sentinel `var` declarations, and `panic` calls.\n\n")
	fmt.Fprintf(&b, "Total: %d error sites.\n\n", len(g.Errors))
	b.WriteString("| Message | Function | File |\n")
	b.WriteString("|---------|----------|------|\n")

	for _, e := range g.Errors {
		msg := e.Message
		if len(msg) > 60 {
			msg = msg[:57] + "..."
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | %s:%d |\n",
			msg, e.Function, e.File, e.Line)
	}
	b.WriteString("\n")

	return WikiPage{Filename: "errors.md", Content: b.String()}
}
