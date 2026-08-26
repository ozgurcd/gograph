// Package graphstate defines the transport-neutral state of the graph used to
// answer one request. It intentionally keeps storage, freshness, completeness,
// precision, refresh, and persistence orthogonal.
package graphstate

import (
	"unicode/utf8"

	"github.com/ozgurcd/gograph/internal/graph"
)

const SchemaVersion = "gograph.graph-state.v1"

const maxDiagnosticRunes = 2048

type Source string

const (
	SourcePersisted Source = "persisted"
	SourceInMemory  Source = "in_memory"
	SourceUnknown   Source = "unknown"
)

type Freshness string

const (
	FreshnessCurrent Freshness = "current"
	FreshnessStale   Freshness = "stale"
	FreshnessUnknown Freshness = "unknown"
)

type Completeness string

const (
	CompletenessComplete Completeness = "complete"
	CompletenessPartial  Completeness = "partial"
	CompletenessUnknown  Completeness = "unknown"
)

type Precision string

const (
	PrecisionAST      Precision = "ast"
	PrecisionPrecise  Precision = "precise"
	PrecisionFallback Precision = "fallback"
	PrecisionUnknown  Precision = "unknown"
)

type Refresh struct {
	Policy     string `json:"policy"`
	Attempted  bool   `json:"attempted"`
	Outcome    string `json:"outcome"`
	Diagnostic string `json:"diagnostic,omitempty"`
}

type Persistence struct {
	Requested  bool   `json:"requested"`
	Outcome    string `json:"outcome"`
	Diagnostic string `json:"diagnostic,omitempty"`
}

// State describes the exact graph state used for a result. None of these axes
// imply another: a persisted graph can be stale, a current graph can be
// partial, and a fallback graph can exist only in memory.
type State struct {
	SchemaVersion string       `json:"schema_version"`
	Source        Source       `json:"source"`
	Freshness     Freshness    `json:"freshness"`
	Completeness  Completeness `json:"completeness"`
	Precision     Precision    `json:"precision"`
	Refresh       Refresh      `json:"refresh"`
	Persistence   Persistence  `json:"persistence"`
}

func New(g *graph.Graph, source Source, freshness Freshness, refresh Refresh, persistence Persistence) State {
	refresh.Diagnostic = boundedDiagnostic(refresh.Diagnostic)
	persistence.Diagnostic = boundedDiagnostic(persistence.Diagnostic)
	state := State{
		SchemaVersion: SchemaVersion,
		Source:        source,
		Freshness:     freshness,
		Completeness:  CompletenessUnknown,
		Precision:     PrecisionUnknown,
		Refresh:       refresh,
		Persistence:   persistence,
	}
	if g == nil || g.Build == nil {
		return state
	}
	state.Completeness = CompletenessPartial
	if g.Build.Complete {
		state.Completeness = CompletenessComplete
	}
	switch g.Build.EffectivePrecision() {
	case graph.PrecisionPrecise:
		state.Precision = PrecisionPrecise
	case graph.PrecisionFallback:
		state.Precision = PrecisionFallback
	default:
		state.Precision = PrecisionAST
	}
	return state
}

func boundedDiagnostic(value string) string {
	if utf8.RuneCountInString(value) <= maxDiagnosticRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxDiagnosticRunes]) + "…"
}

func ManualPersisted(g *graph.Graph, stale bool) State {
	freshness := FreshnessCurrent
	if stale {
		freshness = FreshnessStale
	}
	return New(g, SourcePersisted, freshness, Refresh{
		Policy:    "manual",
		Attempted: false,
		Outcome:   "not_attempted",
	}, Persistence{Requested: false, Outcome: "persisted"})
}
