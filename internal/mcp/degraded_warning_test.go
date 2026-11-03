package mcp

import (
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/graphstate"
)

func TestDegradedGraphWarningNamesStaleServedGraph(t *testing.T) {
	g := &graph.Graph{Root: "/repo"}
	current := graphstate.State{Freshness: graphstate.FreshnessCurrent, Refresh: graphstate.Refresh{Outcome: "not_needed"}}
	if warning := degradedGraphWarning(current, g); warning != "" {
		t.Fatalf("a current graph needs no warning: %q", warning)
	}
	stale := graphstate.State{Freshness: graphstate.FreshnessStale, Refresh: graphstate.Refresh{Outcome: "failed", Diagnostic: "parse error"}}
	warning := degradedGraphWarning(stale, g)
	for _, want := range []string{"stale graph for /repo", "refresh failed: parse error", "not proof of absence"} {
		if !strings.Contains(warning, want) {
			t.Fatalf("stale warning %q lacks %q", warning, want)
		}
	}
}
