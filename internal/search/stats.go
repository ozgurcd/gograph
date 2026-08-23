package search

import (
	"github.com/ozgurcd/gograph/internal/graph"
)

// StatsResult holds a summary of the current graph index.
type StatsResult struct {
	SchemaVersion      string                       `json:"schema_version"`
	GeneratedAt        string                       `json:"generated_at"`
	Precision          graph.PrecisionMode          `json:"precision"`
	TestCallResolution graph.TestCallResolutionMode `json:"test_call_resolution"`
	Packages           int                          `json:"packages"`
	Files              int                          `json:"files"`
	Symbols            int                          `json:"symbols"`
	Calls              int                          `json:"calls"`
	Imports            int                          `json:"imports"`
	Routes             int                          `json:"routes"`
	SQLs               int                          `json:"sqls"`
	EnvReads           int                          `json:"env_reads"`
	TestEdges          int                          `json:"test_edges"`
	FlowFunctions      int                          `json:"flow_functions"`
	BuildStatus        string                       `json:"build_status"`
	ScannedFiles       int                          `json:"scanned_files,omitempty"`
	ParsedFiles        int                          `json:"parsed_files,omitempty"`
	ReusedFiles        int                          `json:"reused_files,omitempty"`
	RebuiltPackages    int                          `json:"rebuilt_packages,omitempty"`
	ParseFailures      int                          `json:"parse_failures,omitempty"`
}

// Stats derives index health counts directly from the in-memory graph. It
// performs no I/O and normalizes missing legacy precision metadata to AST.
func Stats(g *graph.Graph) StatsResult {
	result := StatsResult{
		SchemaVersion:      g.Version,
		GeneratedAt:        g.GeneratedAt.Format("2006-01-02 15:04:05 UTC"),
		Precision:          g.Build.EffectivePrecision(),
		TestCallResolution: g.Build.EffectiveTestCallResolution(),
		Packages:           len(g.Packages),
		Files:              len(g.Files),
		Symbols:            len(g.Symbols),
		Calls:              len(g.Calls),
		Imports:            len(g.Imports),
		Routes:             len(g.Routes),
		SQLs:               len(g.SQLs),
		EnvReads:           len(g.EnvReads),
		TestEdges:          len(g.TestEdges),
		FlowFunctions:      len(g.FlowFunctions),
		BuildStatus:        "unknown",
	}
	if g.Build != nil {
		result.BuildStatus = "partial"
		if g.Build.Complete {
			result.BuildStatus = "complete"
		}
		result.ScannedFiles = g.Build.ScannedFiles
		result.ParsedFiles = g.Build.ParsedFiles
		result.ReusedFiles = g.Build.ReusedFiles
		result.RebuiltPackages = g.Build.RebuiltPackages
		result.ParseFailures = len(g.Build.Failures)
	}
	return result
}
