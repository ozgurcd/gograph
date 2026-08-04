package search

import (
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// ContextResult bundles everything an AI agent needs to understand and work on
// a single symbol into one response, replacing 4–5 separate tool calls.
// This is the primary token-saving primitive for iterative agent workflows.
type ContextResult struct {
	// Node holds the AST details (kind, file, line, signature, doc).
	Node []Result
	// Source is the raw source code of the symbol, empty if unavailable.
	Source string
	// SourceErr holds any error from source extraction (non-fatal).
	SourceErr error
	// Callers lists functions that call this symbol.
	Callers []Result
	// Callees lists functions this symbol calls.
	Callees []Result
	// Tests lists test functions that exercise this symbol.
	Tests []Result
	// Role is a lightweight architectural classification derived from callers,
	// callees, routes, and SQL — without a full Explain computation.
	// Values: "HTTP handler", "data access", "orchestrator", "coordinator",
	//         "utility", "entry point", "internal".
	Role string
}

// ContextPayload is the transport-safe representation shared by CLI JSON and
// MCP context/plan responses. ContextResult keeps SourceErr as an error for
// internal control flow; this DTO preserves it as text and exposes both the
// first node compatibility field and every ambiguous match.
type ContextPayload struct {
	Symbol      string   `json:"symbol,omitempty"`
	Role        string   `json:"role,omitempty"`
	Node        *Result  `json:"node,omitempty"`
	Nodes       []Result `json:"nodes,omitempty"`
	Source      string   `json:"source,omitempty"`
	SourceError string   `json:"source_error,omitempty"`
	Callers     []Result `json:"callers,omitempty"`
	Callees     []Result `json:"callees,omitempty"`
	Tests       []string `json:"tests,omitempty"`
	TestResults []Result `json:"test_results,omitempty"`
}

// NewContextPayload converts an internal context result into the stable
// transport representation used by both front ends.
func NewContextPayload(symbol string, result *ContextResult) ContextPayload {
	payload := ContextPayload{
		Symbol:      symbol,
		Role:        result.Role,
		Nodes:       result.Node,
		Source:      result.Source,
		Callers:     result.Callers,
		Callees:     result.Callees,
		TestResults: result.Tests,
	}
	if len(result.Node) > 0 {
		payload.Node = &result.Node[0]
	}
	if result.SourceErr != nil {
		payload.SourceError = result.SourceErr.Error()
	}
	for _, test := range result.Tests {
		payload.Tests = append(payload.Tests, test.Name)
	}
	return payload
}

// Context finds the best-matching symbol for term and returns a ContextResult
// bundling its node details, source code, callers, callees, test coverage, and
// a lightweight architectural role. Returns nil if no symbol matches.
// rootDir is the repository root for source extraction (pass "." for cwd).
func Context(g *graph.Graph, rootDir, term string, exactMatch bool) *ContextResult {
	node := Node(g, term)
	if exactMatch {
		node = exactContextNodes(g, term)
	}
	if len(node) == 0 {
		return nil
	}

	src, srcErr := Source(g, rootDir, term)
	callers := Callers(g, term, true, exactMatch)
	callees := Callees(g, term, true, exactMatch)

	return &ContextResult{
		Node:      node,
		Source:    src,
		SourceErr: srcErr,
		Callers:   callers,
		Callees:   callees,
		Tests:     Tests(g, term),
		Role:      quickRole(g, term, callers, callees),
	}
}

// exactContextNodes renders only symbols whose complete user-facing identity
// matches term. Filtering Node results by Result.Name loses the canonical ID
// and package metadata, which made exact fully-qualified and receiver-qualified
// queries disappear even though Node had already found the symbol.
func exactContextNodes(g *graph.Graph, term string) []Result {
	var results []Result
	for _, symbol := range g.Symbols {
		if !matchSymbolExact(symbol, term) {
			continue
		}
		name := symbol.Name
		if symbol.Receiver != "" {
			name = "(" + symbol.Receiver + ")." + symbol.Name
		}
		results = append(results, Result{
			Kind:   string(symbol.Kind),
			Name:   name,
			File:   symbol.File,
			Line:   symbol.Line,
			Detail: symbol.Signature,
		})
	}
	sortResults(results)
	return results
}

// quickRole derives an architectural role from data already computed in Context,
// without the full cost of Explain. It is intentionally coarse-grained.
func quickRole(g *graph.Graph, term string, callers, callees []Result) string {
	nl := strings.ToLower(term)

	for _, r := range g.Routes {
		if strings.ToLower(r.Handler) == nl || strings.HasSuffix(strings.ToLower(r.Handler), "."+nl) {
			return "HTTP handler"
		}
	}

	for _, sql := range g.SQLs {
		if strings.ToLower(sql.Function) == nl || strings.HasSuffix(strings.ToLower(sql.Function), "."+nl) {
			return "data access"
		}
	}

	prodCallers := 0
	for _, c := range callers {
		if !isTestFile(c.File) {
			prodCallers++
		}
	}
	calleeCount := len(callees)

	if prodCallers == 0 {
		return "entry point"
	}
	if prodCallers >= 5 && calleeCount >= 5 {
		return "orchestrator"
	}
	if prodCallers >= 5 {
		return "utility"
	}
	if calleeCount >= 5 {
		return "coordinator"
	}
	return "internal"
}
