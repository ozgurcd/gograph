// Package graph defines the core data model for the gograph tool.
package graph

import "time"

// Version is the schema version written into graph.json.
// v2: symbol IDs use module-rooted import paths (e.g. "github.com/org/repo/pkg::Symbol")
//
//	instead of relative file paths ("internal/pkg/file.go::Symbol").
//	This makes IDs stable across file renames within the same package.
//
// Optional v2 fields remain additive at the JSON wire level. Newer readers
// normalize missing precision to AST-only, missing columns to line-only
// locations, and missing synthetic markers to ordinary source calls. Older v2
// readers can decode new graphs, but do not understand the presentation-only
// semantics of additional synthetic forwarding records and may count or show
// them as ordinary edges. Content digests, cache/provenance markers, and reuse
// counters are also additive; missing cache markers simply disable reuse.
const Version = "2"

// CurrentSourcePolicyVersion identifies graphs built after repository source
// reads became confined and descendant source symlinks were excluded. Graphs
// without the exact current marker may contain data derived from files outside
// the indexed repository or use an unknown future policy and must be rebuilt
// before they are queried.
const CurrentSourcePolicyVersion = 1

// CurrentAnalysisCacheVersion identifies graphs whose file-level records can
// be decomposed back into parser output and safely reused by an incremental
// build. Bump this whenever parser/precise provenance changes.
const CurrentAnalysisCacheVersion = 1

// Graph is the top-level data structure written to .gograph/graph.json.
type Graph struct {
	Version       string            `json:"version"`
	GeneratedAt   time.Time         `json:"generated_at"`
	Root          string            `json:"root"`
	Packages      []PackageNode     `json:"packages"`
	Files         []FileNode        `json:"files"`
	Symbols       []SymbolNode      `json:"symbols"`
	Imports       []ImportEdge      `json:"imports"`
	Calls         []CallEdge        `json:"calls"`
	EnvReads      []EnvRead         `json:"env_reads"`
	Dependencies  []Dependency      `json:"dependencies"`
	Routes        []HTTPRoute       `json:"routes,omitempty"`
	SQLs          []SQLEdge         `json:"sqls,omitempty"`
	Errors        []ErrorEdge       `json:"errors,omitempty"`
	Concurrency   []ConcurrencyNode `json:"concurrency,omitempty"`
	TestEdges     []TestEdge        `json:"test_edges,omitempty"`
	Implements    []ImplementsEdge  `json:"implements,omitempty"`
	Mutations     []MutationEdge    `json:"mutations,omitempty"`
	Literals      []LiteralEdge     `json:"literals,omitempty"`
	HTTPCalls     []HTTPCallEdge    `json:"http_calls,omitempty"`
	FlowFunctions []FlowFunction    `json:"flow_functions,omitempty"`
	Baseline      *GraphBaseline    `json:"baseline,omitempty"`
	Build         *BuildMetadata    `json:"build,omitempty"`
}

// BuildMetadata records whether source selection completed without warnings,
// every selected source file contributed to the graph, and the requested
// precision outcome. AST completeness and precision are independent: a
// complete AST graph may have PrecisionFallback when optional type-checked
// enrichment could not run.
type BuildMetadata struct {
	ScannedFiles int           `json:"scanned_files"`
	ParsedFiles  int           `json:"parsed_files"`
	Complete     bool          `json:"complete"`
	Precision    PrecisionMode `json:"precision,omitempty"`
	// SourcePolicyVersion is a security trust marker, independent of the graph
	// schema version. Missing and non-current values are intentionally not trusted.
	SourcePolicyVersion int `json:"source_policy_version,omitempty"`
	// AnalysisCacheVersion guards reuse of serialized file-level analysis.
	// Missing and non-current values force a complete parser rebuild.
	AnalysisCacheVersion int `json:"analysis_cache_version,omitempty"`
	// ReusedFiles is the number of selected files restored from the previous
	// graph without reparsing. RebuiltPackages counts package directories whose
	// selected files were reparsed together.
	ReusedFiles     int `json:"reused_files,omitempty"`
	RebuiltPackages int `json:"rebuilt_packages,omitempty"`
	// BuildContextFingerprint hashes effective build and module-selection
	// inputs, including nested module boundaries. The historical JSON field
	// name is retained for schema-v2 compatibility.
	BuildContextFingerprint string         `json:"build_context_fingerprint,omitempty"`
	Failures                []BuildFailure `json:"failures,omitempty"`
	Warnings                []string       `json:"warnings,omitempty"`
}

