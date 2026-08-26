package search

import (
	"sort"
	"strconv"
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
	// CompactExploreLimit is the low-token default for compact mode.
	CompactExploreLimit = 5
	// DeepExploreLimit is the broader per-section default for deep mode.
	DeepExploreLimit = 25
	// ExploreDeepDepth is the exact identity call depth added by deep mode.
	ExploreDeepDepth = 3
	// MaxExploreLimit prevents a convenience query from accidentally returning
	// an unbounded graph slice. Focused commands remain available for deeper use.
	MaxExploreLimit = 100
)

// ExploreMode selects the amount of evidence returned by Explore.
type ExploreMode string

const (
	ExploreModeStandard ExploreMode = "standard"
	ExploreModeCompact  ExploreMode = "compact"
	ExploreModeDeep     ExploreMode = "deep"
)

// ExploreOptions controls the bounded, composed explore analysis.
type ExploreOptions struct {
	Limit int
	Exact bool
	Mode  ExploreMode
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

// ExploreDeepTotals reports complete sizes before the common response limit.
type ExploreDeepTotals struct {
	Callers        int `json:"callers"`
	Callees        int `json:"callees"`
	PackageContext int `json:"package_context"`
}

// ExploreExplanationTotals reports list sizes within the deep explanation.
type ExploreExplanationTotals struct {
	ProdCallers         int `json:"prod_callers"`
	TestCallers         int `json:"test_callers"`
	EnvKeys             int `json:"env_keys"`
	Routes              int `json:"routes"`
	ConcurrencyKinds    int `json:"concurrency_kinds"`
	SatisfiedInterfaces int `json:"satisfied_interfaces"`
	ConstructorNames    int `json:"constructor_names"`
}

// ExploreDeepPayload contains bounded follow-up evidence requested explicitly
// through deep mode. Possible dispatch is excluded from its identity traversals.
type ExploreDeepPayload struct {
	Depth                 int                      `json:"depth"`
	Totals                ExploreDeepTotals        `json:"totals"`
	Callers               []Result                 `json:"callers"`
	Callees               []Result                 `json:"callees"`
	PackageContext        []Result                 `json:"package_context"`
	Explanation           *ExplainResult           `json:"explanation,omitempty"`
	ExplanationTotals     ExploreExplanationTotals `json:"explanation_totals"`
	ExplanationTruncation []string                 `json:"explanation_truncated_fields"`
}

// ExploreResult is the shared native payload used by both CLI and MCP.
// Specialized commands remain authoritative when callers need one complete,
// unbounded section.
type ExploreResult struct {
	SchemaVersion     string              `json:"schema_version"`
	Query             string              `json:"query"`
	Mode              ExploreMode         `json:"mode"`
	SelectedSymbol    string              `json:"selected_symbol,omitempty"`
	SelectionBasis    string              `json:"selection_basis"`
	Ambiguous         bool                `json:"ambiguous"`
	Limit             int                 `json:"limit"`
	Count             int                 `json:"count"`
	Totals            ExploreTotals       `json:"totals"`
	Matches           []Result            `json:"matches"`
	Context           *ContextPayload     `json:"context,omitempty"`
	Impact            []Result            `json:"impact"`
	Deep              *ExploreDeepPayload `json:"deep,omitempty"`
	TruncatedSections []string            `json:"truncated_sections"`
	OmittedSections   []string            `json:"omitted_sections"`
	Limitations       []string            `json:"limitations"`
}

// NormalizeExploreMode preserves standard mode for zero or unknown internal
// values. CLI and MCP reject conflicting public selectors before this point.
func NormalizeExploreMode(mode ExploreMode) ExploreMode {
	switch mode {
	case ExploreModeCompact, ExploreModeDeep:
		return mode
	default:
		return ExploreModeStandard
	}
}

// DefaultExploreLimitForMode returns the per-section default for a mode.
func DefaultExploreLimitForMode(mode ExploreMode) int {
	switch NormalizeExploreMode(mode) {
	case ExploreModeCompact:
		return CompactExploreLimit
	case ExploreModeDeep:
		return DeepExploreLimit
	default:
		return DefaultExploreLimit
	}
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
	mode := NormalizeExploreMode(options.Mode)
	limit := options.Limit
	if limit <= 0 {
		limit = DefaultExploreLimitForMode(mode)
	} else {
		limit = NormalizeExploreLimit(limit)
	}
	result := ExploreResult{
		SchemaVersion:     ExploreSchemaVersion,
		Query:             query,
		Mode:              mode,
		SelectionBasis:    "none",
		Limit:             limit,
		Matches:           []Result{},
		Impact:            []Result{},
		TruncatedSections: []string{},
		OmittedSections:   []string{},
		Limitations: []string{
			"Static graph evidence is not proof of runtime behavior; inspect precise/fallback graph health before relying on call paths.",
			"Question-like input is matched lexically; selected_symbol and selection_basis identify the symbol used for deep context.",
			"Explore impact follows exact identity-resolved call edges and excludes possible dispatch; use the focused impact command when broader fallback traversal is required.",
		},
	}
	if mode == ExploreModeCompact {
		result.Limitations = append(result.Limitations, "Compact mode returns discovery, selected-node metadata, role, and complete counts while omitting token-heavy evidence bodies.")
	}
	if mode == ExploreModeDeep {
		result.Limitations = append(result.Limitations, "Deep mode adds bounded depth-3 exact identity callers/callees, package context, and an explanation; focused commands remain authoritative for complete output.")
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
	result.addTruncation("nodes", result.Totals.Nodes, len(payload.Nodes))

	impact := exploreIdentityImpact(g, selector)
	result.Totals.Impact = len(impact)
	if mode == ExploreModeCompact {
		payload.Source = ""
		payload.SourceError = ""
		payload.Callers = nil
		payload.Callees = nil
		payload.Tests = nil
		payload.TestResults = nil
		result.Context = &payload
		result.OmittedSections = append(result.OmittedSections, "source", "callers", "callees", "tests", "impact")
		return result
	}

	payload.Callers = limitExploreResults(payload.Callers, limit)
	payload.Callees = limitExploreResults(payload.Callees, limit)
	payload.TestResults = limitExploreResults(payload.TestResults, limit)
	payload.Tests = limitExploreStrings(payload.Tests, limit)
	result.Context = &payload
	result.addTruncation("callers", result.Totals.Callers, len(payload.Callers))
	result.addTruncation("callees", result.Totals.Callees, len(payload.Callees))
	result.addTruncation("tests", result.Totals.Tests, len(payload.TestResults))

	result.Impact = limitExploreResults(impact, limit)
	result.addTruncation("impact", result.Totals.Impact, len(result.Impact))

	if mode == ExploreModeDeep {
		if result.Ambiguous {
			result.OmittedSections = append(result.OmittedSections, "deep")
			result.Limitations = append(result.Limitations, "Deep expansion is omitted when selected_symbol is ambiguous; use an exact fully-qualified symbol ID.")
		} else if deep := buildExploreDeep(g, selector, limit); deep != nil {
			result.Deep = deep
			result.addTruncation("deep.callers", deep.Totals.Callers, len(deep.Callers))
			result.addTruncation("deep.callees", deep.Totals.Callees, len(deep.Callees))
			result.addTruncation("deep.package_context", deep.Totals.PackageContext, len(deep.PackageContext))
			if len(deep.ExplanationTruncation) > 0 {
				result.TruncatedSections = append(result.TruncatedSections, "deep.explanation")
			}
		}
	}
	return result
}

func buildExploreDeep(g *graph.Graph, selector string, limit int) *ExploreDeepPayload {
	selected := exploreSelectedSymbols(g, selector)
	if len(selected) != 1 {
		return nil
	}
	symbol := selected[0]
	callers := exploreIdentityCallers(g, symbol.ID, ExploreDeepDepth)
	callees := exploreIdentityCallees(g, symbol.ID, ExploreDeepDepth)
	packageContext := exploreExactPackageContext(g, symbol)
	explanation := Explain(g, symbol.ID)
	deep := &ExploreDeepPayload{
		Depth: ExploreDeepDepth,
		Totals: ExploreDeepTotals{
			Callers:        len(callers),
			Callees:        len(callees),
			PackageContext: len(packageContext),
		},
		Callers:               limitExploreResults(callers, limit),
		Callees:               limitExploreResults(callees, limit),
		PackageContext:        limitExploreResults(packageContext, limit),
		Explanation:           explanation,
		ExplanationTruncation: []string{},
	}
	if explanation != nil {
		deep.ExplanationTotals = ExploreExplanationTotals{
			ProdCallers:         len(explanation.ProdCallers),
			TestCallers:         len(explanation.TestCallers),
			EnvKeys:             len(explanation.EnvKeys),
			Routes:              len(explanation.Routes),
			ConcurrencyKinds:    len(explanation.ConcurrencyKinds),
			SatisfiedInterfaces: len(explanation.SatisfiedInterfaces),
			ConstructorNames:    len(explanation.ConstructorNames),
		}
		explanation.ProdCallers = limitExploreDeepStrings(explanation.ProdCallers, limit, "prod_callers", &deep.ExplanationTruncation)
		explanation.TestCallers = limitExploreDeepStrings(explanation.TestCallers, limit, "test_callers", &deep.ExplanationTruncation)
		explanation.EnvKeys = limitExploreDeepStrings(explanation.EnvKeys, limit, "env_keys", &deep.ExplanationTruncation)
		explanation.Routes = limitExploreDeepStrings(explanation.Routes, limit, "routes", &deep.ExplanationTruncation)
		explanation.ConcurrencyKinds = limitExploreDeepStrings(explanation.ConcurrencyKinds, limit, "concurrency_kinds", &deep.ExplanationTruncation)
		explanation.SatisfiedInterfaces = limitExploreDeepStrings(explanation.SatisfiedInterfaces, limit, "satisfied_interfaces", &deep.ExplanationTruncation)
		explanation.ConstructorNames = limitExploreDeepStrings(explanation.ConstructorNames, limit, "constructor_names", &deep.ExplanationTruncation)
		explanation.Narrative = renderNarrativeWithCallerCounts(
			exploreDisplaySymbol(symbol), explanation,
			deep.ExplanationTotals.ProdCallers, deep.ExplanationTotals.TestCallers,
		)
	}
	return deep
}

func exploreExactPackageContext(g *graph.Graph, symbol graph.SymbolNode) []Result {
	for _, pkg := range g.Packages {
		for _, file := range pkg.Files {
			if file != symbol.File {
				continue
			}
			scoped := *g
			scoped.Packages = []graph.PackageNode{pkg}
			return Focus(&scoped, pkg.Dir)
		}
	}
	return []Result{}
}

func limitExploreDeepStrings(values []string, limit int, field string, truncated *[]string) []string {
	limited := limitExploreStrings(values, limit)
	if len(values) > len(limited) {
		*truncated = append(*truncated, field)
	}
	return limited
}

func exploreIdentityImpact(g *graph.Graph, selector string) []Result {
	targets := make(map[string]bool)
	for _, symbol := range exploreSelectedSymbols(g, selector) {
		targets[symbol.ID] = true
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

func exploreSelectedSymbols(g *graph.Graph, selector string) []graph.SymbolNode {
	selected := make([]graph.SymbolNode, 0)
	for _, symbol := range g.Symbols {
		if matchSymbolExact(symbol, selector) {
			selected = append(selected, symbol)
		}
	}
	return selected
}

func exploreIdentityCallers(g *graph.Graph, selector string, maxDepth int) []Result {
	selected := exploreSelectedSymbols(g, selector)
	if len(selected) == 0 || maxDepth < 1 {
		return []Result{}
	}
	symbols := make(map[string]graph.SymbolNode, len(g.Symbols))
	syntheticReverse := make(map[string][]string)
	frontier := make(map[string]bool, len(selected))
	seen := make(map[string]bool, len(selected))
	for _, symbol := range g.Symbols {
		symbols[symbol.ID] = symbol
	}
	for _, symbol := range selected {
		frontier[symbol.ID] = true
		seen[symbol.ID] = true
	}
	for _, call := range g.Calls {
		if exploreSyntheticCall(call) {
			syntheticReverse[call.CalleeSymbolID] = append(syntheticReverse[call.CalleeSymbolID], call.CallerSymbolID)
		}
	}
	results := make([]Result, 0)
	for depth := 1; depth <= maxDepth && len(frontier) > 0; depth++ {
		expanded := expandExploreSyntheticIDs(frontier, syntheticReverse)
		next := make(map[string]bool)
		for _, call := range g.Calls {
			if !exploreExactOrdinaryCall(call) || !expanded[call.CalleeSymbolID] || seen[call.CallerSymbolID] {
				continue
			}
			seen[call.CallerSymbolID] = true
			next[call.CallerSymbolID] = true
			symbol, ok := symbols[call.CallerSymbolID]
			if !ok {
				continue
			}
			results = append(results, Result{
				Kind:           "caller",
				Name:           exploreDisplaySymbol(symbol),
				StableID:       symbol.ID,
				File:           symbol.File,
				Line:           symbol.Line,
				Detail:         "depth " + strconv.Itoa(depth) + " — exact identity caller",
				CallSiteFile:   call.File,
				CallSiteLine:   call.Line,
				CallSiteColumn: call.Column,
				Score:          10 - depth,
			})
		}
		frontier = next
	}
	sortResults(results)
	return results
}

func exploreIdentityCallees(g *graph.Graph, selector string, maxDepth int) []Result {
	selected := exploreSelectedSymbols(g, selector)
	if len(selected) == 0 || maxDepth < 1 {
		return []Result{}
	}
	frontier := make(map[string]bool, len(selected))
	seen := make(map[string]bool, len(selected))
	symbols := make(map[string]graph.SymbolNode, len(g.Symbols))
	syntheticForward := make(map[string][]string)
	for _, symbol := range g.Symbols {
		symbols[symbol.ID] = symbol
	}
	for _, symbol := range selected {
		frontier[symbol.ID] = true
		seen[symbol.ID] = true
	}
	for _, call := range g.Calls {
		if exploreSyntheticCall(call) {
			syntheticForward[call.CallerSymbolID] = append(syntheticForward[call.CallerSymbolID], call.CalleeSymbolID)
		}
	}
	syntheticTargets := make(map[string][]string)
	results := make([]Result, 0)
	for depth := 1; depth <= maxDepth && len(frontier) > 0; depth++ {
		expanded := expandExploreSyntheticIDs(frontier, syntheticForward)
		next := make(map[string]bool)
		for _, call := range g.Calls {
			if !exploreExactOrdinaryCall(call) || !expanded[call.CallerSymbolID] {
				continue
			}
			targets, ok := syntheticTargets[call.CalleeSymbolID]
			if !ok {
				targets = exploreForwardSyntheticTargets(call.CalleeSymbolID, syntheticForward)
				syntheticTargets[call.CalleeSymbolID] = targets
			}
			for _, targetID := range targets {
				if seen[targetID] {
					continue
				}
				seen[targetID] = true
				next[targetID] = true
				callee, found := symbols[targetID]
				name := call.CalleeRaw
				file := ""
				line := 0
				stableID := targetID
				if found {
					name = exploreDisplaySymbol(callee)
					file = callee.File
					line = callee.Line
					stableID = callee.ID
				}
				results = append(results, Result{
					Kind:           "callee",
					Name:           name,
					StableID:       stableID,
					File:           file,
					Line:           line,
					Detail:         "depth " + strconv.Itoa(depth) + " — exact identity callee",
					CallSiteFile:   call.File,
					CallSiteLine:   call.Line,
					CallSiteColumn: call.Column,
					Score:          10 - depth,
				})
			}
		}
		frontier = next
	}
	sortResults(results)
	return results
}

func exploreForwardSyntheticTargets(targetID string, syntheticForward map[string][]string) []string {
	closure := expandExploreSyntheticIDs(map[string]bool{targetID: true}, syntheticForward)
	targets := make([]string, 0, len(closure))
	for id := range closure {
		if len(syntheticForward[id]) == 0 {
			targets = append(targets, id)
		}
	}
	if len(targets) == 0 {
		targets = append(targets, targetID)
	}
	sort.Strings(targets)
	return targets
}

func exploreExactOrdinaryCall(call graph.CallEdge) bool {
	return call.CallerSymbolID != "" && call.CalleeSymbolID != "" &&
		call.Resolution != graph.CallResolutionCHA &&
		!call.Synthetic && call.Resolution != graph.CallResolutionSynthetic
}

func expandExploreSyntheticIDs(frontier map[string]bool, adjacency map[string][]string) map[string]bool {
	expanded := copyExploreIDs(frontier)
	queue := make([]string, 0, len(frontier))
	for id := range frontier {
		queue = append(queue, id)
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, adjacent := range adjacency[id] {
			if expanded[adjacent] {
				continue
			}
			expanded[adjacent] = true
			queue = append(queue, adjacent)
		}
	}
	return expanded
}

func exploreSyntheticCall(call graph.CallEdge) bool {
	return call.CallerSymbolID != "" && call.CalleeSymbolID != "" &&
		(call.Synthetic || call.Resolution == graph.CallResolutionSynthetic)
}

func copyExploreIDs(source map[string]bool) map[string]bool {
	copy := make(map[string]bool, len(source))
	for id := range source {
		copy[id] = true
	}
	return copy
}

func exploreDisplaySymbol(symbol graph.SymbolNode) string {
	if symbol.Receiver != "" {
		return "(" + symbol.Receiver + ")." + symbol.Name
	}
	return symbol.Name
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
