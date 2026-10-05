package precise

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/types"
	"path/filepath"

	"github.com/ozgurcd/gograph/internal/graph"
	"golang.org/x/tools/go/packages"
)

type routeDeclaration struct {
	pkg  *packages.Package
	decl *ast.FuncDecl
}

// enrichRouteFactories follows only a single, statically known returned
// function, never a parameter, mutable variable, or choice between handlers.
func enrichRouteFactories(packages []*packages.Package, g *graph.Graph) {
	declarations := make(map[*types.Func]routeDeclaration)
	for _, pkg := range packages {
		if pkg.TypesInfo == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			for _, d := range file.Decls {
				if fn, ok := d.(*ast.FuncDecl); ok {
					if obj, ok := pkg.TypesInfo.Defs[fn.Name].(*types.Func); ok {
						declarations[obj] = routeDeclaration{pkg, fn}
					}
				}
			}
		}
	}
	bySite := make(map[string][]int)
	for i, route := range g.Routes {
		if route.DynamicHandler {
			key := fmt.Sprintf("%s:%d:%d", filepath.Clean(route.File), route.Line, route.Column)
			bySite[key] = append(bySite[key], i)
		}
	}
	for _, pkg := range packages {
		if pkg.TypesInfo == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) < 2 {
					return true
				}
				pos := pkg.Fset.Position(call.Pos())
				rel, err := filepath.Rel(absoluteSourcePath(g.Root, "."), absoluteSourcePath(g.Root, pos.Filename))
				if err != nil {
					return true
				}
				indices := bySite[fmt.Sprintf("%s:%d:%d", filepath.Clean(rel), pos.Line, pos.Column)]
				if len(indices) == 0 {
					return true
				}
				for _, index := range indices {
					route := &g.Routes[index]
					for _, arg := range call.Args[1:] {
						factory, ok := arg.(*ast.CallExpr)
						if !ok {
							continue
						}
						obj := routeFunctionObject(pkg, factory.Fun)
						if obj == nil || route.Handler != obj.Name() && route.Handler != routeExpressionName(factory.Fun) {
							continue
						}
						if handler := returnedRouteHandler(declarations, obj, g, make(map[*types.Func]bool)); handler != nil {
							route.ReturnedHandler = handler
							route.DynamicHandler = false
						}
					}
				}
				return true
			})
		}
	}
}

func routeExpressionName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return routeExpressionName(x.X) + "." + x.Sel.Name
	case *ast.ParenExpr:
		return routeExpressionName(x.X)
	}
	return ""
}

func routeFunctionObject(pkg *packages.Package, expr ast.Expr) *types.Func {
	var obj types.Object
	switch e := expr.(type) {
	case *ast.Ident:
		obj = pkg.TypesInfo.Uses[e]
	case *ast.SelectorExpr:
		obj = pkg.TypesInfo.Uses[e.Sel]
	case *ast.ParenExpr:
		return routeFunctionObject(pkg, e.X)
	}
	fn, _ := obj.(*types.Func)
	return fn
}

func returnedRouteHandler(declarations map[*types.Func]routeDeclaration, fn *types.Func, g *graph.Graph, visited map[*types.Func]bool) *graph.RouteHandler {
	if visited[fn] || len(visited) >= 8 {
		return nil
	}
	visited[fn] = true
	d, ok := declarations[fn]
	if !ok || d.decl.Body == nil {
		return nil
	}
	var returns []*ast.ReturnStmt
	ast.Inspect(d.decl.Body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		if r, ok := n.(*ast.ReturnStmt); ok {
			returns = append(returns, r)
		}
		return true
	})
	if len(returns) != 1 || len(returns[0].Results) != 1 {
		return nil
	}
	expr := returns[0].Results[0]
	for {
		p, ok := expr.(*ast.ParenExpr)
		if !ok {
			break
		}
		expr = p.X
	}
	if literal, ok := expr.(*ast.FuncLit); ok {
		return describeRouteHandler(d.pkg, literal, literal.Body, fn.Name()+"$returned", g)
	}
	if call, ok := expr.(*ast.CallExpr); ok {
		if target := routeFunctionObject(d.pkg, call.Fun); target != nil {
			return returnedRouteHandler(declarations, target, g, visited)
		}
		return nil
	}
	if target := routeFunctionObject(d.pkg, expr); target != nil {
		if named, ok := declarations[target]; ok && named.decl.Body != nil {
			return describeRouteHandler(named.pkg, named.decl, named.decl.Body, target.Name(), g)
		}
	}
	return nil
}

func describeRouteHandler(pkg *packages.Package, node ast.Node, body *ast.BlockStmt, name string, g *graph.Graph) *graph.RouteHandler {
	pos := pkg.Fset.Position(node.Pos())
	rel, err := filepath.Rel(absoluteSourcePath(g.Root, "."), absoluteSourcePath(g.Root, pos.Filename))
	if err != nil {
		return nil
	}
	var b bytes.Buffer
	if printer.Fprint(&b, pkg.Fset, node) != nil {
		return nil
	}
	h := &graph.RouteHandler{Name: name, File: rel, Line: pos.Line, Body: b.String()}
	sites := make(map[string]bool)
	lines := make(map[int]bool)
	ast.Inspect(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		if c, ok := n.(*ast.CallExpr); ok {
			p := pkg.Fset.Position(c.Lparen)
			sites[fmt.Sprintf("%d:%d", p.Line, p.Column)] = true
			lines[p.Line] = true
		}
		return true
	})
	seen := make(map[string]bool)
	for _, call := range g.Calls {
		key := fmt.Sprintf("%d:%d", call.Line, call.Column)
		if filepath.Clean(call.File) == filepath.Clean(rel) && sites[key] && !seen[key+call.CalleeRaw] {
			h.Calls = append(h.Calls, call)
			seen[key+call.CalleeRaw] = true
		}
	}
	for _, sq := range g.SQLs {
		if filepath.Clean(sq.File) == filepath.Clean(rel) && lines[sq.Line] {
			h.SQLs = append(h.SQLs, sq)
		}
	}
	for _, env := range g.EnvReads {
		if filepath.Clean(env.File) == filepath.Clean(rel) && lines[env.Line] {
			h.EnvReads = append(h.EnvReads, env)
		}
	}
	return h
}
