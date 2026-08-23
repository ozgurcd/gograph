package precise

import (
	"fmt"
	"go/ast"
	"go/types"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/buildctx"
	"github.com/ozgurcd/gograph/internal/graph"
	"golang.org/x/tools/go/packages"
)

type typedTestTarget struct {
	id         string
	resolution graph.CallResolution
}

type testCallSite struct {
	file         string
	line, column int
}

// enrichTypedTestCalls is deliberately separate from the production CHA/SSA
// pass. Test packages are useful for attributing coverage, but a broken test
// package must not turn an otherwise complete production graph into a precise
// fallback. Successful packages enrich exact and bounded-possible test call
// targets; omitted or ill-typed tests leave their parser edges unresolved and
// produce typed_partial capability metadata.
func enrichTypedTestCalls(absRoot string, g *graph.Graph, config buildctx.Config) (graph.TestCallResolutionMode, error) {
	if g == nil {
		return graph.TestCallResolutionPartial, fmt.Errorf("cannot resolve test calls in a nil graph")
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedTypesInfo | packages.NeedSyntax,
		Dir:        absRoot,
		Env:        config.Environment(),
		BuildFlags: config.Flags(),
		Tests:      true,
	}
	loaded, loadErr := packages.Load(cfg, "./...")
	if loadErr != nil {
		return graph.TestCallResolutionPartial, fmt.Errorf("load test packages: %w", loadErr)
	}

	symbolIDs := make(map[string]struct{}, len(g.Symbols))
	for _, symbol := range g.Symbols {
		symbolIDs[symbol.ID] = struct{}{}
	}

	selectedTests := make(map[string]struct{})
	for _, file := range g.Files {
		if strings.HasSuffix(filepath.ToSlash(file.Path), "_test.go") {
			selectedTests[filepath.Clean(file.Path)] = struct{}{}
		}
	}
	seenTests := make(map[string]struct{})
	resolved := make(map[testCallSite]map[string]graph.CallResolution)
	var problems []string
	problemSeen := make(map[string]struct{})

	for _, pkg := range loaded {
		for _, pkgErr := range pkg.Errors {
			message := strings.Join(strings.Fields(pkgErr.Msg), " ")
			if message == "" {
				message = "unknown package error"
			}
			problem := pkg.PkgPath + ": " + message
			if _, exists := problemSeen[problem]; !exists {
				problemSeen[problem] = struct{}{}
				problems = append(problems, problem)
			}
		}
		if pkg.Fset == nil || pkg.TypesInfo == nil {
			continue
		}
		for _, syntax := range pkg.Syntax {
			position := pkg.Fset.Position(syntax.Pos())
			rel, ok := repositoryTestPath(absRoot, position.Filename)
			if !ok {
				continue
			}
			seenTests[rel] = struct{}{}
			resolveTestFileCalls(pkg, syntax, rel, symbolIDs, g, resolved)
		}
	}

	applyTypedTestTargets(g, resolved)

	for testFile := range selectedTests {
		if _, ok := seenTests[testFile]; !ok {
			problems = append(problems, "test package loading omitted "+testFile)
		}
	}
	if len(problems) == 0 {
		return graph.TestCallResolutionTyped, nil
	}
	sort.Strings(problems)
	const limit = 3
	details := problems
	if len(details) > limit {
		details = details[:limit]
	}
	message := strings.Join(details, "; ")
	if remaining := len(problems) - len(details); remaining > 0 {
		message += fmt.Sprintf("; and %d more", remaining)
	}
	return graph.TestCallResolutionPartial, fmt.Errorf("typed test call resolution reported %d problem(s): %s", len(problems), message)
}

func repositoryTestPath(root, path string) (string, bool) {
	if path == "" {
		return "", false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) || !strings.HasSuffix(filepath.ToSlash(rel), "_test.go") {
		return "", false
	}
	return filepath.Clean(rel), true
}

