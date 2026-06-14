// Package parser extracts graph nodes from a single Go source file using the
// standard go/ast and go/parser packages. No code from the target repository
// is executed.
package parser

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"net/url"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// FileResult holds everything extracted from one source file.
type FileResult struct {
	File        graph.FileNode
	Symbols     []graph.SymbolNode
	Imports     []graph.ImportEdge
	Calls       []graph.CallEdge
	Env         []graph.EnvRead
	Routes      []graph.HTTPRoute
	SQLs        []graph.SQLEdge
	Errors      []graph.ErrorEdge
	Concurrency []graph.ConcurrencyNode
	TestEdges   []graph.TestEdge
	Mutations   []graph.MutationEdge
	Literals    []graph.LiteralEdge
	HttpCalls   []graph.HttpCallEdge
}

// ParseFile parses a single .go file and extracts its nodes.
// path must be an absolute or repo-relative path.
// relPath is the repo-relative file path, stored in File nodes and graph edges.
// pkgImportPath is the module-rooted import path of the package (e.g. "github.com/org/repo/internal/auth").
// It is used as the stable prefix for symbol IDs so that IDs survive file renames within a package.
func ParseFile(fset *token.FileSet, path, relPath, pkgImportPath string) (*FileResult, error) {
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	tf := fset.File(f.Pos())
	lineCount := 0
	if tf != nil {
		lineCount = tf.LineCount()
	}

	pkgName := f.Name.Name

	result := &FileResult{
		File: graph.FileNode{
			ID:          relPath,
			Path:        relPath,
			PackageName: pkgName,
			Lines:       lineCount,
		},
	}

	// Imports.
	for _, imp := range f.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		alias := ""
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		result.Imports = append(result.Imports, graph.ImportEdge{
			FromFile:    relPath,
			FromPackage: pkgName,
			ImportPath:  importPath,
			Alias:       alias,
		})
	}

	// Top-level declarations.
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			extractGenDecl(fset, d, relPath, pkgName, pkgImportPath, result)
		case *ast.FuncDecl:
			extractFuncDecl(fset, d, relPath, pkgName, pkgImportPath, result)
		}
	}

	// Extract HTTP Routes
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		var method string
		switch f := call.Fun.(type) {
		case *ast.SelectorExpr:
			name := f.Sel.Name
			if name == "GET" || name == "POST" || name == "PUT" || name == "DELETE" || name == "PATCH" || name == "OPTIONS" || name == "HEAD" || name == "Any" || name == "Handle" || name == "HandleFunc" {
				method = name
			}
		}
		if method != "" && len(call.Args) >= 1 {
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				path := strings.Trim(lit.Value, "\"")
				handler := ""
				inlineBody := ""
				if len(call.Args) >= 2 {
					switch h := call.Args[1].(type) {
					case *ast.FuncLit:
						// Inline anonymous function — not a named symbol.
						// Record a descriptive label so callers know it is a closure
						// and can navigate to the exact line in the source file.
						handler = fmt.Sprintf("<inline handler at line %d>", fset.Position(h.Pos()).Line)
						// Render the full function literal source via go/printer so it
						// can be served from graph.json without re-reading the source file.
						var buf bytes.Buffer
						if err := printer.Fprint(&buf, fset, h); err == nil {
							inlineBody = buf.String()
						}
					case *ast.CallExpr:
						// Factory / curried / middleware-wrapped handler patterns:
						//   router.POST("/path", HandleBulkCreateUsers(jobSvc, auditSvc))
						//   mux.HandleFunc("/path", guard(h.method))
						//   mux.HandleFunc("/path", wrap1(wrap2(h.method)))
						//
						// Prefer the *inner* handler reference (the method value or
						// function the wrapper will eventually invoke) over the outer
						// wrapper's name. The orphan-reachability BFS in
						// search.ReachableOrphans seeds entry-point roots from
						// HTTPRoute.Handler; if we record the wrapper name here, the
						// real handler (e.g. (*AdminHandler).customers) appears
						// unreachable because the BFS never reaches it through the
						// opaque wrapper closure.
						//
						// Fall back to the outer call's name only when no inner
						// callable reference can be recovered (e.g. the handler is
						// itself the call's *result*, not an argument — the factory
						// pattern above).
						if inner := extractHandlerRefs(h); len(inner) > 0 {
							handler = inner[0]
						} else {
							handler = calleeString(h)
						}
					default:
						handler = typeString(call.Args[1])
					}
				}
				result.Routes = append(result.Routes, graph.HTTPRoute{
					Method:     method,
					Path:       path,
					Handler:    handler,
					InlineBody: inlineBody,
					File:       relPath,
					Line:       fset.Position(call.Pos()).Line,
				})
			}
		}
		return true
	})

	return result, nil
}

