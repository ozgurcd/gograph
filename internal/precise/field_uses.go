package precise

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"

	"github.com/ozgurcd/gograph/internal/graph"
	"golang.org/x/tools/go/packages"
)

// collectFieldUses records every type-checked struct-field selector and keyed
// composite-literal element in the graph's selected files. It replaces any
// previous field uses, because precise enrichment recomputes them each run.
func collectFieldUses(pkgs []*packages.Package, g *graph.Graph) {
	selected := make(map[string]bool, len(g.Files))
	for _, file := range g.Files {
		selected[filepath.ToSlash(file.Path)] = true
	}
	base := absoluteSourcePath(g.Root, ".")
	seen := make(map[graph.FieldUse]bool)
	var uses []graph.FieldUse
	add := func(use graph.FieldUse) {
		if use.TypeName == "" || seen[use] {
			return
		}
		seen[use] = true
		uses = append(uses, use)
	}
	for _, pkg := range pkgs {
		if pkg.TypesInfo == nil || pkg.Fset == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			filename := pkg.Fset.Position(file.Pos()).Filename
			rel, err := filepath.Rel(base, absoluteSourcePath(g.Root, filename))
			if err != nil || !selected[filepath.ToSlash(rel)] {
				continue
			}
			rel = filepath.ToSlash(rel)
			for _, declaration := range file.Decls {
				function := ""
				if fn, ok := declaration.(*ast.FuncDecl); ok {
					function = fn.Name.Name
					if fn.Recv != nil && len(fn.Recv.List) > 0 {
						function = "(" + types.ExprString(fn.Recv.List[0].Type) + ")." + function
					}
				}
				collectDeclarationFieldUses(pkg, declaration, rel, function, add)
			}
		}
	}
	sort.Slice(uses, func(i, j int) bool {
		left, right := uses[i], uses[j]
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		if left.Column != right.Column {
			return left.Column < right.Column
		}
		return left.Access < right.Access
	})
	g.FieldUses = uses
}

func collectDeclarationFieldUses(pkg *packages.Package, declaration ast.Decl, file, function string, add func(graph.FieldUse)) {
	access := make(map[*ast.SelectorExpr]string)
	position := func(pos token.Pos) (int, int) {
		p := pkg.Fset.Position(pos)
		return p.Line, p.Column
	}
	ast.Inspect(declaration, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				if selector, ok := ast.Unparen(lhs).(*ast.SelectorExpr); ok {
					access[selector] = "write"
				}
			}
		case *ast.IncDecStmt:
			if selector, ok := ast.Unparen(n.X).(*ast.SelectorExpr); ok {
				access[selector] = "write"
			}
		case *ast.UnaryExpr:
			if n.Op == token.AND {
				if selector, ok := ast.Unparen(n.X).(*ast.SelectorExpr); ok {
					access[selector] = "address"
				}
			}
		case *ast.CompositeLit:
			structType, owner, ownerPackage := compositeStruct(pkg.TypesInfo.TypeOf(n))
			if structType == nil {
				return true
			}
			for _, element := range n.Elts {
				pair, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := pair.Key.(*ast.Ident)
				if !ok {
					continue
				}
				for index := 0; index < structType.NumFields(); index++ {
					if structType.Field(index).Name() == key.Name {
						line, column := position(key.Pos())
						add(graph.FieldUse{TypeName: owner, Package: ownerPackage, Field: key.Name, Access: "literal", Function: function, File: file, Line: line, Column: column})
						break
					}
				}
			}
		case *ast.SelectorExpr:
			selection := pkg.TypesInfo.Selections[n]
			if selection == nil || selection.Kind() != types.FieldVal {
				return true
			}
			owner, ownerPackage := fieldOwner(selection.Recv(), selection.Index())
			kind := access[n]
			if kind == "" {
				kind = "read"
			}
			line, column := position(n.Sel.Pos())
			add(graph.FieldUse{TypeName: owner, Package: ownerPackage, Field: n.Sel.Name, Access: kind, Function: function, File: file, Line: line, Column: column})
		}
		return true
	})
}

// fieldOwner follows a field selection's embedding path and names the struct
// type that declares the selected field.
func fieldOwner(receiver types.Type, path []int) (string, string) {
	current := receiver
	for step, index := range path {
		current = types.Unalias(current)
		if pointer, ok := current.(*types.Pointer); ok {
			current = types.Unalias(pointer.Elem())
		}
		structType, ok := current.Underlying().(*types.Struct)
		if !ok || index >= structType.NumFields() {
			return "", ""
		}
		if step == len(path)-1 {
			return namedTypeIdentity(current)
		}
		current = structType.Field(index).Type()
	}
	return "", ""
}

func compositeStruct(value types.Type) (*types.Struct, string, string) {
	if value == nil {
		return nil, "", ""
	}
	value = types.Unalias(value)
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
	}
	structType, ok := value.Underlying().(*types.Struct)
	if !ok {
		return nil, "", ""
	}
	name, path := namedTypeIdentity(value)
	return structType, name, path
}

func namedTypeIdentity(value types.Type) (string, string) {
	named, ok := value.(*types.Named)
	if !ok || named.Obj() == nil {
		return "", ""
	}
	object := named.Origin().Obj()
	if object.Pkg() == nil {
		return object.Name(), ""
	}
	return object.Name(), object.Pkg().Path()
}