func resolveTestFileCalls(pkg *packages.Package, file *ast.File, rel string, symbolIDs map[string]struct{}, g *graph.Graph, resolved map[testCallSite]map[string]graph.CallResolution) {
	bindings := make(map[types.Object][]typedTestTarget)
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			if len(value.Lhs) != len(value.Rhs) {
				return true
			}
			for index, lhs := range value.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok {
					continue
				}
				object := pkg.TypesInfo.Defs[ident]
				if object == nil {
					object = pkg.TypesInfo.Uses[ident]
				}
				if object == nil {
					continue
				}
				targets := resolveTypedTestExpr(pkg.TypesInfo, value.Rhs[index], bindings, symbolIDs, g)
				bindings[object] = mergeTypedTestTargets(bindings[object], targets)
			}
		case *ast.ValueSpec:
			if len(value.Names) != len(value.Values) {
				return true
			}
			for index, ident := range value.Names {
				object := pkg.TypesInfo.Defs[ident]
				if object == nil {
					continue
				}
				targets := resolveTypedTestExpr(pkg.TypesInfo, value.Values[index], bindings, symbolIDs, g)
				bindings[object] = mergeTypedTestTargets(bindings[object], targets)
			}
		}
		return true
	})

	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		position := pkg.Fset.Position(call.Lparen)
		if position.Line <= 0 || position.Column <= 0 {
			return true
		}
		targets := resolveTypedTestExpr(pkg.TypesInfo, call.Fun, bindings, symbolIDs, g)
		if len(targets) == 0 {
			return true
		}
		site := testCallSite{file: rel, line: position.Line, column: position.Column}
		if resolved[site] == nil {
			resolved[site] = make(map[string]graph.CallResolution)
		}
		for _, target := range targets {
			current := resolved[site][target.id]
			if current == "" || current == graph.CallResolutionCHA && target.resolution == graph.CallResolutionStatic {
				resolved[site][target.id] = target.resolution
			}
		}
		return true
	})
}

func resolveTypedTestExpr(info *types.Info, expression ast.Expr, bindings map[types.Object][]typedTestTarget, symbolIDs map[string]struct{}, g *graph.Graph) []typedTestTarget {
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return resolveTypedTestExpr(info, value.X, bindings, symbolIDs, g)
	case *ast.IndexExpr:
		return resolveTypedTestExpr(info, value.X, bindings, symbolIDs, g)
	case *ast.IndexListExpr:
		return resolveTypedTestExpr(info, value.X, bindings, symbolIDs, g)
	case *ast.Ident:
		object := info.Uses[value]
		if function, ok := object.(*types.Func); ok {
			return repositoryFunctionTarget(function, symbolIDs)
		}
		return append([]typedTestTarget(nil), bindings[object]...)
	case *ast.SelectorExpr:
		if selection := info.Selections[value]; selection != nil {
			function, ok := selection.Obj().(*types.Func)
			if !ok {
				return nil
			}
			if interfaceID := namedInterfaceID(selection.Recv()); interfaceID != "" {
				return interfaceMethodTargets(interfaceID, function.Name(), g)
			}
			return repositoryFunctionTarget(function, symbolIDs)
		}
		if function, ok := info.Uses[value.Sel].(*types.Func); ok {
			return repositoryFunctionTarget(function, symbolIDs)
		}
	}
	return nil
}

func repositoryFunctionTarget(function *types.Func, symbolIDs map[string]struct{}) []typedTestTarget {
	id := typesFunctionSymbolID(function)
	if _, ok := symbolIDs[id]; !ok {
		return nil
	}
	return []typedTestTarget{{id: id, resolution: graph.CallResolutionStatic}}
}

func typesFunctionSymbolID(function *types.Func) string {
	if function == nil || function.Pkg() == nil || function.Name() == "" {
		return ""
	}
	signature, _ := function.Type().(*types.Signature)
	if signature != nil && signature.Recv() != nil {
		return fmt.Sprintf("%s::(%s).%s", function.Pkg().Path(), formatReceiverType(signature.Recv().Type()), function.Name())
	}
	return function.Pkg().Path() + "::" + function.Name()
}

