package precise

import (
	"context"
	"encoding/json"
	"fmt"
	"go/token"
	"go/types"
	"maps"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/buildctx"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/scanner"
	"github.com/ozgurcd/gograph/internal/sourcefs"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Enrich applies type-checked precision to the graph.
// It loads the project via go/packages, finds exact interface implementers,
// and uses Class Hierarchy Analysis (CHA) to add precise dynamic dispatch call edges.
func Enrich(absRoot string, g *graph.Graph) error {
	config, err := buildctx.Resolve(context.Background(), absRoot)
	if err != nil {
		return err
	}
	return EnrichWithConfig(absRoot, g, config)
}

// EnrichWithConfig applies type-checked precision using the same effective Go
// build configuration that selected files for the AST graph.
func EnrichWithConfig(absRoot string, g *graph.Graph, config buildctx.Config) error {
	if g == nil {
		return fmt.Errorf("cannot enrich a nil graph")
	}
	payload, err := json.Marshal(g)
	if err != nil {
		return fmt.Errorf("copy AST graph for precise analysis: %w", err)
	}
	var enriched graph.Graph
	if err := json.Unmarshal(payload, &enriched); err != nil {
		return fmt.Errorf("copy AST graph for precise analysis: %w", err)
	}
	if err := enrichWithConfig(absRoot, &enriched, config); err != nil {
		return err
	}
	*g = enriched
	return nil
}

func enrichWithConfig(absRoot string, g *graph.Graph, config buildctx.Config) error {
	if err := scanner.ValidateToolchainSourceInputs(absRoot); err != nil {
		return fmt.Errorf("refusing precise analysis of unsafe repository or Go tool input: %w", err)
	}
	analysisRoot := absRoot
	if config.ModuleRoot() != "" {
		if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
			analysisRoot = resolved
		}
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedSyntax,
		Dir:        analysisRoot,
		Env:        config.Environment(),
		BuildFlags: config.Flags(),
	}

	// Load all packages
	initial, err := packages.Load(cfg, "./...")
	if err != nil {
		return fmt.Errorf("packages.Load failed: %w", err)
	}
	if err := packageLoadError(initial); err != nil {
		return err
	}
	if err := validateLoadedSourcePaths(analysisRoot, initial); err != nil {
		return err
	}
	if err := validatePackageCoverage(analysisRoot, initial, g); err != nil {
		return err
	}

	// Build SSA. ssa.InstantiateGenerics monomorphises generic functions
	// and methods so CHA can see their call sites with source positions
	// (Bug 9.B). Without it, every call into or out of a generic body
	// produces a synthetic edge with edge.Site == nil — which the CHA
	// loop below skips, leaving CalleeSymbolID empty on generic-touching
	// edges. With it, CHA emits one fully-resolved edge per instantiation,
	// each carrying a real *ssa.CallCommon site we can position-match
	// against the parser's AST edges.
	// Build SSA bodies only for the selected repository packages. Imported
	// package types and callable references remain available to local SSA, but
	// constructing every dependency body makes CHA retain an otherwise unused
	// transitive program. It also creates source-less synthetic forwarding from
	// dependency wrappers into unrelated same-signature repository methods.
	// Local interface dispatch and promoted forwarding are protected by the
	// precise call and promoted-wrapper integration tests below this package.
	prog, _ := ssautil.Packages(initial, ssa.InstantiateGenerics)
	prog.Build()

	// 1. Precise Interface Satisfaction
	var interfaces []*types.Interface
	var interfaceNames []string
	var interfaceIDs []string
	var interfacePackages []*types.Package
	var concretes []types.Type
	var concreteNames []string
	var concreteIDs []string

	// Collect all types across all loaded packages
	for _, pkg := range initial {
		if pkg.Types == nil || pkg.Types.Scope() == nil {
			continue
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			if typeName, ok := obj.(*types.TypeName); ok {
				t := typeName.Type()
				if t == nil {
					continue
				}

				// Keep track of interface types
				if iface, isIface := t.Underlying().(*types.Interface); isIface {
					// We only care about interfaces with methods
					iface.Complete()
					if iface.NumMethods() > 0 {
						interfaces = append(interfaces, iface)
						interfaceNames = append(interfaceNames, obj.Name())
						interfaceIDs = append(interfaceIDs, typeObjectID(typeName))
						interfacePackages = append(interfacePackages, typeName.Pkg())
					}
					continue
				}

				// Otherwise it's a concrete type
				concretes = append(concretes, t)
				concreteNames = append(concreteNames, obj.Name())
				concreteIDs = append(concreteIDs, typeObjectID(typeName))
			}
		}
	}
	enrichInterfaceMethods(g, interfaces, interfaceIDs, interfacePackages)

	// Compute precise Implements edges
	for i, iface := range interfaces {
		for j, conc := range concretes {
			// Check value receiver
			if types.Implements(conc, iface) {
				g.Implements = append(g.Implements, graph.ImplementsEdge{
					Interface:   interfaceNames[i],
					Concrete:    concreteNames[j],
					InterfaceID: interfaceIDs[i],
					ConcreteID:  concreteIDs[j],
				})
				continue
			}
			// Check pointer receiver
			ptr := types.NewPointer(conc)
			if types.Implements(ptr, iface) {
				g.Implements = append(g.Implements, graph.ImplementsEdge{
					Interface:   interfaceNames[i],
					Concrete:    concreteNames[j],
					InterfaceID: interfaceIDs[i],
					ConcreteID:  concreteIDs[j],
				})
			}
		}
	}

	// 2. Precise Call Graph via CHA
	cg := cha.CallGraph(prog)
	if cg != nil {
		// Existing AST-sourced call edges have no CalleeSymbolID (parser.go
		// can't resolve callee types without the type checker). Build an
		// index of those edges so CHA can:
		//   (a) skip emitting duplicates, AND
		//   (b) backfill CalleeSymbolID into the existing edge when CHA
		//       resolves the same call site to a concrete target.
		// Both the caller and callee are normalised with cleanName so the
		// keys match what CHA produces (CHA strips package qualifiers; AST
		// does not).
		astEdgeIdx := make(map[string]int)       // caller + callee + exact source site → edge
		astEdgeIndices := make(map[string][]int) // caller + callee + exact source site → all edges
		astSiteIdx := make(map[string]int)       // callee + exact source site → edge
		astSiteIndices := make(map[string][]int) // callee + exact source site → all edges
		syntheticEdges := make(map[string]struct{})
		for i, edge := range g.Calls {
			key := preciseCallKey(edge.CallerName, edge.CalleeRaw, edge.File, edge.Line, edge.Column)
			astEdgeIdx[key] = i
			astEdgeIndices[key] = append(astEdgeIndices[key], i)
			siteKey := preciseSiteKey(edge.CalleeRaw, edge.File, edge.Line, edge.Column)
			astSiteIdx[siteKey] = i
			astSiteIndices[siteKey] = append(astSiteIndices[siteKey], i)
			if edge.CallerSymbolID != "" && edge.CalleeSymbolID != "" &&
				(edge.Synthetic || edge.File == "" && edge.Line == 0) {
				syntheticEdges[edge.CallerSymbolID+"->"+edge.CalleeSymbolID] = struct{}{}
			}
		}
		invokeGroups := make(map[string]*invokeCallGroup)

		for _, node := range cg.Nodes {
			if node.Func == nil {
				continue
			}
			// Walk caller back to its source generic so even instantiated
			// generic functions (whose Pkg pointer is nil) reach the rest of
			// the loop. Origin() returns fn itself for non-generics.
			callerFn := node.Func
			if o := callerFn.Origin(); o != nil {
				callerFn = o
			}
			callerSymID := ssaFuncToSymbolID(callerFn)
			if callerFn.Pkg == nil && callerSymID == "" {
				continue
			}
			callerName := cleanName(callerFn.Name())

			for _, edge := range node.Out {
				if edge.Callee == nil || edge.Callee.Func == nil {
					continue
				}
				// Same Origin() canonicalisation for the callee — an
				// instantiation like NewCache[string,int] resolves back to
				// the source NewCache generic and gets a real Pkg pointer.
				calleeFn := edge.Callee.Func
				if o := calleeFn.Origin(); o != nil {
					calleeFn = o
				}
				calleeSymID := ssaFuncToSymbolID(calleeFn)
				if calleeFn.Pkg == nil && calleeSymID == "" {
					continue
				}
				calleeName := cleanName(calleeFn.Name())

				// Method promotion and implicit receiver adaptation are represented
				// by synthetic SSA wrappers. Those wrappers deliberately have no
				// *ssa.Package and their body call has no source position, but CHA
				// may select the wrapper as the only valid target of an interface
				// invoke. Retain the wrapper-to-declared-method edge so downstream
				// reachability can traverse from the concrete implementation to the
				// source method that actually executes.
				if isSyntheticMethodWrapper(callerFn) {
					calleePos := ssaFunctionOwnerPosition(prog.Fset, calleeFn)
					if calleeSymID == "" || !pathWithinRoot(calleePos.Filename, analysisRoot) {
						continue
					}
					syntheticKey := callerSymID + "->" + calleeSymID
					if _, exists := syntheticEdges[syntheticKey]; !exists {
						syntheticEdges[syntheticKey] = struct{}{}
						g.Calls = append(g.Calls, graph.CallEdge{
							CallerSymbolID: callerSymID,
							CallerName:     ssaFuncDisplayName(callerFn),
							CalleeRaw:      calleeName,
							CalleeSymbolID: calleeSymID,
							Synthetic:      true,
							Precise:        true,
							Resolution:     graph.CallResolutionSynthetic,
						})
					}
					continue
				}

				if edge.Site == nil {
					continue
				}

				pos := prog.Fset.Position(edge.Site.Pos())
				if pos.Filename == "" || !pathWithinRoot(pos.Filename, analysisRoot) {
					continue
				}
				// Normalize to a repo-relative path so the dedup key matches the
				// AST-sourced edge keys (which use relative paths). Without this,
				// every AST call edge gets a CHA duplicate because the keys never
				// match (absolute vs. relative path for the same call site).
				relFile, relErr := filepath.Rel(analysisRoot, pos.Filename)
				if relErr != nil {
					continue
				}

				// Resolve callee to a canonical symbol ID like
				// "github.com/foo/bar::(*Service).Validate". This is the
				// payload Bug 6 needed — exact symbol identity at call
				// sites, so downstream queries can disambiguate same-named
				// methods across types/packages without falling back to
				// substring conflation. Pass the origin-resolved calleeFn
				// so instantiations resolve to their source generic ID.
				key := preciseCallKey(callerName, calleeName, relFile, pos.Line, pos.Column)
				siteKey := preciseSiteKey(calleeName, relFile, pos.Line, pos.Column)
				if edge.Site.Common().IsInvoke() {
					// CHA intentionally emits one edge for every concrete method
					// that can satisfy an interface invoke. Keep every in-repository
					// target: collapsing this set into the first edge makes exact
					// caller queries depend on nondeterministic map iteration order.
					calleePos := ssaFunctionOwnerPosition(prog.Fset, calleeFn)
					if calleeSymID == "" || !pathWithinRoot(calleePos.Filename, analysisRoot) {
						continue
					}
					// The exact source coordinate is the stable identity of the
					// invocation. SSA gives closures synthetic caller names (Run$1),
					// while the parser deliberately attributes their bodies to the
					// enclosing symbol. Fall back to the source-site index so every
					// concrete edge clones that parser provenance.
					group := invokeGroups[siteKey]
					if group == nil {
						group = &invokeCallGroup{
							prototype: graph.CallEdge{
								CallerSymbolID: callerSymID,
								CallerName:     callerName,
								CalleeRaw:      calleeName,
								File:           relFile,
								Line:           pos.Line,
								Column:         pos.Column,
							},
							targets: make(map[string]struct{}),
						}
						indices := astEdgeIndices[key]
						if len(indices) == 0 {
							indices = astSiteIndices[siteKey]
						}
						if len(indices) > 0 {
							group.existingIndices = append(group.existingIndices, indices...)
							// Clone the original AST edge so caller identity, selector
							// spelling, return usage, and call-site provenance survive
							// on every concrete target edge.
							group.prototype = g.Calls[indices[0]]
							group.prototype.CalleeSymbolID = ""
						}
						invokeGroups[siteKey] = group
					}
					group.targets[calleeSymID] = struct{}{}
					continue
				}
				if existingIdx, dup := astEdgeIdx[key]; dup {
					// Backfill: existing AST edge has no CalleeSymbolID.
					// Fill it from CHA's resolution if we got one.
					if calleeSymID != "" && (g.Calls[existingIdx].CalleeSymbolID == "" || g.Calls[existingIdx].CalleeSymbolID == calleeSymID) {
						g.Calls[existingIdx].CalleeSymbolID = calleeSymID
						g.Calls[existingIdx].Resolution = graph.CallResolutionStatic
					}
					continue
				}
				if existingIdx, dup := astSiteIdx[siteKey]; dup {
					if calleeSymID != "" && (g.Calls[existingIdx].CalleeSymbolID == "" || g.Calls[existingIdx].CalleeSymbolID == calleeSymID) {
						g.Calls[existingIdx].CalleeSymbolID = calleeSymID
						g.Calls[existingIdx].Resolution = graph.CallResolutionStatic
					}
					continue
				}

				// CHA treats unconstrained function values (for example the
				// context.CancelFunc returned by context.WithTimeout) as calls to
				// every function with a compatible signature. Those are not useful
				// repository call edges. Keep interface invokes and true static
				// calls; skip unresolved function-value expansions.
				if !edge.Site.Common().IsInvoke() && edge.Site.Common().StaticCallee() == nil {
					continue
				}

				// AST already records direct external calls. New CHA edges are only
				// valuable when their concrete target is defined in this repository;
				// excluding dependency implementations prevents an interface call
				// such as err.Error() from expanding across the whole module cache.
				calleePos := ssaFunctionOwnerPosition(prog.Fset, calleeFn)
				if !pathWithinRoot(calleePos.Filename, analysisRoot) {
					continue
				}
				astEdgeIdx[key] = len(g.Calls)
				astSiteIdx[siteKey] = len(g.Calls)

				// Append a new edge when a repository static call has no
				// parser edge to backfill (for example, a compiler-lowered form).
				g.Calls = append(g.Calls, graph.CallEdge{
					CallerSymbolID: callerSymID,
					CallerName:     callerName,
					CalleeRaw:      calleeName,
					CalleeSymbolID: calleeSymID,
					File:           relFile,
					Line:           pos.Line,
					Column:         pos.Column,
					Precise:        true,
					Resolution:     graph.CallResolutionStatic,
				})
			}
		}
		g.Calls = materializeInvokeCalls(g.Calls, invokeGroups)
	}

	// Test packages are loaded separately so their type errors cannot weaken
	// production precision. Successful test packages still contribute exact
	// selector identities and bounded CHA-possible interface/method-value
	// targets to TestEdges and their corresponding call edges.
	testResolution, testResolutionErr := enrichTypedTestCalls(analysisRoot, g, config)
	if g.Build != nil {
		g.Build.TestCallResolution = testResolution
		if testResolutionErr != nil {
			g.Build.Warnings = append(g.Build.Warnings, "typed test call resolution incomplete: "+testResolutionErr.Error())
		}
	}

	// 3. Indirect mutations via mutating-method calls (Bug 17/28).
	// First discover every method that writes to a receiver field
	// directly; then walk caller bodies for calls into that set (plus the
	// stdlib allowlist) and attribute each call site to the field being
	// addressed. Appends to g.Mutations alongside the AST-direct
	// assignments that the parser already collected.
	userMutators, directExtra := findMutatingMethods(prog, analysisRoot)

	// 3a. Direct stores the AST parser missed. The parser only walks
	// AssignStmt, which excludes IncDecStmt (`c.n++`), augmented
	// assignments (`c.n += 1`), and stores through pointer aliases
	// (`p := &c.n; *p = 5`). SSA lowers all of those to ssa.Store, so we
	// catch them here. Dedup against existing AST mutations by
	// (field, file, line) so we don't double-count regular assignments.
	existing := make(map[mutationKey]bool, len(g.Mutations))
	for _, m := range g.Mutations {
		existing[mutationKey{m.Field, m.File, m.Line}] = true
	}
	for fnID, stores := range directExtra {
		for _, s := range stores {
			k := mutationKey{s.Field, s.File, s.Line}
			if existing[k] {
				continue
			}
			existing[k] = true
			g.Mutations = append(g.Mutations, graph.MutationEdge{
				Field:    s.Field,
				TypeName: s.TypeName,
				Function: fnID,
				File:     s.File,
				Line:     s.Line,
				Precise:  true,
			})
		}
	}

	// 3b. Indirect mutations through mutating-method calls.
	indirect := collectIndirectMutations(prog, analysisRoot, userMutators)
	g.Mutations = append(g.Mutations, indirect...)
	if err := scanner.ValidateNoSourceLinks(absRoot); err != nil {
		return fmt.Errorf("repository source became unsafe during precise analysis: %w", err)
	}
	if err := validateLoadedSourcePaths(analysisRoot, initial); err != nil {
		return fmt.Errorf("repository source became unsafe during precise analysis: %w", err)
	}

	return nil
}

