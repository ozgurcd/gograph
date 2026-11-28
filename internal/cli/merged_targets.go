package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

// maxMergedTargetSelectors bounds the stderr note so a very common name
// (String, Error) cannot flood the answer.
const maxMergedTargetSelectors = 10

// noteMergedTargets tells the caller on stderr when term names several
// declarations exactly, because callers, callees and impact then answer for
// all of them together. Each match is printed as a copy-ready selector, so a
// narrower retry needs no extra lookup. Stdout and JSON stay unchanged.
func noteMergedTargets(command string, g *graph.Graph, term string) {
	// JSON consumers read one stream; the rows already carry each match's
	// identity, and gograph identity --json lists the selectors.
	if jsonMode {
		return
	}
	report := search.IdentityInPackage(g, term, "")
	if report.Status != "ambiguous" {
		return
	}
	selectors := make([]string, 0, len(report.Matches))
	for i, match := range report.Matches {
		if i == maxMergedTargetSelectors {
			selectors = append(selectors, fmt.Sprintf("... %d more (gograph identity %s)", len(report.Matches)-i, term))
			break
		}
		selectors = append(selectors, match.Selector)
	}
	fmt.Fprintf(os.Stderr, "gograph %s: %q matches %d declarations and the answer covers all of them; for one, retry with: %s\n", command, term, len(report.Matches), strings.Join(selectors, " | "))
}