func namedInterfaceID(value types.Type) string {
	if pointer, ok := value.(*types.Pointer); ok {
		value = pointer.Elem()
	}
	named, ok := value.(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return ""
	}
	if _, ok := named.Underlying().(*types.Interface); !ok {
		return ""
	}
	return named.Obj().Pkg().Path() + "::" + named.Obj().Name()
}

func interfaceMethodTargets(interfaceID, method string, g *graph.Graph) []typedTestTarget {
	concreteNames := make(map[string]struct{})
	for _, implementation := range g.Implements {
		if implementation.InterfaceID != interfaceID || implementation.ConcreteID == "" {
			continue
		}
		name := implementation.ConcreteID
		if index := strings.LastIndex(name, "::"); index >= 0 {
			name = name[index+2:]
		}
		concreteNames[strings.TrimPrefix(strings.Trim(name, "()"), "*")] = struct{}{}
	}
	var targets []typedTestTarget
	for _, symbol := range g.Symbols {
		if symbol.Kind != graph.KindMethod || symbol.Name != method {
			continue
		}
		receiver := strings.TrimPrefix(strings.TrimSpace(symbol.Receiver), "*")
		if _, ok := concreteNames[receiver]; ok {
			targets = append(targets, typedTestTarget{id: symbol.ID, resolution: graph.CallResolutionCHA})
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].id < targets[j].id })
	return targets
}

func mergeTypedTestTargets(existing, added []typedTestTarget) []typedTestTarget {
	byID := make(map[string]graph.CallResolution, len(existing)+len(added))
	for _, target := range append(append([]typedTestTarget(nil), existing...), added...) {
		resolution := byID[target.id]
		if resolution == "" || resolution == graph.CallResolutionCHA && target.resolution == graph.CallResolutionStatic {
			byID[target.id] = target.resolution
		}
	}
	result := make([]typedTestTarget, 0, len(byID))
	for id, resolution := range byID {
		result = append(result, typedTestTarget{id: id, resolution: resolution})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].id < result[j].id })
	if len(result) > 1 {
		for index := range result {
			result[index].resolution = graph.CallResolutionCHA
		}
	}
	return result
}

func applyTypedTestTargets(g *graph.Graph, resolved map[testCallSite]map[string]graph.CallResolution) {
	var testEdges []graph.TestEdge
	for _, edge := range g.TestEdges {
		targets := sortedTestSiteTargets(resolved[testCallSite{file: filepath.Clean(edge.File), line: edge.Line, column: edge.Column}])
		if len(targets) == 0 {
			testEdges = append(testEdges, edge)
			continue
		}
		for _, target := range targets {
			resolvedEdge := edge
			resolvedEdge.TargetSymbolID = target.id
			resolvedEdge.Resolution = target.resolution
			testEdges = append(testEdges, resolvedEdge)
		}
	}
	g.TestEdges = testEdges

	var calls []graph.CallEdge
	for _, call := range g.Calls {
		if call.Synthetic || !strings.HasSuffix(filepath.ToSlash(call.File), "_test.go") {
			calls = append(calls, call)
			continue
		}
		targets := sortedTestSiteTargets(resolved[testCallSite{file: filepath.Clean(call.File), line: call.Line, column: call.Column}])
		if len(targets) == 0 {
			calls = append(calls, call)
			continue
		}
		for index, target := range targets {
			resolvedCall := call
			resolvedCall.CalleeSymbolID = target.id
			resolvedCall.Resolution = target.resolution
			resolvedCall.Precise = call.Precise || index > 0
			calls = append(calls, resolvedCall)
		}
	}
	g.Calls = calls
}

func sortedTestSiteTargets(targets map[string]graph.CallResolution) []typedTestTarget {
	result := make([]typedTestTarget, 0, len(targets))
	for id, resolution := range targets {
		result = append(result, typedTestTarget{id: id, resolution: resolution})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result
}