// UsesCurrentSourcePolicy reports whether a graph was built with the current
// repository-source confinement policy.
func (g *Graph) UsesCurrentSourcePolicy() bool {
	return g != nil && g.Build != nil && g.Build.SourcePolicyVersion == CurrentSourcePolicyVersion
}

// PrecisionMode records both the requested analysis strength and its outcome.
// The zero value is reserved for graph.json files written before precision
// metadata existed; EffectivePrecision treats those legacy graphs as AST-only.
type PrecisionMode string

const (
	// PrecisionAST is the normal parser-only graph produced without --precise.
	PrecisionAST PrecisionMode = "ast"
	// PrecisionPrecise means type-checked CHA/SSA enrichment completed.
	PrecisionPrecise PrecisionMode = "precise"
	// PrecisionFallback means precise enrichment was requested but failed, so
	// the AST graph was retained. The request is durable: refreshers should try
	// precise enrichment again after source changes because the edit may fix the
	// type-checking failure.
	PrecisionFallback PrecisionMode = "precise_fallback"
)

// EffectivePrecision normalizes legacy or unrecognized metadata to AST-only.
// Older graph files cannot prove that precise enrichment was requested, so the
// backward-compatible and deterministic policy is to preserve AST behavior.
func (m *BuildMetadata) EffectivePrecision() PrecisionMode {
	if m == nil {
		return PrecisionAST
	}
	switch m.Precision {
	case PrecisionPrecise, PrecisionFallback:
		return m.Precision
	case PrecisionAST, "":
		return PrecisionAST
	default:
		return PrecisionAST
	}
}

// PreciseRequested reports whether future in-memory refreshes should attempt
// precise enrichment. A fallback still represents an explicit precise request.
func (m *BuildMetadata) PreciseRequested() bool {
	mode := m.EffectivePrecision()
	return mode == PrecisionPrecise || mode == PrecisionFallback
}

type BuildFailure struct {
	File  string `json:"file"`
	Error string `json:"error"`
}

// GraphBaseline stores metrics from the previous build for gate comparisons.
type GraphBaseline struct {
	OrphanCount   int `json:"orphan_count"`
	CouplingEdges int `json:"coupling_edges"`
}

// MutationEdge represents a mutation of a struct field. Two kinds:
//
//	Direct  — Via is empty. The function contains a literal assignment to
//	          a selector like  s.field = x  or  *p = v . This is the
//	          original behaviour (Function/Field/File/Line).
//
//	Indirect — Via is set. The function calls a *method* known to mutate
//	           its receiver, and the call site's receiver was a field
//	           selector on the enclosing function's own receiver
//	           (e.g.  s.counter.Increment()  where Increment() writes
//	           to counter's internal state). Via holds the bare name of
//	           the mutating method ("Increment"), so the output can show
//	           "field 'counter' mutated via Increment at line 12" — the
//	           caller can tell apart direct assignments from indirect
//	           mutations through wrapper APIs (atomic.*, sync.Map, etc.).
//
// File/Line always point at the mutation site itself, not at the method
// definition. Indirect mutations are populated by the precise/SSA pass;
// non-precise builds only see direct assignments.
type MutationEdge struct {
	Field    string `json:"field"`
	TypeName string `json:"type_name,omitempty"`
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Via      string `json:"via,omitempty"`
	// Precise marks an edge introduced by type-checked enrichment rather than
	// the parser. It lets incremental builds recover the parser-only base.
	Precise bool `json:"precise,omitempty"`
}

// LiteralEdge records a composite-literal initialization site for a named struct
// (e.g., User{Name: "foo"}). Essential for finding every site that breaks when a
// required field is added to or removed from a struct.
type LiteralEdge struct {
	TypeName string `json:"type_name"`
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

// HTTPCallEdge represents a detected HTTP client call (net/http).
type HTTPCallEdge struct {
	SourceFile     string   `json:"sourceFile"`
	SourceLine     int      `json:"sourceLine"`
	FunctionName   string   `json:"functionName"`
	Method         string   `json:"method"`
	URL            string   `json:"url"`
	StaticSegments []string `json:"staticSegments"`
	HasDynamic     bool     `json:"hasDynamic"`
}

// FlowFunction contains the compact, AST-derived facts used by security-flow
// queries. Facts are stored instead of final findings so query-time sanitizer
// policies can be changed without rebuilding graph.json.
type FlowFunction struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	File   string          `json:"file"`
	Params []FlowParameter `json:"params,omitempty"`
	Facts  []FlowFact      `json:"facts,omitempty"`
}

