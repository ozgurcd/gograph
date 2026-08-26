package search

import (
	"strings"
	"unicode"

	"github.com/ozgurcd/gograph/internal/graph"
)

const (
	// ExploreSchemaVersion identifies the native CLI/MCP explore payload.
	ExploreSchemaVersion = "gograph.explore.v1"
	// DefaultExploreLimit bounds every list in an explore response unless the
	// caller explicitly selects another limit.
	DefaultExploreLimit = 10
	// MaxExploreLimit prevents a convenience query from accidentally returning
	// an unbounded graph slice. Focused commands remain available for deeper use.
	MaxExploreLimit = 100
)

// ExploreOptions controls the bounded, composed explore analysis.
type ExploreOptions struct {
	Limit int
	Exact bool
}

// ExploreTotals reports the complete size of every section before the common
// response limit is applied.
type ExploreTotals struct {
	Matches int `json:"matches"`
	Nodes   int `json:"nodes"`
	Callers int `json:"callers"`
	Callees int `json:"callees"`
	Tests   int `json:"tests"`
	Impact  int `json:"impact"`
}

// ExploreResult is the shared native payload used by both CLI and MCP.
// Specialized commands remain authoritative when callers need one complete,
// unbounded section.
type ExploreResult struct {
	SchemaVersion     string          `json:"schema_version"`
	Query             string          `json:"query"`
	SelectedSymbol    string          `json:"selected_symbol,omitempty"`
	SelectionBasis    string          `json:"selection_basis"`
	Ambiguous         bool            `json:"ambiguous"`
	Limit             int             `json:"limit"`
	Count             int             `json:"count"`
	Totals            ExploreTotals   `json:"totals"`
	Matches           []Result        `json:"matches"`
	Context           *ContextPayload `json:"context,omitempty"`
	Impact            []Result        `json:"impact"`
	TruncatedSections []string        `json:"truncated_sections"`
	Limitations       []string        `json:"limitations"`
}

// NormalizeExploreLimit applies the same limit contract for CLI and MCP.
func NormalizeExploreLimit(limit int) int {
	if limit <= 0 {
		return DefaultExploreLimit
	}
	if limit > MaxExploreLimit {
		return MaxExploreLimit
	}
	return limit
}

// Explore composes broad lexical search with symbol context and upstream impact.
// It never interprets free-form text with a model: question-like input is reduced
// to deterministic search terms and the selected symbol is disclosed explicitly.
func Explore(g *graph.Graph, rootDir, query string, options ExploreOptions) ExploreResult {
	query = strings.TrimSpace(query)
	limit := NormalizeExploreLimit(options.Limit)
	result := ExploreResult{
		SchemaVersion:     ExploreSchemaVersion,
		Query:             query,
		SelectionBasis:    "none",
		Limit:             limit,
		Matches:           []Result{},
		Impact:            []Result{},
		TruncatedSections: []string{},
		Limitations: []string{
			"Static graph evidence is not proof of runtime behavior; inspect precise/fallback graph health before relying on call paths.",
			"Question-like input is matched lexically; selected_symbol and selection_basis identify the symbol used for deep context.",
			"Explore impact follows exact identity-resolved call edges and excludes possible dispatch; use the focused impact command when broader fallback traversal is required.",
		},
	}
	if query == "" {
		return result
	}

	terms := exploreTerms(query)
	matches := Query(g, terms)
	rankExploreMatches(matches, query, terms)
	result.Totals.Matches = len(matches)
	result.Matches = limitExploreResults(matches, limit)
	result.Count = len(result.Matches)
	if len(matches) > len(result.Matches) {
		result.TruncatedSections = append(result.TruncatedSections, "matches")
	}

	selector := query
	contextResult := Context(g, rootDir, selector, true)
	if contextResult != nil {
		result.SelectionBasis = "direct_symbol_match"
	} else if !options.Exact {
		for _, match := range matches {
			if !isExploreSymbolResult(match) {
				continue
			}
			candidate := Context(g, rootDir, match.Name, true)
			if candidate == nil {
				continue
			}
			selector = match.Name
			contextResult = candidate
			result.SelectionBasis = "ranked_lexical_match"
			break
		}
	}
	if contextResult == nil {
		return result
	}
	if result.Totals.Matches == 0 && len(contextResult.Node) > 0 {
		result.Totals.Matches = len(contextResult.Node)
		result.Matches = limitExploreResults(contextResult.Node, limit)
		result.Count = len(result.Matches)
		result.addTruncation("matches", result.Totals.Matches, result.Count)
	}

	result.SelectedSymbol = selector
	result.Ambiguous = len(contextResult.Node) > 1
	result.Totals.Nodes = len(contextResult.Node)
	result.Totals.Callers = len(contextResult.Callers)
	result.Totals.Callees = len(contextResult.Callees)
	result.Totals.Tests = len(contextResult.Tests)

	payload := NewContextPayload(selector, contextResult)
	payload.Nodes = limitExploreResults(payload.Nodes, limit)
	payload.Callers = limitExploreResults(payload.Callers, limit)
	payload.Callees = limitExploreResults(payload.Callees, limit)
	payload.TestResults = limitExploreResults(payload.TestResults, limit)
	payload.Tests = limitExploreStrings(payload.Tests, limit)
	result.Context = &payload
	result.addTruncation("nodes", result.Totals.Nodes, len(payload.Nodes))
	result.addTruncation("callers", result.Totals.Callers, len(payload.Callers))
	result.addTruncation("callees", result.Totals.Callees, len(payload.Callees))
	result.addTruncation("tests", result.Totals.Tests, len(payload.TestResults))

	impact := exploreIdentityImpact(g, selector)
	result.Totals.Impact = len(impact)
	result.Impact = limitExploreResults(impact, limit)
	result.addTruncation("impact", result.Totals.Impact, len(result.Impact))
	return result
}

