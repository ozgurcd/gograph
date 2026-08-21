package parser

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

const (
	flowSourceHTTP = "http_request"
	flowSourceJSON = "decoded_json"
	flowSourceEnv  = "environment"

	flowSinkSQL     = "sql_query"
	flowSinkCommand = "process_execution"
	flowSinkFile    = "filesystem"
	flowSinkHTTP    = "outbound_http"
)

func extractFlowFunction(fset *token.FileSet, d *ast.FuncDecl, relPath string, sym graph.SymbolNode, resolver *fileResolver) graph.FlowFunction {
	params := flowParameters(d.Type.Params)
	return extractFlowBody(fset, d.Body, relPath, sym.ID, flowFunctionName(sym), params, resolver)
}

func extractFlowLiterals(fset *token.FileSet, file *ast.File, relPath, pkgImportPath string, resolver *fileResolver) []graph.FlowFunction {
	var functions []graph.FlowFunction
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.FuncLit)
		if !ok {
			return true
		}
		pos := fset.Position(literal.Pos())
		name := fmt.Sprintf("<func@%d>", pos.Line)
		id := fmt.Sprintf("%s::<func@%s:%d:%d>", pkgImportPath, relPath, pos.Line, pos.Column)
		functions = append(functions, extractFlowBody(
			fset,
			literal.Body,
			relPath,
			id,
			name,
			flowParameters(literal.Type.Params),
			resolver,
		))
		return true
	})
	return functions
}

