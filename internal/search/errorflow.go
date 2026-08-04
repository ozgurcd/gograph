package search

import (
	"fmt"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

type ErrorFlowReport struct {
	Term            string
	DefinitionSites []Result
	ReturnSites     []Result
	Paths           []TraceResult
	RelatedTests    []Result
}

// ErrorFlowPayload is the shared JSON representation used by the CLI and both
// MCP errorflow names. It preserves structured related-test rows as well as the
// compatibility list of test names.
type ErrorFlowPayload struct {
	Query       string        `json:"query"`
	Summary     string        `json:"summary"`
	Definitions []Result      `json:"definitions"`
	Sites       []Result      `json:"sites"`
	Paths       []TraceResult `json:"paths"`
	Tests       []string      `json:"tests"`
	TestResults []Result      `json:"test_results"`
	Limitations []string      `json:"limitations"`
}

func NewErrorFlowPayload(report *ErrorFlowReport) ErrorFlowPayload {
	payload := ErrorFlowPayload{
		Query:       report.Term,
		Summary:     "ErrorFlow Report for " + report.Term,
		Definitions: nonNilResults(report.DefinitionSites),
		Sites:       nonNilResults(report.ReturnSites),
		Paths:       nonNilTraceResults(report.Paths),
		TestResults: nonNilResults(report.RelatedTests),
		Tests:       make([]string, 0, len(report.RelatedTests)),
		Limitations: []string{
			"Errorflow uses heuristic static call-graph and AST reference analysis. It does not perform SSA or full data-flow tracking.",
		},
	}
	for _, test := range report.RelatedTests {
		payload.Tests = append(payload.Tests, test.Name)
	}
	return payload
}

// Count returns the number of concrete evidence rows, so a report containing
// definitions, return sites, or tests is not mislabeled empty merely because
// no complete entrypoint path was found.
func (p ErrorFlowPayload) Count() int {
	return len(p.Definitions) + len(p.Sites) + len(p.Paths) + len(p.TestResults)
}

func nonNilResults(results []Result) []Result {
	if results == nil {
		return []Result{}
	}
	return results
}

func nonNilTraceResults(results []TraceResult) []TraceResult {
	if results == nil {
		return []TraceResult{}
	}
	return results
}

func (r *ErrorFlowReport) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "ErrorFlow Report for %q\n", r.Term)
	sb.WriteString("==================================================\n")
	sb.WriteString("⚠️  DISCLAIMER: Likely error path based on static call graph and AST references.\n")
	sb.WriteString("   Highly useful for navigation, not proof. No SSA/data-flow tracking performed.\n")
	sb.WriteString("==================================================\n\n")

	if len(r.DefinitionSites) > 0 {
		sb.WriteString("1. Definition Sites:\n")
		for _, s := range r.DefinitionSites {
			fmt.Fprintf(&sb, "   - %s (%s:%d) -> %s\n", s.Name, s.File, s.Line, s.Detail)
		}
		sb.WriteString("\n")
	}

	if len(r.ReturnSites) > 0 {
		sb.WriteString("2. Return / Wrap / Check Sites:\n")
		for _, s := range r.ReturnSites {
			fmt.Fprintf(&sb, "   - %s (%s:%d) -> %s\n", s.Name, s.File, s.Line, s.Detail)
		}
		sb.WriteString("\n")
	}

	if len(r.Paths) > 0 {
		sb.WriteString("3. Likely Route / Entrypoint Paths:\n")
		for i, p := range r.Paths {
			confidence := "MEDIUM"
			if len(r.DefinitionSites) > 0 {
				confidence = "HIGH"
			}
			fmt.Fprintf(&sb, "   Path %d [Confidence: %s] (Originates in %s):\n", i+1, confidence, p.Error.Function)
			for j, step := range p.Path {
				fmt.Fprintf(&sb, "      %d. %s (%s:%d) - %s\n", j+1, step.Name, step.File, step.Line, step.Detail)
			}
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString("3. Likely Route / Entrypoint Paths:\n   - No complete path to an HTTP route or main entrypoint found.\n\n")
	}

	if len(r.RelatedTests) > 0 {
		sb.WriteString("4. Related Tests:\n")
		for _, s := range r.RelatedTests {
			fmt.Fprintf(&sb, "   - %s (%s:%d) -> %s\n", s.Name, s.File, s.Line, s.Detail)
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func ErrorFlow(g *graph.Graph, term string, includeTests bool) *ErrorFlowReport {
	report := &ErrorFlowReport{
		Term: term,
	}

	nl := strings.ToLower(term)

	// 1. Find Definition Sites (var ErrInvalidToken)
	for _, s := range g.Symbols {
		if s.Kind == "var" || s.Kind == "const" {
			if strings.EqualFold(s.Name, term) {
				report.DefinitionSites = append(report.DefinitionSites, Result{
					Kind:   "definition",
					Name:   s.Name,
					File:   s.File,
					Line:   s.Line,
					Detail: "sentinel error declaration",
				})
			}
		}
	}

	// 2. Find Return/Wrap Sites in g.Errors
	var matches []graph.ErrorEdge
	for _, e := range g.Errors {
		if isTestFile(e.File) {
			if includeTests && strings.Contains(strings.ToLower(e.Message), nl) {
				report.RelatedTests = append(report.RelatedTests, Result{
					Kind:   "test",
					Name:   e.Function,
					File:   e.File,
					Line:   e.Line,
					Detail: "test asserts or expects error",
				})
			}
			continue
		}

		if strings.Contains(strings.ToLower(e.Message), nl) {
			matches = append(matches, e)
			report.ReturnSites = append(report.ReturnSites, Result{
				Kind:   "return",
				Name:   e.Function,
				File:   e.File,
				Line:   e.Line,
				Detail: "error message: " + e.Message,
			})
		}
	}

	// 3. Find Likely Paths using reverse BFS
	entryPoints := make(map[string]string)
	for _, r := range g.Routes {
		entryPoints[r.Handler] = "HTTP Route: " + r.Method + " " + r.Path
	}
	for _, s := range g.Symbols {
		if s.Name == "main" && !isTestFile(s.File) {
			entryPoints[s.ID] = "CLI Entrypoint"
		}
	}

	revAdj := make(map[string][]graph.CallEdge)
	for _, c := range g.Calls {
		if c.Synthetic {
			continue
		}
		if isTestFile(c.File) {
			continue
		}
		revAdj[c.CalleeRaw] = append(revAdj[c.CalleeRaw], c)
		if idx := strings.LastIndex(c.CalleeRaw, "."); idx != -1 {
			short := c.CalleeRaw[idx+1:]
			revAdj[short] = append(revAdj[short], c)
		}
	}

	for _, e := range matches {
		targetFunc := e.Function

		type state struct {
			node string
			path []graph.CallEdge
		}

		queue := []state{{node: targetFunc}}
		visited := make(map[string]bool)
		visited[targetFunc] = true

		found := false
		var bestPath []Result

		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]

			if entryDesc, isEntry := entryPoints[cur.node]; isEntry && len(cur.path) > 0 {
				var chain []Result
				for i := len(cur.path) - 1; i >= 0; i-- {
					edge := cur.path[i]
					chain = append(chain, Result{
						Kind:   "path",
						Name:   edge.CallerName,
						File:   edge.File,
						Line:   edge.Line,
						Detail: "calls " + edge.CalleeRaw,
					})
				}

				// Label the entry point
				chain[0].Detail = entryDesc + " -> " + chain[0].Detail

				lastEdge := cur.path[0]
				chain = append(chain, Result{
					Kind:   "path",
					Name:   lastEdge.CalleeRaw,
					File:   lastEdge.File,
					Line:   lastEdge.Line,
					Detail: "originates error",
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

		if found {
			report.Paths = append(report.Paths, TraceResult{
				Error: e,
				Path:  bestPath,
			})
		}
	}

	return report
}