// extractGenDecl handles type declarations (structs, interfaces).
func extractGenDecl(fset *token.FileSet, d *ast.GenDecl, relPath, pkgName, pkgImportPath string, result *FileResult) {
	for _, spec := range d.Specs {
		if vs, ok := spec.(*ast.ValueSpec); ok {
			if d.Tok == token.VAR || d.Tok == token.CONST {
				for _, name := range vs.Names {
					pos := fset.Position(name.Pos())
					endPos := fset.Position(vs.End()) // use vs.End() to capture the entire expression block
					doc := ""
					if d.Doc != nil {
						doc = d.Doc.Text()
					} else if vs.Comment != nil {
						doc = vs.Comment.Text()
					}

					symKind := graph.KindVar
					if d.Tok == token.CONST {
						symKind = graph.KindConst
					}

					sym := graph.SymbolNode{
						ID:          fmt.Sprintf("%s::%s", pkgImportPath, name.Name),
						Kind:        symKind,
						Name:        name.Name,
						PackageName: pkgName,
						File:        relPath,
						Line:        pos.Line,
						EndLine:     endPos.Line,
						Doc:         strings.TrimSpace(doc),
					}
					result.Symbols = append(result.Symbols, sym)
				}
				// Walk package-level initializer expressions for call edges.
				// `var x = compute()` and `var y = registry{handler: h.method}`
				// both run at package load time (alongside init() functions);
				// any functions they invoke or function values they capture
				// are live, but without these edges the reachability BFS in
				// search.ReachableOrphans never reaches them. Attribute the
				// edges to a synthetic "init" caller — that name is seeded as
				// a root in advanced.go so the chain is reached from a real
				// entry point.
				for _, v := range vs.Values {
					// Pass A: direct call expressions in the initializer.
					ast.Inspect(v, func(n ast.Node) bool {
						call, ok := n.(*ast.CallExpr)
						if !ok {
							return true
						}
						callee := calleeString(call)
						callPos := fset.Position(call.Pos())
						if callee != "" {
							result.Calls = append(result.Calls, graph.CallEdge{
								CallerName: "init",
								CalleeRaw:  callee,
								File:       relPath,
								Line:       callPos.Line,
							})
						}
						// Same function-value-as-arg handling as in function
						// bodies (Bug 1) — initializers can also pass method
						// values to constructors / registry calls.
						for _, ref := range extractHandlerRefs(call) {
							result.Calls = append(result.Calls, graph.CallEdge{
								CallerName: "init",
								CalleeRaw:  ref,
								File:       relPath,
								Line:       callPos.Line,
							})
						}
						return true
					})
					// Pass B: function values captured by the initializer
					// expression directly (struct/map literals carrying
					// function fields, e.g. `var x = Config{OnEvent: foo}`,
					// or bare-Ident handlers like `var defaultHandler = foo`).
					var refs []string
					collectFuncRefs(v, &refs)
					for _, ref := range refs {
						result.Calls = append(result.Calls, graph.CallEdge{
							CallerName: "init",
							CalleeRaw:  ref,
							File:       relPath,
							Line:       fset.Position(v.Pos()).Line,
						})
					}
				}
			}
			continue
		}

		ts, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		var kind graph.SymbolKind
		var methods map[string]string
		var fields []graph.StructField
		var embeds []string
		switch t := ts.Type.(type) {
		case *ast.StructType:
			kind = graph.KindStruct
			if t.Fields != nil {
				for _, f := range t.Fields.List {
					typeStr := typeString(f.Type)
					tagStr := ""
					if f.Tag != nil {
						tagStr = f.Tag.Value
					}
					if len(f.Names) == 0 {
						embeds = append(embeds, typeStr)
						fields = append(fields, graph.StructField{Name: typeStr, Type: typeStr, Tag: tagStr})
					} else {
						for _, n := range f.Names {
							fields = append(fields, graph.StructField{Name: n.Name, Type: typeStr, Tag: tagStr})
						}
					}
				}
			}
		case *ast.InterfaceType:
			kind = graph.KindInterface
			if t.Methods != nil {
				methods = make(map[string]string)
				for _, m := range t.Methods.List {
					if len(m.Names) > 0 {
						if ft, ok := m.Type.(*ast.FuncType); ok {
							sig := funcTypeSignature(ft)
							methods[m.Names[0].Name] = sig
						}
					}
				}
			}
		default:
			// Type aliases (type Foo = Bar) and named types whose underlying
			// type isn't a struct or interface (type StatusCode int, type
			// HandlerFunc func(...) error, etc.) are all valid Go declarations
			// that should appear as queryable symbols. Without this, `query
			// JSONMap` returns nothing for `type JSONMap = map[string]any`
			// and exported aliases vanish from `gograph public`. Record them
			// as type symbols with the best-effort underlying type string in
			// the Doc-adjacent slot via typeString().
			kind = graph.KindType
		}

		pos := fset.Position(ts.Pos())
		endPos := fset.Position(ts.End())
		doc := ""
		if d.Doc != nil {
			doc = d.Doc.Text()
		} else if ts.Comment != nil {
			doc = ts.Comment.Text()
		}

		sym := graph.SymbolNode{
			ID:               fmt.Sprintf("%s::%s", pkgImportPath, ts.Name.Name),
			Kind:             kind,
			Name:             ts.Name.Name,
			PackageName:      pkgName,
			File:             relPath,
			Line:             pos.Line,
			EndLine:          endPos.Line,
			Doc:              strings.TrimSpace(doc),
			InterfaceMethods: methods,
			StructFields:     fields,
			EmbeddedStructs:  embeds,
		}
		result.Symbols = append(result.Symbols, sym)
	}
}

