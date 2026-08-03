package search

import (
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// HotspotResult is a symbol ranked by how many call paths flow through it.
type HotspotResult struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	File          string `json:"file"`
	Line          int    `json:"line"`
	IncomingCalls int    `json:"incoming_calls"`
}

// Hotspot ranks all functions and methods by incoming call count (fan-in).
// The higher the count, the more central this symbol is to the codebase.
// Results are sorted descending by IncomingCalls.
// top limits the result count; pass 0 for all results.
//
// includeTests controls whether call edges from *_test.go files are
// counted. When false (the default callsite passes false), the result
// reflects production-call fan-in and is not skewed by test helpers
// (which can dominate rankings — `baseReq`, `newTestSvc`, etc. — in
// test-heavy codebases). When true, every edge counts.
func Hotspot(g *graph.Graph, top int, includeTests bool) []HotspotResult {
	// Count distinct source expressions per resolved target. Wrapper targets
	// forward their counts to the promoted source method; unresolved legacy
	// calls retain the raw-name fallback.
	incomingByID, incomingRaw := sourceTargetCallCounts(g.Calls, func(call graph.CallEdge) bool {
		return includeTests || !isTestFile(call.File)
	})

	var results []HotspotResult
	for _, s := range g.Symbols {
		if s.Kind != graph.KindFunction && s.Kind != graph.KindMethod {
			continue
		}

		displayName := s.Name
		if s.Receiver != "" {
			displayName = "(" + s.Receiver + ")." + s.Name
		}

		// Aggregate all plausible callee string forms for this symbol.
		count := incomingByID[s.ID]
		count += incomingRaw[s.Name]
		count += incomingRaw[displayName]
		count += incomingRaw[s.ID]
		// Also check "pkg.FuncName" form (common for package-level calls).
		if s.PackageName != "" {
			count += incomingRaw[s.PackageName+"."+s.Name]
		}

		if count == 0 {
			continue
		}

		results = append(results, HotspotResult{
			Name:          displayName,
			Kind:          string(s.Kind),
			File:          s.File,
			Line:          s.Line,
			IncomingCalls: count,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].IncomingCalls != results[j].IncomingCalls {
			return results[i].IncomingCalls > results[j].IncomingCalls
		}
		return strings.ToLower(results[i].Name) < strings.ToLower(results[j].Name)
	})

	if top > 0 && len(results) > top {
		results = results[:top]
	}
	return results
}
