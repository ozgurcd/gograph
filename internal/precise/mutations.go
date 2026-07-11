package precise

import (
	"go/types"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
	"golang.org/x/tools/go/ssa"
)

// stdlibMutators is a small allowlist of stdlib (and idiomatic concurrency
// primitive) methods that are known to mutate their receiver's internal state.
//
// Why an allowlist exists at all when we have an SSA-based mutation detector:
// ssautil.AllPackages only builds SSA for packages that were loaded from
// source — stdlib packages are usually loaded via compiled export data, so
// their method bodies aren't available for the receiver-field-write walk.
// Without this list, calls like  s.running.Store(true)  on an *atomic.Bool
// would slip through, even though they're some of the most common mutation
// patterns in real Go code.
//
// Key format: "<full pkg path>::(<recv type>).<method>"
// Receiver names use the same shape the parser/precise pass emit
// (e.g. "(*Bool)" with the leading * for pointer receivers).
//
// Channel-send mutations are handled in code (no method call exists for
// them — they're a syntactic form) so they don't appear here.
var stdlibMutators = map[string]bool{
	// sync/atomic — pointer-receiver mutators on the generic atomic types
	"sync/atomic::(*Bool).Store":             true,
	"sync/atomic::(*Bool).Swap":              true,
	"sync/atomic::(*Bool).CompareAndSwap":    true,
	"sync/atomic::(*Int32).Store":            true,
	"sync/atomic::(*Int32).Add":              true,
	"sync/atomic::(*Int32).Swap":             true,
	"sync/atomic::(*Int32).CompareAndSwap":   true,
	"sync/atomic::(*Int64).Store":            true,
	"sync/atomic::(*Int64).Add":              true,
	"sync/atomic::(*Int64).Swap":             true,
	"sync/atomic::(*Int64).CompareAndSwap":   true,
	"sync/atomic::(*Uint32).Store":           true,
	"sync/atomic::(*Uint32).Add":             true,
	"sync/atomic::(*Uint32).Swap":            true,
	"sync/atomic::(*Uint32).CompareAndSwap":  true,
	"sync/atomic::(*Uint64).Store":           true,
	"sync/atomic::(*Uint64).Add":             true,
	"sync/atomic::(*Uint64).Swap":            true,
	"sync/atomic::(*Uint64).CompareAndSwap":  true,
	"sync/atomic::(*Uintptr).Store":          true,
	"sync/atomic::(*Uintptr).Add":            true,
	"sync/atomic::(*Uintptr).Swap":           true,
	"sync/atomic::(*Uintptr).CompareAndSwap": true,
	"sync/atomic::(*Pointer).Store":          true,
	"sync/atomic::(*Pointer).Swap":           true,
	"sync/atomic::(*Pointer).CompareAndSwap": true,
	"sync/atomic::(*Value).Store":            true,
	"sync/atomic::(*Value).Swap":             true,
	"sync/atomic::(*Value).CompareAndSwap":   true,

	// sync.Map — its mutators
	"sync::(*Map).Store":            true,
	"sync::(*Map).Delete":           true,
	"sync::(*Map).LoadAndDelete":    true,
	"sync::(*Map).LoadOrStore":      true,
	"sync::(*Map).Swap":             true,
	"sync::(*Map).CompareAndSwap":   true,
	"sync::(*Map).CompareAndDelete": true,

	// sync.Mutex / RWMutex — lock state is mutation
	"sync::(*Mutex).Lock":      true,
	"sync::(*Mutex).Unlock":    true,
	"sync::(*Mutex).TryLock":   true,
	"sync::(*RWMutex).Lock":    true,
	"sync::(*RWMutex).Unlock":  true,
	"sync::(*RWMutex).RLock":   true,
	"sync::(*RWMutex).RUnlock": true,
	"sync::(*RWMutex).TryLock": true,

	// sync.WaitGroup — Add and Done mutate counter
	"sync::(*WaitGroup).Add":  true,
	"sync::(*WaitGroup).Done": true,

	// sync.Once — Do flips the "done" flag (one-shot mutation)
	"sync::(*Once).Do": true,
}

// directMutation records one place where a method body writes directly to
// a receiver field. Carries the exact store position so output can point
// at the offending line — not just the enclosing function.
type directMutation struct {
	Field    string
	TypeName string
	File     string
	Line     int
}