// extractFuncDecl handles function and method declarations.
func extractFuncDecl(fset *token.FileSet, d *ast.FuncDecl, relPath, pkgName, pkgImportPath string, result *FileResult) {
	pos := fset.Position(d.Pos())
	endPos := fset.Position(d.End())

	receiver := ""
	kind := graph.KindFunction
	if d.Recv != nil && len(d.Recv.List) > 0 {
		kind = graph.KindMethod
		receiver = receiverString(d.Recv.List[0].Type)
	}

	doc := ""
	if d.Doc != nil {
		doc = d.Doc.Text()
	}

	sig := funcSignature(d)
	methodSig := ""
	arity := 0
	if d.Type != nil {
		methodSig = funcTypeSignature(d.Type)
		if d.Type.Params != nil {
			for _, field := range d.Type.Params.List {
				if len(field.Names) > 0 {
					arity += len(field.Names)
				} else {
					arity += 1
				}
			}
		}
	}
	id := fmt.Sprintf("%s::%s", pkgImportPath, d.Name.Name)
	if receiver != "" {
		id = fmt.Sprintf("%s::(%s).%s", pkgImportPath, receiver, d.Name.Name)
	}

	sym := graph.SymbolNode{
		ID:              id,
		Kind:            kind,
		Name:            d.Name.Name,
		Receiver:        receiver,
		PackageName:     pkgName,
		File:            relPath,
		Line:            pos.Line,
		EndLine:         endPos.Line,
		Doc:             strings.TrimSpace(doc),
		Signature:       sig,
		MethodSignature: methodSig,
		Arity:           arity,
	}
	result.Symbols = append(result.Symbols, sym)

	// Extract call edges, env reads, concurrency, and test edges from the body.
	if d.Body != nil {
		callerName := d.Name.Name
		if receiver != "" {
			callerName = fmt.Sprintf("(%s).%s", receiver, d.Name.Name)
		}
		isTestFunc := strings.HasPrefix(d.Name.Name, "Test") || strings.HasPrefix(d.Name.Name, "Benchmark")

		// Concurrency: inspect go statements and channel sends/receives.
		ast.Inspect(d.Body, func(n ast.Node) bool {
			switch stmt := n.(type) {
			case *ast.GoStmt:
				detail := calleeString(stmt.Call)
				result.Concurrency = append(result.Concurrency, graph.ConcurrencyNode{
					Kind:     "goroutine",
					Function: callerName,
					File:     relPath,
					Line:     fset.Position(stmt.Pos()).Line,
					Detail:   "go " + detail,
				})
			case *ast.SendStmt:
				result.Concurrency = append(result.Concurrency, graph.ConcurrencyNode{
					Kind:     "channel_send",
					Function: callerName,
					File:     relPath,
					Line:     fset.Position(stmt.Pos()).Line,
					Detail:   exprName(stmt.Chan) + " <-",
				})
			}
			return true
		})

		returnUsageMap := buildReturnUsageMap(d.Body)

		ast.Inspect(d.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			callPos := fset.Position(call.Pos())
			callee := calleeString(call)

			// Sync primitive detection using suffix matching so "w.mu.Lock",
			// "s.mu.Lock", etc. are all caught regardless of receiver variable name.
			switch {
			case strings.HasSuffix(callee, ".Lock") && !strings.HasSuffix(callee, ".RLock"):
				result.Concurrency = append(result.Concurrency, graph.ConcurrencyNode{
					Kind:     "mutex_lock",
					Function: callerName,
					File:     relPath,
					Line:     callPos.Line,
					Detail:   callee,
				})
			case strings.HasSuffix(callee, ".RLock"):
				result.Concurrency = append(result.Concurrency, graph.ConcurrencyNode{
					Kind:     "mutex_lock",
					Function: callerName,
					File:     relPath,
					Line:     callPos.Line,
					Detail:   callee,
				})
			case strings.HasSuffix(callee, ".Unlock") && !strings.HasSuffix(callee, ".RUnlock"):
				result.Concurrency = append(result.Concurrency, graph.ConcurrencyNode{
					Kind:     "mutex_unlock",
					Function: callerName,
					File:     relPath,
					Line:     callPos.Line,
					Detail:   callee,
				})
			case strings.HasSuffix(callee, ".RUnlock"):
				result.Concurrency = append(result.Concurrency, graph.ConcurrencyNode{
					Kind:     "mutex_unlock",
					Function: callerName,
					File:     relPath,
					Line:     callPos.Line,
					Detail:   callee,
				})
			case strings.HasSuffix(callee, ".Add"):
				result.Concurrency = append(result.Concurrency, graph.ConcurrencyNode{
					Kind:     "waitgroup_add",
					Function: callerName,
					File:     relPath,
					Line:     callPos.Line,
					Detail:   callee,
				})
			case strings.HasSuffix(callee, ".Wait"):
				result.Concurrency = append(result.Concurrency, graph.ConcurrencyNode{
					Kind:     "waitgroup_wait",
					Function: callerName,
					File:     relPath,
					Line:     callPos.Line,
					Detail:   callee,
				})
			case strings.HasSuffix(callee, ".Do"):
				result.Concurrency = append(result.Concurrency, graph.ConcurrencyNode{
					Kind:     "once_do",
					Function: callerName,
					File:     relPath,
					Line:     callPos.Line,
					Detail:   callee,
				})
			}

			// Env-var detection.
			if ev, ok := envRead(call, callee, callPos.Line, relPath, callerName); ok {
				result.Env = append(result.Env, ev)
			}

			// SQL Extraction
			if callee != "" {
				parts := strings.Split(callee, ".")
				method := parts[len(parts)-1]
				isSQLMethod := method == "Query" || method == "QueryRow" || method == "Exec" || method == "QueryContext" || method == "QueryRowContext" || method == "ExecContext" || method == "Raw"

				if isSQLMethod && len(call.Args) > 0 {
					var queryStr string
					var found bool

					for _, arg := range call.Args {
						if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
							queryStr = strings.Trim(lit.Value, "`\"")
							found = true
							break
						} else if id, ok := arg.(*ast.Ident); ok {
							if val, ok := resolveStringLiteral(d.Body, id.Name); ok {
								queryStr = val
								found = true
								break
							}
						}
					}

					if found {
						upperQ := strings.ToUpper(queryStr)
						if strings.Contains(upperQ, "SELECT ") || strings.Contains(upperQ, "INSERT ") ||
							strings.Contains(upperQ, "UPDATE ") || strings.Contains(upperQ, "DELETE ") ||
							strings.Contains(upperQ, "WITH ") || strings.Contains(upperQ, "CREATE ") ||
							strings.Contains(upperQ, "ALTER ") || strings.Contains(upperQ, "DROP ") {

							result.SQLs = append(result.SQLs, graph.SQLEdge{
								Query:    queryStr,
								Function: callerName,
								File:     relPath,
								Line:     callPos.Line,
							})
						}
					}
				}
			}

			// Error/Panic Extraction
			if callee == "panic" || strings.Contains(callee, "Errorf") || strings.Contains(callee, "New") {
				for _, arg := range call.Args {
					if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						result.Errors = append(result.Errors, graph.ErrorEdge{
							Message:  strings.Trim(lit.Value, "\""),
							Function: callerName,
							File:     relPath,
							Line:     callPos.Line,
						})
						break
					}
				}
			}

			// HTTP Client Call Extraction
			if method, ok := extractHTTPMethod(callee, call); ok && len(call.Args) >= 1 {
				urlExpr := call.Args[0]
				var urlStr string
				hasDynamic := true

				if lit, ok2 := urlExpr.(*ast.BasicLit); ok2 && lit.Kind == token.STRING {
					urlStr = strings.Trim(lit.Value, "\"")
					hasDynamic = false
				} else {
					urlStr = exprName(urlExpr)
					if urlStr == "" {
						urlStr = "<dynamic>"
					}
				}

				var staticSegments []string
				if !hasDynamic {
					staticSegments = extractStaticSegments(urlStr)
				}

				result.HttpCalls = append(result.HttpCalls, graph.HttpCallEdge{
					SourceFile:     relPath,
					SourceLine:     callPos.Line,
					FunctionName:   callerName,
					Method:         method,
					URL:            urlStr,
					StaticSegments: staticSegments,
					HasDynamic:     hasDynamic,
				})
			}

			// Test edge: record which production symbols a test calls.
			if isTestFunc && callee != "" && !strings.HasPrefix(callee, "t.") && !strings.HasPrefix(callee, "b.") {
				result.TestEdges = append(result.TestEdges, graph.TestEdge{
					TestFunc: d.Name.Name,
					Target:   callee,
					File:     relPath,
					Line:     callPos.Line,
				})
			}

			if callee != "" {
				// Skip edges for calls whose Fun expression we couldn't
				// resolve to any useful name. Previously calleeString
				// returned the literal "<complex call>" for these and
				// that string surfaced verbatim in callees/concurrency
				// output, polluting every report with parser jargon.
				result.Calls = append(result.Calls, graph.CallEdge{
					CallerSymbolID: sym.ID,
					CallerName:     callerName,
					CalleeRaw:      callee,
					File:           relPath,
					Line:           callPos.Line,
					ReturnUsage:    returnUsageMap[call.Pos()],
				})
			}

			// Additionally, emit "potential call" edges for any function or
			// method value passed as an argument to this call. Without these,
			// reachability analysis loses entire chains of middleware-wrapped
			// handlers, callbacks (jwt KeyFunc, MCP tool handlers, http
			// middleware, table-driven test scenarios, etc.) because gograph
			// has no way to know the callee will eventually invoke them.
			//
			// This is a deliberate, conservative over-approximation: a
			// false-positive edge (treating a passed-but-never-invoked value
			// as called) is strictly safer than a missing edge (treating live
			// callback code as dead). The reuse of extractHandlerRefs keeps
			// the recovery logic in one place — the helper recursively walks
			// nested call expressions, so `wrap1(wrap2(h.method))` correctly
			// yields an edge to `h.method`.
			for _, ref := range extractHandlerRefs(call) {
				result.Calls = append(result.Calls, graph.CallEdge{
					CallerSymbolID: sym.ID,
					CallerName:     callerName,
					CalleeRaw:      ref,
					File:           relPath,
					Line:           callPos.Line,
				})
			}
			return true
		})

		// Mutations Extraction
		ast.Inspect(d.Body, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			// Function-value-as-RHS edge emission. Patterns covered:
			//   encoderConfig.EncodeLevel = coloredLevelEncoder
			//   opts.OnEvent = h.method
			//   localFn := h.method
			//   x, y := f, g
			//   opts := Config{OnEvent: h.method}  // CompositeLit RHS
			// All of these put a function value somewhere that will be
			// invoked later by another code path. Emit a call edge from the
			// enclosing function so reachability tracks the value through
			// the assignment. See Bug 1 in the gograph audit.
			for _, rhs := range assign.Rhs {
				var refs []string
				collectFuncRefs(rhs, &refs)
				for _, ref := range refs {
					result.Calls = append(result.Calls, graph.CallEdge{
						CallerSymbolID: sym.ID,
						CallerName:     callerName,
						CalleeRaw:      ref,
						File:           relPath,
						Line:           fset.Position(assign.Pos()).Line,
					})
				}
			}
			// Only track actual assignments, not declarations (:=) for global mutations
			isDecl := assign.Tok == token.DEFINE

			for _, lhs := range assign.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok {
					result.Mutations = append(result.Mutations, graph.MutationEdge{
						Field:    sel.Sel.Name,
						Function: callerName,
						File:     relPath,
						Line:     fset.Position(assign.Pos()).Line,
					})
				} else if id, ok := lhs.(*ast.Ident); ok && !isDecl {
					result.Mutations = append(result.Mutations, graph.MutationEdge{
						Field:    id.Name,
						Function: callerName,
						File:     relPath,
						Line:     fset.Position(assign.Pos()).Line,
					})
				}
			}
			return true
		})

		// Struct Literal Extraction — finds Foo{...} composite literal sites.
		ast.Inspect(d.Body, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			typeName := compositeLitTypeName(lit)
			if typeName == "" {
				return true
			}
			result.Literals = append(result.Literals, graph.LiteralEdge{
				TypeName: typeName,
				Function: callerName,
				File:     relPath,
				Line:     fset.Position(lit.Pos()).Line,
			})
			return true
		})
	}
}