func exploreIdentityImpact(g *graph.Graph, selector string) []Result {
	targets := make(map[string]bool)
	for _, symbol := range g.Symbols {
		if matchSymbolExact(symbol, selector) {
			targets[symbol.ID] = true
		}
	}
	if len(targets) == 0 {
		return []Result{}
	}

	type incomingCall struct {
		callerID  string
		synthetic bool
	}
	reverse := make(map[string][]incomingCall)
	for _, call := range g.Calls {
		if call.CalleeSymbolID == "" || call.CallerSymbolID == "" || call.Resolution == graph.CallResolutionCHA {
			continue
		}
		reverse[call.CalleeSymbolID] = append(reverse[call.CalleeSymbolID], incomingCall{
			callerID:  call.CallerSymbolID,
			synthetic: call.Synthetic || call.Resolution == graph.CallResolutionSynthetic,
		})
	}

	symbols := make(map[string]graph.SymbolNode)
	for _, symbol := range g.Symbols {
		symbols[symbol.ID] = symbol
	}
	queue := make([]string, 0, len(targets))
	for target := range targets {
		queue = append(queue, target)
	}
	visited := make(map[string]bool, len(targets))
	for target := range targets {
		visited[target] = true
	}
	seenResults := make(map[string]bool)
	results := make([]Result, 0)
	for len(queue) > 0 {
		target := queue[0]
		queue = queue[1:]
		for _, call := range reverse[target] {
			if !visited[call.callerID] {
				visited[call.callerID] = true
				queue = append(queue, call.callerID)
			}
			if targets[call.callerID] {
				continue
			}
			if call.synthetic {
				continue
			}
			if seenResults[call.callerID] {
				continue
			}
			symbol, ok := symbols[call.callerID]
			if !ok {
				continue
			}
			seenResults[call.callerID] = true
			results = append(results, Result{
				Kind:   "impact",
				Name:   symbol.Name,
				File:   symbol.File,
				Line:   symbol.Line,
				Detail: "identity-resolved upstream impact of " + selector,
				Score:  8,
			})
		}
	}
	sortResults(results)
	return results
}

func (result *ExploreResult) addTruncation(section string, total, returned int) {
	if total > returned {
		result.TruncatedSections = append(result.TruncatedSections, section)
	}
}

func exploreTerms(query string) []string {
	words := strings.FieldsFunc(query, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(",;!?()[]{}\"'`", r)
	})
	stopWords := map[string]bool{
		"a": true, "an": true, "and": true, "are": true, "does": true,
		"from": true, "how": true, "is": true, "of": true, "the": true,
		"to": true, "what": true, "where": true, "which": true, "why": true,
		"work": true, "working": true, "works": true,
	}
	terms := make([]string, 0, len(words))
	for _, word := range words {
		word = strings.TrimSpace(word)
		if word == "" || stopWords[strings.ToLower(word)] {
			continue
		}
		terms = append(terms, word)
	}
	if len(terms) == 0 {
		return []string{query}
	}
	return terms
}

func rankExploreMatches(matches []Result, query string, terms []string) {
	query = strings.ToLower(query)
	for i := range matches {
		name := strings.ToLower(matches[i].Name)
		file := strings.ToLower(matches[i].File)
		detail := strings.ToLower(matches[i].Detail)
		if name == query {
			matches[i].Score += 1000
		}
		if isExploreSymbolResult(matches[i]) {
			matches[i].Score += 100
		}
		for _, term := range terms {
			term = strings.ToLower(term)
			switch {
			case name == term:
				matches[i].Score += 100
			case strings.Contains(name, term):
				matches[i].Score += 30
			}
			if strings.Contains(file, term) {
				matches[i].Score += 10
			}
			if strings.Contains(detail, term) {
				matches[i].Score += 5
			}
		}
	}
	sortResults(matches)
}

func isExploreSymbolResult(result Result) bool {
	switch result.Kind {
	case "package", "file", "import", "call":
		return false
	default:
		return true
	}
}

func limitExploreResults(results []Result, limit int) []Result {
	if len(results) == 0 {
		return []Result{}
	}
	if len(results) <= limit {
		return append([]Result{}, results...)
	}
	return append([]Result{}, results[:limit]...)
}

func limitExploreStrings(values []string, limit int) []string {
	if len(values) == 0 {
		return []string{}
	}
	if len(values) <= limit {
		return append([]string{}, values...)
	}
	return append([]string{}, values[:limit]...)
}
