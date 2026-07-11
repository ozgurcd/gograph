package search

import (
	"fmt"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// ExplainResult holds a structured architectural summary of a symbol.
// It composes data from multiple graph queries into a single narrative
// designed for prompt injection or onboarding documentation.
type ExplainResult struct {
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
		return nil
	}

	res := &ExplainResult{
		Symbol:  sym.ID,
		Kind:    string(sym.Kind),
		Package: sym.PackageName,
		File:    sym.File,
		Line:    sym.Line,
		Doc:     sym.Doc,
		Arity:   sym.Arity,
	}

	displayName := sym.Name
	if sym.Receiver != "" {
		displayName = "(" + sym.Receiver + ")." + sym.Name
	}

	// Callers: split into production vs test
	allCallers := Callers(g, term, true, false)
	prodOnly := Callers(g, term, false, false) // excludes test files
	prodSet := make(map[string]bool)
	for _, c := range prodOnly {
		prodSet[c.Name] = true
	}
	for _, c := range allCallers {
		if prodSet[c.Name] {
			res.ProdCallers = append(res.ProdCallers, c.Name)
		} else {
			res.TestCallers = append(res.TestCallers, c.Name)
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
		cxResults := Complexity(g, displayName)
		for _, cx := range cxResults {
			if cx.Symbol == displayName {
				res.Complexity = cx.Score
				res.ComplexityLabel = cx.Label
				break
			}
		}
	}

	// SQL: direct (in this function's body)
	for _, sql := range g.SQLs {
		if matchesSymbol(sql.Function, sym) {
			res.SQLDirect++
		}
	}

	// SQL: via direct callees (1-level deep)
	directCallees := make(map[string]bool)
	for _, call := range g.Calls {
		if matchesSymbol(call.CallerName, sym) {
			directCallees[call.CalleeRaw] = true
		}
	}
	for _, sql := range g.SQLs {
		if directCallees[sql.Function] {
			res.SQLCallees++
		}
	}

	// Env reads (direct only)
	envSet := make(map[string]bool)
	for _, env := range g.EnvReads {
		if matchesSymbol(env.Function, sym) && !envSet[env.Key] {
			envSet[env.Key] = true
			res.EnvKeys = append(res.EnvKeys, env.Key)
		}
	}

	// Routes
	for _, route := range g.Routes {
		if matchesSymbol(route.Handler, sym) {
			res.IsRouteHandler = true
			res.Routes = append(res.Routes, fmt.Sprintf("%s %s", route.Method, route.Path))
		}
	}

	// Concurrency (direct)
	kindSet := make(map[string]bool)
	for _, c := range g.Concurrency {
		if matchesSymbol(c.Function, sym) && !kindSet[c.Kind] {
			kindSet[c.Kind] = true
			res.ConcurrencyKinds = append(res.ConcurrencyKinds, c.Kind)
		}
	}

	// Test coverage
	for _, te := range g.TestEdges {
		if matchesSymbol(te.Target, sym) {
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
		} else if impl.ConcreteID == "" && sym.Kind == graph.KindStruct && strings.EqualFold(impl.Concrete, sym.Name) {
			match = true
		} else if impl.ConcreteID == "" && sym.Receiver != "" && strings.EqualFold(impl.Concrete, sym.Receiver) {
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
			if s.Kind == graph.KindMethod && s.Receiver == sym.Name {
				res.MethodCount++
			}
		}
		ctors := Constructors(g, sym.Name)
		for _, c := range ctors {
			res.ConstructorNames = append(res.ConstructorNames, c.Name)
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
	tl := strings.ToLower(term)

	// Exact ID match first
	for i := range g.Symbols {
		if g.Symbols[i].ID == term {
			return &g.Symbols[i]
		}
	}
	// Exact name match
	for i := range g.Symbols {
		if g.Symbols[i].Name == term {
			return &g.Symbols[i]
		}
	}
	// Structured/qualified match
	for i := range g.Symbols {
		if MatchSymbol(g.Symbols[i], term) {
			return &g.Symbols[i]
		}
	}
	// Case-insensitive substring
	for i := range g.Symbols {
		if strings.Contains(strings.ToLower(g.Symbols[i].ID), tl) {
			return &g.Symbols[i]
		}
	}
	return nil
}

// matchesSymbol checks if a function name in a graph edge matches the given symbol.
func matchesSymbol(funcName string, sym *graph.SymbolNode) bool {
	if funcName == sym.Name || funcName == sym.ID {
		return true
	}
	if sym.Receiver != "" {
		return funcName == sym.Receiver+"."+sym.Name
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
	var sb strings.Builder

	// Opening sentence: identity
	fmt.Fprintf(&sb, "%s is a %s", displayName, r.Kind)
	if r.Package != "" {
		fmt.Fprintf(&sb, " in package %s", r.Package)
	}
	fmt.Fprintf(&sb, " (%s:%d).", r.File, r.Line)

	// Fan-in
	if r.CallerCount > 0 {
		fmt.Fprintf(&sb, " It is called by %d production caller(s)", len(r.ProdCallers))
		if len(r.TestCallers) > 0 {
			fmt.Fprintf(&sb, " and %d test caller(s)", len(r.TestCallers))
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