func mutationReceiverTypeName(value types.Type) string {
	if pointer, ok := value.(*types.Pointer); ok {
		value = pointer.Elem()
	}
	if named, ok := value.(*types.Named); ok && named.Obj() != nil {
		return named.Obj().Name()
	}
	return strings.TrimPrefix(value.String(), "*")
}

// findMutatingMethods walks every *ssa.Function in prog whose receiver is a
// pointer to a struct and inspects its body for stores into the receiver's
// fields.
//
// Returns:
//   - mutatingMethods: map  methodSymbolID -> []fieldName  containing every
//     field directly mutated by that method. Used downstream to recognise
//     indirect mutations (caller invoking a mutating method on its own field).
//   - directMutations: map  methodSymbolID -> []directMutation  with the
//     exact source positions of each store. Lets us emit MutationEdges that
//     cover patterns the AST parser misses — `c.n++` (IncDecStmt), `c.n += 1`
//     (augmented assignment), pointer-aliased writes, etc. — none of which
//     are *ast.AssignStmt.
//
// Detection is intentionally direct-only: we scan for *ssa.Store where the
// address is a *ssa.FieldAddr rooted at the receiver parameter. We do NOT
// follow calls inside the method body, so:
//
//	func (s *Server) Handle() { s.helper() }     // not detected here
//	func (s *Server) helper() { s.n++ }          // detected (recorded under helper)
//
// Transitive attribution would require a fixpoint pass; deferred until
// somebody hits a case where it actually matters. The direct-only view is
// already a large step up from "literal assignments only".
func findMutatingMethods(prog *ssa.Program, absRoot string) (mutatingMethods map[string][]string, directMutations map[string][]directMutation) {
	mutatingMethods = make(map[string][]string)
	directMutations = make(map[string][]directMutation)

	for fn := range ssaAllFunctions(prog) {
		if fn.Signature == nil || fn.Signature.Recv() == nil {
			continue
		}
		recvType := fn.Signature.Recv().Type()
		recvTypeName := mutationReceiverTypeName(recvType)
		// Only pointer receivers can mutate caller-visible state. A value
		// receiver gets a copy and can't affect the caller's struct.
		if _, ptr := recvType.(*types.Pointer); !ptr {
			continue
		}
		if len(fn.Params) == 0 {
			continue
		}
		recvParam := fn.Params[0]
		fnID := ssaFuncToSymbolID(fn)
		if fnID == "" {
			continue
		}

		seenForMethod := make(map[string]bool)        // first-occurrence dedup for the method->fields map
		seenForPosition := make(map[positionKey]bool) // (field+file+line) dedup for direct mutation entries
		for _, blk := range fn.Blocks {
			for _, instr := range blk.Instrs {
				st, ok := instr.(*ssa.Store)
				if !ok {
					continue
				}
				// Walk back through pointer ops to find the underlying
				// FieldAddr (handles  s.field = x  but also  p := &s.field; *p = x ).
				field := fieldNameFromAddr(st.Addr, recvParam)
				if field == "" {
					continue
				}
				// Record the method-level "this method mutates field" once.
				if !seenForMethod[field] {
					seenForMethod[field] = true
					mutatingMethods[fnID] = append(mutatingMethods[fnID], field)
				}
				// Record the per-store direct mutation (filters by absRoot so
				// stdlib stores don't leak into user-facing output).
				pos := prog.Fset.Position(st.Pos())
				if pos.Filename == "" || !strings.HasPrefix(pos.Filename, absRoot) {
					continue
				}
				file := strings.TrimPrefix(pos.Filename, absRoot+"/")
				k := positionKey{field: field, file: file, line: pos.Line}
				if seenForPosition[k] {
					continue
				}
				seenForPosition[k] = true
				directMutations[fnID] = append(directMutations[fnID], directMutation{
					Field: field, TypeName: recvTypeName, File: file, Line: pos.Line,
				})
			}
		}
	}
	return mutatingMethods, directMutations
}

// positionKey dedups direct-mutation records that point at the same source
// location. A single ++ lowers to load+add+store; SSA might emit the same
// store twice across blocks in some inlining shapes — we want one row per
// source line per field.
type positionKey struct {
	field string
	file  string
	line  int
}

