package search

import (
	"fmt"
	"path"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

func validateQualifiedSelector(query string) error {
	if file, name, ok := strings.Cut(query, ".go:"); ok && (file == "" || name == "" || strings.ContainsAny(name, ":/ ")) {
		return fmt.Errorf("malformed selector %q: use path/to/file.go:Name or path/to/file.go:Receiver.Method", query)
	}
	return nil
}

func fileSelector(s graph.SymbolNode) string {
	name := s.Name
	if s.Receiver != "" {
		name = strings.Trim(s.Receiver, "(*)") + "." + name
	}
	return s.File + ":" + name
}

func qualifiedTarget(g *graph.Graph, term string) (graph.SymbolNode, bool) {
	_, qualified := qualifiedSymbolMatch(graph.SymbolNode{}, term)
	if !qualified {
		return graph.SymbolNode{}, false
	}
	var target graph.SymbolNode
	found := false
	for _, s := range g.Symbols {
		if match, _ := qualifiedSymbolMatch(s, term); match {
			if found {
				return graph.SymbolNode{}, false
			}
			target, found = s, true
		}
	}
	return target, found
}

// qualifiedSymbolMatch matches indexed locations, never a filesystem read.
func qualifiedSymbolMatch(s graph.SymbolNode, query string) (bool, bool) {
	if file, name, ok := strings.Cut(query, ".go:"); ok {
		file += ".go"
		pkg, _, _ := strings.Cut(s.ID, "::")
		location := file == s.File || file == pkg+"/"+path.Base(s.File)
		member := s.Name
		if s.Receiver != "" {
			member = strings.TrimPrefix(s.Receiver, "*") + "." + s.Name
		}
		return location && (name == s.Name || name == member), true
	}
	if strings.Contains(query, "/") && !strings.Contains(query, "::") {
		pkg, member, ok := strings.Cut(s.ID, "::")
		normalize := strings.NewReplacer("(", "", ")", "", "*", "").Replace
		return ok && normalize(query) == normalize(pkg+"."+member), true
	}
	return false, false
}