// buildReturnUsageMap walks a function body at the statement level and returns
// a map from each direct CallExpr position to a usage label describing how the
// caller consumes the call's return value.
// Values: "discarded", "assigned", "partially_ignored", "returned",
//
//	"goroutine", "deferred". Calls nested inside other expressions are
//	not recorded (they are "passed" by convention at query time).
func buildReturnUsageMap(body *ast.BlockStmt) map[token.Pos]string {
	m := make(map[token.Pos]string)
	if body == nil {
		return m
	}
	classifyStmtListUsage(body.List, m)
	return m
}

func classifyStmtListUsage(stmts []ast.Stmt, m map[token.Pos]string) {
	for _, s := range stmts {
		classifyOneStmtUsage(s, m)
	}
}

func classifyOneStmtUsage(stmt ast.Stmt, m map[token.Pos]string) {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		if call, ok := s.X.(*ast.CallExpr); ok {
			m[call.Pos()] = "discarded"
		}
	case *ast.AssignStmt:
		for _, rhs := range s.Rhs {
			if call, ok := rhs.(*ast.CallExpr); ok {
				hasBlank := false
				for _, lhs := range s.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Name == "_" {
						hasBlank = true
						break
					}
				}
				if hasBlank {
					m[call.Pos()] = "partially_ignored"
				} else {
					m[call.Pos()] = "assigned"
				}
			}
		}
	case *ast.ReturnStmt:
		for _, res := range s.Results {
			if call, ok := res.(*ast.CallExpr); ok {
				m[call.Pos()] = "returned"
			}
		}
	case *ast.GoStmt:
		m[s.Call.Pos()] = "goroutine"
	case *ast.DeferStmt:
		m[s.Call.Pos()] = "deferred"
	case *ast.IfStmt:
		if s.Init != nil {
			classifyOneStmtUsage(s.Init, m)
		}
		classifyStmtListUsage(s.Body.List, m)
		if s.Else != nil {
			if block, ok := s.Else.(*ast.BlockStmt); ok {
				classifyStmtListUsage(block.List, m)
			} else {
				classifyOneStmtUsage(s.Else, m)
			}
		}
	case *ast.ForStmt:
		if s.Init != nil {
			classifyOneStmtUsage(s.Init, m)
		}
		if s.Post != nil {
			classifyOneStmtUsage(s.Post, m)
		}
		classifyStmtListUsage(s.Body.List, m)
	case *ast.RangeStmt:
		classifyStmtListUsage(s.Body.List, m)
	case *ast.SwitchStmt:
		if s.Init != nil {
			classifyOneStmtUsage(s.Init, m)
		}
		for _, c := range s.Body.List {
			if cc, ok := c.(*ast.CaseClause); ok {
				classifyStmtListUsage(cc.Body, m)
			}
		}
	case *ast.TypeSwitchStmt:
		if s.Init != nil {
			classifyOneStmtUsage(s.Init, m)
		}
		for _, c := range s.Body.List {
			if cc, ok := c.(*ast.CaseClause); ok {
				classifyStmtListUsage(cc.Body, m)
			}
		}
	case *ast.SelectStmt:
		for _, c := range s.Body.List {
			if cc, ok := c.(*ast.CommClause); ok {
				if cc.Comm != nil {
					classifyOneStmtUsage(cc.Comm, m)
				}
				classifyStmtListUsage(cc.Body, m)
			}
		}
	case *ast.BlockStmt:
		classifyStmtListUsage(s.List, m)
	}
}

