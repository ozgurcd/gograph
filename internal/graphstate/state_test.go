package graphstate

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestStateKeepsHealthAxesOrthogonal(t *testing.T) {
	tests := []struct {
		name         string
		build        *graph.BuildMetadata
		completeness Completeness
		precision    Precision
	}{
		{name: "unknown", completeness: CompletenessUnknown, precision: PrecisionUnknown},
		{name: "complete precise", build: &graph.BuildMetadata{Complete: true, Precision: graph.PrecisionPrecise}, completeness: CompletenessComplete, precision: PrecisionPrecise},
		{name: "partial ast", build: &graph.BuildMetadata{Complete: false, Precision: graph.PrecisionAST}, completeness: CompletenessPartial, precision: PrecisionAST},
		{name: "partial fallback", build: &graph.BuildMetadata{Complete: false, Precision: graph.PrecisionFallback}, completeness: CompletenessPartial, precision: PrecisionFallback},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := New(&graph.Graph{Build: test.build}, SourceInMemory, FreshnessStale, Refresh{
				Policy: "automatic", Attempted: true, Outcome: "failed",
			}, Persistence{Requested: true, Outcome: "skipped_degraded"})
			if state.Completeness != test.completeness || state.Precision != test.precision {
				t.Fatalf("state = completeness:%q precision:%q", state.Completeness, state.Precision)
			}
			if state.Source != SourceInMemory || state.Freshness != FreshnessStale {
				t.Fatalf("state source/freshness = %q/%q", state.Source, state.Freshness)
			}
		})
	}
}

func TestManualPersistedReportsStalePartialFallback(t *testing.T) {
	state := ManualPersisted(&graph.Graph{Build: &graph.BuildMetadata{
		Complete:  false,
		Precision: graph.PrecisionFallback,
	}}, true)
	if state.Source != SourcePersisted || state.Freshness != FreshnessStale || state.Completeness != CompletenessPartial || state.Precision != PrecisionFallback {
		t.Fatalf("state = %+v", state)
	}
	if state.Refresh.Attempted || state.Refresh.Outcome != "not_attempted" || state.Persistence.Outcome != "persisted" {
		t.Fatalf("manual state operations = refresh:%+v persistence:%+v", state.Refresh, state.Persistence)
	}
}

func TestNewBoundsOperationDiagnosticsWithoutBreakingUTF8(t *testing.T) {
	long := strings.Repeat("é", maxDiagnosticRunes+10)
	state := New(nil, SourceUnknown, FreshnessUnknown,
		Refresh{Diagnostic: long},
		Persistence{Diagnostic: long},
	)
	for name, diagnostic := range map[string]string{
		"refresh":     state.Refresh.Diagnostic,
		"persistence": state.Persistence.Diagnostic,
	} {
		if !utf8.ValidString(diagnostic) {
			t.Fatalf("%s diagnostic is invalid UTF-8", name)
		}
		if utf8.RuneCountInString(diagnostic) != maxDiagnosticRunes+1 {
			t.Fatalf("%s diagnostic length = %d runes, want %d including ellipsis", name, utf8.RuneCountInString(diagnostic), maxDiagnosticRunes+1)
		}
	}
}
