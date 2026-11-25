package search

import (
	"path"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// callTargetMatcher decides whether a call edge calls the symbols a callers
// query resolved to. Edges with a CalleeSymbolID (precise graphs) match by
// identity. Edges without one (every edge of an AST graph) match by the
// called identifier and its qualifier, so exact selectors such as
// "dir/file.go:Name" or a symbol ID find callers in AST graphs too. A query
// that names no repository function or method matches whole identifiers of
// the raw callee text, never a prefix of a longer name.
type callTargetMatcher struct {
	query      string
	exact      bool
	targetIDs  map[string]bool
	targets    []graph.SymbolNode
	qualifiers map[string]map[string]string // file -> import path -> qualifier
	receivers  *receiverTypeIndex
}

func newCallTargetMatcher(g *graph.Graph, query string, exact bool, resolved map[string]bool, symbols []graph.SymbolNode) *callTargetMatcher {
	matcher := &callTargetMatcher{query: strings.TrimSpace(query), exact: exact, targetIDs: make(map[string]bool)}
	for id := range resolved {
		matcher.targetIDs[id] = true
	}
	for _, symbol := range symbols {
		if symbol.Kind != graph.KindFunction && symbol.Kind != graph.KindMethod {
			continue
		}
		matcher.targetIDs[symbol.ID] = true
		matcher.targets = append(matcher.targets, symbol)
	}
	if len(matcher.targets) > 0 {
		matcher.indexImports(g)
	}
	return matcher
}

// newSymbolCallMatcher matches calls and test references to one symbol.
func newSymbolCallMatcher(g *graph.Graph, target graph.SymbolNode) *callTargetMatcher {
	matcher := &callTargetMatcher{query: target.ID, exact: true, targetIDs: map[string]bool{target.ID: true}, targets: []graph.SymbolNode{target}}
	matcher.indexImports(g)
	return matcher
}

func (matcher *callTargetMatcher) indexImports(g *graph.Graph) {
	matcher.receivers = newReceiverTypeIndex(g)
	matcher.qualifiers = make(map[string]map[string]string)
	{
		for _, edge := range g.Imports {
			qualifier := edge.Alias
			if qualifier == "_" {
				continue
			}
			if qualifier == "" {
				qualifier = path.Base(edge.ImportPath)
			}
			byPath := matcher.qualifiers[edge.FromFile]
			if byPath == nil {
				byPath = make(map[string]string)
				matcher.qualifiers[edge.FromFile] = byPath
			}
			byPath[edge.ImportPath] = qualifier
		}
	}
}

// callTargets returns the symbols a callers query names. Exactly matching
// symbols win, so "pkg.Name" never also selects pkg.NameSuffix; the fuzzy
// MatchSymbol lookup is used only when nothing matches exactly.
func callTargets(g *graph.Graph, query string, resolved map[string]bool, interfaceMethodQuery bool) (map[string]bool, []graph.SymbolNode) {
	if interfaceMethodQuery {
		return resolved, nil
	}
	var exact []graph.SymbolNode
	for _, symbol := range g.Symbols {
		if matchSymbolExact(symbol, query) {
			exact = append(exact, symbol)
		}
	}
	if len(exact) == 0 {
		return resolved, FindSymbols(g, query)
	}
	seeds := make(map[string]bool, len(exact))
	for _, symbol := range exact {
		seeds[symbol.ID] = true
	}
	return expandForwardingTargets(g, seeds), exact
}

func symbolPackagePath(id string) string {
	pkg, _, _ := strings.Cut(id, "::")
	return pkg
}

// splitRawCallee returns the qualifier and called identifier of raw callee
// text such as "pkg.Name", "recv.field.Method" or "Name[T]".
func splitRawCallee(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if position := strings.Index(raw, "["); position >= 0 {
		raw = raw[:position]
	}
	raw = strings.ReplaceAll(raw, "::", ".")
	if position := strings.LastIndex(raw, "."); position >= 0 {
		return raw[:position], raw[position+1:]
	}
	return "", raw
}

func (matcher *callTargetMatcher) matches(edge graph.CallEdge, callerID string) bool {
	if edge.CalleeSymbolID != "" && matcher.targetIDs[edge.CalleeSymbolID] {
		return true
	}
	if len(matcher.targets) > 0 {
		if edge.CalleeSymbolID != "" {
			return false
		}
		for _, target := range matcher.targets {
			if matcher.rawCallMayTarget(edge, callerID, target) {
				return true
			}
		}
		return false
	}
	return rawCalleeMatchesName(edge.CalleeRaw, matcher.query, matcher.exact)
}

// rawCallMayTarget reports whether an unresolved call can call target: the
// called identifier must equal the target's name; a function must be called
// unqualified from its own package or through the qualifier the calling file
// imports its package under; a method must be called through some receiver
// expression whose lexically known type, if any, can reach the target.
func (matcher *callTargetMatcher) rawCallMayTarget(edge graph.CallEdge, callerID string, target graph.SymbolNode) bool {
	qualifier, name := splitRawCallee(edge.CalleeRaw)
	if name != target.Name {
		return false
	}
	if target.Kind == graph.KindMethod {
		// "pkg.Name" through an imported package name is a function call,
		// not a method call; "pkg.Var.Method" stays a candidate.
		if qualifier == "" || matcher.isImportQualifier(edge.File, qualifier) {
			return false
		}
		return !matcher.receivers.excludes(qualifier, edge.QualifierType, edge.File, target)
	}
	if !strings.Contains(target.ID, "::") {
		// Legacy IDs carry no package, so only the name can be checked.
		return true
	}
	targetPackage := symbolPackagePath(target.ID)
	imported, ok := matcher.qualifiers[edge.File][targetPackage]
	if qualifier == "" {
		// Same package, or a dot import of the target's package. Without a
		// caller identity the package is unknown, so the call stays a
		// candidate.
		return !strings.Contains(callerID, "::") || symbolPackagePath(callerID) == targetPackage || (ok && imported == ".")
	}
	return ok && qualifier == imported
}

func (matcher *callTargetMatcher) isImportQualifier(file, qualifier string) bool {
	for _, imported := range matcher.qualifiers[file] {
		if imported == qualifier {
			return true
		}
	}
	return false
}

// rawCalleeMatchesName matches a name that resolves to no repository
// function: the called identifiers must be equal (case-insensitively), and a
// qualified query must match the end of the call's qualifier.
func rawCalleeMatchesName(raw, query string, exact bool) bool {
	raw, query = strings.ToLower(strings.TrimSpace(raw)), strings.ToLower(strings.TrimSpace(query))
	if raw == "" || query == "" {
		return false
	}
	if exact {
		return raw == query
	}
	rawQualifier, rawName := splitRawCallee(selectorNormalizer.Replace(raw))
	queryQualifier, queryName := splitRawCallee(selectorNormalizer.Replace(query))
	if rawName != queryName {
		return false
	}
	if queryQualifier == "" {
		return true
	}
	return rawQualifier == queryQualifier || strings.HasSuffix(rawQualifier, "."+queryQualifier) || strings.HasSuffix(rawQualifier, "/"+queryQualifier)
}