// compositeLitTypeName returns the type name from a composite literal, or ""
// if the literal has no explicit type (anonymous) or is not a named struct type.
func compositeLitTypeName(lit *ast.CompositeLit) string {
	if lit.Type == nil {
		return ""
	}
	switch t := lit.Type.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	}
	return ""
}

// receiverString converts an AST receiver type expression to a short string.
//
// Handles four forms a method receiver can take:
//   - *ast.Ident          — non-generic receiver:    func (c Cache) ...
//   - *ast.StarExpr       — pointer:                 func (c *Cache) ...
//   - *ast.IndexExpr      — single-param generic:    func (c *List[T]) ...
//   - *ast.IndexListExpr  — multi-param generic:     func (c *Cache[K, V]) ...
//
// Without the IndexListExpr case, multi-param generic methods fell through
// to the %T default and produced symbol IDs like
// "pkg::(**ast.IndexListExpr).Get" — gibberish that broke gograph's symbol
// table for any modern Go codebase using parametric data structures
// (Bug 9.A — discovered via synthetic test repo when Bug 6 closed).
func receiverString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return "*" + receiverString(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return receiverString(t.X)
	case *ast.IndexListExpr:
		return receiverString(t.X)
	default:
		return fmt.Sprintf("%T", expr)
	}
}