// fieldNameFromAddr unwraps an SSA pointer expression and returns the field
// name if the address ultimately roots at recvParam. Returns "" if the store
// doesn't target a receiver field (e.g. writing to a local variable, or to
// a field of some unrelated struct).
//
// Handles two common shapes:
//
//	*ssa.FieldAddr           — direct  &recv.field  /  &(*recv).field
//	*ssa.UnOp (Op == Deref)  — pointer-aliased writes  *p = x  where p was &recv.field
//
// More exotic patterns (writing through a slice of pointers, through a
// returned pointer-to-field, etc.) fall through to "".
func fieldNameFromAddr(addr ssa.Value, recvParam *ssa.Parameter) string {
	switch a := addr.(type) {
	case *ssa.FieldAddr:
		// Recurse into the source — covers nested cases like
		//   &s.outer.field  → FieldAddr(FieldAddr(s))
		if isReceiverRoot(a.X, recvParam) {
			return fieldNameOf(a)
		}
	}
	return ""
}

// isReceiverRoot reports whether v ultimately references recvParam.
// FieldAddr's X can be the receiver param itself, or another FieldAddr
// chain rooted at the receiver (for nested struct fields).
func isReceiverRoot(v ssa.Value, recvParam *ssa.Parameter) bool {
	for {
		switch x := v.(type) {
		case *ssa.Parameter:
			return x == recvParam
		case *ssa.FieldAddr:
			v = x.X
		case *ssa.UnOp:
			// Dereference of a pointer; keep walking up.
			v = x.X
		default:
			return false
		}
	}
}

// fieldNameOf returns the struct field name targeted by a FieldAddr,
// using the type info to map the numeric Field index to its declared name.
// Returns "" if type info is missing or doesn't match a struct.
func fieldNameOf(fa *ssa.FieldAddr) string {
	t := fa.X.Type()
	// FieldAddr.X is always a pointer; deref once to find the struct.
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	st, ok := t.Underlying().(*types.Struct)
	if !ok {
		return ""
	}
	if fa.Field < 0 || fa.Field >= st.NumFields() {
		return ""
	}
	return st.Field(fa.Field).Name()
}

// ssaAllFunctions returns an iterator-like channel over every function known
// to prog, including methods and anonymous functions. Implemented as a
// generator goroutine to keep call sites readable.
//
// We use this in a range over a map-of-channel pattern (range chan): each
// emitted *ssa.Function appears exactly once.
func ssaAllFunctions(prog *ssa.Program) <-chan *ssa.Function {
	ch := make(chan *ssa.Function)
	go func() {
		defer func() {
			_ = recover()
		}()
		defer close(ch)
		for _, pkg := range prog.AllPackages() {
			for _, m := range pkg.Members {
				switch mem := m.(type) {
				case *ssa.Function:
					ch <- mem
					for _, anon := range mem.AnonFuncs {
						ch <- anon
					}
				case *ssa.Type:
					// Methods on a named type are reachable via prog.MethodSets
					// of that type. Enumerate both value-receiver and
					// pointer-receiver method sets so we cover everything.
					mset := prog.MethodSets.MethodSet(mem.Type())
					for i := 0; i < mset.Len(); i++ {
						if fn := prog.MethodValue(mset.At(i)); fn != nil {
							ch <- fn
						}
					}
					ptrMset := prog.MethodSets.MethodSet(types.NewPointer(mem.Type()))
					for i := 0; i < ptrMset.Len(); i++ {
						if fn := prog.MethodValue(ptrMset.At(i)); fn != nil {
							ch <- fn
						}
					}
				}
			}
		}
	}()
	return ch
}