type FlowParameter struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// FlowFact is one operation in a function's path-insensitive taint model.
// Kind is source, transfer, call, return, or sink. Return and multi-result call
// targets use :N suffixes so status/error values do not contaminate data values.
type FlowFact struct {
	Kind       string     `json:"kind"`
	Target     string     `json:"target,omitempty"`
	Inputs     []string   `json:"inputs,omitempty"`
	Arguments  [][]string `json:"arguments,omitempty"`
	Callee     string     `json:"callee,omitempty"`
	SourceKind string     `json:"source_kind,omitempty"`
	SinkKind   string     `json:"sink_kind,omitempty"`
	Detail     string     `json:"detail,omitempty"`
	Line       int        `json:"line"`
	Column     int        `json:"column,omitempty"`
}

// ImplementsEdge records absolute proof that a concrete type implements an interface.
type ImplementsEdge struct {
	Interface   string `json:"interface"`
	Concrete    string `json:"concrete"`
	InterfaceID string `json:"interface_id,omitempty"`
	ConcreteID  string `json:"concrete_id,omitempty"`
}

// SQLEdge represents an extracted SQL query.
type SQLEdge struct {
	Query    string `json:"query"`
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

// ErrorEdge represents an extracted error message or panic.
type ErrorEdge struct {
	Message  string `json:"message"`
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

// ConcurrencyNode represents a concurrency primitive found in code.
// Kind is one of: "goroutine", "channel_send", "mutex_lock",
// "mutex_unlock", "rwmutex_lock", "rwmutex_unlock", "waitgroup_add",
// "waitgroup_wait", "once_do".
type ConcurrencyNode struct {
	Kind     string `json:"kind"`
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Detail   string `json:"detail,omitempty"`
}

// TestEdge links a *testing.T test function to the symbols it exercises.
type TestEdge struct {
	TestFunc string `json:"test_func"`
	Target   string `json:"target"` // callee name that looks like a production symbol
	File     string `json:"file"`
	Line     int    `json:"line"`
}

// Dependency represents a go.mod dependency.
type Dependency struct {
	Module  string `json:"module"`
	Version string `json:"version"`
}

// HTTPRoute represents an HTTP REST endpoint found in the AST.
type HTTPRoute struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Handler string `json:"handler"`
	// InlineBody holds the rendered source of an anonymous handler function.
	// Populated only when the handler is a *ast.FuncLit (closure), empty otherwise.
	// Captured at build time via go/printer — no file I/O needed at query time.
	InlineBody string `json:"inline_body,omitempty"`
	// DynamicHandler is true when the handler argument is a factory call (e.g.,
	// promhttp.Handler(), authMiddleware()) whose concrete type cannot be
	// statically resolved by AST analysis. The route path is accurate; the
	// handler field contains the factory call name as a best-effort label.
	// Agents should not report these routes as "missed" — they ARE recorded,
	// but the handler cannot be linked to a specific named symbol.
	DynamicHandler bool   `json:"dynamic_handler,omitempty"`
	File           string `json:"file"`
	Line           int    `json:"line"`
}

// PackageNode represents a Go package found in the repository.
type PackageNode struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	ImportPathBestEffort string   `json:"import_path_best_effort"`
	Dir                  string   `json:"dir"`
	Files                []string `json:"files"`
}

// FileNode represents a single .go source file.
type FileNode struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	PackageName string `json:"package_name"`
	Lines       int    `json:"lines"`
	Generated   bool   `json:"generated"`
	// ContentDigest is the SHA-256 digest of the exact source bytes parsed into
	// this file node. It drives freshness checks and safe incremental reuse.
	ContentDigest string `json:"content_digest,omitempty"`
}

// SymbolKind categorises a symbol.
type SymbolKind string