// calleeString converts a call expression to a compact string representation.
func calleeString(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		obj := exprName(fn.X)
		if obj == "" {
			return fn.Sel.Name
		}
		return obj + "." + fn.Sel.Name
	case *ast.CallExpr:
		// Curried/returned-function invocation: getFunc()() — recurse to
		// extract the innermost callable name. The outer call's result is
		// the callee here; if we can identify what it returns, that's the
		// useful symbol to attribute.
		return calleeString(fn)
	case *ast.IndexExpr:
		// Generic function instantiation as callee: Foo[int]() or
		// fnMap[key](). For instantiation, fn.X is the function reference;
		// for map/slice indexing, fn.X is the container and we can't
		// statically know the value. Either way, fn.X is the best we have.
		switch x := fn.X.(type) {
		case *ast.Ident:
			return x.Name
		case *ast.SelectorExpr:
			return exprName(x)
		}
		return ""
	case *ast.IndexListExpr:
		// Generic function with multiple type parameters: Foo[A, B]().
		switch x := fn.X.(type) {
		case *ast.Ident:
			return x.Name
		case *ast.SelectorExpr:
			return exprName(x)
		}
		return ""
	case *ast.TypeAssertExpr:
		// Type-asserted call: x.(*T).Method() — fn is the type assertion;
		// the method selector is one level out and already handled by the
		// SelectorExpr case above. If we land here it means the assertion
		// itself is being invoked (function-typed assertion), which is
		// rare and unresolvable without type info — return empty.
		return ""
	case *ast.ParenExpr:
		// (someExpr)() — strip parens and recurse.
		return calleeString(&ast.CallExpr{Fun: fn.X})
	case *ast.FuncLit:
		// Immediately-invoked function literal: func(){...}(). No symbol
		// name to attribute; return empty so the edge is dropped rather
		// than polluting downstream output with a placeholder.
		return ""
	default:
		// Unknown shape — return empty so the caller can decide to skip the
		// edge. Returning a placeholder like "<complex call>" caused that
		// literal to leak verbatim into user-facing output (callees,
		// concurrency, etc.).
		return ""
	}
}

