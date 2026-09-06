package parser

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// httpClientCall accepts only an imported, unshadowed net/http package. Client
// methods need typed receiver evidence and are deliberately not guessed here.
func httpClientCall(resolver *fileResolver, body *ast.BlockStmt, call *ast.CallExpr) (method string, urlIndex int, requestOnly, ok bool) {
	name, valid := httpImportedCall(resolver, call, "net/http")
	if !valid {
		return "", 0, false, false
	}
	switch name {
	case "Get", "Head", "Post", "PostForm":
		method = strings.ToUpper(name)
		if name == "PostForm" {
			method = "POST"
		}
	case "NewRequest", "NewRequestWithContext":
		requestOnly, urlIndex = true, 1
		if name == "NewRequestWithContext" {
			urlIndex = 2
		}
		if len(call.Args) <= urlIndex {
			return "", 0, false, false
		}
		var static bool
		method, static = httpStaticString(body, call.Args[urlIndex-1], call.Pos(), make(map[*staticStringObject]bool), 0)
		if !static {
			method, static = httpMethodConstant(resolver, call.Args[urlIndex-1])
		}
		if !static {
			method = "ANY"
		} else if method == "" {
			method = "GET" // net/http's documented empty-method default.
		}
	default:
		return "", 0, false, false
	}
	return method, urlIndex, requestOnly, len(call.Args) > urlIndex
}

func httpImportedCall(resolver *fileResolver, call *ast.CallExpr, importPath string) (string, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || resolver == nil {
		return "", false
	}
	base, ok := selector.X.(*ast.Ident)
	if !ok || base.Obj != nil || resolver.imports[base.Name] != importPath {
		return "", false
	}
	return selector.Sel.Name, true
}

func extractHTTPURL(body *ast.BlockStmt, expr ast.Expr, before token.Pos, resolver *fileResolver) graph.HTTPCallEdge {
	if literal, ok := httpStaticString(body, expr, before, make(map[*staticStringObject]bool), 0); ok {
		return graph.HTTPCallEdge{URL: literal, StaticSegments: extractStaticSegments(literal)}
	}
	base, suffix, ok := httpURLParts(body, expr, before, resolver, make(map[*staticStringObject]bool), 0)
	fact := graph.HTTPCallEdge{URL: "<dynamic>", HasDynamic: true}
	if ok {
		fact.URLBase, fact.URLSuffix, fact.URLSuffixStatic = base, suffix, true
		fact.URL = base + suffix
		fact.StaticSegments = extractStaticSegments(suffix)
	}
	return fact
}

// httpURLParts follows bounded lexical aliases, not runtime data flow. It only
// accepts one dynamic base followed by a wholly static suffix. In particular,
// cfg.URL + "/users/" + userID must not become an exact "/users" contract.
func httpURLParts(body *ast.BlockStmt, expr ast.Expr, before token.Pos, resolver *fileResolver, resolving map[*staticStringObject]bool, depth int) (string, string, bool) {
	if expr == nil || depth >= maxStaticStringDepth {
		return "", "", false
	}
	switch value := expr.(type) {
	case *ast.ParenExpr:
		return httpURLParts(body, value.X, before, resolver, resolving, depth+1)
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return "", "", false
		}
		base, suffix, ok := httpURLParts(body, value.X, before, resolver, resolving, depth+1)
		tail, static := httpStaticString(body, value.Y, before, make(map[*staticStringObject]bool), 0)
		if !ok || !static || len(suffix) > maxStaticStringBytes-len(tail) {
			return "", "", false
		}
		return base, suffix + tail, true
	case *ast.Ident:
		if value.Obj != nil {
			if httpStringEscapes(body, value.Obj, before) {
				return "", "", false
			}
			if httpPackageVariable(body, value.Obj) {
				return value.Name, "", value.Name != "_"
			}
			if resolving[value.Obj] {
				return "", "", false
			}
			candidate, pos, ok := staticStringDeclaration(value.Obj)
			if ok {
				if pos >= before || staticStringReassigned(body, value.Obj, pos, before) {
					return "", "", false
				}
				resolving[value.Obj] = true
				base, suffix, found := httpURLParts(body, candidate, pos, resolver, resolving, depth+1)
				delete(resolving, value.Obj)
				return base, suffix, found
			}
			// A parameter or otherwise uninitialized variable can be explicitly
			// configured by name; an assignment before use invalidates that claim.
			if staticStringReassigned(body, value.Obj, token.NoPos, before) {
				return "", "", false
			}
		}
		return value.Name, "", value.Name != "_"
	case *ast.SelectorExpr:
		name, ok := httpURLSelector(value, depth)
		return name, "", ok
	case *ast.CallExpr:
		name, ok := httpImportedCall(resolver, value, "os")
		if !ok || name != "Getenv" || len(value.Args) != 1 {
			return "", "", false
		}
		key, ok := httpStaticString(body, value.Args[0], before, make(map[*staticStringObject]bool), 0)
		if !ok || key == "" || strings.ContainsAny(key, "=\x00") {
			return "", "", false
		}
		return "env:" + key, "", true
	}
	return "", "", false
}

