package precise

import (
	"go/ast"
	"go/constant"
	"go/types"
	"path/filepath"
	"sort"

	"github.com/ozgurcd/gograph/internal/graph"
	"golang.org/x/tools/go/packages"
)

// enrichPackageFacts uses the already loaded type information. No source is
// reparsed and no user getter is executed. Getter facts are conditional: an
// injected function may replace the default os.Getenv implementation.
func enrichPackageFacts(loaded []*packages.Package, g *graph.Graph) {
	files := make(map[string]string)
	symbols := make(map[string]bool)
	for _, f := range g.Files {
		files[absoluteSourcePath(g.Root, f.Path)] = f.Path
	}
	for _, s := range g.Symbols {
		symbols[s.ID] = true
	}
	uses := make(map[graph.VariableUse]bool)
	for _, u := range g.VariableUses {
		uses[u] = true
	}
	envs := make(map[graph.EnvRead]bool)
	for _, e := range g.EnvReads {
		envs[e] = true
	}
	for _, pkg := range loaded {
		if pkg.Fset == nil || pkg.TypesInfo == nil {
			continue
		}
		info := pkg.TypesInfo
		// A summary maps a helper's key parameter to an invocation of a
		// function parameter with an observed os.Getenv fallback assignment.
		helpers := make(map[types.Object]int)
		for _, file := range pkg.Syntax {
			rel, selected := files[filepath.Clean(pkg.Fset.Position(file.Pos()).Filename)]
			if !selected {
				continue
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				parameters := make(map[types.Object]int)
				index := 0
				if fn.Type.Params != nil {
					for _, field := range fn.Type.Params.List {
						for _, name := range field.Names {
							parameters[info.Defs[name]] = index
							index++
						}
					}
				}
				getters := make(map[types.Object]bool)
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					assign, ok := n.(*ast.AssignStmt)
					if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
						return true
					}
					lhs, ok := assign.Lhs[0].(*ast.Ident)
					if !ok {
						return true
					}
					obj := info.ObjectOf(lhs)
					if _, ok := parameters[obj]; !ok {
						return true
					}
					if sel, ok := assign.Rhs[0].(*ast.SelectorExpr); ok {
						f, ok := info.ObjectOf(sel.Sel).(*types.Func)
						if ok && f.Pkg() != nil && f.Pkg().Path() == "os" && f.Name() == "Getenv" {
							getters[obj] = true
						}
					}
					return true
				})
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok || len(call.Args) != 1 {
						return true
					}
					id, ok := call.Fun.(*ast.Ident)
					if !ok || !getters[info.ObjectOf(id)] {
						return true
					}
					if value := info.Types[call.Args[0]].Value; value != nil && value.Kind() == constant.String {
						envs[graph.EnvRead{Precise: true, Key: constant.StringVal(value), Accessor: "parameter (may use os.Getenv)", Function: fn.Name.Name, File: rel, Line: pkg.Fset.Position(call.Pos()).Line}] = true
					} else if key, ok := call.Args[0].(*ast.Ident); ok {
						if parameter, ok := parameters[info.ObjectOf(key)]; ok {
							helpers[info.Defs[fn.Name]] = parameter
						}
					}
					return true
				})
			}
		}
		for _, file := range pkg.Syntax {
			rel, selected := files[filepath.Clean(pkg.Fset.Position(file.Pos()).Filename)]
			if !selected {
				continue
			}
			for _, decl := range file.Decls {
				function := "package initializer"
				if fn, ok := decl.(*ast.FuncDecl); ok {
					function = fn.Name.Name
				}
				ast.Inspect(decl, func(n ast.Node) bool {
					if id, ok := n.(*ast.Ident); ok {
						v := info.Uses[id]
						_, variable := v.(*types.Var)
						_, constant := v.(*types.Const)
						if (variable || constant) && v.Pkg() != nil && v.Parent() == v.Pkg().Scope() {
							target := v.Pkg().Path() + "::" + v.Name()
							if symbols[target] {
								pos := pkg.Fset.Position(id.Pos())
								uses[graph.VariableUse{SymbolID: target, PackageName: v.Pkg().Name(), Name: v.Name(), Function: function, File: rel, Line: pos.Line, Column: pos.Column}] = true
							}
						}
					}
					if call, ok := n.(*ast.CallExpr); ok {
						if id, ok := call.Fun.(*ast.Ident); ok {
							if key, ok := helpers[info.ObjectOf(id)]; ok && key < len(call.Args) {
								if value := info.Types[call.Args[key]].Value; value != nil && value.Kind() == constant.String {
									envs[graph.EnvRead{Precise: true, Key: constant.StringVal(value), Accessor: "parameter via " + id.Name + " (may use os.Getenv)", Function: function, File: rel, Line: pkg.Fset.Position(call.Pos()).Line}] = true
								}
							}
						}
					}
					return true
				})
			}
		}
	}
	g.VariableUses = nil
	for u := range uses {
		g.VariableUses = append(g.VariableUses, u)
	}
	sort.Slice(g.VariableUses, func(i, j int) bool {
		a, b := g.VariableUses[i], g.VariableUses[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		return a.SymbolID < b.SymbolID
	})
	for _, existing := range g.EnvReads {
		delete(envs, existing)
	}
	for e := range envs {
		g.EnvReads = append(g.EnvReads, e)
	}
	sort.Slice(g.EnvReads, func(i, j int) bool {
		a, b := g.EnvReads[i], g.EnvReads[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		return a.Accessor < b.Accessor
	})
}
