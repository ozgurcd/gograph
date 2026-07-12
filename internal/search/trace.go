package search

import (
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// TraceResult contains the error and the reverse path from entry points
type TraceResult struct {
	Error graph.ErrorEdge
	Path  []Result
}

// Trace finds the shortest path from any entry point down to the function
// that generated the given error string.
// Set includeTests to false to exclude errors and test entry points.
func Trace(g *graph.Graph, errStr string, includeTests bool) []TraceResult {
	nl := strings.ToLower(errStr)
	var matches []graph.ErrorEdge

	for _, e := range g.Errors {
		if !includeTests && isTestFile(e.File) {
			continue
		}
		if strings.Contains(strings.ToLower(e.Message), nl) {
			matches = append(matches, e)
		}
	}

	if len(matches) == 0 {
		return nil
	}

	entryPoints := make(map[string]bool)
	for _, r := range g.Routes {
		entryPoints[r.Handler] = true
	}
	for _, s := range g.Symbols {
		if s.Name == "main" {
			if !includeTests && isTestFile(s.File) {
				continue
			}
			entryPoints[s.ID] = true
		}
	}

	// Precompute reverse adjacency list to find callers quickly
	revAdj := make(map[string][]graph.CallEdge)
	for _, c := range g.Calls {
		if c.Synthetic {
			continue
		}
		if !includeTests && isTestFile(c.File) {
			continue
		}
		// In reverse BFS, we go from CalleeRaw -> CallerName
		revAdj[c.CalleeRaw] = append(revAdj[c.CalleeRaw], c)
		// Also strip package paths to ensure matching
		if idx := strings.LastIndex(c.CalleeRaw, "."); idx != -1 {
			short := c.CalleeRaw[idx+1:]
			revAdj[short] = append(revAdj[short], c)
		}
	}

	var results []TraceResult
	for _, e := range matches {
		targetFunc := e.Function

		// Perform a reverse BFS from targetFunc up to any entryPoint
		var bestPath []Result

		type state struct {
			node string
			path []graph.CallEdge
		}

		queue := []state{{node: targetFunc}}
		visited := make(map[string]bool)
		visited[targetFunc] = true

		found := false
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]

			if entryPoints[cur.node] && len(cur.path) > 0 {
				// Reconstruct path forward
				var chain []Result
				for i := len(cur.path) - 1; i >= 0; i-- {
					edge := cur.path[i]
					chain = append(chain, Result{
						Kind:   "path",
						Name:   edge.CallerName,
						File:   edge.File,
						Line:   edge.Line,
						Detail: "calls " + edge.CalleeRaw,
						Score:  10,
					})
				}
				// Add the final destination (targetFunc)
				lastEdge := cur.path[0]
				chain = append(chain, Result{
					Kind:   "path",
					Name:   lastEdge.CalleeRaw,
					File:   lastEdge.File,
					Line:   lastEdge.Line,
					Detail: "destination",
					Score:  10,
				})

				bestPath = chain
				found = true
				break
			}

			for _, edge := range revAdj[cur.node] {
				caller := edge.CallerName
				if !visited[caller] {
					visited[caller] = true
					newPath := make([]graph.CallEdge, len(cur.path)+1)
					copy(newPath, cur.path)
					newPath[len(cur.path)] = edge
					queue = append(queue, state{node: caller, path: newPath})
				}
			}
		}

		if !found {
			// Fallback: Just return immediate callers using reverse adjacency
			if callers, ok := revAdj[targetFunc]; ok && len(callers) > 0 {
				var impacts []Result
				for i, c := range sourceCallSites(callers) {
					if i >= 5 {
						break // limit fallback
					}
					impacts = append(impacts, Result{
						Kind:   "impact",
						Name:   c.CallerName,
						File:   c.File,
						Line:   c.Line,
						Detail: "calls " + targetFunc,
						Score:  5,
					})
				}
				bestPath = impacts
			}
		}

		results = append(results, TraceResult{
			Error: e,
			Path:  bestPath,
		})
	}

	return results
}
