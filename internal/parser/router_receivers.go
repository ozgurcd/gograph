package parser

import (
	"go/ast"
	"path"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

func routerImport(file *ast.File, alias string) string {
	for _, imp := range file.Imports {
		pkg := strings.Trim(imp.Path.Value, `"`)
		name := path.Base(pkg)
		if strings.HasPrefix(name, "v") && len(name) > 1 && name[1] >= '0' && name[1] <= '9' {
			name = path.Base(path.Dir(pkg))
		}
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name == alias {
			return pkg
		}
	}
	return ""
}

func supportedRouterType(file *ast.File, expr ast.Expr, unresolved ...bool) bool {
	switch x := expr.(type) {
	case *ast.StarExpr:
		return supportedRouterType(file, x.X, unresolved...)
	case *ast.ParenExpr:
		return supportedRouterType(file, x.X, unresolved...)
	case *ast.SelectorExpr:
		if pkg, ok := x.X.(*ast.Ident); ok && pkg.Obj == nil {
			return graph.RouterType(routerImport(file, pkg.Name), x.Sel.Name)
		}
	case *ast.Ident:
		return x.Obj == nil && (graph.RouterType(routerImport(file, "."), x.Name) || len(unresolved) > 0 && unresolved[0] && routerImport(file, ".") == "")
	}
	return false
}

func supportedRouteReceiver(file *ast.File, expr ast.Expr, seen map[ast.Node]bool, unresolved ...bool) bool {
	if len(seen) > 32 {
		return false
	}
	switch x := expr.(type) {
	case *ast.ParenExpr:
		return supportedRouteReceiver(file, x.X, seen, unresolved...)
	case *ast.UnaryExpr:
		return supportedRouteReceiver(file, x.X, seen, unresolved...)
	case *ast.CompositeLit:
		return supportedRouterType(file, x.Type, unresolved...)
	case *ast.Ident:
		if x.Obj == nil {
			return routerImport(file, x.Name) == "net/http"
		}
		declaration, ok := x.Obj.Decl.(ast.Node)
		if !ok || seen[declaration] {
			return false
		}
		seen[declaration] = true
		switch d := x.Obj.Decl.(type) {
		case *ast.Field:
			return supportedRouterType(file, d.Type, unresolved...)
		case *ast.ValueSpec:
			if supportedRouterType(file, d.Type, unresolved...) {
				return true
			}
			for i, name := range d.Names {
				if name.Obj == x.Obj && i < len(d.Values) {
					return supportedRouteReceiver(file, d.Values[i], seen, unresolved...)
				}
			}
		case *ast.AssignStmt:
			for i, lhs := range d.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Obj == x.Obj && i < len(d.Rhs) {
					return supportedRouteReceiver(file, d.Rhs[i], seen, unresolved...)
				}
			}
		}
	case *ast.CallExpr:
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Obj == nil {
				p := routerImport(file, pkg.Name)
				switch sel.Sel.Name {
				case "New", "Default", "NewRouter", "NewMux", "NewServeMux":
					for _, name := range []string{"Engine", "Echo", "App", "Router", "Mux", "ServeMux"} {
						if graph.RouterType(p, name) {
							return true
						}
					}
				}
			}
			switch sel.Sel.Name {
			case "Group", "With", "Subrouter", "PathPrefix":
				return supportedRouteReceiver(file, sel.X, seen, unresolved...)
			}
		}
	}
	return false
}