func httpURLSelector(expr ast.Expr, depth int) (string, bool) {
	if depth >= maxStaticStringDepth {
		return "", false
	}
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name, value.Name != "_"
	case *ast.SelectorExpr:
		base, ok := httpURLSelector(value.X, depth+1)
		if !ok || len(base)+len(value.Sel.Name)+1 > maxStaticStringBytes {
			return "", false
		}
		return base + "." + value.Sel.Name, true
	}
	return "", false
}

func httpMethodConstant(resolver *fileResolver, expr ast.Expr) (string, bool) {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	base, ok := selector.X.(*ast.Ident)
	if !ok || base.Obj != nil || resolver.imports[base.Name] != "net/http" {
		return "", false
	}
	switch selector.Sel.Name {
	case "MethodGet", "MethodHead", "MethodPost", "MethodPut", "MethodPatch", "MethodDelete", "MethodConnect", "MethodOptions", "MethodTrace":
		return strings.ToUpper(strings.TrimPrefix(selector.Sel.Name, "Method")), true
	}
	return "", false
}

func httpPackageVariable(body *ast.BlockStmt, obj *staticStringObject) bool {
	_, valueDeclaration := obj.Decl.(*ast.ValueSpec)
	return valueDeclaration && obj.Kind == ast.Var && (body == nil || obj.Pos() < body.Pos() || obj.Pos() >= body.End())
}

func httpStringEscapes(body *ast.BlockStmt, obj *staticStringObject, before token.Pos) bool {
	if body == nil {
		return false
	}
	escapes := false
	ast.Inspect(body, func(node ast.Node) bool {
		if escapes || node != nil && node.Pos() >= before {
			return false
		}
		if address, ok := node.(*ast.UnaryExpr); ok && address.Op == token.AND {
			if id, ok := address.X.(*ast.Ident); ok && id.Obj == obj {
				escapes = true
			}
		}
		return !escapes
	})
	return escapes
}

// Package variables can be changed by other functions, and escaped locals can
// be changed through pointers. They are never promoted to static URL evidence.
func httpStaticString(body *ast.BlockStmt, expr ast.Expr, before token.Pos, resolving map[*staticStringObject]bool, depth int) (string, bool) {
	if expr == nil || depth >= maxStaticStringDepth {
		return "", false
	}
	switch value := expr.(type) {
	case *ast.BasicLit:
		return evalStaticString(body, value, before, resolving, depth)
	case *ast.ParenExpr:
		return httpStaticString(body, value.X, before, resolving, depth+1)
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return "", false
		}
		left, leftOK := httpStaticString(body, value.X, before, resolving, depth+1)
		right, rightOK := httpStaticString(body, value.Y, before, resolving, depth+1)
		if !leftOK || !rightOK || len(left) > maxStaticStringBytes-len(right) {
			return "", false
		}
		return left + right, true
	case *ast.Ident:
		if value.Obj == nil || resolving[value.Obj] || httpPackageVariable(body, value.Obj) || httpStringEscapes(body, value.Obj, before) {
			return "", false
		}
		candidate, pos, ok := staticStringDeclaration(value.Obj)
		if ok && staticStringReassigned(body, value.Obj, pos, before) {
			return "", false
		}
		if !ok {
			candidate, pos, ok = directStaticAssignment(body, value.Obj, before)
		}
		if !ok || pos >= before {
			return "", false
		}
		resolving[value.Obj] = true
		result, found := httpStaticString(body, candidate, pos, resolving, depth+1)
		delete(resolving, value.Obj)
		return result, found
	}
	return "", false
}
