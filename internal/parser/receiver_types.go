package parser

import (
	"go/ast"
	"go/token"
)

// lexicalTypes maps the identifiers bound in one function to the named type
// they are declared with. A name bound more than once with different or
// unknown types maps to "", so a shadowed variable never claims a type.
type lexicalTypes map[string]string

func (types lexicalTypes) bind(name, typeName string) {
	if name == "_" {
		return
	}
	if previous, ok := types[name]; ok && previous != typeName {
		types[name] = ""
		return
	}
	types[name] = typeName
}

func (types lexicalTypes) bindFields(fields *ast.FieldList) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		typeName := namedTypeName(field.Type)
		for _, name := range field.Names {
			types.bind(name.Name, typeName)
		}
	}
}

// functionLexicalTypes records receiver, parameter, result and local
// bindings whose type is spelled in the source: typed parameters and var
// declarations, and := of a composite literal, &T{...} or new(T).
func functionLexicalTypes(d *ast.FuncDecl) lexicalTypes {
	types := lexicalTypes{}
	types.bindFields(d.Recv)
	types.bindFields(d.Type.Params)
	types.bindFields(d.Type.Results)
	if d.Body != nil {
		types.bindBody(d.Body)
	}
	// A type parameter (func F[T any](x T) or a receiver's [T]) names no
	// package type, so drop bindings that use one.
	typeParams := map[string]bool{}
	addTypeParams := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			for _, name := range field.Names {
				typeParams[name.Name] = true
			}
		}
	}
	addTypeParams(d.Type.TypeParams)
	if d.Recv != nil && len(d.Recv.List) > 0 {
		receiver := d.Recv.List[0].Type
		if star, ok := receiver.(*ast.StarExpr); ok {
			receiver = star.X
		}
		switch generic := receiver.(type) {
		case *ast.IndexExpr:
			if name, ok := generic.Index.(*ast.Ident); ok {
				typeParams[name.Name] = true
			}
		case *ast.IndexListExpr:
			for _, index := range generic.Indices {
				if name, ok := index.(*ast.Ident); ok {
					typeParams[name.Name] = true
				}
			}
		}
	}
	for name, typeName := range types {
		if typeParams[typeName] {
			types[name] = ""
		}
	}
	return types
}

func (types lexicalTypes) bindBody(body *ast.BlockStmt) {
	ast.Inspect(body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.FuncLit:
			types.bindFields(value.Type.Params)
			types.bindFields(value.Type.Results)
		case *ast.ValueSpec:
			declared := namedTypeName(value.Type)
			for i, name := range value.Names {
				typeName := declared
				if value.Type == nil {
					typeName = ""
					if len(value.Values) == len(value.Names) {
						typeName = spelledValueType(value.Values[i])
					}
				}
				types.bind(name.Name, typeName)
			}
		case *ast.AssignStmt:
			if value.Tok != token.DEFINE {
				return true
			}
			for i, lhs := range value.Lhs {
				name, ok := lhs.(*ast.Ident)
				if !ok {
					continue
				}
				typeName := ""
				if len(value.Lhs) == len(value.Rhs) {
					typeName = spelledValueType(value.Rhs[i])
				}
				types.bind(name.Name, typeName)
			}
		case *ast.RangeStmt:
			if value.Tok == token.DEFINE {
				for _, expr := range []ast.Expr{value.Key, value.Value} {
					if name, ok := expr.(*ast.Ident); ok {
						types.bind(name.Name, "")
					}
				}
			}
		case *ast.TypeSwitchStmt:
			if assign, ok := value.Assign.(*ast.AssignStmt); ok {
				for _, lhs := range assign.Lhs {
					if name, ok := lhs.(*ast.Ident); ok {
						types.bind(name.Name, "")
					}
				}
			}
		}
		return true
	})
}

// namedTypeName returns "T" or "pkg.T" for a named type expression, with
// pointers, parentheses and type arguments removed, and "" for anything else.
func namedTypeName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		if pkg, ok := value.X.(*ast.Ident); ok {
			return pkg.Name + "." + value.Sel.Name
		}
	case *ast.StarExpr:
		return namedTypeName(value.X)
	case *ast.ParenExpr:
		return namedTypeName(value.X)
	case *ast.IndexExpr:
		return namedTypeName(value.X)
	case *ast.IndexListExpr:
		return namedTypeName(value.X)
	}
	return ""
}

// spelledReceiverType is spelledValueType for the receiver expression of a
// method call, which may also be a type assertion x.(T).
func spelledReceiverType(expr ast.Expr) string {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			break
		}
		expr = paren.X
	}
	if assertion, ok := expr.(*ast.TypeAssertExpr); ok {
		return namedTypeName(assertion.Type)
	}
	return spelledValueType(expr)
}

func spelledValueType(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.CompositeLit:
		return namedTypeName(value.Type)
	case *ast.UnaryExpr:
		if value.Op == token.AND {
			return spelledValueType(value.X)
		}
	case *ast.ParenExpr:
		return spelledValueType(value.X)
	case *ast.CallExpr:
		if fun, ok := value.Fun.(*ast.Ident); ok && fun.Name == "new" && len(value.Args) == 1 {
			return namedTypeName(value.Args[0])
		}
	}
	return ""
}

// qualifierType returns the lexical type of the root identifier of a method
// call's receiver chain (x in x.f.M()), the spelled type of a method called
// directly on T{...}, &T{...}, new(T) or x.(T), or "" when the receiver is
// none of these or its type is unknown.
func (types lexicalTypes) qualifierType(call *ast.CallExpr) string {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	expr := selector.X
	switch expr.(type) {
	case *ast.Ident, *ast.SelectorExpr:
	default:
		return spelledReceiverType(expr)
	}
	for {
		switch value := expr.(type) {
		case *ast.SelectorExpr:
			expr = value.X
			continue
		case *ast.Ident:
			return types[value.Name]
		}
		return ""
	}
}
