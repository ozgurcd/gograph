package search

import (
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// Repository impact retains its conservative default. ExactOnly excludes paths
// containing AST/CHA uncertainty; default results label that uncertainty rather
// than presenting possible impact as proof of a runtime call.
type ImpactOptions struct {
	IncludeTests bool
	ExactOnly    bool
}

func ImpactWithOptions(g *graph.Graph, names []string, reason string, options ImpactOptions) []Result {
	return NewSnapshot(g).Impact(names, reason, options)
}

type impactIndex struct {
	symbols    map[string]graph.SymbolNode
	incoming   map[string][]graph.CallEdge
	unresolved []graph.CallEdge
}

func buildImpactIndex(g *graph.Graph) impactIndex {
	symbols := make(map[string]graph.SymbolNode, len(g.Symbols))
	for _, symbol := range g.Symbols {
		symbols[symbol.ID] = symbol
	}
	incoming := make(map[string][]graph.CallEdge)
	var unresolved []graph.CallEdge
	for _, call := range g.Calls {
		if call.CalleeSymbolID == "" {
			// Unique raw-spelling fallback is possible evidence only. An edge
			// already resolved elsewhere can never enter this fallback.
			candidates := FindSymbols(g, call.CalleeRaw)
			if len(candidates) != 1 {
				unresolved = append(unresolved, call)
				continue
			}
			call.CalleeSymbolID = candidates[0].ID
			call.Resolution = ""
		}
		incoming[call.CalleeSymbolID] = append(incoming[call.CalleeSymbolID], call)
	}
	return impactIndex{symbols: symbols, incoming: incoming, unresolved: unresolved}
}

func (snapshot *Snapshot) Impact(names []string, reason string, options ImpactOptions) []Result {
	g := snapshot.g
	if g == nil {
		return []Result{}
	}
	snapshot.impactOnce.Do(func() { snapshot.impact = buildImpactIndex(g) })
	symbols := snapshot.impact.symbols
	// Only unresolved-query seeds are request-local; cached adjacency is never
	// extended or filtered in place.
	incoming := snapshot.impact.incoming
	seedIncoming := make(map[string][]graph.CallEdge)
	unresolved := snapshot.impact.unresolved
	targets := make(map[string]bool)
	for _, name := range names {
		resolved, _ := resolvedCallTargetIDs(g, name)
		for id := range resolved {
			targets[id] = true
		}
		if len(resolved) > 0 || isFullyQualifiedID(name) {
			continue
		}
		key := "unresolved-query:" + name
		for _, call := range unresolved {
			if name != "" && strings.Contains(strings.ToLower(call.CalleeRaw), strings.ToLower(name)) {
				call.Resolution = ""
				seedIncoming[key] = append(seedIncoming[key], call)
				targets[key] = true
			}
		}
	}
	reach := func(exactOnly bool) map[string]bool {
		visited := make(map[string]bool)
		queue := make([]string, 0, len(targets))
		for id := range targets {
			visited[id] = true
			queue = append(queue, id)
		}
		for index := 0; index < len(queue); index++ {
			calls := incoming[queue[index]]
			if seed, ok := seedIncoming[queue[index]]; ok {
				calls = seed
			}
			for _, call := range calls {
				if !options.IncludeTests && isTestFile(call.File) {
					continue
				}
				if exactOnly && callResolution(call.Resolution) != "exact" && !call.Synthetic {
					continue
				}
				id := call.CallerSymbolID
				if id != "" && !visited[id] {
					visited[id] = true
					queue = append(queue, id)
				}
			}
		}
		return visited
	}
	exact := reach(true)
	selected := exact
	if !options.ExactOnly {
		selected = reach(false)
	}
	results := make([]Result, 0, len(selected))
	for id := range selected {
		if targets[id] {
			continue
		}
		symbol, ok := symbols[id]
		if !ok {
			continue
		} // synthetic wrappers are traversed, not presented
		status := "possible"
		if exact[id] {
			status = "exact"
		}
		results = append(results, Result{Kind: "impact", Name: symbol.Name, StableID: symbol.ID, File: symbol.File, Line: symbol.Line, Detail: reason, Score: 8, ResolutionStatus: status})
	}
	sortResults(results)
	return results
}
