// Package graph defines the core data model for the gograph tool.
package graph

import "time"

// Version is the schema version written into graph.json.
// v2: symbol IDs use module-rooted import paths (e.g. "github.com/org/repo/pkg::Symbol")
//
//	instead of relative file paths ("internal/pkg/file.go::Symbol").
//	This makes IDs stable across file renames within the same package.
const Version = "2"

// Graph is the top-level data structure written to .gograph/graph.json.
type Graph struct {
	Version      string            `json:"version"`
	GeneratedAt  time.Time         `json:"generated_at"`
	Root         string            `json:"root"`
	Packages     []PackageNode     `json:"packages"`
	Files        []FileNode        `json:"files"`
	Symbols      []SymbolNode      `json:"symbols"`
	Imports      []ImportEdge      `json:"imports"`
	Calls        []CallEdge        `json:"calls"`
	EnvReads     []EnvRead         `json:"env_reads"`
	Dependencies []Dependency      `json:"dependencies"`
	Routes       []HTTPRoute       `json:"routes,omitempty"`
	SQLs         []SQLEdge         `json:"sqls,omitempty"`
	Errors       []ErrorEdge       `json:"errors,omitempty"`
	Concurrency  []ConcurrencyNode `json:"concurrency,omitempty"`
	TestEdges    []TestEdge        `json:"test_edges,omitempty"`
	Implements   []ImplementsEdge  `json:"implements,omitempty"`
	Mutations    []MutationEdge    `json:"mutations,omitempty"`
	Literals     []LiteralEdge     `json:"literals,omitempty"`
	HttpCalls    []HttpCallEdge    `json:"http_calls,omitempty"`
	Baseline     *GraphBaseline    `json:"baseline,omitempty"`
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
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Via      string `json:"via,omitempty"`
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

// HttpCallEdge represents a detected HTTP client call (net/http).
type HttpCallEdge struct {
	SourceFile     string   `json:"sourceFile"`
	SourceLine     int      `json:"sourceLine"`
	FunctionName   string   `json:"functionName"`
	Method         string   `json:"method"`
	URL            string   `json:"url"`
	StaticSegments []string `json:"staticSegments"`
	HasDynamic     bool     `json:"hasDynamic"`
}

// ImplementsEdge records absolute proof that a concrete type implements an interface.
type ImplementsEdge struct {
	Interface string `json:"interface"`
	Concrete  string `json:"concrete"`
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
// Kind is one of: "goroutine", "channel_send", "channel_recv", "mutex_lock",
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
	File       string `json:"file"`
	Line       int    `json:"line"`
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

// SymbolNode represents a named symbol (function, method, struct, interface).
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
	StructFields     []StructField     `json:"struct_fields,omitempty"`
	EmbeddedStructs  []string          `json:"embedded_structs,omitempty"`
	Arity            int               `json:"arity,omitempty"`
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
	// populated by the precise/CHA pass when type info uniquely resolves
	// the call target. Empty when:
	//   - gograph was built without --precise
	//   - the callee is in stdlib or a non-source package the type-checker
	//     didn't load
	//   - the call site is dynamic (interface method, function value, etc.)
	//     and CHA couldn't pin a single concrete target
	// Consumers should prefer CalleeSymbolID for exact symbol matching
	// (eliminates the (*A).M vs (*B).M name-conflation footgun) and fall
	// back to CalleeRaw for legacy or unresolvable edges.
	CalleeSymbolID string `json:"callee_symbol_id,omitempty"`
	File           string `json:"file"`
	Line           int    `json:"line"`
	// ReturnUsage describes how the caller consumes the return value.
	// Values: "discarded", "assigned", "partially_ignored", "returned",
	//         "goroutine", "deferred", "" (nested/passed as argument).
	ReturnUsage string `json:"return_usage,omitempty"`
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
