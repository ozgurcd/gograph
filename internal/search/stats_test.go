package search_test

import (
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestStatsReportsPrecisionState(t *testing.T) {
	tests := []struct {
		name      string
		metadata  *graph.BuildMetadata
		precision graph.PrecisionMode
		status    string
	}{
		{name: "legacy graph", precision: graph.PrecisionAST, status: "unknown"},
		{name: "legacy build metadata", metadata: &graph.BuildMetadata{Complete: true}, precision: graph.PrecisionAST, status: "complete"},
		{name: "explicit AST", metadata: &graph.BuildMetadata{Complete: true, Precision: graph.PrecisionAST}, precision: graph.PrecisionAST, status: "complete"},
		{name: "precise", metadata: &graph.BuildMetadata{Complete: true, Precision: graph.PrecisionPrecise}, precision: graph.PrecisionPrecise, status: "complete"},
		{name: "precise fallback", metadata: &graph.BuildMetadata{Complete: true, Precision: graph.PrecisionFallback}, precision: graph.PrecisionFallback, status: "complete"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := search.Stats(&graph.Graph{Build: tt.metadata})
			if result.Precision != tt.precision {
				t.Fatalf("precision = %q, want %q", result.Precision, tt.precision)
			}
			if result.BuildStatus != tt.status {
				t.Fatalf("build status = %q, want %q", result.BuildStatus, tt.status)
			}
		})
	}
}

func TestBuildMetadataPreciseRequested(t *testing.T) {
	tests := []struct {
		mode graph.PrecisionMode
		want bool
	}{
		{mode: "", want: false},
		{mode: graph.PrecisionAST, want: false},
		{mode: graph.PrecisionPrecise, want: true},
		{mode: graph.PrecisionFallback, want: true},
		{mode: "future-value", want: false},
	}
	for _, tt := range tests {
		metadata := &graph.BuildMetadata{Precision: tt.mode}
		if got := metadata.PreciseRequested(); got != tt.want {
			t.Errorf("PreciseRequested(%q) = %v, want %v", tt.mode, got, tt.want)
		}
	}
}