func extractFlowBody(fset *token.FileSet, body *ast.BlockStmt, relPath, functionID, functionName string, params []graph.FlowParameter, resolver *fileResolver) graph.FlowFunction {
	function := graph.FlowFunction{ID: functionID, Name: functionName, File: relPath, Params: params}
	if body == nil {
		return function
	}

	callTargets := make(map[*ast.CallExpr]string)
	parameterTypes := make(map[string]string, len(params))
	for _, param := range params {
		parameterTypes[param.Name] = resolvedFlowType(param.Type, resolver)
	}
	jsonDecoders := flowJSONDecoderNames(body, resolver)
	walkFlowBody(body, func(node ast.Node) {
		if call, ok := node.(*ast.CallExpr); ok {
			pos := fset.Position(call.Pos())
			callTargets[call] = fmt.Sprintf("$call:%d:%d", pos.Line, pos.Column)
		}
	})

	for _, param := range params {
		if param.Name == "" || !isUntrustedRequestType(param.Type, resolver) {
			continue
		}
		pos := fset.Position(body.Lbrace)
		function.Facts = append(function.Facts, graph.FlowFact{
			Kind:       "source",
			Target:     param.Name,
			SourceKind: flowSourceHTTP,
			Detail:     fmt.Sprintf("untrusted request parameter %s %s", param.Name, param.Type),
			Line:       pos.Line,
			Column:     pos.Column,
		})
	}

	walkFlowBody(body, func(node ast.Node) {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return
		}
		pos := fset.Position(call.Pos())
		callee := calleeString(call)
		arguments := make([][]string, 0, len(call.Args))
		var inputs []string
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
			inputs = append(inputs, flowExprRefs(selector.X, callTargets)...)
		}
		for _, arg := range call.Args {
			refs := flowExprRefs(arg, callTargets)
			arguments = append(arguments, refs)
			inputs = append(inputs, refs...)
		}
		function.Facts = append(function.Facts, graph.FlowFact{
			Kind:      "call",
			Target:    callTargets[call],
			Inputs:    uniqueFlowRefs(inputs),
			Arguments: arguments,
			Callee:    callee,
			Detail:    renderFlowExpr(fset, call),
			Line:      pos.Line,
			Column:    pos.Column,
		})

		if sourceKind, label, targets := classifyFlowSource(fset, call, callee, relPath, functionName, resolver, callTargets, parameterTypes, jsonDecoders); sourceKind != "" {
			if len(targets) == 0 {
				targets = []string{callTargets[call], callTargets[call] + ":0"}
			}
			for _, target := range targets {
				if target == "" {
					continue
				}
				function.Facts = append(function.Facts, graph.FlowFact{
					Kind:       "source",
					Target:     target,
					SourceKind: sourceKind,
					Detail:     label,
					Line:       pos.Line,
					Column:     pos.Column,
				})
			}
		}

		if sinkKind, indexes := classifyFlowSink(call, callee, resolver); sinkKind != "" {
			var sinkInputs []string
			for _, index := range indexes {
				if index >= 0 && index < len(arguments) {
					sinkInputs = append(sinkInputs, arguments[index]...)
				}
			}
			function.Facts = append(function.Facts, graph.FlowFact{
				Kind:     "sink",
				Inputs:   uniqueFlowRefs(sinkInputs),
				Callee:   callee,
				SinkKind: sinkKind,
				Detail:   renderFlowExpr(fset, call),
				Line:     pos.Line,
				Column:   pos.Column,
			})
		}
	})

	walkFlowBody(body, func(node ast.Node) {
		pos := fset.Position(node.Pos())
		switch statement := node.(type) {
		case *ast.AssignStmt:
			if len(statement.Rhs) == 0 {
				return
			}
			for index, lhs := range statement.Lhs {
				rhsIndex := index
				if rhsIndex >= len(statement.Rhs) {
					rhsIndex = len(statement.Rhs) - 1
				}
				appendFlowTransfer(&function, flowTarget(lhs), flowAssignmentRefs(statement.Rhs[rhsIndex], index, len(statement.Lhs), len(statement.Rhs), callTargets), renderFlowExpr(fset, statement), pos)
			}
		case *ast.ValueSpec:
			if len(statement.Values) == 0 {
				return
			}
			for index, name := range statement.Names {
				valueIndex := index
				if valueIndex >= len(statement.Values) {
					valueIndex = len(statement.Values) - 1
				}
				appendFlowTransfer(&function, name.Name, flowAssignmentRefs(statement.Values[valueIndex], index, len(statement.Names), len(statement.Values), callTargets), renderFlowExpr(fset, statement), pos)
			}
		case *ast.RangeStmt:
			inputs := flowExprRefs(statement.X, callTargets)
			appendFlowTransfer(&function, flowTarget(statement.Key), inputs, "range key", pos)
			appendFlowTransfer(&function, flowTarget(statement.Value), inputs, "range value", pos)
		case *ast.ReturnStmt:
			for index, result := range statement.Results {
				inputs := flowExprRefs(result, callTargets)
				if len(inputs) == 0 {
					continue
				}
				function.Facts = append(function.Facts, graph.FlowFact{
					Kind:   "return",
					Target: fmt.Sprintf("$return:%d", index),
					Inputs: uniqueFlowRefs(inputs),
					Detail: renderFlowExpr(fset, statement),
					Line:   pos.Line,
					Column: pos.Column,
				})
			}
		}
	})

	sort.SliceStable(function.Facts, func(i, j int) bool {
		left, right := function.Facts[i], function.Facts[j]
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		if left.Column != right.Column {
			return left.Column < right.Column
		}
		return flowFactOrder(left.Kind) < flowFactOrder(right.Kind)
	})
	return function
}

func flowAssignmentRefs(expr ast.Expr, targetIndex, targetCount, valueCount int, callTargets map[*ast.CallExpr]string) []string {
	if targetCount > 1 && valueCount == 1 {
		if call, ok := expr.(*ast.CallExpr); ok {
			if target := callTargets[call]; target != "" {
				return []string{fmt.Sprintf("%s:%d", target, targetIndex)}
			}
		}
	}
	return flowExprRefs(expr, callTargets)
}

func appendFlowTransfer(function *graph.FlowFunction, target string, inputs []string, detail string, pos token.Position) {
	inputs = uniqueFlowRefs(inputs)
	if target == "" || len(inputs) == 0 {
		return
	}
	function.Facts = append(function.Facts, graph.FlowFact{
		Kind:   "transfer",
		Target: target,
		Inputs: inputs,
		Detail: detail,
		Line:   pos.Line,
		Column: pos.Column,
	})
}

func flowFactOrder(kind string) int {
	switch kind {
	case "source":
		return 0
	case "call":
		return 1
	case "transfer":
		return 2
	case "return":
		return 3
	case "sink":
		return 4
	default:
		return 5
	}
}

