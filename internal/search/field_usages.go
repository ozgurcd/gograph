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

// fieldUsages answers usages for a struct field: "Type.Field",
// "pkg.Type.Field", or a bare field name that is not also a type name. ok is
// false when the target does not name a declared struct field, so type
// usages keep their existing behavior.
func fieldUsages(g *graph.Graph, target string) ([]Result, bool) {
	typePart, field := "", target
	if dot := strings.LastIndex(target, "."); dot >= 0 {
		typePart, field = target[:dot], target[dot+1:]
	}
	if field == "" || strings.ContainsAny(field, "/:*()[] ") {
		return nil, false
	}
	typeName, packageName := typePart, ""
	if dot := strings.LastIndex(typePart, "."); dot >= 0 {
		packageName, typeName = typePart[:dot], typePart[dot+1:]
	}
	if typePart == "" {
		for _, symbol := range g.Symbols {
			if symbol.Name == field && (symbol.Kind == graph.KindStruct || symbol.Kind == graph.KindInterface || symbol.Kind == graph.KindType) {
				return nil, false
			}
		}
	}

	owners := make(map[string]graph.SymbolNode)
	var declarations []Result
	for _, symbol := range g.Symbols {
		if symbol.Kind != graph.KindStruct || (typeName != "" && symbol.Name != typeName) {
			continue
		}
		if packageName != "" && !strings.EqualFold(symbol.PackageName, packageName) && !strings.HasSuffix(strings.SplitN(symbol.ID, "::", 2)[0], "/"+packageName) {
			continue
		}
		for _, declared := range symbol.StructFields {
			if declared.Name != field {
				continue
			}
			owners[symbol.ID] = symbol
			declarations = append(declarations, Result{
				Kind: "field", Name: symbol.Name + "." + field, StableID: symbol.ID,
				File: symbol.File, Line: symbol.Line, Detail: "declared field of type " + declared.Type, Score: 10,
			})
		}
	}
	if len(owners) == 0 {
		return nil, false
	}

	results := append([]Result(nil), declarations...)
	if len(g.FieldUses) > 0 {
		for _, use := range g.FieldUses {
			if use.Field != field {
				continue
			}
			if _, ok := owners[use.Package+"::"+use.TypeName]; !ok {
				continue
			}
			detail := use.Access
			if use.Function != "" {
				detail += " in " + use.Function
			}
			results = append(results, Result{
				Kind: "field_" + use.Access, Name: use.TypeName + "." + use.Field,
				File: use.File, Line: use.Line, Detail: detail, Score: 10,
			})
		}
		return results, true
	}

	possible, diagnostic := possibleFieldUses(g, field)
	limit := "field uses are indexed by precise builds; this graph has none, so these rows match the field name only and may include other types' fields. Run gograph build . --precise for type-checked field uses."
	if diagnostic != "" {
		limit += " " + diagnostic
	}
	results = append(results, Result{Kind: "limit", Name: target, Detail: limit})
	return append(results, possible...), true
}

// possibleFieldUses scans indexed source for selectors and keyed literal
// elements with the field's name. It is a name-only fallback for graphs
// without typed field uses.
func possibleFieldUses(g *graph.Graph, field string) ([]Result, string) {
	reader, err := sourcefs.Open(g.Root)
	if err != nil {
		return nil, "Source could not be opened: " + err.Error()
	}
	defer func() { _ = reader.Close() }()
	functionsByFile := make(map[string][]graph.SymbolNode)
	for _, symbol := range g.Symbols {
		if symbol.Kind == graph.KindFunction || symbol.Kind == graph.KindMethod {
			functionsByFile[symbol.File] = append(functionsByFile[symbol.File], symbol)
		}
	}
	var results []Result
	unreadable := 0
	for _, file := range g.Files {
		if !strings.HasSuffix(file.Path, ".go") {
			continue
		}
		data, err := reader.ReadRegularFileLimit(file.Path, maxStringSourceBytes)
		if err != nil {
			unreadable++
			continue
		}
		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, file.Path, data, parser.SkipObjectResolution)
		if err != nil {
			unreadable++
			continue
		}
		record := func(pos token.Pos, how string) {
			line := fileSet.Position(pos).Line
			detail := "possible " + how
			if function := enclosingFunction(functionsByFile[file.Path], line); function != "" {
				detail += " in " + function
			}
			results = append(results, Result{Kind: "possible_field_use", Name: field, File: file.Path, Line: line, Detail: detail + " (name-only AST match)", Score: 1})
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.SelectorExpr:
				if n.Sel.Name == field {
					record(n.Sel.Pos(), "selector")
				}
			case *ast.CompositeLit:
				for _, element := range n.Elts {
					if pair, ok := element.(*ast.KeyValueExpr); ok {
						if key, ok := pair.Key.(*ast.Ident); ok && key.Name == field {
							record(key.Pos(), "literal key")
						}
					}
				}
			}
			return true
		})
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].File != results[j].File {
			return results[i].File < results[j].File
		}
		return results[i].Line < results[j].Line
	})
	if unreadable > 0 {
		return results, fmt.Sprintf("%d indexed files could not be read or parsed and were not searched.", unreadable)
	}
	return results, ""
}
