package search

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// ExplainResult holds a structured architectural summary of a symbol.
// It composes data from multiple graph queries into a single narrative
// designed for prompt injection or onboarding documentation.
type ExplainResult struct {
	SchemaVersion string           `json:"schema_version"`
	Status        string           `json:"status"`
	Candidates    []SymbolIdentity `json:"candidates,omitempty"`
	// Identity
	Symbol  string `json:"symbol"`
	Kind    string `json:"kind"` // "function", "method", "struct", "interface"
	Package string `json:"package"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Doc     string `json:"doc,omitempty"`

	// Fan-in
	CallerCount int      `json:"caller_count"`
	ProdCallers []string `json:"prod_callers,omitempty"`
	TestCallers []string `json:"test_callers,omitempty"`

	// Fan-out (func/method only)
	CalleeCount         int `json:"callee_count,omitempty"`
	CrossPkgCalleeCount int `json:"cross_pkg_callee_count,omitempty"`

	// Complexity (func/method only)
	Complexity      int    `json:"complexity,omitempty"`
	ComplexityLabel string `json:"complexity_label,omitempty"`

	// I/O surface
	SQLDirect  int      `json:"sql_direct"`
	SQLCallees int      `json:"sql_callees"`
	EnvKeys    []string `json:"env_keys,omitempty"`

	// HTTP
	IsRouteHandler bool     `json:"is_route_handler"`
	Routes         []string `json:"routes,omitempty"`

	// Concurrency
	ConcurrencyKinds []string `json:"concurrency_kinds,omitempty"`

	// Test coverage
	DirectTestCount int  `json:"direct_test_count"`
	HasDirectTests  bool `json:"has_direct_tests"`

	// Interface contracts
	SatisfiedInterfaces []string `json:"satisfied_interfaces,omitempty"`

	// Struct-specific
	FieldCount       int      `json:"field_count,omitempty"`
	MethodCount      int      `json:"method_count,omitempty"`
	ConstructorNames []string `json:"constructor_names,omitempty"`
	Arity            int      `json:"arity,omitempty"`

	// Flags
	IsOrphan bool `json:"is_orphan"`

	// Pre-rendered prose
	Narrative string `json:"narrative"`

	// Architectural role classification
	Role string `json:"role"`
}

// Explain synthesizes a structured natural-language description of a symbol's
// architectural role by composing data from multiple graph queries.
// Returns nil if no matching symbol is found.
func Explain(g *graph.Graph, term string) *ExplainResult {
	// Resolve the symbol
	sym := resolveSymbol(g, term)
	if sym == nil {
		candidates := explanationCandidates(g, term)
		if len(candidates) > 1 {
			result := &ExplainResult{SchemaVersion: "gograph.explain.v1", Status: "ambiguous", Narrative: "Several symbols match. Retry with one of the fully qualified candidate IDs."}
			for _, candidate := range candidates {
				result.Candidates = append(result.Candidates, symbolIdentity(candidate))
			}
			return result
		}
		return nil
	}
	// All subsequent relationships must use the selected identity, not the
	// original shorthand that could match another package or receiver.
	term = sym.ID

	res := &ExplainResult{
		SchemaVersion: "gograph.explain.v1",
		Status:        "ok",
		Symbol:        sym.ID,
		Kind:          string(sym.Kind),
		Package:       sym.PackageName,
		File:          sym.File,
		Line:          sym.Line,
		Doc:           sym.Doc,
		Arity:         sym.Arity,
	}

	displayName := sym.Name
	if sym.Receiver != "" {
		displayName = "(" + sym.Receiver + ")." + sym.Name
	}

	// Callers: split into production vs test
	callerGraph := *g
	callerGraph.Calls = nil
	for _, call := range g.Calls {
		if target := explanationTarget(g, call.CalleeSymbolID, call.CalleeRaw, call.File); target != nil && target.ID == sym.ID {
			call.CalleeSymbolID = sym.ID
			callerGraph.Calls = append(callerGraph.Calls, call)
		}
	}
	allCallers := Callers(&callerGraph, term, true, true)
	for _, c := range allCallers {
		if isTestFile(c.CallSiteFile) || isTestFile(c.File) {
			res.TestCallers = append(res.TestCallers, c.Name)
		} else {
			res.ProdCallers = append(res.ProdCallers, c.Name)
		}
	}
	res.CallerCount = len(allCallers)

	// Callees (func/method only)
	if sym.Kind == graph.KindFunction || sym.Kind == graph.KindMethod {
		callees := Callees(g, term, false, false)
		res.CalleeCount = len(callees)

		// Count cross-package callees: if the callee name contains a dot
		// and the prefix doesn't match the symbol's own package, it's cross-package.
		for _, ce := range callees {
			if parts := strings.SplitN(ce.Name, ".", 2); len(parts) == 2 {
				if !strings.EqualFold(parts[0], sym.PackageName) {
					res.CrossPkgCalleeCount++
				}
			}
		}

		// Complexity — reuse the Complexity function, find the matching result
		cxResults := Complexity(g, sym.ID)
		for _, cx := range cxResults {
			if cx.File == sym.File && cx.Line == sym.Line {
				res.Complexity = cx.Score
				res.ComplexityLabel = cx.Label
				break
			}
		}
	}

	// SQL: direct (in this function's body)
	for _, sql := range g.SQLs {
		if explanationFactMatches(sql.Function, sql.File, sql.Line, sym) {
			res.SQLDirect++
		}
	}

	// SQL: via direct callees (1-level deep)
	directCallees := make(map[string]graph.SymbolNode)
	for _, call := range g.Calls {
		if call.CallerSymbolID == sym.ID || call.CallerSymbolID == "" && explanationFactMatches(call.CallerName, call.File, call.Line, sym) {
			if target := explanationTarget(g, call.CalleeSymbolID, call.CalleeRaw, call.File); target != nil {
				directCallees[target.ID] = *target
			}
		}
	}
	for _, sql := range g.SQLs {
		for _, callee := range directCallees {
			if explanationFactMatches(sql.Function, sql.File, sql.Line, &callee) {
				res.SQLCallees++
				break
			}
		}
	}

	// Env reads (direct only)
	envSet := make(map[string]bool)
	for _, env := range g.EnvReads {
		if explanationFactMatches(env.Function, env.File, env.Line, sym) && !envSet[env.Key] {
			envSet[env.Key] = true
			res.EnvKeys = append(res.EnvKeys, env.Key)
		}
	}

	// Routes
	for _, route := range g.Routes {
		if target := explanationTarget(g, "", route.Handler, route.File); !route.DynamicHandler && target != nil && target.ID == sym.ID {
			res.IsRouteHandler = true
			res.Routes = append(res.Routes, fmt.Sprintf("%s %s", route.Method, route.Path))
		}
	}

	// Concurrency (direct)
	kindSet := make(map[string]bool)
	for _, c := range g.Concurrency {
		if explanationFactMatches(c.Function, c.File, c.Line, sym) && !kindSet[c.Kind] {
			kindSet[c.Kind] = true
			res.ConcurrencyKinds = append(res.ConcurrencyKinds, c.Kind)
		}
	}

	// Test coverage
	seenTests := make(map[string]bool)
	for _, te := range g.TestEdges {
		key := te.File + "\x00" + te.TestFunc
		if target := explanationTarget(g, te.TargetSymbolID, te.Target, te.File); target != nil && target.ID == sym.ID && !seenTests[key] {
			seenTests[key] = true
			res.DirectTestCount++
		}
	}
	res.HasDirectTests = res.DirectTestCount > 0

	// Interface satisfaction (for methods: check receiver; for structs: check name)
	ifaceSet := make(map[string]bool)
	for _, impl := range g.Implements {
		match := false
		if impl.ConcreteID != "" && sym.Kind == graph.KindStruct && strings.EqualFold(impl.ConcreteID, sym.ID) {
			match = true
		} else if impl.ConcreteID != "" && sym.Receiver != "" {
			pkg := sym.ID
			if idx := strings.Index(pkg, "::"); idx >= 0 {
				pkg = pkg[:idx]
			}
			receiverID := pkg + "::" + strings.TrimPrefix(sym.Receiver, "*")
			match = strings.EqualFold(impl.ConcreteID, receiverID)
		} else if impl.ConcreteID == "" && sym.Kind == graph.KindStruct && strings.EqualFold(impl.Concrete, sym.Name) && explanationUniqueType(g, sym) {
			match = true
		}
		if match && !ifaceSet[impl.Interface] {
			ifaceSet[impl.Interface] = true
			res.SatisfiedInterfaces = append(res.SatisfiedInterfaces, impl.Interface)
		}
	}

	// Struct-specific: fields, methods, constructors
	if sym.Kind == graph.KindStruct {
		res.FieldCount = len(sym.StructFields)
		// Count methods with this struct as receiver
		for _, s := range g.Symbols {
			if s.Kind == graph.KindMethod && strings.TrimPrefix(s.Receiver, "*") == sym.Name && explanationPackage(s) == explanationPackage(*sym) && s.PackageName == sym.PackageName {
				res.MethodCount++
			}
		}
		ctors := Constructors(g, sym.Name)
		for _, c := range ctors {
			for _, candidate := range g.Symbols {
				if candidate.File == c.File && candidate.Line == c.Line && explanationPackage(candidate) == explanationPackage(*sym) && candidate.PackageName == sym.PackageName {
					res.ConstructorNames = append(res.ConstructorNames, c.Name)
					break
				}
			}
		}
	}

	// Orphan check (zero production callers, non-test, non-main)
	res.IsOrphan = len(res.ProdCallers) == 0 &&
		sym.Kind != graph.KindStruct &&
		sym.Kind != graph.KindInterface &&
		sym.Kind != graph.KindVar &&
		sym.Kind != graph.KindConst &&
		sym.Name != "main" && sym.Name != "init" &&
		!strings.HasPrefix(sym.Name, "Test")

	// Classify architectural role
	res.Role = classifyRole(res)

	// Render prose narrative
	res.Narrative = renderNarrative(displayName, res)

	return res
}

// resolveSymbol finds the best-matching SymbolNode for the given term.
func resolveSymbol(g *graph.Graph, term string) *graph.SymbolNode {
	candidates := explanationCandidates(g, term)
	if len(candidates) != 1 {
		return nil
	}
	return &candidates[0]
}

func explanationCandidates(g *graph.Graph, term string) []graph.SymbolNode {
	term = strings.TrimSpace(term)
	if g == nil || term == "" {
		return nil
	}
	identity := Identity(g, term)
	ids := make(map[string]bool)
	for _, candidate := range identity.Matches {
		ids[candidate.StableID] = true
	}
	var candidates []graph.SymbolNode
	for _, symbol := range g.Symbols {
		if len(ids) > 0 {
			if ids[symbol.ID] {
				candidates = append(candidates, symbol)
			}
		} else if !isFullyQualifiedID(term) && (MatchSymbol(symbol, term) || strings.Contains(strings.ToLower(symbol.ID), strings.ToLower(term))) {
			candidates = append(candidates, symbol)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	return candidates
}

// matchesSymbol checks if a function name in a graph edge matches the given symbol.
func matchesSymbol(funcName string, sym *graph.SymbolNode) bool {
	if funcName == sym.Name || funcName == sym.ID {
		return true
	}
	// These spellings come from source facts, not a case-insensitive user query.
	// Preserve Go identifier case while accepting pointer/receiver notation.
	normalize := strings.NewReplacer("(", "", ")", "", "*", "", " ", "")
	name := normalize.Replace(funcName)
	if name == sym.PackageName+"."+sym.Name {
		return true
	}
	if sym.Receiver != "" {
		receiver := normalize.Replace(sym.Receiver) + "." + sym.Name
		return name == receiver || name == sym.PackageName+"."+receiver
	}
	return false
}

// classifyRole produces an opinionated architectural classification based on
// fan-in/fan-out ratios and I/O surface.
func classifyRole(r *ExplainResult) string {
	switch r.Kind {
	case "struct":
		if r.MethodCount >= 10 || r.FieldCount >= 15 {
			return "large data model or potential god object"
		}
		if r.MethodCount == 0 {
			return "data transfer object (no methods)"
		}
		return "data model"

	case "interface":
		return "contract definition"
	}

	// Function/method classification
	if r.IsRouteHandler {
		return "HTTP handler (entry point)"
	}

	prodCount := len(r.ProdCallers)
	calleeCount := r.CalleeCount

	if prodCount == 0 && !r.HasDirectTests && r.Kind != "method" {
		return "unused or entry point (no detected production callers)"
	}

	if prodCount >= 5 && calleeCount <= 2 {
		return "high-traffic leaf utility"
	}
	if prodCount >= 5 && calleeCount >= 5 {
		return "high-traffic orchestrator"
	}
	if calleeCount >= 5 && prodCount <= 2 {
		return "service orchestrator (coordinator)"
	}
	if r.SQLDirect > 0 || r.SQLCallees > 0 {
		return "data access layer"
	}
	if calleeCount == 0 {
		return "leaf function"
	}

	return "internal utility"
}

// renderNarrative produces a human-readable prose paragraph from the structured data.
func renderNarrative(displayName string, r *ExplainResult) string {
	return renderNarrativeWithCallerCounts(displayName, r, len(r.ProdCallers), len(r.TestCallers))
}

func renderNarrativeWithCallerCounts(displayName string, r *ExplainResult, prodCallerCount, testCallerCount int) string {
	var sb strings.Builder

	// Opening sentence: identity
	fmt.Fprintf(&sb, "%s is a %s", displayName, r.Kind)
	if r.Package != "" {
		fmt.Fprintf(&sb, " in package %s", r.Package)
	}
	fmt.Fprintf(&sb, " (%s:%d).", r.File, r.Line)

	// Fan-in
	if r.CallerCount > 0 {
		fmt.Fprintf(&sb, " It is called by %d production caller(s)", prodCallerCount)
		if testCallerCount > 0 {
			fmt.Fprintf(&sb, " and %d test caller(s)", testCallerCount)
		}
		sb.WriteString(".")
	} else if r.Kind == "function" || r.Kind == "method" {
		sb.WriteString(" It has no detected callers.")
	}

	// Fan-out
	if r.CalleeCount > 0 {
		fmt.Fprintf(&sb, " It delegates to %d callee(s)", r.CalleeCount)
		if r.CrossPkgCalleeCount > 0 {
			fmt.Fprintf(&sb, " (%d cross-package)", r.CrossPkgCalleeCount)
		}
		sb.WriteString(".")
	}

	// SQL
	if r.SQLDirect > 0 || r.SQLCallees > 0 {
		fmt.Fprintf(&sb, " It touches SQL: %d direct", r.SQLDirect)
		if r.SQLCallees > 0 {
			fmt.Fprintf(&sb, ", %d via direct callees", r.SQLCallees)
		}
		sb.WriteString(".")
	}

	// Env
	if len(r.EnvKeys) > 0 {
		fmt.Fprintf(&sb, " Reads env: %s.", strings.Join(r.EnvKeys, ", "))
	}

	// HTTP routes
	if r.IsRouteHandler {
		fmt.Fprintf(&sb, " Registered as HTTP handler: %s.", strings.Join(r.Routes, ", "))
	}

	// Complexity
	if r.Complexity > 0 {
		fmt.Fprintf(&sb, " Cyclomatic complexity: %d (%s).", r.Complexity, r.ComplexityLabel)
	}

	// Concurrency
	if len(r.ConcurrencyKinds) > 0 {
		fmt.Fprintf(&sb, " Uses concurrency: %s.", strings.Join(r.ConcurrencyKinds, ", "))
	}

	// Test coverage
	if r.HasDirectTests {
		fmt.Fprintf(&sb, " Has %d direct test(s).", r.DirectTestCount)
	} else if r.Kind == "function" || r.Kind == "method" {
		sb.WriteString(" No direct test coverage.")
	}

	// Interfaces
	if len(r.SatisfiedInterfaces) > 0 {
		fmt.Fprintf(&sb, " Satisfies interface(s): %s.", strings.Join(r.SatisfiedInterfaces, ", "))
	}

	// Struct-specific
	if r.Kind == "struct" {
		fmt.Fprintf(&sb, " %d field(s), %d method(s)", r.FieldCount, r.MethodCount)
		if len(r.ConstructorNames) > 0 {
			fmt.Fprintf(&sb, ", constructors: %s", strings.Join(r.ConstructorNames, ", "))
		}
		sb.WriteString(".")
	}

	// Arity
	if r.Arity > 4 {
		fmt.Fprintf(&sb, " High arity: %d parameters.", r.Arity)
	}

	// Architectural role — the opinionated conclusion
	fmt.Fprintf(&sb, "\n\nARCHITECTURAL ROLE: %s.", titleCase(r.Role))

	return sb.String()
}

// titleCase capitalises the first letter of each word in s.
// Used in place of the deprecated strings.Title.
func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}