// collectIndirectMutations scans every caller's SSA body for ssa.Call
// instructions whose target is in the mutating set (either userMutators
// detected via findMutatingMethods, or stdlibMutators). When the call's
// receiver is a FieldAddr rooted at the caller's own receiver, the field
// being addressed is the one getting mutated — we emit a MutationEdge with
// Via=<method-name>.
//
// Two-step logic per call site:
//  1. Is the callee something we believe mutates its receiver?
//  2. Is the call's receiver argument structurally  caller-receiver.field
//     (with possible inner FieldAddr chain for nested fields)? If yes,
//     attribute the mutation to that field of the caller's receiver type.
//
// Mutations that don't fit step 2 (e.g. mutation through a local variable,
// a parameter, or a returned pointer) are skipped — they're real mutations
// but we can't attribute them to a specific field on the caller's struct
// without a heavier alias analysis.
func collectIndirectMutations(prog *ssa.Program, absRoot string, userMutators map[string][]string) []graph.MutationEdge {
	var out []graph.MutationEdge
	isMutator := func(id string) bool {
		if id == "" {
			return false
		}
		if stdlibMutators[id] {
			return true
		}
		_, ok := userMutators[id]
		return ok
	}
	for fn := range ssaAllFunctions(prog) {
		// fn is the *caller*. We need to know its receiver param so we can
		// recognise calls of the form  caller-recv.field.Method().
		var callerRecvParam *ssa.Parameter
		callerTypeName := ""
		if fn.Signature != nil && fn.Signature.Recv() != nil && len(fn.Params) > 0 {
			callerRecvParam = fn.Params[0]
			callerTypeName = mutationReceiverTypeName(fn.Signature.Recv().Type())
		}
		// Channel-send mutations: ssa.Send instructions targeting a field
		// channel are emitted as mutations regardless of receiver chain
		// (the send is the mutation; no method-call indirection).
		callerSymID := ssaFuncToSymbolID(fn)
		for _, blk := range fn.Blocks {
			for _, instr := range blk.Instrs {
				switch i := instr.(type) {
				case *ssa.Send:
					if callerRecvParam == nil {
						continue
					}
					field := fieldNameFromValue(i.Chan, callerRecvParam)
					if field == "" {
						continue
					}
					pos := prog.Fset.Position(i.Pos())
					if pos.Filename == "" || !strings.HasPrefix(pos.Filename, absRoot) {
						continue
					}
					out = append(out, graph.MutationEdge{
						Field:    field,
						TypeName: callerTypeName,
						Function: callerSymID,
						File:     strings.TrimPrefix(pos.Filename, absRoot+"/"),
						Line:     pos.Line,
						Via:      "chan<-",
					})
				case ssa.CallInstruction:
					common := i.Common()
					if common == nil {
						continue
					}
					calleeFn := common.StaticCallee()
					if calleeFn == nil {
						continue
					}
					if o := calleeFn.Origin(); o != nil {
						calleeFn = o
					}
					calleeID := ssaFuncToSymbolID(calleeFn)
					if !isMutator(calleeID) {
						continue
					}
					if callerRecvParam == nil {
						continue
					}
					// Receiver of the call is in common.Args[0] for a
					// static method call. For a plain function call this
					// is just the first parameter — but we already filtered
					// to functions that are method-valued via the mutating
					// set, so Args[0] is meaningful.
					if len(common.Args) == 0 {
						continue
					}
					field := fieldNameFromValue(common.Args[0], callerRecvParam)
					if field == "" {
						continue
					}
					pos := prog.Fset.Position(i.Pos())
					if pos.Filename == "" || !strings.HasPrefix(pos.Filename, absRoot) {
						continue
					}
					out = append(out, graph.MutationEdge{
						Field:    field,
						TypeName: callerTypeName,
						Function: callerSymID,
						File:     strings.TrimPrefix(pos.Filename, absRoot+"/"),
						Line:     pos.Line,
						Via:      calleeFn.Name(),
					})
				}
			}
		}
	}
	return out
}

// fieldNameFromValue is the receiver-side analogue of fieldNameFromAddr:
// given a value expression (the receiver of a call, or the target of a
// channel send), it walks back through field-addressing operations and
// reports the field name iff the chain roots at recvParam.
//
// We need a separate function (instead of reusing fieldNameFromAddr)
// because call-site receivers and channel-send targets appear as ssa.Value
// (the value being passed/sent), whereas store-site addresses live in
// ssa.Store.Addr — they're the same conceptually but enter through
// different opcodes. The recursion is otherwise identical.
func fieldNameFromValue(v ssa.Value, recvParam *ssa.Parameter) string {
	switch x := v.(type) {
	case *ssa.FieldAddr:
		if isReceiverRoot(x.X, recvParam) {
			return fieldNameOf(x)
		}
	case *ssa.UnOp:
		// Load of a *T field, then method call on it — the load wraps the
		// FieldAddr we actually care about.
		return fieldNameFromValue(x.X, recvParam)
	}
	return ""
}