// extractHandlerRefs walks a CallExpr's arguments recursively and returns
// any callable references found — method values (h.method), qualified
// function references (pkg.Func), or bare function identifiers.
//
// Used by the HTTP-route extractor and by call-edge extraction to recover
// the *real* handler/callback from indirection patterns like:
//
//	mux.HandleFunc("/p", guard(h.method))            // wrapper call
//	mux.HandleFunc("/p", wrap1(wrap2(h.method)))     // nested wrappers
//	register(opts{OnSuccess: h.method})              // function value in struct literal
//	jwt.Parse(token, km.keyFunc)                     // method value as arg
//
// where the inner method value is what will actually be invoked later. Without
// this, gograph would record only the outer call's name and downstream
// reachability would never mark the inner method as reachable.
//
// Returned strings are in `receiver.method` or `pkg.func` form (best-
// effort via exprName). They flow through search.normalizeSymbolName
// during orphan analysis, which strips them down to the bare
// method/function name for matching against symbol IDs.
func extractHandlerRefs(call *ast.CallExpr) []string {
	var refs []string
	for _, a := range call.Args {
		collectFuncRefs(a, &refs)
	}
	return refs
}

// collectFuncRefs walks an arbitrary expression and appends every
// function/method-value reference it finds to `out`. It is the workhorse
// behind extractHandlerRefs and is also used directly by AssignStmt and
// composite-literal scanners to recover function values that reach
// later-invoked sites through fields and assignments rather than direct
// call arguments.
//
// Recurses through:
//   - *ast.CallExpr      — nested wrappers like wrap1(wrap2(h.method))
//   - *ast.CompositeLit  — struct/map/slice literals carrying function fields,
//     e.g. opts{OnEvent: h.method}
//   - *ast.KeyValueExpr  — for each key-value pair, scan the value
//   - *ast.UnaryExpr     — &Foo{...} pointer-to-literal
//   - *ast.ParenExpr     — (h.method)
//
// Records:
//   - *ast.Ident         — bare function reference (filters nil/true/false/iota)
//   - *ast.SelectorExpr  — method value or qualified function (h.method, pkg.Foo)
func collectFuncRefs(e ast.Expr, out *[]string) {
	switch x := e.(type) {
	case *ast.Ident:
		switch x.Name {
		case "nil", "true", "false", "iota", "":
			return
		}
		*out = append(*out, x.Name)
	case *ast.SelectorExpr:
		if n := exprName(x); n != "" {
			*out = append(*out, n)
		}
	case *ast.CallExpr:
		for _, a := range x.Args {
			collectFuncRefs(a, out)
		}
	case *ast.CompositeLit:
		for _, elt := range x.Elts {
			collectFuncRefs(elt, out)
		}
	case *ast.KeyValueExpr:
		// Map/struct field — only the value side can be a function ref;
		// the key is either a field name (Ident) or a constant (BasicLit).
		collectFuncRefs(x.Value, out)
	case *ast.UnaryExpr:
		// &Foo{...} — recurse into operand.
		collectFuncRefs(x.X, out)
	case *ast.ParenExpr:
		collectFuncRefs(x.X, out)
	}
}

// exprName returns a best-effort single-identifier name for an expression.
func exprName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		prefix := exprName(e.X)
		if prefix == "" {
			return e.Sel.Name
		}
		return prefix + "." + e.Sel.Name
	default:
		return ""
	}
}

// funcSignature builds a best-effort human-readable signature string.
func funcSignature(d *ast.FuncDecl) string {
	var sb strings.Builder
	sb.WriteString("func ")
	if d.Recv != nil && len(d.Recv.List) > 0 {
		sb.WriteString("(")
		sb.WriteString(receiverString(d.Recv.List[0].Type))
		sb.WriteString(") ")
	}
	sb.WriteString(d.Name.Name)
	sb.WriteString("(")
	if d.Type.Params != nil {
		sb.WriteString(fieldListString(d.Type.Params))
	}
	sb.WriteString(")")
	if d.Type.Results != nil && len(d.Type.Results.List) > 0 {
		sb.WriteString(" (")
		sb.WriteString(fieldListString(d.Type.Results))
		sb.WriteString(")")
	}
	return sb.String()
}

