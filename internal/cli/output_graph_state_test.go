package cli

import (
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestCurrentOutputGraphStateUsesTheGraphLoadedForTheCommand(t *testing.T) {
	resetOutputGraph()
	t.Cleanup(resetOutputGraph)

	loaded := &graph.Graph{
		Root: t.TempDir(),
		Build: &graph.BuildMetadata{
			Complete:  true,
			Precision: graph.PrecisionPrecise,
		},
	}
	rememberOutputGraph(loaded)

	state := currentOutputGraphState()
	if state == nil {
		t.Fatal("expected graph state")
	}
	if state.Precision != "precise" || state.Completeness != "complete" {
		t.Fatalf("graph state = %+v, want the exact loaded graph metadata", state)
	}
}
