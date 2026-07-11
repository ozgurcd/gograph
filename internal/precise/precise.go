package precise

import (
	"fmt"
	"go/types"
	"path/filepath"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Enrich applies type-checked precision to the graph.
// It loads the project via go/packages, finds exact interface implementers,
// and uses Class Hierarchy Analysis (CHA) to add precise dynamic dispatch call edges.
func Enrich(absRoot string, g *graph.Graph) error {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedSyntax,
		Dir: absRoot,
	}

	// Load all packages
	initial, err := packages.Load(cfg, "./...")
	if err != nil {
		return fmt.Errorf("packages.Load failed: %w", err)
	}

	// Build SSA. ssa.InstantiateGenerics monomorphises generic functions
	// and methods so CHA can see their call sites with source positions
	// (Bug 9.B). Without it, every call into or out of a generic body
	// produces a synthetic edge with edge.Site == nil — which the CHA
	// loop below skips, leaving CalleeSymbolID empty on generic-touching
	// edges. With it, CHA emits one fully-resolved edge per instantiation,
	// each carrying a real *ssa.CallCommon site we can position-match
	// against the parser's AST edges.
	prog, _ := ssautil.AllPackages(initial, ssa.InstantiateGenerics)
	prog.Build()

	// 1. Precise Interface Satisfaction
	var interfaces []*types.Interface
	var interfaceNames []string
	var interfaceIDs []string
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
					if iface.NumMethods() > 0 {
						interfaces = append(interfaces, iface)
						interfaceNames = append(interfaceNames, obj.Name())
						interfaceIDs = append(interfaceIDs, typeObjectID(typeName))
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
		astEdgeIdx := make(map[string]int) // dedup-key → index in g.Calls
		astSiteIdx := make(map[string]int) // callee + source site → AST edge
		for i, edge := range g.Calls {
			key := fmt.Sprintf("%s->%s@%s:%d", cleanName(edge.CallerName), cleanName(edge.CalleeRaw), edge.File, edge.Line)
			astEdgeIdx[key] = i
			siteKey := fmt.Sprintf("%s@%s:%d", cleanName(edge.CalleeRaw), edge.File, edge.Line)
			astSiteIdx[siteKey] = i
		}

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
			if callerFn.Pkg == nil {
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
				if calleeFn.Pkg == nil {
					continue
				}
				calleeName := cleanName(calleeFn.Name())

				if edge.Site == nil {
					continue
				}

				pos := prog.Fset.Position(edge.Site.Pos())
				if pos.Filename == "" || !strings.HasPrefix(pos.Filename, absRoot) {
					continue
				}
				// Normalize to a repo-relative path so the dedup key matches the
				// AST-sourced edge keys (which use relative paths). Without this,
				// every AST call edge gets a CHA duplicate because the keys never
				// match (absolute vs. relative path for the same call site).
				relFile := strings.TrimPrefix(pos.Filename, absRoot+"/")

				// Resolve callee to a canonical symbol ID like
				// "github.com/foo/bar::(*Service).Validate". This is the
				// payload Bug 6 needed — exact symbol identity at call
				// sites, so downstream queries can disambiguate same-named
				// methods across types/packages without falling back to
				// substring conflation. Pass the origin-resolved calleeFn
				// so instantiations resolve to their source generic ID.
				calleeSymID := ssaFuncToSymbolID(calleeFn)

				key := fmt.Sprintf("%s->%s@%s:%d", callerName, calleeName, relFile, pos.Line)
				if existingIdx, dup := astEdgeIdx[key]; dup {
					// Backfill: existing AST edge has no CalleeSymbolID.
					// Fill it from CHA's resolution if we got one.
					if calleeSymID != "" && g.Calls[existingIdx].CalleeSymbolID == "" {
						g.Calls[existingIdx].CalleeSymbolID = calleeSymID
					}
					continue
				}
				siteKey := fmt.Sprintf("%s@%s:%d", calleeName, relFile, pos.Line)
				if existingIdx, dup := astSiteIdx[siteKey]; dup && !edge.Site.Common().IsInvoke() {
					if calleeSymID != "" && g.Calls[existingIdx].CalleeSymbolID == "" {
						g.Calls[existingIdx].CalleeSymbolID = calleeSymID
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
				calleePos := prog.Fset.Position(calleeFn.Pos())
				if !pathWithinRoot(calleePos.Filename, absRoot) {
					continue
				}
				astEdgeIdx[key] = len(g.Calls)
				astSiteIdx[siteKey] = len(g.Calls)

				// Append a new edge — CHA found a call AST didn't see
				// (typically dynamic dispatch through an interface).
				g.Calls = append(g.Calls, graph.CallEdge{
					CallerName:     callerName,
					CalleeRaw:      calleeName,
					CalleeSymbolID: calleeSymID,
					File:           relFile,
					Line:           pos.Line,
				})
			}
		}
	}

	// 3. Indirect mutations via mutating-method calls (Bug 17/28).
	// First discover every method that writes to a receiver field
	// directly; then walk caller bodies for calls into that set (plus the
	// stdlib allowlist) and attribute each call site to the field being
	// addressed. Appends to g.Mutations alongside the AST-direct
	// assignments that the parser already collected.
	userMutators, directExtra := findMutatingMethods(prog, absRoot)

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
			})
		}
	}

	// 3b. Indirect mutations through mutating-method calls.
	indirect := collectIndirectMutations(prog, absRoot, userMutators)
	g.Mutations = append(g.Mutations, indirect...)

	return nil
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
	if fn.Pkg == nil || fn.Pkg.Pkg == nil {
		return ""
	}
	pkgPath := fn.Pkg.Pkg.Path()
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