func flowFunctionName(symbol graph.SymbolNode) string {
	if symbol.Receiver == "" {
		return symbol.Name
	}
	return fmt.Sprintf("(%s).%s", symbol.Receiver, symbol.Name)
}

func flowParameters(fields *ast.FieldList) []graph.FlowParameter {
	if fields == nil {
		return nil
	}
	var params []graph.FlowParameter
	for _, field := range fields.List {
		typeName := typeString(field.Type)
		for _, name := range field.Names {
			params = append(params, graph.FlowParameter{Name: name.Name, Type: typeName})
		}
	}
	return params
}

func isUntrustedRequestType(typeName string, resolver *fileResolver) bool {
	clean := resolvedFlowType(typeName, resolver)
	for _, suffix := range []string{
		"net/http.Request",
		"github.com/gin-gonic/gin.Context",
		"github.com/labstack/echo/v4.Context",
		"github.com/labstack/echo/v5.Context",
		"github.com/gofiber/fiber/v2.Ctx",
		"github.com/gofiber/fiber/v3.Ctx",
		"github.com/valyala/fasthttp.RequestCtx",
	} {
		if clean == suffix || strings.HasSuffix(clean, "/"+suffix) {
			return true
		}
	}
	return false
}

func resolvedFlowType(typeName string, resolver *fileResolver) string {
	clean := strings.TrimLeft(strings.ReplaceAll(typeName, " ", ""), "*")
	if dot := strings.Index(clean, "."); dot > 0 {
		if importPath := resolver.imports[clean[:dot]]; importPath != "" {
			clean = importPath + clean[dot:]
		}
	}
	return clean
}

func classifyFlowSource(fset *token.FileSet, call *ast.CallExpr, callee, relPath, functionName string, resolver *fileResolver, callTargets map[*ast.CallExpr]string, parameterTypes, jsonDecoders map[string]string) (string, string, []string) {
	pos := fset.Position(call.Pos())
	if env, ok := envRead(call, callee, pos.Line, relPath, functionName); ok {
		label := env.Accessor
		if env.Key != "<dynamic>" && env.Key != "" {
			label += "(" + env.Key + ")"
		}
		return flowSourceEnv, label, nil
	}

	method := flowMethodName(callee)
	importPath := flowCallImportPath(call, resolver)
	if (importPath == "os" && (method == "Getenv" || method == "LookupEnv")) ||
		(importPath == "github.com/spf13/viper" && (method == "GetString" || method == "GetBool" || method == "GetInt" || method == "Get")) {
		return flowSourceEnv, renderFlowExpr(fset, call), nil
	}
	var destination int
	switch {
	case importPath == "encoding/json" && method == "Unmarshal":
		destination = 1
	case importPath == "encoding/json/v2" && (method == "Unmarshal" || method == "UnmarshalRead" || method == "UnmarshalDecode"):
		destination = 1
	case method == "Decode" && isFlowJSONDecoderCall(call, resolver, jsonDecoders):
		destination = 0
	case isFlowFrameworkBindingCall(call, method, parameterTypes):
		destination = 0
	default:
		return "", "", nil
	}
	if destination >= len(call.Args) {
		return "", "", nil
	}
	targets := flowExprRefs(stripFlowAddress(call.Args[destination]), callTargets)
	return flowSourceJSON, renderFlowExpr(fset, call), targets
}

func flowJSONDecoderNames(body *ast.BlockStmt, resolver *fileResolver) map[string]string {
	decoders := make(map[string]string)
	walkFlowBody(body, func(node ast.Node) {
		switch value := node.(type) {
		case *ast.AssignStmt:
			for index, lhs := range value.Lhs {
				if index >= len(value.Rhs) {
					continue
				}
				if flowIsJSONDecoderExpr(value.Rhs[index], resolver, decoders) {
					if target := flowTarget(lhs); target != "" {
						decoders[target] = "encoding/json.Decoder"
					}
				}
			}
		case *ast.ValueSpec:
			for index, name := range value.Names {
				if index < len(value.Values) && flowIsJSONDecoderExpr(value.Values[index], resolver, decoders) {
					decoders[name.Name] = "encoding/json.Decoder"
				}
			}
		}
	})
	return decoders
}

