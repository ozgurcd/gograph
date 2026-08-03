package search

import "github.com/ozgurcd/gograph/internal/graph"

type sourceCallSiteKey struct {
	callerIdentity string
	file           string
	line           int
	column         int
	calleeRaw      string
}

func callSourceSiteKey(call graph.CallEdge) sourceCallSiteKey {
	callerIdentity := call.CallerSymbolID
	if callerIdentity == "" {
		callerIdentity = call.CallerName
	}
	return sourceCallSiteKey{
		callerIdentity: callerIdentity,
		file:           call.File,
		line:           call.Line,
		column:         call.Column,
		calleeRaw:      call.CalleeRaw,
	}
}

func hasSourceCallLocation(call graph.CallEdge) bool {
	return call.File != "" && call.Line > 0
}

// sourceCallSites returns one representative edge per source call expression.
// Precise interface dispatch stores one parallel edge per valid target, while
// source-oriented metrics must count the invocation itself only once.
// Synthetic wrapper forwarding has no source expression and is excluded.
func sourceCallSites(calls []graph.CallEdge) []graph.CallEdge {
	seen := make(map[sourceCallSiteKey]struct{}, len(calls))
	sites := make([]graph.CallEdge, 0, len(calls))
	for _, call := range calls {
		if call.Synthetic {
			continue
		}
		// Legacy and hand-built graphs may have no source provenance. There is
		// no evidence that two such records are the same invocation, so preserve
		// their historical edge-count behavior instead of collapsing them.
		if !hasSourceCallLocation(call) {
			sites = append(sites, call)
			continue
		}
		key := callSourceSiteKey(call)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		sites = append(sites, call)
	}
	return sites
}

// targetCallSites retains one edge per distinct target at each source
// expression. This is the correct view for per-symbol fan-in metrics: parallel
// interface targets count once each, while duplicate edges to the same target
// do not inflate the result.
func targetCallSites(calls []graph.CallEdge) []graph.CallEdge {
	type targetSiteKey struct {
		site   sourceCallSiteKey
		target string
	}
	seen := make(map[targetSiteKey]struct{}, len(calls))
	sites := make([]graph.CallEdge, 0, len(calls))
	for _, call := range calls {
		if call.Synthetic {
			continue
		}
		if !hasSourceCallLocation(call) {
			sites = append(sites, call)
			continue
		}
		target := call.CalleeSymbolID
		if target == "" {
			target = call.CalleeRaw
		}
		key := targetSiteKey{site: callSourceSiteKey(call), target: target}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		sites = append(sites, call)
	}
	return sites
}

// sourceTargetCallCounts counts distinct source-target pairs and propagates a
// real wrapper target through traversal-only forwarding edges to the declared
// promoted method that executes.
func sourceTargetCallCounts(calls []graph.CallEdge, include func(graph.CallEdge) bool) (map[string]int, map[string]int) {
	type countedTarget struct {
		site   sourceCallSiteKey
		target string
	}
	forward := make(map[string][]string)
	for _, call := range calls {
		if call.Synthetic && call.CallerSymbolID != "" && call.CalleeSymbolID != "" {
			forward[call.CallerSymbolID] = append(forward[call.CallerSymbolID], call.CalleeSymbolID)
		}
	}

	byID := make(map[string]int)
	byRaw := make(map[string]int)
	counted := make(map[countedTarget]struct{})
	for _, call := range targetCallSites(calls) {
		if include != nil && !include(call) {
			continue
		}
		if call.CalleeSymbolID == "" {
			if !hasSourceCallLocation(call) {
				byRaw[call.CalleeRaw]++
				continue
			}
			key := countedTarget{site: callSourceSiteKey(call), target: call.CalleeRaw}
			if _, exists := counted[key]; !exists {
				counted[key] = struct{}{}
				byRaw[call.CalleeRaw]++
			}
			continue
		}
		visited := make(map[string]bool)
		queue := []string{call.CalleeSymbolID}
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if id == "" || visited[id] {
				continue
			}
			visited[id] = true
			if !hasSourceCallLocation(call) {
				byID[id]++
			} else {
				key := countedTarget{site: callSourceSiteKey(call), target: id}
				if _, exists := counted[key]; !exists {
					counted[key] = struct{}{}
					byID[id]++
				}
			}
			queue = append(queue, forward[id]...)
		}
	}
	return byID, byRaw
}
