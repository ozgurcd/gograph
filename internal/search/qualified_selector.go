package search

import (
	"fmt"
	"path"
	"path/filepath"
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

// fileSelectorNameNormalizer turns "(*Recv).Method" and "(Recv).Method"
// into the "Recv.Method" form fileSelector prints.
var fileSelectorNameNormalizer = strings.NewReplacer("(", "", ")", "", "*", "", " ", "")

// parseFileSelector splits "dir/file.go:Name" into the file and the
// normalized name.
func parseFileSelector(query string) (string, string, bool) {
	file, name, ok := strings.Cut(strings.TrimSpace(query), ".go:")
	if !ok {
		return "", "", false
	}
	return strings.ReplaceAll(file, "\\", "/") + ".go", fileSelectorNameNormalizer.Replace(name), true
}

// fileSelectorLocation reports whether a selector's file names the symbol's
// file: its repository-relative path, its package import path plus file
// name, or, for a selector with no directory, its file name alone. A file
// name shared by several packages selects each; callers report the
// ambiguity with the full selectors.
func fileSelectorLocation(file string, s graph.SymbolNode) bool {
	if file == s.File {
		return true
	}
	pkg, _, _ := strings.Cut(s.ID, "::")
	if file == pkg+"/"+path.Base(s.File) {
		return true
	}
	return !strings.Contains(file, "/") && file == path.Base(s.File)
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
	if file, name, ok := parseFileSelector(query); ok {
		// file.go:Name selects a function, type, variable or constant;
		// file.go:Receiver.Method selects a method. A bare name never matches
		// a method, so the selector fileSelector prints for a function stays
		// unique when the same file declares a method of the same name.
		if !fileSelectorLocation(file, s) {
			return false, true
		}
		if s.Receiver == "" {
			return name == s.Name, true
		}
		return name == strings.Trim(s.Receiver, "(*)")+"."+s.Name, true
	}
	if strings.Contains(query, "/") || strings.Contains(query, "::") {
		return packagePathSymbolMatch(s, query), true
	}
	return false, false
}

// packagePathSymbolMatch matches path/to/pkg.Member, path/to/pkg::Member and
// pkg::Member selectors. The package part may be the full import path, a "/"-bounded
// suffix of it (the repository-relative path internal/app), or the symbol's
// repository-relative directory.
func packagePathSymbolMatch(s graph.SymbolNode, query string) bool {
	pkg, member, ok := strings.Cut(s.ID, "::")
	if !ok {
		return false
	}
	normalize := func(value string) string {
		return strings.Replace(receiverNormalizer.Replace(value), "::", ".", 1)
	}
	q := normalize(strings.TrimPrefix(strings.TrimSpace(query), "./"))
	full := normalize(pkg + "." + member)
	if q == full || strings.HasSuffix(full, "/"+q) {
		return true
	}
	return q == normalize(filepath.ToSlash(filepath.Dir(s.File))+"."+member)
}

// FileSelectorHint explains a file selector that names a method without its
// receiver, listing the receiver-qualified selectors that would match.
func FileSelectorHint(g *graph.Graph, query string) string {
	file, name, ok := parseFileSelector(query)
	if !ok || g == nil || name == "" {
		return ""
	}
	fileKnown := false
	var methods, sameName []string
	for _, symbol := range g.Symbols {
		if matched, _ := qualifiedSymbolMatch(symbol, query); matched {
			return "" // the selector resolves; an empty answer is real
		}
		inFile := fileSelectorLocation(file, symbol)
		fileKnown = fileKnown || inFile
		shortName := name
		if position := strings.LastIndex(name, "."); position >= 0 {
			shortName = name[position+1:]
		}
		if symbol.Name != shortName {
			continue
		}
		if inFile && symbol.Receiver != "" && !strings.Contains(name, ".") {
			methods = append(methods, fileSelector(symbol))
		} else {
			sameName = append(sameName, fileSelector(symbol))
		}
	}
	limit := func(selectors []string) string {
		sort.Strings(selectors)
		if len(selectors) > 5 {
			selectors = append(selectors[:5], fmt.Sprintf("and %d more", len(selectors)-5))
		}
		return strings.Join(selectors, ", ")
	}
	switch {
	case len(methods) > 0:
		return "methods need their receiver: " + limit(methods)
	case len(sameName) > 0:
		return "no " + name + " in " + file + "; selectors for that name: " + limit(sameName)
	case !fileKnown:
		return "no indexed file matches " + file + "; use the repository-relative path (gograph query <name> prints it)"
	}
	return ""
}