func flowIsJSONDecoderExpr(expr ast.Expr, resolver *fileResolver, decoders map[string]string) bool {
	switch value := expr.(type) {
	case *ast.CallExpr:
		return flowCallImportPath(value, resolver) == "encoding/json" && flowMethodName(calleeString(value)) == "NewDecoder"
	case *ast.Ident:
		return decoders[value.Name] != ""
	case *ast.ParenExpr:
		return flowIsJSONDecoderExpr(value.X, resolver, decoders)
	}
	return false
}

func isFlowJSONDecoderCall(call *ast.CallExpr, resolver *fileResolver, decoders map[string]string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if receiver := flowTarget(selector.X); receiver != "" && decoders[receiver] != "" {
		return true
	}
	receiverCall, ok := selector.X.(*ast.CallExpr)
	return ok && flowIsJSONDecoderExpr(receiverCall, resolver, decoders)
}

func isFlowFrameworkBindingCall(call *ast.CallExpr, method string, parameterTypes map[string]string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	receiver := flowTarget(selector.X)
	typeName := parameterTypes[receiver]
	switch typeName {
	case "github.com/gin-gonic/gin.Context":
		return method == "Bind" || method == "BindJSON" || method == "ShouldBind" || method == "ShouldBindJSON" || method == "MustBindWith"
	case "github.com/labstack/echo/v4.Context", "github.com/labstack/echo/v5.Context":
		return method == "Bind"
	case "github.com/gofiber/fiber/v2.Ctx", "github.com/gofiber/fiber/v3.Ctx":
		return method == "BodyParser"
	default:
		return false
	}
}

func classifyFlowSink(call *ast.CallExpr, callee string, resolver *fileResolver) (string, []int) {
	method := flowMethodName(callee)
	importPath := flowCallImportPath(call, resolver)

	switch method {
	case "Query", "QueryRow", "Exec", "Prepare", "Raw":
		return flowSinkSQL, []int{0}
	case "QueryContext", "QueryRowContext", "ExecContext", "PrepareContext":
		return flowSinkSQL, []int{1}
	}

	if importPath == "os/exec" {
		switch method {
		case "Command":
			return flowSinkCommand, flowIndexes(0, len(call.Args))
		case "CommandContext":
			return flowSinkCommand, flowIndexes(1, len(call.Args))
		}
	}

	if importPath == "os" {
		switch method {
		case "WriteFile", "ReadFile", "Open", "OpenFile", "OpenRoot", "Create", "Remove", "RemoveAll", "Mkdir", "MkdirAll", "Chmod", "Chown", "Lchown", "Chtimes", "Truncate", "Stat", "Lstat", "Readlink", "DirFS":
			return flowSinkFile, []int{0}
		case "CreateTemp", "MkdirTemp", "Rename", "Symlink", "Link":
			return flowSinkFile, []int{0, 1}
		}
	}

	if importPath == "net/http" {
		switch method {
		case "Get", "Head", "Post", "PostForm":
			return flowSinkHTTP, []int{0}
		case "NewRequest":
			return flowSinkHTTP, []int{1}
		case "NewRequestWithContext":
			return flowSinkHTTP, []int{2}
		}
	}
	if method == "Do" {
		return flowSinkHTTP, []int{0}
	}
	return "", nil
}

func flowCallImportPath(call *ast.CallExpr, resolver *fileResolver) string {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	ident, ok := selector.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return resolver.imports[ident.Name]
}

func flowMethodName(callee string) string {
	if index := strings.LastIndex(callee, "."); index >= 0 {
		return callee[index+1:]
	}
	return callee
}

func flowIndexes(start, length int) []int {
	if start >= length {
		return nil
	}
	indexes := make([]int, 0, length-start)
	for index := start; index < length; index++ {
		indexes = append(indexes, index)
	}
	return indexes
}

