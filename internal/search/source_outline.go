package search

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

// FileOutline lists one indexed file's declarations with their line ranges, so
// a caller can pick the region to read with --lines instead of guessing. It
// reads only that file, to refuse an outline whose lines are stale.
func FileOutline(g *graph.Graph, rootDir, file string) (string, error) {
	if g == nil {
		return "", fmt.Errorf("graph is unavailable")
	}
	path, ok := indexedGoFile(g, file)
	if !ok {
		return "", fmt.Errorf("file %q is not indexed", file)
	}
	reader, err := sourcefs.Open(rootDir)
	if err != nil {
		return "", fmt.Errorf("open repository source root: %w", err)
	}
	defer func() { _ = reader.Close() }()
	data, err := reader.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read source file %s: %w", path, err)
	}
	if recorded := indexedFileDigest(g, path); recorded != "" && recorded != graph.SourceDigest(data) {
		return "", fmt.Errorf("%s changed after the graph was built, so its indexed lines are stale; run gograph build . and retry", path)
	}
	lineCount := strings.Count(string(data), "\n")
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lineCount++
	}

	var symbols []graph.SymbolNode
	packageName := ""
	for _, f := range g.Files {
		if f.Path == path {
			packageName = f.PackageName
			break
		}
	}
	for _, symbol := range g.Symbols {
		if symbol.File == path {
			symbols = append(symbols, symbol)
		}
	}
	sort.SliceStable(symbols, func(i, j int) bool {
		if symbols[i].Line != symbols[j].Line {
			return symbols[i].Line < symbols[j].Line
		}
		return symbols[i].Name < symbols[j].Name
	})

	var b strings.Builder
	fmt.Fprintf(&b, "// %s: package %s, %d lines, %d declarations. Outline only; read a region with: gograph source %s --lines START-END\n", path, packageName, lineCount, len(symbols), path)
	if len(symbols) > 0 && symbols[0].Line > 1 {
		fmt.Fprintf(&b, "%-11s package clause and imports\n", fmt.Sprintf("1-%d", symbols[0].Line-1))
	}
	for _, symbol := range symbols {
		end := symbol.EndLine
		if end < symbol.Line {
			end = symbol.Line
		}
		fmt.Fprintf(&b, "%-11s %s\n", fmt.Sprintf("%d-%d", symbol.Line, end), outlineLabel(symbol))
	}
	out := strings.TrimRight(b.String(), "\n")
	if len(out) > maxSourceLinesBytes {
		return "", fmt.Errorf("outline of %s is %d bytes, exceeding the %d byte budget; use gograph skeleton %s or --lines START-END", path, len(out), maxSourceLinesBytes, path)
	}
	return out, nil
}

func outlineLabel(symbol graph.SymbolNode) string {
	switch {
	case symbol.Signature != "":
		return symbol.Signature
	case symbol.Kind == graph.KindStruct && len(symbol.StructFields) > 0:
		names := make([]string, 0, len(symbol.StructFields))
		for _, field := range symbol.StructFields {
			names = append(names, field.Name)
		}
		return fmt.Sprintf("struct %s {%s}", symbol.Name, strings.Join(names, ", "))
	default:
		return fmt.Sprintf("%s %s", symbol.Kind, symbol.Name)
	}
}