func validateLoadedSourcePaths(root string, initial []*packages.Package) error {
	reader, err := sourcefs.Open(root)
	if err != nil {
		return fmt.Errorf("open precise source root: %w", err)
	}
	defer func() { _ = reader.Close() }()
	// Initial packages are the repository packages selected by ./.... Their Go
	// source must not resolve outside the analysis root. Dependencies reached
	// through imports are intentionally open-world, but any dependency source
	// that is itself beneath the root is validated through the same reader.
	for _, pkg := range initial {
		for _, path := range pkg.GoFiles {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil || !filepath.IsLocal(rel) {
				return fmt.Errorf("precise package loader selected source outside repository: %s", path)
			}
		}
	}

	seen := make(map[string]struct{})
	var validationErr error
	packages.Visit(initial, nil, func(pkg *packages.Package) {
		if validationErr != nil {
			return
		}
		files := make([]string, 0, len(pkg.GoFiles)+len(pkg.CompiledGoFiles))
		files = append(files, pkg.GoFiles...)
		files = append(files, pkg.CompiledGoFiles...)
		for _, path := range files {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil || !filepath.IsLocal(rel) {
				continue
			}
			if _, ok := seen[rel]; ok {
				continue
			}
			seen[rel] = struct{}{}
			if _, readErr := reader.ReadFile(rel); readErr != nil {
				validationErr = fmt.Errorf("validate precise source %s: %w", rel, readErr)
				return
			}
		}
	})
	if validationErr != nil {
		return validationErr
	}
	return nil
}