const (
	KindFunction  SymbolKind = "function"
	KindMethod    SymbolKind = "method"
	KindStruct    SymbolKind = "struct"
	KindInterface SymbolKind = "interface"
	KindVar       SymbolKind = "var"
	KindConst     SymbolKind = "const"
	// KindType covers type declarations whose underlying type is neither a
	// struct nor an interface: type aliases (type Foo = Bar), named
	// primitives (type StatusCode int), function types
	// (type HandlerFunc func(...)), channel/map/slice types, etc.
	// Without this, those declarations were silently dropped from the
	// symbol table and became invisible to query/node/public/usages.
	KindType SymbolKind = "type"
)

// SymbolNode represents a named function, method, struct, interface, type,
// variable, or constant.
type SymbolNode struct {
	ID               string            `json:"id"`
	Kind             SymbolKind        `json:"kind"`
	Name             string            `json:"name"`
	Receiver         string            `json:"receiver,omitempty"`
	PackageName      string            `json:"package_name"`
	File             string            `json:"file"`
	Line             int               `json:"line"`
	EndLine          int               `json:"end_line"`
	Doc              string            `json:"doc,omitempty"`
	Signature        string            `json:"signature,omitempty"`
	MethodSignature  string            `json:"method_signature,omitempty"`
	InterfaceMethods map[string]string `json:"interface_methods,omitempty"`
	// DeclaredInterfaceMethods retains parser-owned declarations before precise
	// enrichment adds methods inherited through embedded interfaces.
	DeclaredInterfaceMethods map[string]string `json:"declared_interface_methods,omitempty"`
	StructFields             []StructField     `json:"struct_fields,omitempty"`
	EmbeddedStructs          []string          `json:"embedded_structs,omitempty"`
	Arity                    int               `json:"arity,omitempty"`
}

// StructField represents a field inside a struct.
type StructField struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Tag  string `json:"tag,omitempty"`
}

// CallEdge records a call expression found inside a function/method body.
type CallEdge struct {
	CallerSymbolID string `json:"caller_symbol_id"`
	CallerName     string `json:"caller_name"`
	CalleeRaw      string `json:"callee_raw"`
	// CalleeSymbolID is the *resolved* fully-qualified symbol ID of the
	// callee (e.g. "github.com/foo/bar/internal/auth::(*Service).Validate"),
	// populated by the precise/CHA pass when type info resolves a possible
	// call target. A dynamic interface invocation is represented by parallel
	// CallEdges with identical source provenance and one CalleeSymbolID per
	// valid in-repository CHA target. Empty when:
	//   - gograph was built without --precise
	//   - the callee is in stdlib or a non-source package the type-checker
	//     didn't load
	//   - the dynamic call target is unresolved or outside the repository
	// Consumers should prefer CalleeSymbolID for exact symbol matching
	// (eliminates the (*A).M vs (*B).M name-conflation footgun) and fall
	// back to CalleeRaw for legacy or unresolvable edges.
	CalleeSymbolID string `json:"callee_symbol_id,omitempty"`
	File           string `json:"file"`
	Line           int    `json:"line"`
	Column         int    `json:"column,omitempty"`
	// Synthetic marks an identity-only forwarding edge introduced by precise
	// analysis for an SSA method wrapper. It has no source call site and is
	// retained for graph traversal/reachability only; presentation layers must
	// not report it as a file:line invocation.
	Synthetic bool `json:"synthetic,omitempty"`
	// Precise marks a call introduced by type-checked enrichment. Parser calls
	// remain false, even when precise analysis resolves their CalleeSymbolID.
	Precise bool `json:"precise,omitempty"`
	// ReturnUsage describes how the caller consumes the return value.
	// Values: "discarded", "assigned", "partially_ignored", "returned",
	//         "goroutine", "deferred", "" (nested/passed as argument).
	ReturnUsage string `json:"return_usage,omitempty"`
	// Potential marks a best-effort function value found in a call argument or
	// assignment. BuildGraph resolves these candidates against repository
	// function and method symbols before serializing the graph.
	Potential bool `json:"-"`
}

// ImportEdge records an import statement in a file.
type ImportEdge struct {
	FromFile    string `json:"from_file"`
	FromPackage string `json:"from_package"`
	ImportPath  string `json:"import_path"`
	Alias       string `json:"alias,omitempty"`
}

// EnvRead records a detected environment variable read.
type EnvRead struct {
	Key      string `json:"key"`
	Accessor string `json:"accessor"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Function string `json:"function,omitempty"`
}
