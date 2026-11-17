package search

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// FileSelectorLimitDescription names legacy queries whose evidence is lexical,
// rather than bound to the declaration selected by a file-qualified spelling.
const FileSelectorLimitDescription = "File-qualified selectors are not supported by embeds, constructors, literals, returnusage, mutate, path or endpoint. These queries retain lexical relationship records that cannot reliably bind a file-qualified declaration; use their documented name/type/field/route forms. Usages supports file-qualified variables/constants but reports a named limit for types, whose reference records are lexical."

// FileSelectorLimit refuses unsupported selectors rather than returning an
// apparently complete empty answer or conflating same-named declarations.
func FileSelectorLimit(command string, values ...string) error {
	switch command {
	case "embeds", "constructors", "literals", "returnusage", "mutate", "path", "endpoint":
		for _, value := range values {
			if strings.Contains(value, ".go:") {
				return fmt.Errorf("%s file-selector limit: %s", command, FileSelectorLimitDescription)
			}
		}
	}
	return nil
}

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
		// file.go:Name selects a function, type, variable or constant;
		// file.go:Receiver.Method selects a method. A bare name never matches
		// a method, so the selector fileSelector prints for a function stays
		// unique when the same file declares a method of the same name.
		if s.Receiver == "" {
			return location && name == s.Name, true
		}
		return location && name == strings.Trim(s.Receiver, "(*)")+"."+s.Name, true
	}
	if strings.Contains(query, "/") && !strings.Contains(query, "::") {
		pkg, member, ok := strings.Cut(s.ID, "::")
		normalize := receiverNormalizer.Replace
		return ok && normalize(query) == normalize(pkg+"."+member), true
	}
	return false, false
}

// FileSelectorHint explains a file selector that names a method without its
// receiver, listing the receiver-qualified selectors that would match.
func FileSelectorHint(g *graph.Graph, query string) string {
	file, name, ok := strings.Cut(strings.TrimSpace(query), ".go:")
	if !ok || g == nil || strings.Contains(name, ".") {
		return ""
	}
	var selectors []string
	for _, symbol := range g.Symbols {
		if symbol.Receiver == "" || symbol.Name != name {
			continue
		}
		pkg, _, _ := strings.Cut(symbol.ID, "::")
		if file+".go" == symbol.File || file+".go" == pkg+"/"+path.Base(symbol.File) {
			selectors = append(selectors, fileSelector(symbol))
		}
	}
	if len(selectors) == 0 {
		return ""
	}
	sort.Strings(selectors)
	return "methods need their receiver: " + strings.Join(selectors, ", ")
}