func flowExprRefs(expr ast.Expr, callTargets map[*ast.CallExpr]string) []string {
	if expr == nil {
		return nil
	}
	var refs []string
	switch value := expr.(type) {
	case *ast.Ident:
		if value.Name != "nil" && value.Name != "true" && value.Name != "false" {
			refs = append(refs, value.Name)
		}
	case *ast.SelectorExpr:
		if name := flowSelectorName(value); name != "" {
			refs = append(refs, name)
		}
		refs = append(refs, flowExprRefs(value.X, callTargets)...)
	case *ast.CallExpr:
		if target := callTargets[value]; target != "" {
			refs = append(refs, target)
		}
	case *ast.ParenExpr:
		refs = append(refs, flowExprRefs(value.X, callTargets)...)
	case *ast.StarExpr:
		refs = append(refs, flowExprRefs(value.X, callTargets)...)
	case *ast.UnaryExpr:
		refs = append(refs, flowExprRefs(value.X, callTargets)...)
	case *ast.BinaryExpr:
		refs = append(refs, flowExprRefs(value.X, callTargets)...)
		refs = append(refs, flowExprRefs(value.Y, callTargets)...)
	case *ast.IndexExpr:
		refs = append(refs, flowExprRefs(value.X, callTargets)...)
		refs = append(refs, flowExprRefs(value.Index, callTargets)...)
	case *ast.IndexListExpr:
		refs = append(refs, flowExprRefs(value.X, callTargets)...)
	case *ast.SliceExpr:
		refs = append(refs, flowExprRefs(value.X, callTargets)...)
		refs = append(refs, flowExprRefs(value.Low, callTargets)...)
		refs = append(refs, flowExprRefs(value.High, callTargets)...)
		refs = append(refs, flowExprRefs(value.Max, callTargets)...)
	case *ast.TypeAssertExpr:
		refs = append(refs, flowExprRefs(value.X, callTargets)...)
	case *ast.CompositeLit:
		for _, element := range value.Elts {
			switch element := element.(type) {
			case *ast.KeyValueExpr:
				refs = append(refs, flowExprRefs(element.Value, callTargets)...)
			case ast.Expr:
				refs = append(refs, flowExprRefs(element, callTargets)...)
			}
		}
	case *ast.KeyValueExpr:
		refs = append(refs, flowExprRefs(value.Value, callTargets)...)
	}
	return uniqueFlowRefs(refs)
}

func flowTarget(expr ast.Expr) string {
	switch value := expr.(type) {
	case nil:
		return ""
	case *ast.Ident:
		if value.Name == "_" {
			return ""
		}
		return value.Name
	case *ast.SelectorExpr:
		return flowSelectorName(value)
	case *ast.IndexExpr:
		return flowTarget(value.X)
	case *ast.StarExpr:
		return flowTarget(value.X)
	case *ast.ParenExpr:
		return flowTarget(value.X)
	default:
		return ""
	}
}

func flowSelectorName(selector *ast.SelectorExpr) string {
	parts := []string{selector.Sel.Name}
	current := selector.X
	for {
		switch value := current.(type) {
		case *ast.Ident:
			parts = append(parts, value.Name)
			reverseFlowStrings(parts)
			return strings.Join(parts, ".")
		case *ast.SelectorExpr:
			parts = append(parts, value.Sel.Name)
			current = value.X
		case *ast.ParenExpr:
			current = value.X
		default:
			return ""
		}
	}
}

func reverseFlowStrings(values []string) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func stripFlowAddress(expr ast.Expr) ast.Expr {
	if unary, ok := expr.(*ast.UnaryExpr); ok && unary.Op == token.AND {
		return unary.X
	}
	return expr
}

func uniqueFlowRefs(refs []string) []string {
	seen := make(map[string]bool, len(refs))
	result := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		result = append(result, ref)
	}
	return result
}

func renderFlowExpr(fset *token.FileSet, node ast.Node) string {
	var buffer bytes.Buffer
	if err := printer.Fprint(&buffer, fset, node); err != nil {
		return ""
	}
	return strings.TrimSpace(buffer.String())
}

func walkFlowBody(body *ast.BlockStmt, visit func(ast.Node)) {
	ast.Inspect(body, func(node ast.Node) bool {
		if node == nil {
			return true
		}
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		visit(node)
		return true
	})
}
