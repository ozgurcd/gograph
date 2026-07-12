package search

import (
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// ReturnUsages returns all call sites of the named function with a label
// describing how the caller uses the return value.
//
// Labels: "discarded" (return not captured), "assigned" (all returns captured),
// "partially_ignored" (some returns blanked with _), "returned" (passed up the
// stack), "goroutine" (go call), "deferred" (defer call), "passed" (nested
// inside another call — return goes directly to another function's argument).
//
// Use this before changing a function's return signature to find every caller
// that silently discards a value that will now carry different semantics.
func ReturnUsages(g *graph.Graph, funcName string) []Result {
	nl := strings.ToLower(funcName)
	type siteKey struct {
		callerIdentity string
		calleeRaw      string
		file           string
		line           int
		column         int
	}
	seen := make(map[siteKey]bool)
	var results []Result
	for _, call := range g.Calls {
		if call.Synthetic {
			continue
		}
		callee := strings.ToLower(call.CalleeRaw)
		if callee != nl && !strings.HasSuffix(callee, "."+nl) {
			continue
		}
		callerIdentity := call.CallerSymbolID
		if callerIdentity == "" {
			callerIdentity = call.CallerName
		}
		key := siteKey{callerIdentity: callerIdentity, calleeRaw: call.CalleeRaw, file: call.File, line: call.Line, column: call.Column}
		if seen[key] {
			continue
		}
		seen[key] = true
		usage := call.ReturnUsage
		if usage == "" {
			usage = "passed"
		}
		results = append(results, Result{
			Kind:   "call",
			Name:   call.CallerName,
			File:   call.File,
			Line:   call.Line,
			Detail: usage + " ← " + call.CalleeRaw,
			Score:  10,
		})
	}
	sortResults(results)
	return results
}
