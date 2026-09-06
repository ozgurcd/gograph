package search

import (
	"path"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// Direct facts carry an enclosing spelling and source location, not a resolved
// identity. Both are required: names alone conflate packages and receivers.
func explanationFactMatches(name, file string, line int, sym *graph.SymbolNode) bool {
	if !matchesSymbol(name, sym) || path.Clean(file) != path.Clean(sym.File) {
		return false
	}
	return sym.EndLine == 0 || line >= sym.Line && line <= sym.EndLine
}

func explanationPackage(sym graph.SymbolNode) string {
	if i := strings.Index(sym.ID, "::"); i >= 0 {
		return sym.ID[:i]
	}
	return path.Dir(sym.File)
}

// Resolved targets are authoritative. For legacy spellings only report a
// unique, lexically scoped candidate; arbitrary receiver variables and
// ambiguous names cannot establish an explanation fact.
func explanationTarget(g *graph.Graph, resolved, raw, file string) *graph.SymbolNode {
	if resolved != "" {
		raw = resolved
	}
	var found *graph.SymbolNode
	for _, symbol := range g.Symbols {
		if resolved != "" || isFullyQualifiedID(raw) {
			if symbol.ID != raw {
				continue
			}
		} else {
			qualifiedFunction := file != "" && symbol.Kind == graph.KindFunction && strings.HasSuffix(raw, "."+symbol.Name)
			if !matchesSymbol(raw, &symbol) && !qualifiedFunction {
				continue
			}
			if file != "" && !explanationReferenceScope(g, raw, file, symbol) {
				continue
			}
		}
		if found != nil {
			return nil
		}
		copy := symbol
		found = &copy
	}
	return found
}

func explanationReferenceScope(g *graph.Graph, raw, file string, symbol graph.SymbolNode) bool {
	if path.Dir(file) == path.Dir(symbol.File) && matchesSymbol(raw, &symbol) {
		// A sibling external-test package is not the local package.
		for _, f := range g.Files {
			if f.Path == file && f.PackageName != "" && f.PackageName != symbol.PackageName {
				return false
			}
		}
		return true
	}
	qualifier, _, qualified := strings.Cut(raw, ".")
	if !qualified {
		return false
	}
	for _, imp := range g.Imports {
		if imp.FromFile != file || imp.ImportPath != explanationPackage(symbol) {
			continue
		}
		alias := imp.Alias
		if alias == "" {
			alias = symbol.PackageName
		}
		if alias == qualifier {
			return true
		}
	}
	return false
}

func explanationUniqueType(g *graph.Graph, symbol *graph.SymbolNode) bool {
	count := 0
	for _, candidate := range g.Symbols {
		if candidate.Kind == graph.KindStruct && candidate.Name == symbol.Name {
			count++
		}
	}
	return count == 1
}