// funcTypeSignature builds just the parameter and return type signature (names omitted for duck-typing)
func funcTypeSignature(ft *ast.FuncType) string {
	var sb strings.Builder
	sb.WriteString("func(")
	if ft.Params != nil {
		sb.WriteString(typeOnlyFieldListString(ft.Params))
	}
	sb.WriteString(")")
	if ft.Results != nil && len(ft.Results.List) > 0 {
		sb.WriteString(" (")
		sb.WriteString(typeOnlyFieldListString(ft.Results))
		sb.WriteString(")")
	}
	return sb.String()
}

// typeOnlyFieldListString converts an ast.FieldList to a string of types only, ignoring parameter names.
func typeOnlyFieldListString(fl *ast.FieldList) string {
	parts := make([]string, 0, len(fl.List))
	for _, f := range fl.List {
		typStr := typeString(f.Type)
		if len(f.Names) == 0 {
			parts = append(parts, typStr)
		} else {
			for range f.Names {
				parts = append(parts, typStr)
			}
		}
	}
	return strings.Join(parts, ", ")
}

// fieldListString converts an ast.FieldList to a compact string.
func fieldListString(fl *ast.FieldList) string {
	parts := make([]string, 0, len(fl.List))
	for _, f := range fl.List {
		typStr := typeString(f.Type)
		if len(f.Names) == 0 {
			parts = append(parts, typStr)
		} else {
			names := make([]string, len(f.Names))
			for i, n := range f.Names {
				names[i] = n.Name
			}
			parts = append(parts, strings.Join(names, ", ")+" "+typStr)
		}
	}
	return strings.Join(parts, ", ")
}

// typeString converts a type expression to a compact string.
func typeString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + typeString(t.X)
	case *ast.ArrayType:
		return "[]" + typeString(t.Elt)
	case *ast.MapType:
		return "map[" + typeString(t.Key) + "]" + typeString(t.Value)
	case *ast.SelectorExpr:
		return exprName(t.X) + "." + t.Sel.Name
	case *ast.Ellipsis:
		return "..." + typeString(t.Elt)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.FuncType:
		return "func(...)"
	case *ast.ChanType:
		return "chan " + typeString(t.Value)
	case *ast.IndexExpr:
		return typeString(t.X)
	default:
		return "_"
	}
}

// envAccessors is the set of function call patterns we detect for env reads.
var envAccessors = map[string]bool{
	"os.Getenv":       true,
	"os.LookupEnv":    true,
	"viper.GetString": true,
	"viper.GetBool":   true,
	"viper.GetInt":    true,
	"viper.Get":       true,
}

// envRead attempts to detect an environment variable read in a call expression.
func envRead(call *ast.CallExpr, callee string, line int, file, fn string) (graph.EnvRead, bool) {
	if !envAccessors[callee] {
		return graph.EnvRead{}, false
	}
	if len(call.Args) == 0 {
		return graph.EnvRead{}, false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok {
		return graph.EnvRead{}, false
	}
	key := strings.Trim(lit.Value, `"`)
	return graph.EnvRead{
		Key:      key,
		Accessor: callee,
		File:     file,
		Line:     line,
		Function: fn,
	}, true
}

// extractHTTPMethod checks whether a call expression is an HTTP client call
// and returns the HTTP method (GET, POST, etc.) if so.
func extractHTTPMethod(callee string, call *ast.CallExpr) (string, bool) {
	switch callee {
	case "http.Get":
		return "GET", true
	case "http.Post":
		return "POST", true
	case "http.PostForm":
		return "POST", true
	case "http.Head":
		return "HEAD", true
	}
	// Check for *http.Client method calls by method name suffix.
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	switch sel.Sel.Name {
	case "Get":
		return "GET", true
	case "Post":
		return "POST", true
	case "PostForm":
		return "POST", true
	case "Head":
		return "HEAD", true
	case "Do":
		return "GET", true
	}
	return "", false
}

// extractStaticSegments parses a URL string and returns non-empty path segments.
func extractStaticSegments(rawURL string) []string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func resolveStringLiteral(body *ast.BlockStmt, identName string) (string, bool) {
	if body == nil {
		return "", false
	}
	var found string
	var ok bool
	ast.Inspect(body, func(n ast.Node) bool {
		if ok {
			return false
		}
		assign, isAssign := n.(*ast.AssignStmt)
		if !isAssign {
			return true
		}
		for i, lhs := range assign.Lhs {
			if id, isId := lhs.(*ast.Ident); isId && id.Name == identName {
				if i < len(assign.Rhs) {
					rhs := assign.Rhs[i]
					if lit, isLit := rhs.(*ast.BasicLit); isLit && lit.Kind == token.STRING {
						found = strings.Trim(lit.Value, "`\"")
						ok = true
						return false
					}
				}
			}
		}
		return true
	})
	return found, ok
}