// validatePackageCoverage ensures precise mode actually type-checked the
// production files already present in the AST graph. go/packages may return an
// empty successful result, and it intentionally excludes files hidden by the
// active build tags or a nested module boundary. Publishing such a mixed graph
// as fully precise would make the precision metadata stronger than the data.
func validatePackageCoverage(absRoot string, initial []*packages.Package, g *graph.Graph) error {
	if len(initial) == 0 {
		return fmt.Errorf("precise package loading matched no packages")
	}
	if g == nil || len(g.Files) == 0 {
		return nil
	}

	loaded := make(map[string]struct{})
	packages.Visit(initial, nil, func(pkg *packages.Package) {
		files := make([]string, 0, len(pkg.GoFiles)+len(pkg.CompiledGoFiles))
		files = append(files, pkg.GoFiles...)
		files = append(files, pkg.CompiledGoFiles...)
		for _, file := range files {
			loaded[absoluteSourcePath(absRoot, file)] = struct{}{}
		}
	})

	missingSet := make(map[string]struct{})
	for _, file := range g.Files {
		if strings.HasSuffix(file.Path, "_test.go") {
			continue
		}
		if _, ok := loaded[absoluteSourcePath(absRoot, file.Path)]; !ok {
			missingSet[file.Path] = struct{}{}
		}
	}
	if len(missingSet) == 0 {
		return nil
	}

	missing := make([]string, 0, len(missingSet))
	for file := range missingSet {
		missing = append(missing, file)
	}
	sort.Strings(missing)
	const detailLimit = 3
	details := missing
	if len(details) > detailLimit {
		details = details[:detailLimit]
	}
	message := strings.Join(details, ", ")
	if remaining := len(missing) - len(details); remaining > 0 {
		message += fmt.Sprintf(", and %d more", remaining)
	}
	return fmt.Errorf("precise package loading omitted %d indexed production file(s): %s", len(missing), message)
}