// fieldTarget resolves Type.Field (any selector spelling of the struct, then
// .Field) to the field's own declaration lines, as a synthetic symbol whose ID
// is the struct ID plus ".Field". ok is false when term does not name a field
// of an indexed struct, so the caller keeps its own not-found answer.
func fieldTarget(g *graph.Graph, rootDir, term string) (graph.SymbolNode, bool, error) {
	term = strings.TrimSpace(term)
	dot := strings.LastIndex(term, ".")
	if g == nil || dot <= 0 || dot == len(term)-1 || strings.HasSuffix(term, ".go") {
		return graph.SymbolNode{}, false, nil
	}
	typeSelector, field := term[:dot], term[dot+1:]
	if !token.IsIdentifier(field) {
		return graph.SymbolNode{}, false, nil
	}
	var structs []graph.SymbolNode
	for _, candidate := range FindSymbols(g, typeSelector) {
		if candidate.Kind != graph.KindStruct || !matchSymbolExact(candidate, typeSelector) {
			continue
		}
		for _, f := range candidate.StructFields {
			if f.Name == field {
				structs = append(structs, candidate)
				break
			}
		}
	}
	if len(structs) > 1 {
		var sameCase []graph.SymbolNode
		for _, candidate := range structs {
			if strings.HasSuffix(typeSelector, candidate.Name) {
				sameCase = append(sameCase, candidate)
			}
		}
		if len(sameCase) > 0 {
			structs = sameCase
		}
	}
	switch len(structs) {
	case 0:
		return graph.SymbolNode{}, false, nil
	case 1:
	default:
		candidates := make([]string, 0, len(structs))
		for _, s := range structs {
			candidates = append(candidates, fmt.Sprintf("%s.%s (%s:%d; selector %s.%s)", s.ID, field, s.File, s.Line, fileSelector(s), field))
		}
		sort.Strings(candidates)
		return graph.SymbolNode{}, true, fmt.Errorf("field %q is ambiguous; select a candidate: %s", term, strings.Join(candidates, "; "))
	}
	owner := structs[0]
	reader, err := sourcefs.Open(rootDir)
	if err != nil {
		return graph.SymbolNode{}, true, fmt.Errorf("open repository source root: %w", err)
	}
	defer func() { _ = reader.Close() }()
	data, err := reader.ReadFile(owner.File)
	if err != nil {
		return graph.SymbolNode{}, true, fmt.Errorf("read source file %s: %w", owner.File, err)
	}
	if recorded := indexedFileDigest(g, owner.File); recorded != "" && recorded != graph.SourceDigest(data) {
		return graph.SymbolNode{}, true, fmt.Errorf("%s changed after the graph was built, so its indexed lines are stale; run gograph build . and retry", owner.File)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, owner.File, data, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil && file == nil {
		return graph.SymbolNode{}, true, fmt.Errorf("parse %s: %w", owner.File, err)
	}
	first, last := 0, 0
	ast.Inspect(file, func(n ast.Node) bool {
		if first != 0 {
			return false
		}
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name.Name != owner.Name {
			return true
		}
		st, ok := spec.Type.(*ast.StructType)
		if !ok || st.Fields == nil {
			return false
		}
		if line := fset.Position(spec.Pos()).Line; line < owner.Line || (owner.EndLine > 0 && line > owner.EndLine) {
			return false
		}
		for _, f := range st.Fields.List {
			if !fieldDeclares(f, field) {
				continue
			}
			start, end := f.Pos(), f.End()
			if f.Doc != nil {
				start = f.Doc.Pos()
			}
			if f.Comment != nil {
				end = f.Comment.End()
			}
			first, last = fset.Position(start).Line, fset.Position(end).Line
			return false
		}
		return false
	})
	if first == 0 {
		return graph.SymbolNode{}, true, fmt.Errorf("field %s of %s was not found in %s; run gograph build . and retry", field, owner.ID, owner.File)
	}
	return graph.SymbolNode{
		ID:          owner.ID + "." + field,
		Kind:        graph.SymbolKind("field"),
		Name:        field,
		Receiver:    owner.Name,
		PackageName: owner.PackageName,
		File:        owner.File,
		Line:        first,
		EndLine:     last,
	}, true, nil
}

func fieldDeclares(f *ast.Field, name string) bool {
	if len(f.Names) == 0 {
		return embeddedFieldName(f.Type) == name
	}
	for _, ident := range f.Names {
		if ident.Name == name {
			return true
		}
	}
	return false
}

func embeddedFieldName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return embeddedFieldName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.IndexExpr:
		return embeddedFieldName(t.X)
	case *ast.IndexListExpr:
		return embeddedFieldName(t.X)
	}
	return ""
}
