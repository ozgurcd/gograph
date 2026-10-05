package precise

import (
	"fmt"
	"go/ast"
	"go/types"
	"path/filepath"

	"github.com/ozgurcd/gograph/internal/graph"
	"golang.org/x/tools/go/packages"
)

func verifyRouteReceivers(packages []*packages.Package, g *graph.Graph) {
	if g.RouteCandidates == nil {
		return
	}
	verified := make(map[string]bool)
	for _, pkg := range packages {
		if pkg.TypesInfo == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !typedRouterCall(pkg, sel) {
					return true
				}
				pos := pkg.Fset.Position(call.Pos())
				rel, err := filepath.Rel(absoluteSourcePath(g.Root, "."), absoluteSourcePath(g.Root, pos.Filename))
				if err == nil {
					verified[fmt.Sprintf("%s:%d:%d", filepath.Clean(rel), pos.Line, pos.Column)] = true
				}
				return true
			})
		}
	}
	g.Routes = nil
	for _, route := range g.RouteCandidates {
		if verified[fmt.Sprintf("%s:%d:%d", filepath.Clean(route.File), route.Line, route.Column)] {
			route.ReceiverVerified = true
			route.ReceiverUnresolved = false
			g.Routes = append(g.Routes, route)
		}
	}
}

func typedRouterCall(pkg *packages.Package, selector *ast.SelectorExpr) bool {
	obj, _ := pkg.TypesInfo.Uses[selector.Sel].(*types.Func)
	if obj == nil {
		return false
	}
	sig, ok := obj.Type().(*types.Signature)
	if !ok {
		return false
	}
	if sig.Recv() == nil {
		return obj.Pkg() != nil && obj.Pkg().Path() == "net/http" && (obj.Name() == "Handle" || obj.Name() == "HandleFunc")
	}
	t := types.Unalias(pkg.TypesInfo.TypeOf(selector.X))
	if ptr, ok := t.(*types.Pointer); ok {
		t = types.Unalias(ptr.Elem())
	}
	if named, ok := t.(*types.Named); ok && named.Obj().Pkg() != nil {
		if graph.RouterType(named.Obj().Pkg().Path(), named.Obj().Name()) {
			return true
		}
	}
	// A locally declared router adapter can implement the same typed contract:
	// a string path followed by a function handler (possibly variadic).
	// Any(key, any) and Values.Get(key) do not satisfy this contract.
	if obj.Pkg() != pkg.Types || sig.Params().Len() < 2 {
		return false
	}
	first, ok := sig.Params().At(0).Type().Underlying().(*types.Basic)
	if !ok || first.Kind() != types.String {
		return false
	}
	handler := sig.Params().At(1).Type().Underlying()
	if list, ok := handler.(*types.Slice); ok {
		handler = list.Elem().Underlying()
	}
	_, ok = handler.(*types.Signature)
	return ok
}