func absoluteSourcePath(root, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

// enrichInterfaceMethods fills parser interface nodes from the completed
// go/types method set. The parser records explicit methods, but an embedded
// interface contributes methods with no named AST field; completing the type
// makes those inherited methods queryable as OuterInterface.Method.
func enrichInterfaceMethods(g *graph.Graph, interfaces []*types.Interface, ids []string, owners []*types.Package) {
	if g == nil {
		return
	}
	type interfaceInfo struct {
		iface *types.Interface
		owner *types.Package
	}
	byID := make(map[string]interfaceInfo, len(ids))
	for i, id := range ids {
		if id == "" || i >= len(interfaces) || i >= len(owners) {
			continue
		}
		byID[id] = interfaceInfo{iface: interfaces[i], owner: owners[i]}
	}

	for i := range g.Symbols {
		symbol := &g.Symbols[i]
		if symbol.Kind != graph.KindInterface {
			continue
		}
		info, ok := byID[symbol.ID]
		if !ok || info.iface == nil {
			continue
		}
		methods := make(map[string]string, info.iface.NumMethods())
		maps.Copy(methods, symbol.InterfaceMethods)
		for method := range info.iface.Methods() {
			if _, exists := methods[method.Name()]; exists {
				continue
			}
			if signature, ok := method.Type().(*types.Signature); ok {
				methods[method.Name()] = preciseMethodSignature(signature, info.owner)
			}
		}
		symbol.InterfaceMethods = methods
	}
}

func preciseMethodSignature(signature *types.Signature, owner *types.Package) string {
	if signature == nil {
		return ""
	}
	var result strings.Builder
	result.WriteString("func(")
	result.WriteString(preciseTupleSignature(signature.Params(), signature.Variadic(), owner))
	result.WriteString(")")
	if signature.Results() != nil && signature.Results().Len() > 0 {
		result.WriteString(" (")
		result.WriteString(preciseTupleSignature(signature.Results(), false, owner))
		result.WriteString(")")
	}
	return result.String()
}

func preciseTupleSignature(tuple *types.Tuple, variadic bool, owner *types.Package) string {
	if tuple == nil || tuple.Len() == 0 {
		return ""
	}
	parts := make([]string, 0, tuple.Len())
	qualifier := func(pkg *types.Package) string {
		if pkg == nil || owner != nil && pkg.Path() == owner.Path() {
			return ""
		}
		return pkg.Name()
	}
	for i := 0; i < tuple.Len(); i++ {
		typeValue := tuple.At(i).Type()
		if variadic && i == tuple.Len()-1 {
			if slice, ok := typeValue.(*types.Slice); ok {
				parts = append(parts, "..."+types.TypeString(slice.Elem(), qualifier))
				continue
			}
		}
		parts = append(parts, types.TypeString(typeValue, qualifier))
	}
	return strings.Join(parts, ", ")
}

func preciseCallKey(caller, callee, file string, line, column int) string {
	return fmt.Sprintf("%s->%s@%s:%d:%d", cleanName(caller), cleanName(callee), file, line, column)
}

func preciseSiteKey(callee, file string, line, column int) string {
	return fmt.Sprintf("%s@%s:%d:%d", cleanName(callee), file, line, column)
}

// packageLoadError reports type/load failures that packages.Load returns on
// Package.Errors while its top-level error remains nil. Rejecting these before
// SSA construction keeps precise enrichment all-or-nothing and lets callers
// accurately record an AST fallback instead of publishing partial precision.
func packageLoadError(initial []*packages.Package) error {
	seen := make(map[string]struct{})
	var problems []string
	packages.Visit(initial, nil, func(pkg *packages.Package) {
		name := pkg.PkgPath
		if name == "" {
			name = pkg.ID
		}
		for _, pkgErr := range pkg.Errors {
			message := strings.Join(strings.Fields(pkgErr.Msg), " ")
			if message == "" {
				message = "unknown package error"
			}
			problem := name + ": " + message
			if _, ok := seen[problem]; ok {
				continue
			}
			seen[problem] = struct{}{}
			problems = append(problems, problem)
		}
	})
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	const detailLimit = 3
	details := problems
	if len(details) > detailLimit {
		details = details[:detailLimit]
	}
	message := strings.Join(details, "; ")
	if remaining := len(problems) - len(details); remaining > 0 {
		message += fmt.Sprintf("; and %d more", remaining)
	}
	return fmt.Errorf("precise package loading reported %d error(s): %s", len(problems), message)
}

// invokeCallGroup accumulates all concrete CHA targets for one interface call
// site. existingIndices refer only to edges that were present before this
// enrichment pass, allowing materializeInvokeCalls to be idempotent when
// Enrich is called repeatedly on the same graph.
type invokeCallGroup struct {
	prototype       graph.CallEdge
	existingIndices []int
	targets         map[string]struct{}
}

// materializeInvokeCalls replaces the unresolved AST edge for each interface
// invoke with one metadata-preserving edge per concrete repository target.
// Both group and target ordering are explicit so output does not depend on the
// iteration order of the SSA/CHA maps.
func materializeInvokeCalls(calls []graph.CallEdge, groups map[string]*invokeCallGroup) []graph.CallEdge {
	if len(groups) == 0 {
		return calls
	}

	groupKeys := make([]string, 0, len(groups))
	for key := range groups {
		groupKeys = append(groupKeys, key)
	}
	sort.Strings(groupKeys)

	replacements := make(map[int][]graph.CallEdge)
	skip := make(map[int]bool)
	var appendOnly [][]graph.CallEdge

	for _, key := range groupKeys {
		group := groups[key]
		targetIDs := make([]string, 0, len(group.targets))
		for targetID := range group.targets {
			targetIDs = append(targetIDs, targetID)
		}
		sort.Strings(targetIDs)
		if len(targetIDs) == 0 {
			continue
		}

		edges := make([]graph.CallEdge, 0, len(targetIDs))
		for targetIndex, targetID := range targetIDs {
			edge := group.prototype
			edge.CalleeSymbolID = targetID
			edge.Resolution = graph.CallResolutionCHA
			// Preserve one parser-owned edge when an unresolved AST edge existed;
			// additional concrete targets are precise-only enrichment records.
			edge.Precise = len(group.existingIndices) == 0 || targetIndex > 0
			edges = append(edges, edge)
		}

		if len(group.existingIndices) == 0 {
			appendOnly = append(appendOnly, edges)
			continue
		}
		at := group.existingIndices[0]
		replacements[at] = edges
		for _, index := range group.existingIndices {
			skip[index] = true
		}
	}

	result := make([]graph.CallEdge, 0, len(calls)+len(appendOnly))
	for i, edge := range calls {
		if edges, ok := replacements[i]; ok {
			result = append(result, edges...)
		}
		if skip[i] {
			continue
		}
		result = append(result, edge)
	}
	for _, edges := range appendOnly {
		result = append(result, edges...)
	}
	return result
}

func typeObjectID(typeName *types.TypeName) string {
	if typeName == nil || typeName.Pkg() == nil {
		return ""
	}
	return typeName.Pkg().Path() + "::" + typeName.Name()
}

func pathWithinRoot(path, root string) bool {
	if path == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// mutationKey dedups MutationEdges that point at the same source position
// for the same field. Used when merging SSA-derived direct mutations with
// the AST parser's AssignStmt scan so a plain  s.field = x  isn't recorded
// twice (once from each pass).
type mutationKey struct {
	field, file string
	line        int
}

// ssaFuncToSymbolID renders an SSA function as a fully-qualified symbol ID
// in the same shape as graph.SymbolNode.ID:
//
//	pkg/path::FuncName                  for top-level functions
//	pkg/path::(*Type).Method            for pointer-receiver methods
//	pkg/path::(Type).Method             for value-receiver methods
//
// Returns "" when the function lacks enough type info (e.g. anonymous
// closures whose Pkg.Pkg is nil). The empty return is the safe default —
// downstream consumers treat empty CalleeSymbolID as "fall back to
// name-based matching".
//
// Generic handling (Bug 9.B): SSA monomorphises generics — every
// instantiation (e.g. NewCache[string,int], NewCache[int,string]) appears
// as its own *ssa.Function with the type parameters baked into Name() and,
// in some cases, a nil Pkg pointer. We use fn.Origin() to recover the
// source generic from any instantiation; that's the symbol the parser
// emitted into the symbol table, so CalleeSymbolID matches a real ID.
func ssaFuncToSymbolID(fn *ssa.Function) string {
	if fn == nil {
		return ""
	}
	// Walk an instantiation back to its source generic. For non-generic
	// functions Origin() returns fn itself, so this is safe to always call.
	if origin := fn.Origin(); origin != nil {
		fn = origin
	}
	pkgPath := ssaFunctionPackagePath(fn)
	if pkgPath == "" {
		return ""
	}
	name := fn.Name()
	if name == "" {
		return ""
	}
	// SSA may include type parameters in the name for some generic forms
	// ("Process[int]"). The parser emits the bare source name ("Process"),
	// so strip any "[...]" suffix to keep the IDs aligned.
	if i := strings.Index(name, "["); i >= 0 {
		name = name[:i]
	}
	// Methods carry a receiver in the signature; render it as the
	// parser does — "(*Type).Method" or "(Type).Method", preserving the
	// pointer marker but stripping the package-path prefix from the
	// receiver type's name.
	if fn.Signature != nil {
		if recv := fn.Signature.Recv(); recv != nil && recv.Type() != nil {
			return fmt.Sprintf("%s::(%s).%s", pkgPath, formatReceiverType(recv.Type()), name)
		}
	}
	return fmt.Sprintf("%s::%s", pkgPath, name)
}

// ssaFunctionPackagePath returns the import path that owns fn's graph
// identity. Declared functions carry it on fn.Pkg. Synthetic method wrappers
// intentionally do not, so derive their owner from the named receiver type.
func ssaFunctionPackagePath(fn *ssa.Function) string {
	if fn == nil {
		return ""
	}
	if fn.Pkg != nil && fn.Pkg.Pkg != nil {
		return fn.Pkg.Pkg.Path()
	}
	if fn.Signature == nil || fn.Signature.Recv() == nil {
		return ""
	}
	named := namedReceiverType(fn.Signature.Recv().Type())
	if named == nil || named.Obj() == nil || named.Obj().Pkg() == nil {
		return ""
	}
	return named.Obj().Pkg().Path()
}

// ssaFunctionOwnerPosition identifies the source declaration that owns fn's
// graph identity. A promoted wrapper's fn.Pos points at the underlying method,
// which may live in an external dependency; its receiver type declaration is
// the correct ownership test for retaining an in-repository wrapper target.
func ssaFunctionOwnerPosition(fset *token.FileSet, fn *ssa.Function) token.Position {
	if fset == nil || fn == nil {
		return token.Position{}
	}
	if fn.Pkg == nil && fn.Signature != nil && fn.Signature.Recv() != nil {
		if named := namedReceiverType(fn.Signature.Recv().Type()); named != nil && named.Obj() != nil {
			return fset.Position(named.Obj().Pos())
		}
	}
	return fset.Position(fn.Pos())
}

func namedReceiverType(receiver types.Type) *types.Named {
	if pointer, ok := receiver.(*types.Pointer); ok {
		receiver = pointer.Elem()
	}
	named, _ := receiver.(*types.Named)
	return named
}

func isSyntheticMethodWrapper(fn *ssa.Function) bool {
	return fn != nil && fn.Pkg == nil && fn.Signature != nil && fn.Signature.Recv() != nil &&
		strings.HasPrefix(fn.Synthetic, "wrapper for ")
}

func ssaFuncDisplayName(fn *ssa.Function) string {
	if fn == nil {
		return ""
	}
	name := cleanName(fn.Name())
	if fn.Signature != nil {
		if receiver := fn.Signature.Recv(); receiver != nil && receiver.Type() != nil {
			return fmt.Sprintf("(%s).%s", formatReceiverType(receiver.Type()), name)
		}
	}
	return name
}

// formatReceiverType renders a method's receiver type as it appears in
// parser-emitted symbol IDs: "*Type" for pointer receivers, "Type" for
// value receivers. The full package-qualified form from go/types
// (e.g. "*github.com/foo/bar.Service") is stripped to just the bare
// type name so it matches the parser's output exactly.
func formatReceiverType(t types.Type) string {
	s := t.String()
	prefix := ""
	if strings.HasPrefix(s, "*") {
		prefix = "*"
		s = s[1:]
	}
	if i := strings.LastIndex(s, "."); i >= 0 {
		s = s[i+1:]
	}
	// Strip generic instantiation suffix ("[int]", "[T]" etc.) so methods
	// on List[int] and List[string] map to the same symbol ID — matches
	// the parser's behaviour (Bug 9 mitigation is intentional here too).
	if i := strings.Index(s, "["); i >= 0 {
		s = s[:i]
	}
	return prefix + s
}

// cleanName strips package paths or pointer indicators from SSA names to match AST names.
func cleanName(name string) string {
	name = strings.TrimPrefix(name, "*")
	if idx := strings.LastIndex(name, "."); idx != -1 {
		return name[idx+1:]
	}
	return name
}
