package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/projectfile"
	"github.com/ozgurcd/gograph/internal/scanner"
	"github.com/ozgurcd/gograph/internal/search"
	"github.com/ozgurcd/gograph/internal/session"
	"github.com/ozgurcd/gograph/internal/sourcefs"
	"github.com/ozgurcd/gograph/internal/wiki"
)

var safeGitRef = regexp.MustCompile(`^[A-Za-z0-9._/\-~^]+$`)

func graphRoot(g *graph.Graph) string {
	if g != nil && g.Root != "" {
		return filepath.Clean(g.Root)
	}
	return "."
}

func persistedGraph(fallback *graph.Graph) *graph.Graph {
	root := graphRoot(fallback)
	reader, err := sourcefs.Open(root)
	if err != nil {
		return fallback
	}
	defer func() { _ = reader.Close() }()
	data, err := reader.ReadRegularFile(filepath.Join(".gograph", "graph.json"))
	if err != nil {
		return fallback
	}
	var loaded graph.Graph
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fallback
	}
	if !loaded.UsesCurrentSourcePolicy() {
		return fallback
	}
	// The startup graph determines the trusted repository. Never allow a
	// persisted JSON Root value to redirect later source reads.
	loaded.Root = root
	return &loaded
}

func boolArg(args map[string]any, name string) bool {
	value, _ := args[name].(bool)
	return value
}

func integerArg(args map[string]any, name string, fallback int) (int, error) {
	raw, exists := args[name]
	if !exists {
		return fallback, nil
	}
	var value int
	switch raw := raw.(type) {
	case float64:
		maxExclusive := float64(math.MaxInt) + 1
		minInclusive := float64(math.MinInt)
		if math.IsNaN(raw) || math.IsInf(raw, 0) || math.Trunc(raw) != raw || raw < minInclusive || raw >= maxExclusive {
			return 0, fmt.Errorf("%s must be an integer", name)
		}
		value = int(raw)
	case int:
		value = raw
	case int64:
		if raw < int64(math.MinInt) || raw > int64(math.MaxInt) {
			return 0, fmt.Errorf("%s must be an integer", name)
		}
		value = int(raw)
	default:
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return value, nil
}

func intArg(args map[string]any, name string, fallback, min, max int) (int, error) {
	value, err := integerArg(args, name, fallback)
	if err != nil {
		return 0, err
	}
	if value < min {
		return min, nil
	}
	if max > 0 && value > max {
		return max, nil
	}
	return value, nil
}

// MCPResponse is the stable structured data payload returned by complex tools.
type MCPResponse struct {
	Query          string               `json:"query,omitempty"`
	Summary        string               `json:"summary,omitempty"`
	Source         string               `json:"source,omitempty"`
	SourceError    string               `json:"source_error,omitempty"`
	Node           *search.Result       `json:"node,omitempty"`
	Nodes          []search.Result      `json:"nodes,omitempty"`
	Role           string               `json:"role,omitempty"`
	Callers        []search.Result      `json:"callers,omitempty"`
	Callees        []search.Result      `json:"callees,omitempty"`
	Findings       []search.Result      `json:"findings,omitempty"`
	InspectFirst   []search.Result      `json:"inspect_first,omitempty"`
	ChangedSymbols []search.Result      `json:"changed_symbols,omitempty"`
	Definitions    []search.Result      `json:"definitions,omitempty"`
	Sites          []search.Result      `json:"sites,omitempty"`
	Paths          []search.TraceResult `json:"paths,omitempty"`
	Files          []string             `json:"files,omitempty"`
	Symbols        []string             `json:"symbols,omitempty"`
	Routes         []string             `json:"routes,omitempty"`
	Tests          []string             `json:"tests,omitempty"`
	TestResults    []search.Result      `json:"test_results,omitempty"`
	SQL            []string             `json:"sql,omitempty"`
	Env            []string             `json:"env,omitempty"`
	Errors         []string             `json:"errors,omitempty"`
	Globals        []string             `json:"globals,omitempty"`
	Risk           map[string]any       `json:"risk,omitempty"`
	Limitations    []string             `json:"limitations,omitempty"`
}

type symbolContext = search.ContextPayload

func newSymbolContext(symbol string, result *search.ContextResult) symbolContext {
	return search.NewContextPayload(symbol, result)
}

// ExposeToolsForTesting allows tests to access internal tool handlers. Set to a non-nil map before calling NewServer.
var ExposeToolsForTesting map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)

// ServerOptions configures behavior that affects the MCP server's advertised
// contract. PersistRefresh is opt-in because source-analysis tools can replace
// generated artifacts under .gograph when it is enabled.
type ServerOptions struct {
	PersistRefresh bool
}

func resolveServerOptions(options []ServerOptions) ServerOptions {
	if len(options) == 0 {
		return ServerOptions{}
	}
	return options[len(options)-1]
}

func toolRefreshesGraph(name string) bool {
	switch name {
	case "gograph_capabilities",
		"gograph_stale",
		"gograph_stats",
		"gograph_doc",
		"gograph_session_create",
		"gograph_session_end",
		"gograph_session_audit",
		"gograph_session_cleanup":
		return false
	default:
		return true
	}
}

func persistRefreshDescription(description string) string {
	replacer := strings.NewReplacer(
		"Read-only; no persistent side effects.", "",
		"Read-only; no side effects.", "",
		"Read-only apart from temporary baseline extraction.", "Git baseline extraction uses a temporary directory that is removed after the call.",
		"Read-only; archives only a temp directory that is removed after the call.", "Git baseline extraction uses a temporary directory that is removed after the call.",
	)
	description = strings.Join(strings.Fields(replacer.Replace(description)), " ")
	return description + " Because this server was started with --persist-refresh, a successful stale-graph refresh may replace generated artifacts under .gograph before this tool runs."
}

// NewServer creates and returns the MCP server with all tools registered.
// version is passed in by the caller (cli.Version) so this package does not
// import internal/cli, which would create an import cycle.
func NewServer(
	g *graph.Graph,
	rebuild func() (*graph.Graph, error),
	buildGraph func(string) (*graph.Graph, error),
	buildBaseline func(context.Context, string) (*graph.Graph, error),
	version string,
	options ...ServerOptions,
) *server.MCPServer {
	selectedOptions := resolveServerOptions(options)
	s := server.NewMCPServer(
		"gograph",
		version,
		server.WithToolCapabilities(true),
	)
	serverRoot := graphRoot(g)

	// sessionTools lists the tool names that manage the session lifecycle itself.
	// These are excluded from telemetry recording to avoid noise in the audit log.
	sessionTools := map[string]bool{
		"gograph_session_create":  true,
		"gograph_session_end":     true,
		"gograph_session_audit":   true,
		"gograph_session_cleanup": true,
	}
	type toolBehavior struct {
		readOnly    bool
		destructive bool
		idempotent  bool
		openWorld   bool
	}
	toolBehaviors := map[string]toolBehavior{
		"gograph_boundaries_create": {readOnly: false, destructive: false, idempotent: false},
		"gograph_doc":               {readOnly: true, destructive: false, idempotent: true, openWorld: true},
		"gograph_session_create":    {readOnly: false, destructive: false, idempotent: false},
		"gograph_session_end":       {readOnly: false, destructive: false, idempotent: false},
		"gograph_session_cleanup":   {readOnly: false, destructive: true, idempotent: true},
		"gograph_wiki":              {readOnly: false, destructive: true, idempotent: true},
	}
	var handlerMu sync.Mutex

	addTool := func(tool mcp.Tool, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)) {
		behavior, ok := toolBehaviors[tool.Name]
		if !ok {
			behavior = toolBehavior{readOnly: true, idempotent: true}
		}
		readOnly := behavior.readOnly
		destructive := behavior.destructive
		idempotent := behavior.idempotent
		openWorld := behavior.openWorld
		if selectedOptions.PersistRefresh && toolRefreshesGraph(tool.Name) {
			readOnly = false
			destructive = true
			tool.Description = persistRefreshDescription(tool.Description)
		}

		tool.Annotations.ReadOnlyHint = &readOnly
		tool.Annotations.DestructiveHint = &destructive
		tool.Annotations.IdempotentHint = &idempotent
		tool.Annotations.OpenWorldHint = &openWorld

		// Wrap the handler to record observational command telemetry into an
		// active session. MCP records command, duration, and status so plan/review
		// calls count toward the audit; unlike CLI Run, it has no intention field
		// and deliberately omits tool arguments.
		toolName := tool.Name
		instrumentedHandler := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			handlerMu.Lock()
			defer handlerMu.Unlock()
			return handler(ctx, req)
		}
		if !sessionTools[toolName] {
			instrumentedHandler = func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				handlerMu.Lock()
				defer handlerMu.Unlock()
				start := time.Now()
				result, err := handler(ctx, req)
				elapsed := time.Since(start)
				status := "success"
				if err != nil || (result != nil && result.IsError) {
					status = "failure"
				}
				// Strip the "gograph_" prefix so the command name matches the CLI
				// convention (e.g. "plan", "review", "callers") used by RunAudit.
				cmd := strings.TrimPrefix(toolName, "gograph_")
				_ = session.LogCommandAt(serverRoot, cmd, nil, "", elapsed, status)
				return result, err
			}
		}

		s.AddTool(tool, instrumentedHandler)
		if ExposeToolsForTesting != nil {
			ExposeToolsForTesting[tool.Name] = instrumentedHandler
		}
	}

	// Tool: gograph_capabilities
	capabilitiesTool := mcp.NewTool("gograph_capabilities",
		mcp.WithDescription("List all available gograph MCP tools, their purposes, and recommended agent workflows. Once the project-scoped MCP server has started, this tool has no additional graph-state prerequisite. Read-only; no side effects. WHEN TO USE: Call once per session to orient before issuing analytical queries. NOT TO USE: Do not repeat after capabilities are cached in context. RETURNS: Structured JSON with every registered tool name, one-line purposes, recommended workflow sequences, and known static-analysis limitations."),
	)
	addTool(capabilitiesTool, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		startupGraph := "auto-builds an in-memory graph when it is missing, unreadable, unsafe, or uses an unsupported source policy"
		if selectedOptions.PersistRefresh {
			startupGraph = "auto-builds and publishes graph.json plus nine reports when the artifact is missing, unreadable, unsafe, or uses an unsupported source policy"
		}
		resp := map[string]any{
			"summary":      "gograph MCP capabilities",
			"prerequisite": "The MCP server loads only a regular, repository-confined .gograph/graph.json with the current source-policy marker and " + startupGraph + ". Its serialized root is ignored. Source-analysis tools compare selected-file content digests and the build/module fingerprint per call, then incrementally rebuild changed package ASTs after edits using the latest requested analysis mode; precise CHA/SSA enrichment remains repository-wide for correctness and separately attempts typed test-call attribution without making test compilation a prerequisite for production precision. gograph_stale, gograph_changes without git_ref, and gograph_stats inspect that trusted persisted index, or the startup auto-build fallback when no usable artifact exists. Descendant symlinks and special files recognized as Go build inputs are excluded, and linked/non-regular Go tool metadata (go.mod, go.sum, go.work, go.work.sum, and vendor/modules.txt) is rejected before toolchain use. Applicable workspace members must remain beneath the workspace directory, with each directory, go.mod, and optional go.sum validated before cmd/go. Use the current binary for untrusted repositories because older binaries do not enforce this contract. Run `gograph build . --precise` for type-checked CHA/SSA enrichment; MCP adopts a newer precise graph and re-runs precision after source changes instead of silently downgrading to AST-only analysis. Session lifecycle tools write local telemetry, gograph_session_cleanup deletes stale logs, gograph_boundaries_create writes configuration, and gograph_wiki writes documentation; their repository-controlled paths use rooted regular-file operations that reject descendant links. When an audit session is active, non-session MCP calls append observational command telemetry even when their analysis contract is read-only. Saved graph baselines must be regular files inside the project root, have no linked path component, and carry the exact current source-policy marker; their serialized root is ignored. gograph_doc rejects filesystem-shaped queries and source-tree links the Go toolchain may inspect across the selected root plus its effective module root, or the workspace root and member trees; .git and .gograph are excluded from that preflight. It invokes the local Go toolchain after repository source/metadata validation and is annotated open-world because dependency resolution follows the user's Go environment.",
			"refresh_persistence": map[string]any{
				"enabled":            selectedOptions.PersistRefresh,
				"artifact_directory": ".gograph",
				"scope":              "latest successful refresh only; not a multi-branch cache",
				"artifact_set":       "graph.json plus nine Markdown reports; .artifacts.lock remains as operational coordination state",
				"updates_gitignore":  false,
				"failure_behavior":   "startup publication failure prevents serving; later failures are returned and the fresh in-memory graph is retried without rebuilding",
				"tool_annotations":   "refresh-capable tools are non-read-only only when persistence is enabled",
			},
			"mermaid": map[string]any{
				"parameter": "set mermaid=true to return Markdown-fenced Mermaid instead of structured JSON",
				"tools":     []string{"gograph_callers", "gograph_callees", "gograph_impact", "gograph_endpoint", "gograph_dependents", "gograph_deps", "gograph_path", "gograph_coupling"},
			},
			"tools": []map[string]string{
				{"name": "gograph_capabilities", "purpose": "List all available tools and recommended workflows once the MCP server has started; no additional graph-state prerequisite."},
				{"name": "gograph_stale", "purpose": "Check whether trusted persisted graph.json, or the startup fallback when no usable artifact exists, is outdated versus selected source files or the effective Go build context."},
				{"name": "gograph_session_create", "purpose": "Start a telemetry audit session for tracking agent compliance and tool success metrics."},
				{"name": "gograph_session_end", "purpose": "End the active telemetry session cleanly and write end-of-session logs."},
				{"name": "gograph_session_audit", "purpose": "Review and grade agent compliance (Plan rule, Review rule, Composability/Efficiency) and tool success rates."},
				{"name": "gograph_session_cleanup", "purpose": "Delete stale inactive regular session logs without following linked repository paths; preserves the active log."},
				{"name": "gograph_query", "purpose": "Search symbols, packages, files, and imports by one term or an OR-combined terms array."},
				{"name": "gograph_focus", "purpose": "Full structural summary of one package: files, symbols, internal call edges, and imports. Use before editing an unfamiliar package."},
				{"name": "gograph_context", "purpose": "Pre-flight bundle: first node plus all ambiguous nodes, source/source_error, callers, callees, structured tests, and top-level role. Use uncommitted=true for all modified symbols."},
				{"name": "gograph_plan", "purpose": "Pre-edit plan: symbols to inspect first, tests, routes, env, and risk flags. Set with_context=true to inline the same complete context bundles."},
				{"name": "gograph_review", "purpose": "Post-edit scope summary: changed symbols, tests, routes, env, SQL, and risk flags. Use uncommitted=true after editing."},
				{"name": "gograph_risk", "purpose": "Evaluate the change risk profile of target symbol(s) or uncommitted changes. Returns 0-100 risk score and verdict (SAFE/REVIEW/DANGER)."},
				{"name": "gograph_callers", "purpose": "Direct callers of a function or Interface.Method (one-hop fan-in). Precise interface queries expand every recorded implementation and deduplicate shared source sites."},
				{"name": "gograph_callees", "purpose": "Direct callees of a function (one-hop fan-out). Use to understand downstream dependencies."},
				{"name": "gograph_impact", "purpose": "Full transitive upstream blast radius. Modes: symbol=, uncommitted=true, since=<ref>. Use before refactoring a core function."},
				{"name": "gograph_implementers", "purpose": "Structs that implement a named interface (duck-typing). Set test_only=true for mocks/stubs only."},
				{"name": "gograph_interfaces", "purpose": "Interfaces satisfied by a named struct — inverse of gograph_implementers. Use before refactoring a method to know which contracts break."},
				{"name": "gograph_fields", "purpose": "All fields, types, and struct tags of a named struct."},
				{"name": "gograph_source", "purpose": "Repository-confined source for a named function, method, struct, interface, type, variable, or constant."},
				{"name": "gograph_node", "purpose": "AST metadata for a symbol: kind, file, line, signature, doc. Lighter than gograph_source."},
				{"name": "gograph_orphans", "purpose": "Dead code: functions unreachable from any entry point via full BFS reachability."},
				{"name": "gograph_boundaries", "purpose": "Verify imports against a regular, non-linked in-project boundaries.json. Returns pass/fail and violation list."},
				{"name": "gograph_boundaries_create", "purpose": "Create a regular, repository-rooted boundaries.json; refuses linked paths and overwrite."},
				{"name": "gograph_endpoint", "purpose": "Full vertical slice for one HTTP route: handler, 1-20-depth BFS call chain, SQL, and env reads. include_tests adds routes registered in *_test.go; mermaid selects flowchart output."},
				{"name": "gograph_api", "purpose": "API drift against a Git ref or regular non-linked in-project .json graph with the exact current source-policy marker; serialized roots are ignored."},
				{"name": "gograph_routes", "purpose": "All HTTP routes in the codebase: method, path, handler. Use before gograph_endpoint."},
				{"name": "gograph_flow", "purpose": "Find potential paths from HTTP requests, decoded JSON, or environment values to SQL query text, process execution, filesystem access, or outbound HTTP."},
				{"name": "gograph_errorflow", "purpose": "Trace error sentinel propagation: definition sites, return sites, and upstream call chains to entry points."},
				{"name": "gograph_imports", "purpose": "All files and packages that import a specific package by exact import path."},
				{"name": "gograph_dependents", "purpose": "All packages that import the named package (inverse of gograph_deps). Essential before package-level refactors."},
				{"name": "gograph_deps", "purpose": "Import dependency tree of a package. transitive=true for full BFS closure."},
				{"name": "gograph_envs", "purpose": "All os.Getenv/os.LookupEnv and supported Viper Get* reads in the codebase. Filter by key name substring."},
				{"name": "gograph_tests", "purpose": "Statically attributed test calls. Precise direct calls carry exact symbol IDs; interface targets retain conservative CHA provenance. Omit symbol to list all test edges."},
				{"name": "gograph_hotspot", "purpose": "Functions ranked by fan-in (incoming call count). High fan-in = highest-risk change target."},
				{"name": "gograph_httpcalls", "purpose": "All outbound HTTP client calls via net/http (Get, Post, PostForm, Head). Filter by method or URL."},
				{"name": "gograph_changes", "purpose": "Symbols modified/added/deleted. Deleted includes files absent from the current safely selected inventory. Without git_ref: changes since trusted persisted graph or startup fallback. With git_ref: static diff vs that ref."},
				{"name": "gograph_path", "purpose": "Shortest BFS call chain between two symbols. Confirms whether a handler reaches a given function."},
				{"name": "gograph_complexity", "purpose": "Cyclomatic complexity per function, sorted highest first. Labels: LOW/MEDIUM/HIGH/VERY HIGH; source that cannot be read or parsed safely is retained as UNKNOWN with score -1."},
				{"name": "gograph_coupling", "purpose": "Fan-in (Ca), fan-out (Ce), and instability I=Ce/(Ca+Ce) per package. 0=stable, 1=unstable."},
				{"name": "gograph_returnusage", "purpose": "How each caller uses a function's return value: discarded/assigned/partially_ignored/returned/passed. Run before changing a return signature."},
				{"name": "gograph_arity", "purpose": "Functions meeting an inclusive parameter-count threshold. Default minimum: 5; min=0 includes zero-arity functions."},
				{"name": "gograph_concurrency", "purpose": "Indexed concurrency sites: goroutine spawns, channel sends, mutex/RWMutex, WaitGroup, and Once calls. Filter by kind."},
				{"name": "gograph_fixtures", "purpose": "Test helper structs and factory functions in *_test.go files for a package. Not external data files."},
				{"name": "gograph_godobj", "purpose": "God Object candidates scored by method count, field count, and outgoing calls. Exceeding any enabled threshold qualifies a candidate."},
				{"name": "gograph_skeleton", "purpose": "Full repo API signatures with bodies stripped. WARNING: can be very large on big repos."},
				{"name": "gograph_mutate", "purpose": "Struct-field and package-global mutations; precise mode adds ++/+=, aliases, atomic/sync/wrapper calls, and channel evidence."},
				{"name": "gograph_sql", "purpose": "SQL literals embedded in Go source with enclosing function context. Filter by keyword or table name."},
				{"name": "gograph_errors", "purpose": "All error and panic sites: errors.New, fmt.Errorf, sentinel declarations, and panic calls. Filter by message substring."},
				{"name": "gograph_embeds", "purpose": "All structs that embed the named struct via anonymous field composition."},
				{"name": "gograph_public", "purpose": "Exported symbols of a specific package: functions, methods, types/interfaces, variables, and constants."},
				{"name": "gograph_usages", "purpose": "Every place a named type appears in function signatures (param/return) and struct field types. Run before changing an interface."},
				{"name": "gograph_literals", "purpose": "All composite-literal initialization sites Foo{...} for a named struct. Run before adding a required field — every site returned breaks at compile time."},
				{"name": "gograph_constructors", "purpose": "Factory functions that return the named struct."},
				{"name": "gograph_schema", "purpose": "Structs mapped to a database table via struct tags (db, gorm, etc.)."},
				{"name": "gograph_globals", "purpose": "Package-level variable declarations and the functions that mutate them in a specific package."},
				{"name": "gograph_mocks", "purpose": "Alias for gograph_implementers with test_only=true. Kept for compatibility."},
				{"name": "gograph_explain", "purpose": "LLM-ready narrative for a symbol: role, callers, callees, complexity, SQL, env, routes, concurrency, tests, interfaces — all synthesized."},
				{"name": "gograph_stats", "purpose": "Trusted persisted-index statistics, or startup-fallback statistics when no usable artifact exists: complete/partial build, production precision and test-call resolution status, file/reuse/rebuilt-package counts, failures, and graph totals."},
				{"name": "gograph_trace", "purpose": "Compatibility alias for gograph_errorflow."},
				{"name": "gograph_diagram", "purpose": "Generate Mermaid architecture diagrams grouped by package, module, service, or file."},
				{"name": "gograph_check", "purpose": "Run static policy checks; relative/default config is project-confined, and saved graph baselines use the same trust policy as gograph_api."},
				{"name": "gograph_summary", "purpose": "Single-call codebase briefing: top 3 hotspots, worst instability package, highest complexity function, orphan count, and god-object count. Replaces 5 separate tool calls."},
				{"name": "gograph_untested", "purpose": "Sweep the full graph for called production functions without an exact/static attributed test edge; typed precise test selectors bind exact symbols while CHA targets remain explicit test_resolution=possible candidates."},
				{"name": "gograph_doc", "purpose": "Fetch Go doc after rejecting source-tree links the Go toolchain may inspect across the selected root plus its effective module root, or the workspace root and member trees (.git/.gograph excluded), and validating Go tool metadata plus confined workspace members. Filesystem-shaped queries are rejected; dependency resolution is open-world. Returns a one-element JSON array with query and raw-text output."},
				{"name": "gograph_wiki", "purpose": "Generate machine-first Markdown; relative output is project-rooted, while absolute output is an explicit real local destination."},
			},
			"recommended_workflows": map[string][]string{
				"session_start":  {"READ llm-wiki/index.md", "READ llm-wiki/project.md", "READ llm-wiki/agent-rules.md", "READ llm-wiki/agent-contract.md", "gograph_summary", "gograph_stale"},
				"before_edit":    {"gograph_context", "gograph_plan"},
				"after_edit":     {"gograph_review", "gograph_risk", "gograph_api", "gograph_boundaries"},
				"error_changes":  {"gograph_errorflow", "gograph_review"},
				"security_audit": {"gograph_flow", "gograph_source", "gograph_callers"},
				"api_changes":    {"gograph_api", "gograph_review"},
			},
			"limitations": []string{
				"gograph is static analysis.",
				"MCP tools do not execute target repository code.",
				"The MCP transport is local stdio. Default AST analysis does not call application services, but indexing asks the installed Go toolchain for effective build/module context. Precise analysis additionally type-loads packages and gograph_doc runs go doc; dependency/toolchain resolution follows the user's configured module/cache/network policy and remains open-world.",
				"Repository-directed source, Go build inputs, module/workspace metadata, configs, and persisted-index reads reject relevant linked or special entries. Applicable workspace members are confined beneath their workspace directory and validated before cmd/go; precise package loading and gograph_doc reject source-tree links the Go toolchain may inspect across the selected root plus its effective module root, or the workspace root and member trees (.git/.gograph excluded). Graphs missing the current source-policy marker are rebuilt, and serialized roots are ignored. Older binaries do not enforce this contract and should not be used for untrusted repositories.",
				"Errorflow uses heuristic static call-graph and AST reference analysis. It does not perform SSA or full data-flow tracking.",
				"Security flow analysis is interprocedural and path-insensitive, with call/return matching across at most 16 nested repository calls. Findings are review leads, not proof; unresolved external calls lower confidence.",
				"Ambiguous short names can be disambiguated in MCP tools whose symbol argument advertises standard Go dot-separated package qualification or fully-qualified IDs. gograph_callers additionally supports Interface.Method and expands every recorded precise implementation.",
				"Precise interface dispatch uses conservative CHA. It retains every valid named in-repository implementation, including promoted methods through hidden traversal-only wrapper edges, and may therefore include targets that are not instantiated in one runtime configuration; reflection, unsafe, plugins, unresolved function values, test-only packages, unnamed concrete types, and module-external implementations can remain incomplete.",
				"Test-call attribution is independent from production precision. AST graphs use selector-name heuristics; precise builds separately bind compiling direct test calls exactly while retaining interface candidates as possible. typed_partial means some tests stayed heuristic, and static attribution never proves runtime coverage.",
				"Constant Gin/Echo/Fiber Group prefixes and Chi Route closure prefixes are composed statically, including nested groups. Dynamically computed prefixes remain unresolved; search those routes by their known suffix or handler symbol.",
			},
		}
		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_query
	queryTool := mcp.NewTool("gograph_query",
		mcp.WithDescription("Search the graph index for symbols, packages, files, and import edges that match one or more keyword substrings. Multiple terms use OR semantics, matching CLI `query term...`. The MCP server refreshes source analysis before the call. Read-only; no persistent side effects. WHEN TO USE: During initial exploration when you have a keyword or feature name but do not know its package. NOT TO USE: When you already know the exact symbol (use gograph_source or gograph_node); for package dependency trees (use gograph_deps). RETURNS: Matching symbols, files, and imports; empty when no terms match."),
		mcp.WithString("term", mcp.Description("One keyword search term (e.g. 'AuthService')")),
		mcp.WithArray("terms", mcp.Description("Optional list of keyword terms combined with OR semantics"), mcp.WithStringItems()),
	)
	addTool(queryTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		var terms []string
		if term, ok := args["term"].(string); ok && term != "" {
			terms = append(terms, term)
		}
		if rawTerms, ok := args["terms"].([]any); ok {
			for _, raw := range rawTerms {
				if term, ok := raw.(string); ok && term != "" {
					terms = append(terms, term)
				}
			}
		} else if rawTerms, ok := args["terms"].([]string); ok {
			terms = append(terms, rawTerms...)
		}
		if len(terms) == 0 {
			return mcp.NewToolResultError("provide term or at least one terms entry"), nil
		}
		results := search.Query(g, terms)
		return formatResults(results), nil
	})

	// Tool: gograph_focus
	focusTool := mcp.NewTool("gograph_focus",
		mcp.WithDescription("Extract a comprehensive structural summary of one Go package: all files, defined symbols, internal call edges, and package-level imports. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: When orienting to an unfamiliar package before editing it — provides a full map of what the package contains and how it connects to the rest of the codebase. NOT TO USE: For a single symbol's details (use gograph_context or gograph_source); for global keyword searches (use gograph_query). RETURNS: All files, symbol names, call edges, and import paths within the package; empty when the package is not found."),
		mcp.WithString("package", mcp.Required(), mcp.Description("The package path or name to focus on (e.g., 'internal/auth')")),
	)
	addTool(focusTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		pkg, ok := args["package"].(string)
		if !ok {
			return mcp.NewToolResultError("package must be a string"), nil
		}
		results := search.Focus(g, pkg)
		return formatResults(results), nil
	})

	// Tool: gograph_callers
	callersTool := mcp.NewTool("gograph_callers",
		mcp.WithDescription("Find functions and methods that call the specified function or interface method. Defaults to one-hop fan-in; depth 2-10 expands callers-of-callers. In a precise graph, Interface.Method expands through all recorded implementations and reports a shared source call site once. The MCP server refreshes source analysis before the call. Read-only; no persistent side effects. WHEN TO USE: Before renaming, removing, or changing a function or interface method signature. NOT TO USE: For unbounded upstream blast radius (use gograph_impact); for downstream callees (use gograph_callees). RETURNS: Caller symbols with package paths, file locations, and call-site line numbers; with mermaid=true, Mermaid flowchart text."),
		mcp.WithString("function", mcp.Required(), mcp.Description("The target function or method (supports short name 'BuildGraph', interface notation 'Repository.Delete', concrete dot-notation 'Store.Delete', or a fully-qualified ID)")),
		mcp.WithInteger("depth", mcp.Description("Traversal depth from 1 to 10 (default 1)")),
		mcp.WithBoolean("no_tests", mcp.Description("Exclude call edges originating in *_test.go files")),
		mcp.WithBoolean("exact", mcp.Description("Require an exact symbol-name or fully-qualified-ID match")),
		mcp.WithBoolean("mermaid", mcp.Description("Return Mermaid flowchart text instead of the normal response")),
	)
	addTool(callersTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		fn, ok := args["function"].(string)
		if !ok {
			return mcp.NewToolResultError("function must be a string"), nil
		}
		depth, err := intArg(args, "depth", 1, 1, 10)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		includeTests := !boolArg(args, "no_tests")
		exact := boolArg(args, "exact")
		if boolArg(args, "mermaid") {
			return mcp.NewToolResultText(search.CallersToMermaid(g, fn, depth, includeTests, exact)), nil
		}
		var results []search.Result
		if depth > 1 {
			results = search.CallersDepth(g, fn, depth, includeTests, exact)
		} else {
			results = search.Callers(g, fn, includeTests, exact)
		}
		return formatResults(results), nil
	})

	// Tool: gograph_callees
	calleesTool := mcp.NewTool("gograph_callees",
		mcp.WithDescription("Find functions and methods called by the specified function. Defaults to one-hop fan-out; depth 2-10 expands the downstream call graph. The MCP server refreshes source analysis before the call. Read-only; no persistent side effects. WHEN TO USE: To understand a function's downstream dependencies. NOT TO USE: For upstream callers (use gograph_callers); for package dependency trees (use gograph_deps). RETURNS: Callee symbols with package paths, file locations, and call-site line numbers; with mermaid=true, Mermaid flowchart text."),
		mcp.WithString("function", mcp.Required(), mcp.Description("The name of the calling function to inspect callees for (supports short name 'Serve', dot-notation 'graph.Graph.Build', or fully-qualified ID)")),
		mcp.WithInteger("depth", mcp.Description("Traversal depth from 1 to 10 (default 1)")),
		mcp.WithBoolean("no_tests", mcp.Description("Exclude call edges originating in *_test.go files")),
		mcp.WithBoolean("mermaid", mcp.Description("Return Mermaid flowchart text instead of the normal response")),
	)
	addTool(calleesTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		fn, ok := args["function"].(string)
		if !ok {
			return mcp.NewToolResultError("function must be a string"), nil
		}
		depth, err := intArg(args, "depth", 1, 1, 10)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		includeTests := !boolArg(args, "no_tests")
		if boolArg(args, "mermaid") {
			return mcp.NewToolResultText(search.CalleesToMermaid(g, fn, depth, includeTests)), nil
		}
		var results []search.Result
		if depth > 1 {
			results = search.CalleesDepth(g, fn, depth, includeTests)
		} else {
			results = search.Callees(g, fn, includeTests, false)
		}
		return formatResults(results), nil
	})

	// Tool: gograph_implementers
	implementersTool := mcp.NewTool("gograph_implementers",
		mcp.WithDescription("Find all concrete structs that implement a named Go interface via duck-typing (structs whose method set is a superset of the interface's methods). The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. Set test_only=true to restrict to structs in *_test.go files (mocks/stubs). WHEN TO USE: When tracing polymorphism, locating dependency injection points, or finding all mock implementations of an interface. NOT TO USE: For interfaces a struct satisfies — inverse direction (use gograph_interfaces instead); for struct fields (use gograph_fields). RETURNS: List of implementing struct names with package paths and file locations; empty when no struct implements the interface."),
		mcp.WithString("interface", mcp.Required(), mcp.Description("The name of the interface (e.g., 'AuthService')")),
		mcp.WithBoolean("test_only", mcp.Description("If true, return only structs defined in test or mock files (replaces gograph_mocks)")),
	)
	addTool(implementersTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		iface, ok := args["interface"].(string)
		if !ok {
			return mcp.NewToolResultError("interface must be a string"), nil
		}
		if testOnly, _ := args["test_only"].(bool); testOnly {
			results := search.Mocks(g, iface)
			return formatResults(results), nil
		}
		results := search.Implementers(g, iface)
		return formatResults(results), nil
	})

	// Tool: gograph_fields
	fieldsTool := mcp.NewTool("gograph_fields",
		mcp.WithDescription("Extract all declared fields from a named Go struct: field names, Go types, and raw struct tag strings (json, db, yaml, gorm, etc.). The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: When mapping JSON/DB serialization tags, inspecting struct layouts, or enumerating fields before adding a new one. NOT TO USE: For methods on the struct (use gograph_node or gograph_source); for all struct initialization sites (use gograph_literals). RETURNS: Array of field entries with name, type, and tag string; empty when the struct is not found."),
		mcp.WithString("struct", mcp.Required(), mcp.Description("The exact name of the target struct to inspect fields for (e.g., 'Config', 'User')")),
	)
	addTool(fieldsTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		structName, ok := args["struct"].(string)
		if !ok {
			return mcp.NewToolResultError("struct must be a string"), nil
		}
		results := search.Fields(g, structName)
		return formatResults(results), nil
	})

	// Tool: gograph_source
	sourceTool := mcp.NewTool("gograph_source",
		mcp.WithDescription("Retrieve verbatim Go source for a named function, method, struct, interface, type, variable, or constant, including complete bodies or declarations. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Source reads are confined to regular .go files beneath the analyzed repository and reject symlink path components. Read-only; no side effects. WHEN TO USE: When you need a specific implementation or declaration in full without loading a large file — a targeted alternative to reading the whole file. NOT TO USE: For call hierarchy information (use gograph_callers/gograph_callees); for AST metadata without the full source (use gograph_node). RETURNS: Raw Go source blocks with file paths and line numbers. It errors when the symbol is absent or no matching block can be read safely; an ambiguous query may still return its safely readable matches."),
		mcp.WithString("symbol", mcp.Required(), mcp.Description("The name of the symbol to retrieve source for (supports short name 'ValidateToken', dot-notation 'graph.Graph', or fully-qualified ID)")),
	)
	addTool(sourceTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		sym, ok := args["symbol"].(string)
		if !ok {
			return mcp.NewToolResultError("symbol must be a string"), nil
		}
		// MCP currently defaults to root = g.Root
		code, err := search.Source(g, g.Root, sym)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(code), nil
	})

	// Tool: gograph_orphans
	orphansTool := mcp.NewTool("gograph_orphans",
		mcp.WithDescription("Find functions and methods unreachable from runtime roots (main/init), test/benchmark/fuzz roots, HTTP route handlers, and eligible externally callable exports; exports confined under internal/ are not roots. Uses full BFS reachability. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: During code cleanup or dead-code audits. NOT TO USE: For checking one symbol's usages (use gograph_usages or gograph_callers). RETURNS: Orphan symbols with package paths and file locations; empty means no unreachable code was detected."),
	)
	addTool(orphansTool, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		results := search.ReachableOrphans(g)
		return formatResults(results), nil
	})

	// Tool: gograph_impact
	impactTool := mcp.NewTool("gograph_impact",
		mcp.WithDescription("Traverse the call graph backwards to find every symbol that transitively calls the target — the full upstream blast radius of a change. The MCP server checks freshness before the call. Read-only; no side effects. Three modes: (1) single symbol via `symbol`; (2) uncommitted changes via `uncommitted=true`; (3) git-ref changes via `since`. WHEN TO USE: Before refactoring a core function to see what breaks. NOT TO USE: For direct one-hop callers only (use gograph_callers). RETURNS: Transitive upstream affected symbols; with mermaid=true, Mermaid flowchart text; count:0 JSON when no changed symbols exist."),
		mcp.WithString("symbol", mcp.Description("Symbol name for single-symbol blast radius (supports short name 'ValidateToken', dot-notation 'graph.Graph', or fully-qualified ID)")),
		mcp.WithBoolean("uncommitted", mcp.Description("If true, compute blast radius of all uncommitted modified symbols")),
		mcp.WithString("since", mcp.Description("Git ref (e.g. 'main', 'HEAD~5'): blast radius of all symbols changed since this ref")),
		mcp.WithBoolean("mermaid", mcp.Description("Return Mermaid flowchart text instead of the normal response")),
	)
	addTool(impactTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}

		// --since <ref> mode
		if ref, ok := args["since"].(string); ok && ref != "" {
			root := g.Root
			changes, err := search.ChangesByGitRef(g, root, ref)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if len(changes.Symbols) == 0 {
				return mcp.NewToolResultText(fmt.Sprintf(`{"count":0,"message":"No Go symbol changes found since %q."}`, ref)), nil
			}
			names := make([]string, 0, len(changes.Symbols))
			for _, s := range changes.Symbols {
				names = append(names, s.Name)
			}
			reason := fmt.Sprintf("downstream impact of changes since %s (%d symbols)", ref, len(names))
			if boolArg(args, "mermaid") {
				return mcp.NewToolResultText(search.ImpactMultipleToMermaid(g, names, true)), nil
			}
			results := search.ImpactMultiple(g, names, reason, true)
			return formatResults(results), nil
		}

		// --uncommitted mode
		if u, _ := args["uncommitted"].(bool); u {
			syms, err := search.UncommittedSymbols(g)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if len(syms) == 0 {
				return mcp.NewToolResultText(`{"count":0,"message":"No uncommitted modified symbols found."}`), nil
			}
			reason := fmt.Sprintf("downstream impact of uncommitted changes (%d symbols)", len(syms))
			if boolArg(args, "mermaid") {
				return mcp.NewToolResultText(search.ImpactMultipleToMermaid(g, syms, true)), nil
			}
			results := search.ImpactMultiple(g, syms, reason, true)
			return formatResults(results), nil
		}

		// single symbol mode
		sym, ok := args["symbol"].(string)
		if !ok || sym == "" {
			return mcp.NewToolResultError("must provide symbol, set uncommitted=true, or provide a since ref"), nil
		}
		if boolArg(args, "mermaid") {
			return mcp.NewToolResultText(search.ImpactToMermaid(g, sym, true)), nil
		}
		results := search.Impact(g, sym, true)
		return formatResults(results), nil
	})

	// Tool: gograph_boundaries
	boundariesTool := mcp.NewTool("gograph_boundaries",
		mcp.WithDescription("Refresh source analysis and check package imports against a boundaries.json configuration. The required config defaults to .gograph/boundaries.json; explicit paths must remain inside the analyzed project, and every path component plus the final regular file is read through the rooted repository boundary. Create it with gograph_boundaries_create. Read-only; no side effects. WHEN TO USE: In CI gates or post-edit reviews to enforce layer separation. NOT TO USE: For unconstrained dependency exploration (use gograph_deps or gograph_coupling). RETURNS: Structured pass state and boundary violations."),
		mcp.WithString("config", mcp.Description("Optional in-project path to a regular, non-linked boundary config (default .gograph/boundaries.json)")),
	)
	addTool(boundariesTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}

		configPath := ".gograph/boundaries.json"
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			if cp, ok := args["config"].(string); ok && cp != "" {
				configPath = cp
			}
		}

		results, err := search.Boundaries(g, configPath)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("could not read boundaries config at %s: %v", configPath, err)), nil
		}

		summary := "Boundary violations found."
		pass := false
		if len(results) == 0 {
			summary = "No boundary violations found."
			pass = true
		}

		resp := map[string]any{
			"summary":  summary,
			"findings": results,
			"risk": map[string]any{
				"pass":            pass,
				"violation_count": len(results),
			},
		}

		b, _ := json.MarshalIndent(resp, "", "  ")
		return mcp.NewToolResultText(string(b)), nil
	})

	// Tool: gograph_boundaries_create
	boundariesCreateTool := mcp.NewTool("gograph_boundaries_create",
		mcp.WithDescription("Create a baseline architecture boundary configuration from the repository's current package imports. Defaults to .gograph/boundaries.json under the graph root, uses repository-rooted regular-file creation, and refuses linked paths or overwrite. Mutating and non-idempotent; no network access. WHEN TO USE: Once when adopting boundary checks in an existing repository, then review and tighten the generated rules. NOT TO USE: To verify an existing configuration (use gograph_boundaries). RETURNS: The written config path or an error when the path is unsafe or already exists."),
		mcp.WithString("config", mcp.Description("Optional in-project output path, absolute or repository-relative; linked components and existing entries are refused (default .gograph/boundaries.json)")),
	)
	addTool(boundariesCreateTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		configPath := ".gograph/boundaries.json"
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			if value, ok := args["config"].(string); ok && value != "" {
				configPath = value
			}
		}
		if err := search.CreateBoundaries(g, configPath); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		resolved := configPath
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(graphRoot(g), resolved)
		}
		data, _ := json.MarshalIndent(map[string]any{
			"created": true,
			"config":  filepath.Clean(resolved),
		}, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_endpoint
	endpointTool := mcp.NewTool("gograph_endpoint",
		mcp.WithDescription("Build a full vertical slice for one HTTP route: the matched handler symbol, a BFS call chain downstream (default depth 5), all SQL queries emitted in that chain, and all env vars read. Constant nested Gin/Echo/Fiber Group prefixes and Chi Route closure prefixes are composed into final paths; dynamically computed prefixes remain unresolved and can still be queried by suffix or handler. The MCP server checks content-digest freshness before this call and incrementally refreshes changed package ASTs in the current requested analysis mode; precise and precise_fallback graphs retry repository-wide CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: When auditing what an API endpoint does end-to-end — its downstream dependencies, database queries, and configuration reads. NOT TO USE: For listing all routes (use gograph_routes first to find the pattern); for raw handler source code only (use gograph_source). RETURNS: Array of endpoint slices with route, handler, call chain, SQL, and env fields; found:false with a suggestion when the query does not match any route. `query` accepts route pattern (\"POST /api/users\"), path fragment (\"/users\"), or handler name. `depth` controls call-chain BFS depth (default: 5)."),
		mcp.WithString("query", mcp.Required(), mcp.Description(`Route pattern ("POST /api/users"), path suffix ("POST /users"), or handler symbol name ("CreateUser"). Constant grouped prefixes are resolved; dynamic prefixes remain best-effort.`)),
		mcp.WithInteger("depth", mcp.Description("BFS depth for call chain traversal, clamped to 1-20 (default: 5)")),
		mcp.WithBoolean("include_tests", mcp.Description("Include routes registered in *_test.go files")),
		mcp.WithBoolean("mermaid", mcp.Description("Return Mermaid flowchart text instead of structured JSON")),
	)
	addTool(endpointTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, _ := request.Params.Arguments.(map[string]any)
		query, _ := args["query"].(string)
		if query == "" {
			return mcp.NewToolResultError("query is required"), nil
		}
		depth, err := intArg(args, "depth", 5, 1, 20)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		slices := search.Endpoint(g, query, depth, boolArg(args, "include_tests"))
		if len(slices) == 0 {
			b, _ := json.MarshalIndent(map[string]any{
				"query":   query,
				"found":   false,
				"message": "No matching HTTP routes found. Run gograph_routes to see available routes.",
			}, "", "  ")
			return mcp.NewToolResultText(string(b)), nil
		}
		if boolArg(args, "mermaid") {
			return mcp.NewToolResultText(search.EndpointToMermaid(slices)), nil
		}
		b, _ := json.MarshalIndent(map[string]any{
			"query":  query,
			"found":  true,
			"count":  len(slices),
			"slices": slices,
		}, "", "  ")
		return mcp.NewToolResultText(string(b)), nil
	})

	// Tool: gograph_api

	apiTool := mcp.NewTool("gograph_api",
		mcp.WithDescription("Detect public API surface drift by comparing exported Go symbols between the current working tree and a baseline. A `since` value ending in `.json` loads a regular saved graph inside the project root with no linked path component and the exact current repository source-policy marker; its serialized root is ignored. Otherwise gograph validates the value as a Git ref and uses `git archive` to build a temporary baseline. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only apart from reading the selected graph or extracting a temporary archive that is removed after the call. WHEN TO USE: Before releasing or merging a PR to catch breaking-change regressions — exported symbols added, removed, or renamed since the baseline. NOT TO USE: For listing current exports without a diff baseline (use gograph_public or gograph_skeleton instead). RETURNS: JSON with baseline and breaking flags; nested exported_symbols, interfaces, structs, and routes groups containing added/removed arrays plus changed detail objects; affected_tests, affected_mocks, and findings arrays. Empty groups indicate no drift."),
		mcp.WithString("since", mcp.Required(), mcp.Description("A baseline Git ref (for example 'main' or 'HEAD~1') or regular in-project saved graph path ending in .json, with no linked component and the exact current source-policy marker")),
	)
	addTool(apiTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		sinceRef, ok := args["since"].(string)
		if !ok {
			return mcp.NewToolResultError("since must be a string"), nil
		}

		if !strings.HasSuffix(sinceRef, ".json") {
			if err := sanitizeGitRef(sinceRef); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
		}

		baselineGraph, err := buildBaseline(ctx, sinceRef)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("error building baseline graph: %v", err)), nil
		}

		res := search.APIDrift(baselineGraph, g, sinceRef)

		// Convert the APIDriftResult into formatted JSON string for the agent
		b, _ := json.MarshalIndent(res, "", "  ")
		return mcp.NewToolResultText(string(b)), nil
	})

	// Tool: gograph_routes
	routesTool := mcp.NewTool("gograph_routes",
		mcp.WithDescription("List all HTTP routes registered in the codebase with their HTTP methods, URL patterns, and handler function names. Constant nested Gin/Echo/Fiber Group prefixes and Chi Route closure prefixes are composed into final paths; dynamically computed prefixes remain unresolved. The MCP server checks content-digest freshness before this call and incrementally refreshes changed package ASTs in the current requested analysis mode; precise and precise_fallback graphs retry repository-wide CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: To get the complete API surface of a service before deep-diving into a specific route with gograph_endpoint. NOT TO USE: For full call chain analysis of a route (use gograph_endpoint instead). RETURNS: Structured table of method/path/handler triples; empty when no HTTP routes are registered in the graph."),
	)
	addTool(routesTool, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		results := search.Routes(g)
		return formatResults(results), nil
	})

	// Tool: gograph_context
	contextTool := mcp.NewTool("gograph_context",
		mcp.WithDescription("Fetch a pre-flight context bundle for a single Go symbol: AST node metadata, source code, direct callers, direct callees, linked test functions, and architectural role classification — all in one call. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only analysis; an active audit session may append local command telemetry. Set uncommitted=true to bundle context for all currently modified symbols at once. WHEN TO USE: As the first call before editing a symbol — eliminates 4–5 separate tool roundtrips. NOT TO USE: For package-level orientation (use gograph_focus); for transitive blast radius (use gograph_impact). RETURNS: JSON with node (first match), nodes[] (all matches), source, callers[], callees[], tests[], test_results[], and top-level role; empty object {} when symbol not found. With uncommitted=true, returns a contexts[] array; count:0 when no uncommitted symbols exist."),
		mcp.WithString("symbol", mcp.Description("The exact name, dot-notation 'graph.Graph', or ID of the symbol to retrieve context for.")),
		mcp.WithBoolean("uncommitted", mcp.Description("If true, return context for all uncommitted modified symbols bundled in one response.")),
		mcp.WithBoolean("exact", mcp.Description("Require an exact symbol-name or fully-qualified-ID match in single-symbol mode.")),
	)
	addTool(contextTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}

		root := g.Root

		if u, _ := args["uncommitted"].(bool); u {
			syms, err := search.UncommittedSymbols(g)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if len(syms) == 0 {
				data, _ := json.MarshalIndent(map[string]any{
					"summary":  "No uncommitted modified symbols found.",
					"count":    0,
					"contexts": []any{},
				}, "", "  ")
				return mcp.NewToolResultText(string(data)), nil
			}
			var contexts []symbolContext
			for _, sym := range syms {
				r := search.Context(g, root, sym, false)
				if r == nil {
					continue
				}
				contexts = append(contexts, newSymbolContext(sym, r))
			}
			data, err := json.MarshalIndent(map[string]any{
				"summary":  fmt.Sprintf("Context for %d uncommitted symbol(s)", len(contexts)),
				"count":    len(contexts),
				"contexts": contexts,
			}, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(string(data)), nil
		}

		symbol, ok := args["symbol"].(string)
		if !ok || symbol == "" {
			return mcp.NewToolResultError("must provide either symbol or set uncommitted to true"), nil
		}
		result := search.Context(g, root, symbol, boolArg(args, "exact"))
		if result == nil {
			return mcp.NewToolResultText("{}"), nil
		}
		resp := struct {
			Summary string `json:"summary"`
			search.ContextPayload
			Risk map[string]any `json:"risk"`
		}{
			Summary:        "Context for " + symbol,
			ContextPayload: search.NewContextPayload(symbol, result),
			Risk:           map[string]any{"role": result.Role},
		}
		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_plan
	planTool := mcp.NewTool("gograph_plan",
		mcp.WithDescription("Generate a structured pre-edit plan for a target symbol: which symbols to read first, which tests cover them, which routes and env vars they touch, and whether the change is public-API or SQL-touching. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. Set with_context=true to inline full source+callers+callees for each symbol to inspect — eliminates follow-up gograph_context calls. WHEN TO USE: Before multi-file refactoring or architectural changes to understand scope upfront. NOT TO USE: For trivial single-line fixes; for post-edit verification (use gograph_review instead). RETURNS: JSON with inspect_first[], tests[], routes[], env[], and a risk object (public_api, touches_sql, etc.); with with_context=true, also includes inspect_contexts[] with full per-symbol bundles."),
		mcp.WithString("symbol", mcp.Description("The name of the symbol you intend to modify (supports short name 'ValidateToken', dot-notation 'graph.Graph', or fully-qualified ID)")),
		mcp.WithBoolean("uncommitted", mcp.Description("Set to true to generate a global plan for all currently uncommitted changes across the repository")),
		mcp.WithBoolean("with_context", mcp.Description("If set to true, bundles full context, source code, callers, callees, and architectural roles for each symbol to be inspected")),
	)
	addTool(planTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		var symbolNames []string
		var title string
		if u, ok := args["uncommitted"].(bool); ok && u {
			syms, err := search.UncommittedSymbols(g)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			symbolNames = syms
			title = "Uncommitted Changes"
		} else if sym, ok := args["symbol"].(string); ok && sym != "" {
			symbolNames = []string{sym}
			title = sym
		} else {
			return mcp.NewToolResultError("must provide either symbol or set uncommitted to true"), nil
		}

		planRes := search.Plan(g, symbolNames, title)
		withContext, _ := args["with_context"].(bool)

		resp := map[string]any{
			"summary":       "Change plan for " + planRes.Title,
			"inspect_first": planRes.ReadFirst,
			"tests":         planRes.Tests,
			"routes":        planRes.Routes,
			"env":           planRes.Envs,
			"risk": map[string]any{
				"public_api":     planRes.PublicAPI,
				"touches_sql":    planRes.TouchesSQL,
				"touches_routes": len(planRes.Routes) > 0,
				"touches_env":    len(planRes.Envs) > 0,
			},
		}

		if withContext {
			root := g.Root
			var contexts []symbolContext
			for _, sym := range planRes.ReadFirst {
				_, r := search.ContextForPlanResult(g, root, sym)
				if r == nil {
					continue
				}
				contexts = append(contexts, newSymbolContext(sym.Name, r))
			}
			resp["inspect_contexts"] = contexts
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_review
	reviewTool := mcp.NewTool("gograph_review",
		mcp.WithDescription("Summarize the scope and risk profile of a change: which symbols changed, which tests cover them, which routes and env vars they touch, and whether SQL is involved. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. Requires either symbol or uncommitted=true. WHEN TO USE: After editing — as a post-edit verification step before committing; confirms the blast radius matches expectations. Use uncommitted=true to review all current unstaged changes at once. NOT TO USE: For boundary constraint enforcement (use gograph_boundaries); for pre-edit planning (use gograph_plan). RETURNS: JSON with changed_symbols[], tests[], routes[], env[], errors[], and a risk object (public_api, touches_sql, touches_routes, touches_env)."),
		mcp.WithString("symbol", mcp.Description("The name of the target symbol to run the design review for (e.g. 'AuthService')")),
		mcp.WithBoolean("uncommitted", mcp.Description("Set to true to review all uncommitted/modified changes in the repository")),
	)
	addTool(reviewTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		var symbolNames []string
		var title string
		if u, ok := args["uncommitted"].(bool); ok && u {
			syms, err := search.UncommittedSymbols(g)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			symbolNames = syms
			title = "Uncommitted Changes"
		} else if sym, ok := args["symbol"].(string); ok && sym != "" {
			symbolNames = []string{sym}
			title = sym
		} else {
			return mcp.NewToolResultError("must provide either symbol or set uncommitted to true"), nil
		}

		revRes := search.Review(g, symbolNames, title)

		resp := MCPResponse{
			Summary:        "Code Review for " + revRes.Title,
			ChangedSymbols: revRes.Changes,
			Tests:          revRes.Tests,
			Routes:         revRes.Routes,
			Env:            revRes.Envs,
			Errors:         revRes.Errors,
			Risk: map[string]any{
				"public_api":      revRes.PublicAPI,
				"touches_sql":     revRes.TouchesSQL,
				"touches_routes":  len(revRes.Routes) > 0,
				"touches_env":     len(revRes.Envs) > 0,
				"touches_errors":  len(revRes.Errors) > 0,
				"touches_globals": false,
			},
		}
		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_risk
	riskTool := mcp.NewTool("gograph_risk",
		mcp.WithDescription("Evaluate the change risk profile of target symbol(s) or uncommitted changes. Combines blast radius, cyclomatic complexity, test coverage, and downstream environment/SQL dependencies into a normalized 0–100 risk score and verdict. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. Requires either symbol or uncommitted=true. WHEN TO USE: Before committing edits or when planning changes to understand the technical risk. NOT TO USE: For post-edit review checklist generation (use gograph_review); for pre-edit plan generation (use gograph_plan). RETURNS: JSON with title, results[] containing risk scores, verdicts, and breakdown metrics, and optional message."),
		mcp.WithString("symbol", mcp.Description("The name of the target symbol to run the risk evaluation for (e.g. 'AuthService')")),
		mcp.WithBoolean("uncommitted", mcp.Description("Set to true to evaluate risk for all uncommitted/modified changes in the repository")),
	)
	addTool(riskTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		var symbolNames []string
		var title string
		if u, ok := args["uncommitted"].(bool); ok && u {
			syms, err := search.UncommittedSymbols(g)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			symbolNames = syms
			title = "Uncommitted Changes"
		} else if sym, ok := args["symbol"].(string); ok && sym != "" {
			symbolNames = []string{sym}
			title = sym
		} else {
			return mcp.NewToolResultError("must provide either symbol or set uncommitted to true"), nil
		}

		// Calculate risk report
		report := search.Risk(g, symbolNames, title)

		// Ensure arrays in JSON are initialized to empty slices rather than nil
		if report.Results == nil {
			report.Results = []search.RiskDetail{}
		}

		reportData, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(reportData)), nil
	})

	// Tool: gograph_errorflow
	flowTool := mcp.NewTool("gograph_flow",
		mcp.WithDescription("Find potential untrusted-data paths from HTTP request objects, decoded JSON values, or environment variables to SQL query text, process execution arguments, filesystem paths, or outbound HTTP targets. The MCP server refreshes source analysis before this call; run `gograph build . --precise` first for stronger method/interface targets. Read-only; no side effects. WHEN TO USE: During a security review or before changing request parsing, command execution, file access, SQL construction, or URL handling. NOT TO USE: As proof of exploitability; the analysis is path-insensitive and matches call/return context for at most 16 nested repository calls. RETURNS: Structured findings with source, sink, severity, confidence, and path steps. Configure trusted return-value sanitizers in .gograph/flow.json or with config."),
		mcp.WithString("term", mcp.Description("Optional substring filter matched against functions, files, endpoints, and path steps")),
		mcp.WithString("source", mcp.Description("Optional source kind: http_request, decoded_json, or environment")),
		mcp.WithString("sink", mcp.Description("Optional sink kind: sql_query, process_execution, filesystem, or outbound_http")),
		mcp.WithString("config", mcp.Description("Sanitizer policy path inside the graph root (default .gograph/flow.json when present)")),
		mcp.WithBoolean("no_tests", mcp.Description("Exclude functions in *_test.go files")),
	)
	addTool(flowTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		stringOption := func(name string) (string, error) {
			value, exists := args[name]
			if !exists {
				return "", nil
			}
			text, ok := value.(string)
			if !ok {
				return "", fmt.Errorf("%s must be a string", name)
			}
			return text, nil
		}
		term, err := stringOption("term")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		source, err := stringOption("source")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sink, err := stringOption("sink")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		config, err := stringOption("config")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		results, err := search.Flow(g, search.FlowOptions{
			Term: term, Source: source, Sink: sink, ConfigPath: config,
			IncludeTests: !boolArg(args, "no_tests"),
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		response := struct {
			Query       string              `json:"query,omitempty"`
			Count       int                 `json:"count"`
			Findings    []search.FlowResult `json:"findings"`
			Limitations []string            `json:"limitations"`
		}{
			Query: term, Count: len(results), Findings: results,
			Limitations: []string{"Path-insensitive static analysis with at most 16 nested call-site frames; review findings in source before acting."},
		}
		data, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_errorflow
	errorflowTool := mcp.NewTool("gograph_errorflow",
		mcp.WithDescription("Trace how a named error sentinel or error message string is defined, returned, and propagates up the call graph toward HTTP handlers or CLI entry points. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. Accepts either `query` (preferred) or `term` as the error name or message substring. WHEN TO USE: When auditing how a specific error is produced and handled end-to-end — find definition sites, all return sites, and upstream propagation paths (e.g., ErrNotFound). NOT TO USE: For general upstream traversal of any function (use gograph_callers or gograph_impact); for listing all error definitions (use gograph_errors). RETURNS: Definition sites, return sites, propagation path chains, and related test names; paths is empty when no propagation chain is found. Note: heuristic analysis — does not perform SSA or full data-flow tracking."),
		mcp.WithString("term", mcp.Description("The error string or sentinel error name (e.g., 'ErrInvalidToken' or 'invalid token')")),
		mcp.WithString("query", mcp.Description("The error string or sentinel error name (preferred over term)")),
		mcp.WithBoolean("no_tests", mcp.Description("If true, exclude test files from related-test collection (matches CLI --no-tests)")),
	)
	addTool(errorflowTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}

		var term string
		if q, ok := args["query"].(string); ok && q != "" {
			term = q
		} else if t, ok := args["term"].(string); ok && t != "" {
			term = t
		}

		if term == "" {
			return mcp.NewToolResultError("query or term must be a non-empty string"), nil
		}

		noTests, _ := args["no_tests"].(bool)
		report := search.ErrorFlow(g, term, !noTests)

		resp := search.NewErrorFlowPayload(report)
		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_imports
	importsTool := mcp.NewTool("gograph_imports",
		mcp.WithDescription("Find all files and packages in the codebase that import a specific package by its exact import path. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: When isolating usage of a third-party library before removing or replacing it, or tracing where an internal package is consumed from outside. NOT TO USE: For a package's own outgoing imports (use gograph_deps); for reverse package-level dependency lookup by short name (use gograph_dependents). RETURNS: File paths and package names of all importers; empty when the package is imported nowhere."),
		mcp.WithString("package", mcp.Required(), mcp.Description("The exact import path of the target package to trace imports for (e.g., 'github.com/redis/go-redis')")),
	)
	addTool(importsTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		pkg, ok := args["package"].(string)
		if !ok {
			return mcp.NewToolResultError("package must be a string"), nil
		}
		results := search.ExternalImports(g, pkg)
		return formatResults(results), nil
	})

	// Tool: gograph_dependents
	dependentsTool := mcp.NewTool("gograph_dependents",
		mcp.WithDescription("Find all packages that import the named package (inverse of gograph_deps). The MCP server refreshes source analysis before the call. Read-only; no side effects. WHEN TO USE: Before a package-level interface change or removal. NOT TO USE: For a single function's callers (use gograph_callers). RETURNS: Dependent packages; with mermaid=true, Mermaid flowchart text."),
		mcp.WithString("package", mcp.Required(), mcp.Description("The package to find dependents for (e.g., 'internal/auth', 'auth', or a full import path)")),
		mcp.WithBoolean("mermaid", mcp.Description("Return Mermaid flowchart text instead of the normal response")),
	)
	addTool(dependentsTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		pkg, ok := args["package"].(string)
		if !ok || pkg == "" {
			return mcp.NewToolResultError("package must be a non-empty string"), nil
		}
		results := search.Dependents(g, pkg)
		if boolArg(args, "mermaid") {
			return mcp.NewToolResultText(search.DependentsToMermaid(pkg, results)), nil
		}
		return formatResults(results), nil
	})

	// Tool: gograph_sql
	sqlTool := mcp.NewTool("gograph_sql",
		mcp.WithDescription("Find all SQL query literals embedded in Go source code, with their enclosing function context and file/line locations. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. Optional `term` filters by SQL keyword or table name (e.g., \"SELECT\", \"users\"). WHEN TO USE: When auditing database interactions, reviewing queries for performance issues, or locating all queries that touch a specific table. NOT TO USE: For ORM struct-to-table mappings (use gograph_schema); for env-based configuration (use gograph_envs). RETURNS: List of SQL string literals with file, line, and enclosing function name; empty when no matches found."),
		mcp.WithString("term", mcp.Description("Optional SQL keyword or table name to filter database queries (e.g., 'SELECT', 'users')")),
	)
	addTool(sqlTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		term := ""
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			if t, ok := args["term"].(string); ok {
				term = t
			}
		}
		results := search.SQL(g, term)
		return formatResults(results), nil
	})

	// Tool: gograph_errors
	errorsTool := mcp.NewTool("gograph_errors",
		mcp.WithDescription("Find all error and panic sites in the codebase: errors.New, fmt.Errorf, sentinel var declarations, and panic calls. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. Optional `term` filters by message substring (e.g., \"ErrInvalid\", \"unauthorized\"). WHEN TO USE: When cataloging error codes and panic paths, standardizing error messages, or checking whether a specific error string is already defined before adding a new one. NOT TO USE: For tracing how an error propagates up the call stack (use gograph_errorflow instead). RETURNS: List of error or panic sites with message text, file path, and line number; empty when no matches found."),
		mcp.WithString("term", mcp.Description("Optional keyword to filter the returned error structures (e.g., 'ErrInvalid', 'unauthorized')")),
		mcp.WithBoolean("no_tests", mcp.Description("Exclude error sites in *_test.go files")),
	)
	addTool(errorsTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		term := ""
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			if t, ok := args["term"].(string); ok {
				term = t
			}
		}
		includeTests := true
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			includeTests = !boolArg(args, "no_tests")
		}
		results := search.Errors(g, term, includeTests)
		return formatResults(results), nil
	})

	// Tool: gograph_embeds
	embedsTool := mcp.NewTool("gograph_embeds",
		mcp.WithDescription("Find all Go structs that embed the named struct via anonymous field composition. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: When understanding how a base type is extended throughout the codebase, or before modifying a shared embedded struct to estimate blast radius. NOT TO USE: For interface implementations (use gograph_implementers); for named field type references in other structs (use gograph_usages). RETURNS: List of embedding parent struct names with package paths and file locations; empty when the struct is embedded nowhere."),
		mcp.WithString("struct", mcp.Required(), mcp.Description("The exact name of the target struct to inspect embedding relationships for (e.g., 'Symbol', 'PackageNode')")),
	)
	addTool(embedsTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		structName, ok := args["struct"].(string)
		if !ok {
			return mcp.NewToolResultError("struct must be a string"), nil
		}
		results := search.Embeds(g, structName)
		return formatResults(results), nil
	})

	// Tool: gograph_public
	publicTool := mcp.NewTool("gograph_public",
		mcp.WithDescription("List all exported (public) symbols of a specific package, including functions, methods, types/interfaces, variables, and constants. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: When reviewing a package's public contract before changing it, building integration documentation, or checking what a package exposes to callers. NOT TO USE: For unexported/private symbols (use gograph_node or gograph_focus); for API drift detection against a baseline (use gograph_api). RETURNS: List of exported symbol names with kinds and file locations; empty when the package has no exports or is not found."),
		mcp.WithString("package", mcp.Required(), mcp.Description("The package name or path to inspect (e.g., 'internal/auth')")),
	)
	addTool(publicTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		pkgName, ok := args["package"].(string)
		if !ok {
			return mcp.NewToolResultError("package must be a string"), nil
		}
		results := search.Public(g, pkgName)
		return formatResults(results), nil
	})

	initNewTools(g, rebuild, buildGraph, buildBaseline, addTool)

	// Start stdio server
	return s
}

// Serve runs the gograph MCP server over stdio.
func Serve(
	g *graph.Graph,
	rebuild func() (*graph.Graph, error),
	buildGraph func(string) (*graph.Graph, error),
	buildBaseline func(context.Context, string) (*graph.Graph, error),
	version string,
	options ...ServerOptions,
) error {
	s := NewServer(g, rebuild, buildGraph, buildBaseline, version, options...)
	return server.ServeStdio(s)
}

func formatResults(results []search.Result) *mcp.CallToolResult {
	if len(results) == 0 {
		return mcp.NewToolResultText("No results found.")
	}

	var sb strings.Builder
	for _, r := range results {
		sb.WriteString(r.String())
		sb.WriteString("\n")
	}

	return mcp.NewToolResultText(sb.String())
}

func initNewTools(
	g *graph.Graph,
	rebuild func() (*graph.Graph, error),
	buildGraph func(string) (*graph.Graph, error),
	buildBaseline func(context.Context, string) (*graph.Graph, error),
	addTool func(tool mcp.Tool, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)),
) {
	indexedGraph := g
	serverRoot := graphRoot(indexedGraph)

	// Tool: gograph_usages
	usagesTool := mcp.NewTool("gograph_usages",
		mcp.WithDescription("Find every place a named Go type appears in function parameter lists, return type signatures, and struct field type declarations. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: Before changing an interface or type definition — see the full consumption blast radius across all signatures and struct fields. NOT TO USE: For call sites of a function (use gograph_callers); for struct composite-literal initialization sites (use gograph_literals); for all transitive callers (use gograph_impact). RETURNS: File paths and line locations where the type name appears in signatures or struct fields; empty when the type is not referenced."),
		mcp.WithString("type", mcp.Required(), mcp.Description("The type name to search for (e.g., 'AuthService', 'Repository')")),
	)
	addTool(usagesTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		typeName, ok := args["type"].(string)
		if !ok || typeName == "" {
			return mcp.NewToolResultError("type must be a non-empty string"), nil
		}
		results := search.Usages(g, typeName)
		return formatResults(results), nil
	})

	// Tool: gograph_literals
	literalsTool := mcp.NewTool("gograph_literals",
		mcp.WithDescription("Find every composite-literal initialization site for a named Go struct — all locations where Foo{...} syntax is used to construct the struct. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: Before adding a required field to a struct — every site returned will fail to compile if the new field has no default; run this first to scope the migration blast radius. NOT TO USE: For finding string or integer magic values (use gograph_envs or grep for those); for factory functions that return the struct (use gograph_constructors). RETURNS: All file paths and line numbers where the named struct is composite-initialized; empty when the struct has no direct initialization sites."),
		mcp.WithString("struct", mcp.Required(), mcp.Description("The name of the struct (e.g., 'User')")),
	)
	addTool(literalsTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		structName, ok := args["struct"].(string)
		if !ok || structName == "" {
			return mcp.NewToolResultError("struct must be a non-empty string"), nil
		}
		results := search.Literals(g, structName)
		return formatResults(results), nil
	})

	// Tool: gograph_constructors
	constructorsTool := mcp.NewTool("gograph_constructors",
		mcp.WithDescription("Find all factory and constructor functions that instantiate and return a named Go struct (functions whose return type includes the struct name). The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: When looking for the canonical way to create a struct, or before modifying struct initialization to ensure all construction paths are updated. NOT TO USE: For direct composite-literal sites (use gograph_literals); for struct fields (use gograph_fields). RETURNS: List of constructor function names with signatures, package paths, and file locations; empty when no factory functions are found."),
		mcp.WithString("struct", mcp.Required(), mcp.Description("The exact name of the target Go struct to find constructors for (e.g., 'User', 'Config')")),
	)
	addTool(constructorsTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		str, ok := args["struct"].(string)
		if !ok || str == "" {
			return mcp.NewToolResultError("missing 'struct' argument"), nil
		}
		results := search.Constructors(g, str)
		return formatResults(results), nil
	})

	// Tool: gograph_schema
	schemaTool := mcp.NewTool("gograph_schema",
		mcp.WithDescription("Find Go structs that declare a mapping to a specific database table via struct tags (e.g., `db:\"table_name\"`, `gorm:\"table:table_name\"`). The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: When tracing which Go types represent a database table, or before writing a migration to understand the current ORM model. NOT TO USE: For non-tagged Go structs used as query results (use gograph_fields or gograph_query instead). RETURNS: Matching struct names with package paths and file locations; empty when no structs map to the named table."),
		mcp.WithString("table", mcp.Required(), mcp.Description("The table or schema name to search for in struct tags (e.g., 'users', 'roles')")),
	)
	addTool(schemaTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		tbl, ok := args["table"].(string)
		if !ok || tbl == "" {
			return mcp.NewToolResultError("missing 'table' argument"), nil
		}
		results := search.Schema(g, tbl)
		return formatResults(results), nil
	})

	// Tool: gograph_globals
	globalsTool := mcp.NewTool("gograph_globals",
		mcp.WithDescription("Find package-level variable declarations (var blocks) and the functions that mutate them in a specific package. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: When auditing mutable global state, identifying thread-safety hazards, or locating shared singleton variables before a concurrency refactor. NOT TO USE: For local-scope variables; for environment variable reads (use gograph_envs). RETURNS: Package-level variable names, types, and the functions that write to them; empty when the package has no package-level variables."),
		mcp.WithString("package", mcp.Required(), mcp.Description("The package name or path to inspect (e.g., 'internal/config')")),
	)
	addTool(globalsTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		pkg, ok := args["package"].(string)
		if !ok || pkg == "" {
			return mcp.NewToolResultError("missing 'package' argument"), nil
		}
		results := search.Globals(g, pkg)
		return formatResults(results), nil
	})

	// Tool: gograph_mocks
	mocksTool := mcp.NewTool("gograph_mocks",
		mcp.WithDescription("Find structs in *_test.go files that implement a named interface — test doubles, mocks, and stubs. Equivalent to gograph_implementers with test_only=true; kept for compatibility. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: When writing tests and wanting to find existing mock implementations before creating a new one. NOT TO USE: For production interface implementers (use gograph_implementers without test_only); prefer gograph_implementers(test_only=true) for new code. RETURNS: Test-file struct names implementing the interface with file locations; empty when no test mocks exist for the interface."),
		mcp.WithString("interface", mcp.Required(), mcp.Description("The name of the interface (e.g., 'AuthService')")),
	)
	addTool(mocksTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		iface, ok := args["interface"].(string)
		if !ok || iface == "" {
			return mcp.NewToolResultError("missing 'interface' argument"), nil
		}
		results := search.Mocks(g, iface)
		return formatResults(results), nil
	})

	// Tool: gograph_explain
	explainTool := mcp.NewTool("gograph_explain",
		mcp.WithDescription("Generate a synthesized, LLM-ready narrative for a Go symbol: role classification, callers, callees, complexity, SQL, env vars, HTTP routes, concurrency primitives, tests, and interface satisfaction — all in one structured document. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: For onboarding to an unfamiliar symbol, generating PR documentation, or getting an opinionated architectural assessment without issuing multiple tool calls. NOT TO USE: For raw source code (use gograph_source); for targeted blast-radius analysis (use gograph_impact). RETURNS: Rich structured JSON with role, narrative summary, and all associated cross-references; {\"found\":false} when symbol is not in the graph."),
		mcp.WithString("symbol", mcp.Required(), mcp.Description("The name or ID of the symbol to explain (supports short name 'CreateUser', dot-notation 'graph.Graph', or fully-qualified ID)")),
	)
	addTool(explainTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		sym, ok := args["symbol"].(string)
		if !ok || sym == "" {
			return mcp.NewToolResultError("symbol must be a non-empty string"), nil
		}
		result := search.Explain(g, sym)
		if result == nil {
			return mcp.NewToolResultText(fmt.Sprintf(`{"symbol":"%s","found":false}`, sym)), nil
		}
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_node
	nodeTool := mcp.NewTool("gograph_node",
		mcp.WithDescription("Fetch AST metadata for a named symbol, package, or file: kind, file path, line number, full signature, doc comment, and struct fields if applicable. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: When you need structural metadata (kind, signature, line number) without the full source body — lighter than gograph_source for metadata-only lookups. NOT TO USE: For full source code (use gograph_source); for call relationships (use gograph_callers/gograph_callees). RETURNS: Node properties array with kind, file, line, and signature; empty when the name is not found."),
		mcp.WithString("name", mcp.Required(), mcp.Description("The exact symbol, package path, or Go file name to inspect (e.g., 'Graph', 'internal/search', 'server.go')")),
	)
	addTool(nodeTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		name, ok := args["name"].(string)
		if !ok || name == "" {
			return mcp.NewToolResultError("name must be a non-empty string"), nil
		}
		results := search.Node(g, name)
		return formatResults(results), nil
	})

	// Tool: gograph_envs
	envsTool := mcp.NewTool("gograph_envs",
		mcp.WithDescription("Find all environment variable reads in the codebase via os.Getenv, os.LookupEnv, and common config frameworks, with their enclosing function context. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. Optional `term` filters by key name substring (e.g., \"DATABASE\" matches DATABASE_URL and DATABASE_HOST). WHEN TO USE: When compiling a deployment configuration manifest, documenting required env vars, or auditing what secrets a service reads at startup. NOT TO USE: For reading actual runtime env values (this is static analysis); for database queries (use gograph_sql). RETURNS: List of env key names, calling function, and file/line; empty when no env reads match the filter."),
		mcp.WithString("term", mcp.Description("Optional filter term (e.g., 'DATABASE' matches DATABASE_URL, DATABASE_HOST, etc.)")),
	)
	addTool(envsTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		term := ""
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			if t, ok := args["term"].(string); ok {
				term = t
			}
		}
		results := search.Envs(g, term)
		return formatResults(results), nil
	})

	// Tool: gograph_interfaces
	interfacesTool := mcp.NewTool("gograph_interfaces",
		mcp.WithDescription("Find all Go interfaces satisfied by a named concrete struct (duck-typing resolution — inverse of gograph_implementers). Given a struct name, returns every interface whose complete method set is a subset of that struct's methods. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: When you need to know which contracts a struct implicitly fulfills — useful before refactoring a method to understand which interface contracts will break. NOT TO USE: For finding structs that implement an interface (use gograph_implementers); for listing interface declarations in a package (use gograph_node or gograph_public). RETURNS: Interface names, method signatures, and file locations; empty when the struct satisfies no known interfaces."),
		mcp.WithString("struct", mcp.Required(), mcp.Description("The name of the struct (e.g., 'AuthService')")),
	)
	addTool(interfacesTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		structName, ok := args["struct"].(string)
		if !ok || structName == "" {
			return mcp.NewToolResultError("struct must be a non-empty string"), nil
		}
		results := search.Interfaces(g, structName)
		return formatResults(results), nil
	})

	// Tool: gograph_tests
	testsTool := mcp.NewTool("gograph_tests",
		mcp.WithDescription("Find test functions in *_test.go files that statically exercise a named symbol, or list all attributed test edges when no symbol is given. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise graphs separately type-resolve compiling test packages. Direct selectors and local method values can bind exact symbol IDs; interface dispatch remains bounded CHA-possible evidence, and test-package failures are reported through test_call_resolution=typed_partial rather than weakening production precision. Read-only; no side effects. WHEN TO USE: Before editing a function — check what tests are statically attributed so you know what to run; or to audit test coverage candidates across the codebase. NOT TO USE: For test helper infrastructure (use gograph_fixtures); for running the tests or proving runtime coverage (use `go test` and coverage evidence). RETURNS: Test function names, attributed targets, and file locations; returns all test edges when symbol is omitted; empty when no test edge matches the symbol."),
		mcp.WithString("symbol", mcp.Description("The symbol name to find tests for (optional)")),
	)
	addTool(testsTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		term := ""
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			if s, ok := args["symbol"].(string); ok {
				term = s
			}
		}
		results := search.Tests(g, term)
		return formatResults(results), nil
	})

	// Tool: gograph_hotspot
	hotspotTool := mcp.NewTool("gograph_hotspot",
		mcp.WithDescription("Rank functions by incoming call count (fan-in) to identify the most-depended-on symbols in the codebase. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. `top` controls result count (default: 10; 0 = all). Set include_tests=true to count test-file call edges — by default excluded so test helpers don't dominate rankings in test-heavy codebases. WHEN TO USE: When deciding where to invest refactoring effort or documentation — high fan-in functions are the highest-risk change targets. NOT TO USE: For single-package metrics (use gograph_focus or gograph_coupling); for complexity scores (use gograph_complexity). RETURNS: Ranked list of function names with fan-in count and package location."),
		mcp.WithInteger("top", mcp.Description("Number of results to return (default: 10, 0 = all)")),
		mcp.WithBoolean("include_tests", mcp.Description("Include call edges from *_test.go files. Default false — production fan-in only, otherwise test helpers (baseReq, newTestFoo, etc.) tend to dominate rankings in test-heavy codebases.")),
	)
	addTool(hotspotTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		top := 10
		includeTests := false
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			var err error
			top, err = integerArg(args, "top", top)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if b, ok := args["include_tests"].(bool); ok {
				includeTests = b
			}
		}
		results := search.Hotspot(g, top, includeTests)
		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_deps
	depsTool := mcp.NewTool("gograph_deps",
		mcp.WithDescription("List the import dependencies of a named package. With transitive=false (default), returns direct imports; true returns the BFS closure. The MCP server refreshes source analysis before the call. Read-only; no side effects. WHEN TO USE: When auditing package layering. NOT TO USE: For reverse lookup (use gograph_dependents). RETURNS: direct[] and transitive[] arrays; with mermaid=true, Mermaid flowchart text; found:false when absent."),
		mcp.WithString("package", mcp.Required(), mcp.Description("The target package path or name to inspect (e.g., 'internal/search', 'internal/cli')")),
		mcp.WithBoolean("transitive", mcp.Description("If true, return the full transitive import closure via Breadth-First Search (BFS)")),
		mcp.WithBoolean("mermaid", mcp.Description("Return Mermaid flowchart text instead of structured JSON")),
	)
	addTool(depsTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		pkg, ok := args["package"].(string)
		if !ok || pkg == "" {
			return mcp.NewToolResultError("package must be a non-empty string"), nil
		}
		transitive, _ := args["transitive"].(bool)
		result := search.Deps(g, pkg, transitive)
		if result == nil {
			return mcp.NewToolResultText(fmt.Sprintf(`{"package":%q,"found":false}`, pkg)), nil
		}
		if boolArg(args, "mermaid") {
			return mcp.NewToolResultText(search.DepsToMermaid(g, result)), nil
		}
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_changes
	changesTool := mcp.NewTool("gograph_changes",
		mcp.WithDescription("List Go symbols that have been structurally modified, added, or deleted. In working-tree mode, deleted also covers a prior graph file that is no longer in the current safely selected inventory because it is absent, ignored, build-inactive, or unsafe. Without git_ref, compares against trusted persisted graph.json without refreshing it, or against the startup fallback when no usable artifact exists. With git_ref, refreshes source analysis and performs a static symbol diff against the named Git reference. Read-only; no side effects. WHEN TO USE: After editing to confirm which symbols changed before gograph_impact or gograph_review. NOT TO USE: For line-level text diffs (use git diff); for blast radius (use gograph_impact). RETURNS: Changed symbols grouped by added/modified/deleted; empty arrays when no structural changes are detected."),
		mcp.WithString("git_ref", mcp.Description("Optional git reference to compare against (e.g., 'main', 'HEAD~5', 'v1.4.50')")),
	)
	addTool(changesTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		gitRef := ""
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			if r, ok := args["git_ref"].(string); ok {
				gitRef = r
			}
		}
		if gitRef != "" {
			if newG, err := rebuild(); err == nil {
				g = newG
			} else {
				return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
			}
			root := graphRoot(g)
			result, err := search.ChangesByGitRef(g, root, gitRef)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			data, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(string(data)), nil
		}
		base := persistedGraph(indexedGraph)
		result := search.Changes(base, graphRoot(base))
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_path
	pathTool := mcp.NewTool("gograph_path",
		mcp.WithDescription("Find the shortest BFS call chain from one symbol to another. The MCP server refreshes source analysis before the call. Read-only; no side effects. WHEN TO USE: To confirm reachability between non-adjacent symbols. NOT TO USE: For all transitive upstream callers (use gograph_impact). RETURNS: from, to, found, and steps[]; with mermaid=true, Mermaid flowchart text."),
		mcp.WithString("from", mcp.Required(), mcp.Description("The starting symbol name")),
		mcp.WithString("to", mcp.Required(), mcp.Description("The target symbol name")),
		mcp.WithBoolean("mermaid", mcp.Description("Return Mermaid flowchart text instead of structured JSON")),
	)
	addTool(pathTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		from, ok := args["from"].(string)
		if !ok || from == "" {
			return mcp.NewToolResultError("from must be a non-empty string"), nil
		}
		to, ok := args["to"].(string)
		if !ok || to == "" {
			return mcp.NewToolResultError("to must be a non-empty string"), nil
		}
		chain := search.Path(g, from, to, true)
		if len(chain) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf(`{"from":%q,"to":%q,"found":false}`, from, to)), nil
		}
		if boolArg(args, "mermaid") {
			return mcp.NewToolResultText(search.PathToMermaid(chain)), nil
		}
		data, err := json.MarshalIndent(map[string]any{
			"from":  from,
			"to":    to,
			"found": true,
			"steps": chain,
		}, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_stale
	staleTool := mcp.NewTool("gograph_stale",
		mcp.WithDescription("Check whether the trusted persisted graph index loaded from a regular, repository-confined .gograph/graph.json differs from the current selected-file inventory, effective Go build context, or selected source content digests. Modification times are returned only as diagnostics; legacy indexes without digests temporarily use the former mtime fallback until rebuilt. This tool intentionally does not refresh first; when the artifact is missing, unreadable, unsafe, or uses an unsupported source policy it compares against the startup auto-build fallback. Read-only; no side effects. WHEN TO USE: To decide whether CLI snapshot analysis or precise enrichment needs rebuilding. NOT TO USE: For module dependency freshness; for changed symbols (use gograph_changes). RETURNS: is_stale, graph_age, newest source metadata, changed_files, and build_context_changed."),
	)
	addTool(staleTool, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		base := persistedGraph(indexedGraph)
		sr := search.Stale(base, graphRoot(base))
		data, err := json.MarshalIndent(sr, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_complexity
	complexityTool := mcp.NewTool("gograph_complexity",
		mcp.WithDescription("Report estimated cyclomatic complexity for Go functions, sorted highest-to-lowest with severity labels (LOW/MEDIUM/HIGH/VERY HIGH). A function whose repository source cannot be read or parsed safely is retained as UNKNOWN with score -1. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. Optional `symbol` substring filters to a specific function or set of functions. WHEN TO USE: During code quality audits, identifying functions that need decomposition, or setting complexity budgets in CI. NOT TO USE: For import dependency metrics (use gograph_coupling or gograph_deps); for God Object detection (use gograph_godobj). RETURNS: Structured list of functions with complexity score and severity label; empty when no functions match the filter."),
		mcp.WithString("symbol", mcp.Description("Optional Go function or method symbol name substring to filter the complexity report (e.g., 'Build')")),
	)
	addTool(complexityTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		term := ""
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			if s, ok := args["symbol"].(string); ok {
				term = s
			}
		}
		results := search.Complexity(g, term)
		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_coupling
	couplingTool := mcp.NewTool("gograph_coupling",
		mcp.WithDescription("Report fan-in (Ca), fan-out (Ce), and instability I=Ce/(Ca+Ce) per package. The MCP server refreshes source analysis before the call. Read-only; no side effects. package filters by substring; include_stdlib and internal_only control scope. WHEN TO USE: To evaluate package isolation. RETURNS: Package coupling records; with mermaid=true, Mermaid flowchart text."),
		mcp.WithString("package", mcp.Description("Optional package name substring to filter results")),
		mcp.WithBoolean("include_stdlib", mcp.Description("Include standard-library packages in the report. Default false — users asking 'how coupled is my code?' rarely care about stdlib coupling.")),
		mcp.WithBoolean("internal_only", mcp.Description("Restrict the report to the project's own packages (anything starting with the module path from go.mod). Strictly stronger than excluding stdlib — also excludes third-party deps.")),
		mcp.WithBoolean("mermaid", mcp.Description("Return Mermaid flowchart text instead of structured JSON")),
	)
	addTool(couplingTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		term := ""
		opts := search.CouplingOptions{}
		mermaid := false
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			if p, ok := args["package"].(string); ok {
				term = p
			}
			if b, ok := args["include_stdlib"].(bool); ok {
				opts.IncludeStdlib = b
			}
			if b, ok := args["internal_only"].(bool); ok && b {
				if mod := search.ReadModulePath(graphRoot(g)); mod != "" {
					opts.ModuleOnly = mod
				}
			}
			mermaid = boolArg(args, "mermaid")
		}
		if mermaid {
			return mcp.NewToolResultText(search.CouplingToMermaid(g, term, opts)), nil
		}
		results := search.Coupling(g, term, opts)
		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_arity
	arityTool := mcp.NewTool("gograph_arity",
		mcp.WithDescription("Find functions and methods with at least a threshold number of parameters — the long-parameter-list smell. The MCP server checks freshness before this call. Read-only; no side effects. `min` sets the inclusive minimum (default: 5; 0 includes zero-arity functions), matching CLI --min. WHEN TO USE: During code smell audits. NOT TO USE: For struct field counts (use gograph_fields or gograph_godobj). RETURNS: Functions meeting the threshold with parameter count, signature, and file location."),
		mcp.WithInteger("min", mcp.Description("Inclusive minimum argument count to report (default: 5; 0 includes zero-arity functions)")),
	)
	addTool(arityTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		minArgs := 5
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			var err error
			minArgs, err = integerArg(args, "min", minArgs)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
		}
		results := search.Arity(g, minArgs)
		return formatResults(results), nil
	})

	// Tool: gograph_concurrency
	concurrencyTool := mcp.NewTool("gograph_concurrency",
		mcp.WithDescription("Find indexed concurrency sites in the codebase: goroutine spawns (`go` statements), channel sends, and calls on sync.Mutex/RWMutex, sync.WaitGroup, and sync.Once. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. Optional `term` filter (e.g., \"mutex\", \"goroutine\", \"channel\"). WHEN TO USE: When auditing race safety, understanding async flow, or locating synchronization points before a concurrency refactor. NOT TO USE: For standard sequential call flow analysis (use gograph_callers/gograph_callees). RETURNS: File locations, line numbers, and primitive kind for each indexed concurrency site; empty when no sites are found. Channel receives and select statements are not indexed."),
		mcp.WithString("term", mcp.Description("Optional filter term (e.g., 'goroutine', 'mutex', 'channel')")),
	)
	addTool(concurrencyTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		term := ""
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			if t, ok := args["term"].(string); ok {
				term = t
			}
		}
		results := search.Concurrency(g, term)
		return formatResults(results), nil
	})

	// Tool: gograph_httpcalls
	httpcallsTool := mcp.NewTool("gograph_httpcalls",
		mcp.WithDescription("Find all outbound HTTP client calls detected in the codebase via net/http package-level functions: http.Get, http.Post, http.PostForm, http.Head. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. Optional `term` filters by method, URL, or function name substring. WHEN TO USE: When auditing external API dependencies, understanding which services your code calls, or identifying all outbound HTTP traffic. NOT TO USE: For HTTP server route definitions (use gograph_routes). RETURNS: List of HTTP method, URL, static path segments, dynamic flag, calling function, and file/line; empty when no HTTP client calls match."),
		mcp.WithString("term", mcp.Description("Optional filter term (matches method, URL, or function name — e.g., 'POST' or 'api.example.com')")),
	)
	addTool(httpcallsTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		term := ""
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			if t, ok := args["term"].(string); ok {
				term = t
			}
		}
		results := search.HTTPCalls(g, term)
		return formatResults(results), nil
	})

	// Tool: gograph_fixtures
	fixturesTool := mcp.NewTool("gograph_fixtures",
		mcp.WithDescription("Find test helper structs and factory/builder functions declared in *_test.go files for a named package. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: Before writing new tests — check what test infrastructure (helper builders, stub factories, shared setup structs) already exists in the package to avoid duplication. NOT TO USE: For test functions that exercise a symbol (use gograph_tests); for external test data files on disk (those are not tracked in the graph — use filesystem search). RETURNS: Symbols defined in test files for the package including helper structs and factory functions; empty when the package has no test helper infrastructure."),
		mcp.WithString("package", mcp.Required(), mcp.Description("The package path or name (e.g., 'internal/auth')")),
	)
	addTool(fixturesTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		pkg, ok := args["package"].(string)
		if !ok || pkg == "" {
			return mcp.NewToolResultError("package must be a non-empty string"), nil
		}
		results := search.Fixtures(g, pkg)
		return formatResults(results), nil
	})

	// Tool: gograph_godobj
	godobjTool := mcp.NewTool("gograph_godobj",
		mcp.WithDescription("Detect God Object anti-pattern candidates by scoring structs on method count, field count, and outgoing call count. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. Thresholds: `methods` (default: 5), `fields` (default: 8), `calls` (default: 15); `top` limits results (default: 10). Exceeding any enabled threshold qualifies a struct, and the combined excess determines rank. WHEN TO USE: During architecture reviews to find monolithic structs that should be decomposed. NOT TO USE: For general struct layout inspection (use gograph_fields); for single-function complexity (use gograph_complexity). RETURNS: Ranked candidates with method, field, and call counts; empty when no threshold is exceeded."),
		mcp.WithInteger("methods", mcp.Description("Minimum method count (default: 5)")),
		mcp.WithInteger("fields", mcp.Description("Minimum field count (default: 8)")),
		mcp.WithInteger("calls", mcp.Description("Minimum outgoing call count (default: 15)")),
		mcp.WithInteger("top", mcp.Description("Maximum results to return (default: 10)")),
	)
	addTool(godobjTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		p := search.DefaultGodObjectParams()
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			methods, err := integerArg(args, "methods", p.MinMethods)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			fields, err := integerArg(args, "fields", p.MinFields)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			calls, err := integerArg(args, "calls", p.MinCalls)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			top, err := integerArg(args, "top", p.Top)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if methods >= 0 {
				p.MinMethods = methods
			}
			if fields >= 0 {
				p.MinFields = fields
			}
			if calls >= 0 {
				p.MinCalls = calls
			}
			if top >= 0 {
				p.Top = top
			}
		}
		candidates := search.GodObjects(g, p)
		data, err := json.MarshalIndent(candidates, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_skeleton
	skeletonTool := mcp.NewTool("gograph_skeleton",
		mcp.WithDescription("Emit the full repository's API signatures with function bodies stripped — struct definitions, interface declarations, and function/method signatures only. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. WARNING: output can be very large on big repositories — consider using gograph_public per package for targeted queries. WHEN TO USE: When an LLM needs a compact map of the entire codebase's shape without reading source files individually. NOT TO USE: For full implementations (use gograph_source); for a single package (use gograph_public). RETURNS: Multi-line text of all stripped declarations across all packages; always non-empty when the graph has symbols."),
	)
	addTool(skeletonTool, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		return mcp.NewToolResultText(search.Skeleton(g)), nil
	})

	// Tool: gograph_returnusage
	returnusageTool := mcp.NewTool("gograph_returnusage",
		mcp.WithDescription("Show how each caller uses the return value(s) of a named function: discarded, assigned, partially ignored, returned upstream, or passed directly to another call. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: Before changing a function's return signature — see which callers ignore the error or only use some return values. NOT TO USE: For error propagation tracing (use gograph_errorflow); for finding all callers without usage detail (use gograph_callers). RETURNS: List of call sites with usage classification (discarded/assigned/partially_ignored/returned/passed); empty when the function has no callers."),
		mcp.WithString("function", mcp.Required(), mcp.Description("The function name to analyse (e.g., 'ValidateToken')")),
	)
	addTool(returnusageTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		fn, ok := args["function"].(string)
		if !ok || fn == "" {
			return mcp.NewToolResultError("function must be a non-empty string"), nil
		}
		results := search.ReturnUsages(g, fn)
		return formatResults(results), nil
	})

	// Tool: gograph_mutate
	mutateTool := mcp.NewTool("gograph_mutate",
		mcp.WithDescription("Find struct-field and package-global mutation sites. Use Type.Field to exclude same-named fields on unrelated types; ordinary local-variable assignments are excluded. The MCP server refreshes in the current requested analysis mode; a precise graph adds ++/+=, pointer-alias, atomic/sync/wrapper, and channel mutations and re-runs that analysis after source edits. Read-only; no side effects. WHEN TO USE: Diagnosing state changes or auditing mutability. NOT TO USE: For field declarations (gograph_fields) or whole-struct initialization (gograph_literals). RETURNS: Mutation locations and indirect mutator method names when applicable."),
		mcp.WithString("field", mcp.Required(), mcp.Description("The field name to search for mutations (e.g., 'Status')")),
	)
	addTool(mutateTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		field, ok := args["field"].(string)
		if !ok || field == "" {
			return mcp.NewToolResultError("field must be a non-empty string"), nil
		}
		results := search.Mutate(g, field)
		return formatResults(results), nil
	})

	// Tool: gograph_stats
	statsTool := mcp.NewTool("gograph_stats",
		mcp.WithDescription("Report trusted persisted-index health and counts without refreshing source analysis, or startup-fallback health when graph.json is missing, unreadable, unsafe, or uses an unsupported source policy: schema/build timestamps, complete/partial status, ast/precise/precise_fallback analysis status, scanned/parsed/reused/rebuilt-package/failure counts, and graph entity totals. Read-only; no side effects. WHEN TO USE: To validate the snapshot/fallback before relying on its data. NOT TO USE: For a live symbol profile (use gograph_node or gograph_complexity). RETURNS: Structured build health and repository counts."),
	)
	addTool(statsTool, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		st := search.Stats(persistedGraph(indexedGraph))
		data, err := json.MarshalIndent(st, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_trace
	// Alias for gograph_errorflow -- kept for backward compatibility with agents
	// that learned the 'trace' name from earlier CLI versions or documentation.
	traceTool := mcp.NewTool("gograph_trace",
		mcp.WithDescription("Alias for gograph_errorflow. Refreshes in-memory source analysis, then traces an error string heuristically from its definition up through the call chain to HTTP handlers. Read-only; no side effects. WHEN TO USE: Prefer gograph_errorflow; this alias exists for compatibility. RETURNS: The same structured output as gograph_errorflow."),
		mcp.WithString("term", mcp.Required(), mcp.Description("Error string or symbol name to trace (e.g. 'ErrNotFound', 'permission denied')")),
		mcp.WithBoolean("no_tests", mcp.Description("If true, skip collecting related test functions")),
	)
	addTool(traceTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		term, ok := args["term"].(string)
		if !ok || term == "" {
			return mcp.NewToolResultError("term must be a non-empty string"), nil
		}
		noTests := false
		if v, ok := args["no_tests"].(bool); ok {
			noTests = v
		}
		result := search.ErrorFlow(g, term, !noTests)
		data, err := json.MarshalIndent(search.NewErrorFlowPayload(result), "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_diagram
	diagramTool := mcp.NewTool("gograph_diagram",
		mcp.WithDescription("Refresh source analysis and generate a Mermaid architecture diagram of the package dependency graph. Read-only; no side effects. WHEN TO USE: Onboarding, architecture review, or communicating package structure. Use group_by=module for monorepos and group_by=file for drill-downs. NOT TO USE: For call-graph traversal or single-package focus. RETURNS: Mermaid text; use max_depth or coarser grouping for large graphs."),
		mcp.WithString("group_by", mcp.Description("Grouping level: 'package' (default), 'module', 'service', or 'file'")),
		mcp.WithInteger("max_depth", mcp.Description("Maximum BFS depth from graph roots (0 = unlimited)")),
		mcp.WithBoolean("include_stdlib", mcp.Description("If true, include Go standard library packages in the diagram")),
	)
	addTool(diagramTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, _ := request.Params.Arguments.(map[string]any)
		groupBy := "package"
		maxDepth := 0
		includeStdlib := false
		if args != nil {
			if v, ok := args["group_by"].(string); ok && v != "" {
				groupBy = v
			}
			var err error
			maxDepth, err = integerArg(args, "max_depth", maxDepth)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if v, ok := args["include_stdlib"].(bool); ok {
				includeStdlib = v
			}
		}
		diagram := search.DiagramToMermaid(g, groupBy, maxDepth, includeStdlib)
		return mcp.NewToolResultText(diagram), nil
	})

	// Tool: gograph_check
	checkTool := mcp.NewTool("gograph_check",
		mcp.WithDescription("Refresh source analysis and run static policy checks: boundaries, API drift, changed-route/export tests, test coverage, orphans, globals, arity, and complexity. The default or a relative checks config is confined to a regular non-linked file beneath the project; an absolute config is an explicit operator-selected regular file. Baselines use the same validated builder as CLI: a value ending in `.json` loads a regular saved graph inside the project root with no linked component and the exact current source-policy marker, ignoring its serialized root; otherwise it is treated as a Git ref and extracted temporarily. WHEN TO USE: During PR review or pre-commit analysis. NOT TO USE: For CI process exit enforcement (use CLI gograph gate). RETURNS: Structured pass/warn/fail status, findings, and summary counts."),
		mcp.WithString("since", mcp.Description("Git ref or regular in-project saved graph path ending in .json, with no linked component and the exact current source-policy marker, for api_drift")),
		mcp.WithBoolean("uncommitted", mcp.Description("If true, include uncommitted changes in the analysis scope")),
		mcp.WithString("config", mcp.Description("Optional checks.json path; relative/default paths are project-confined, while an absolute path explicitly selects a regular local file")),
	)
	addTool(checkTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		args, _ := request.Params.Arguments.(map[string]any)
		var sinceRef string
		uncommitted := false
		configPath := ""
		if args != nil {
			if v, ok := args["since"].(string); ok {
				sinceRef = v
			}
			if v, ok := args["uncommitted"].(bool); ok {
				uncommitted = v
			}
			if v, ok := args["config"].(string); ok {
				configPath = v
			}
		}
		root := g.Root
		if root == "" {
			root = "."
		}
		cfg := &search.CheckConfig{
			Checks: map[string]any{
				"boundaries":     "warn",
				"max_arity":      map[string]any{"level": "warn", "value": 6.0},
				"max_complexity": map[string]any{"level": "warn", "value": 20.0},
			},
			BoundariesConfig: filepath.Join(root, ".gograph", "boundaries.json"),
		}
		data, _, foundConfig, err := projectfile.ReadConfig(root, configPath, ".gograph/checks.json")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to read config: %v", err)), nil
		}
		if foundConfig {
			if err := json.Unmarshal(data, cfg); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse config: %v", err)), nil
			}
		}
		if cfg.BoundariesConfig != "" && !filepath.IsAbs(cfg.BoundariesConfig) {
			cfg.BoundariesConfig = filepath.Join(root, cfg.BoundariesConfig)
		}
		if sinceRef != "" {
			cfg.Baseline = sinceRef
		}
		var baselineGraph *graph.Graph
		if cfg.Baseline != "" {
			var err error
			baselineGraph, err = buildBaseline(ctx, cfg.Baseline)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to build baseline graph for %q: %v", cfg.Baseline, err)), nil
			}
		}
		p := &search.CheckParams{
			CurrentGraph:  g,
			BaselineGraph: baselineGraph,
			Config:        cfg,
			SinceRef:      cfg.Baseline,
			Uncommitted:   uncommitted,
			RootDir:       root,
		}
		report, err := search.RunChecks(p)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("check failed: %v", err)), nil
		}
		checkData, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(checkData)), nil
	})

	// Tool: gograph_session_create
	sessionCreateTool := mcp.NewTool("gograph_session_create",
		mcp.WithDescription("Start a telemetry audit session for tracking agent compliance and tool success metrics. Writes only regular, repository-confined session state under .gograph/sessions and refuses linked storage; MCP annotations mark it mutating and non-idempotent. No prerequisites once the MCP server is running. WHEN TO USE: Call once at the start of a multi-step coding task to track your work. NOT TO USE: When a session is already active. RETURNS: Structured message with the newly generated session ID."),
		mcp.WithString("custom_word", mcp.Description("Optional custom word prefix to incorporate in the timestamped session ID (e.g. 'implement_feature')")),
	)
	addTool(sessionCreateTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := request.Params.Arguments.(map[string]any)
		customWord := ""
		if args != nil {
			if w, ok := args["custom_word"].(string); ok {
				customWord = w
			}
		}
		sessionID, err := session.StartSessionAt(serverRoot, customWord)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Session %q successfully created and activated.", sessionID)), nil
	})

	// Tool: gograph_session_end
	sessionEndTool := mcp.NewTool("gograph_session_end",
		mcp.WithDescription("End the active telemetry session cleanly, append its end record, and remove the active-session pointer through repository-confined regular-file operations. MCP annotations mark it mutating and non-idempotent. No additional prerequisite once the MCP server is running. WHEN TO USE: Call once after you have completed all edits and post-edit reviews. NOT TO USE: When no session is active. RETURNS: Message confirming ending of the session."),
	)
	addTool(sessionEndTool, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sessionID, err := session.EndSessionAt(serverRoot)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Session %q successfully ended.", sessionID)), nil
	})

	// Tool: gograph_session_audit
	sessionAuditTool := mcp.NewTool("gograph_session_audit",
		mcp.WithDescription("Review and grade agent compliance (Plan rule, Review rule, Composability/Efficiency) and tool success rates. Session IDs are strictly validated and only regular repository-confined logs are read. No additional prerequisite once the MCP server is running. WHEN TO USE: After ending a session to obtain compliance metrics and recommendations. RETURNS: Audited session details and grade."),
		mcp.WithString("session_id", mcp.Description("Optional session ID to audit. If not supplied, audits the most recent session in the repository.")),
		mcp.WithBoolean("json", mcp.Description("Set to true to return structured JSON format instead of human-readable ASCII layout.")),
	)
	addTool(sessionAuditTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := request.Params.Arguments.(map[string]any)
		sessionID := ""
		jsonMode := false
		if args != nil {
			if s, ok := args["session_id"].(string); ok {
				sessionID = s
			}
			if j, ok := args["json"].(bool); ok {
				jsonMode = j
			}
		}

		var stdout, stderr bytes.Buffer
		exitCode := session.RunAuditToAt(serverRoot, sessionID, jsonMode, &stdout, &stderr)

		if exitCode != 0 {
			return mcp.NewToolResultError(fmt.Sprintf("Audit failed: %s", stderr.String())), nil
		}

		return mcp.NewToolResultText(stdout.String()), nil
	})

	// Tool: gograph_session_cleanup
	sessionCleanupTool := mcp.NewTool("gograph_session_cleanup",
		mcp.WithDescription("Delete stale inactive regular session telemetry JSONL logs without following linked repository paths. If no session is active, it deletes all eligible logs; an active log is preserved. MCP annotations mark this operation mutating and destructive. No prerequisites. WHEN TO USE: Call after auditing to keep the repository clean. RETURNS: Number of deleted session files."),
	)
	addTool(sessionCleanupTool, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		count, err := session.CleanupSessionsAt(serverRoot)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Successfully deleted %d stale session log files.", count)), nil
	})
	// Tool: gograph_wiki
	wikiTool := mcp.NewTool("gograph_wiki",
		mcp.WithDescription("Generate the llm-wiki/ directory of machine-first markdown pages from the static graph. Pages produced: overview.md, architecture.md, hotspots.md, routes.md, env.md, errors.md, concurrency.md, api-surface.md, and one packages/<name>.md per internal package. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. A relative output is anchored beneath the graph root and rejects linked components; an absolute output explicitly selects a local destination whose final directory must be real. Generated page paths and regular-file writes remain confined beneath the selected output root. Writes may overwrite existing regular files; MCP annotations mark it mutating and destructive. WHEN TO USE: At the start of an agent session on an unfamiliar codebase — run once to get a token-efficient orientation without issuing dozens of individual tool calls. NOT TO USE: For targeted symbol lookups (use gograph_context or gograph_source). RETURNS: JSON manifest of written page filenames and a count; error when the graph cannot be loaded or the output directory is unsafe or cannot be created."),
		mcp.WithString("output", mcp.Description("Wiki directory: relative paths are graph-rooted; an absolute path explicitly selects a real local output root (default 'llm-wiki')")),
	)
	addTool(wikiTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		outputDir := "llm-wiki"
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			if v, ok := args["output"].(string); ok && v != "" {
				outputDir = v
			}
		}
		gen := wiki.New(g)
		pages, err := gen.Generate(outputDir)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("wiki generation failed: %v", err)), nil
		}
		var written []string
		for _, p := range pages {
			if p.Content != "" {
				written = append(written, p.Filename)
			}
		}
		resolvedOutput := outputDir
		if !filepath.IsAbs(resolvedOutput) {
			resolvedOutput = filepath.Join(graphRoot(g), filepath.Clean(resolvedOutput))
		}
		data, err := json.MarshalIndent(map[string]any{
			"output": resolvedOutput,
			"count":  len(written),
			"pages":  written,
		}, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_summary
	summaryTool := mcp.NewTool("gograph_summary",
		mcp.WithDescription("Single-call codebase briefing: top 3 hotspots (most-called symbols), worst instability package, highest cyclomatic complexity function, total orphan count, and god-object count. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise and precise_fallback graphs retry CHA/SSA after source changes. Read-only; no side effects. WHEN TO USE: At the very start of any session — replaces running gograph_hotspot + gograph_coupling + gograph_orphans + gograph_complexity + gograph_godobj separately (5 calls → 1). NOT TO USE: For detailed drill-down into a specific metric (use the dedicated tool after reviewing summary). RETURNS: JSON with symbols, packages, hotspots[], worst_instability, top_complexity, orphan_count, and god_object_count."),
	)
	addTool(summaryTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		hotspots := search.Hotspot(g, 3, false)
		coupling := search.Coupling(g, "", search.CouplingOptions{})
		complexity := search.Complexity(g, "")
		orphanList := search.ReachableOrphans(g)
		godObjs := search.GodObjects(g, search.DefaultGodObjectParams())
		stats := search.Stats(g)

		type summaryResult struct {
			Symbols    int                      `json:"symbols"`
			Packages   int                      `json:"packages"`
			Hotspots   []search.HotspotResult   `json:"hotspots"`
			WorstPkg   *search.PackageCoupling  `json:"worst_instability,omitempty"`
			TopComplex *search.ComplexityResult `json:"top_complexity,omitempty"`
			Orphans    int                      `json:"orphan_count"`
			GodObjects int                      `json:"god_object_count"`
		}
		res := summaryResult{
			Symbols:    stats.Symbols,
			Packages:   stats.Packages,
			Hotspots:   hotspots,
			Orphans:    len(orphanList),
			GodObjects: len(godObjs),
		}
		if len(coupling) > 0 {
			res.WorstPkg = &coupling[0]
		}
		if len(complexity) > 0 {
			res.TopComplex = &complexity[0]
		}
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_untested
	untestedTool := mcp.NewTool("gograph_untested",
		mcp.WithDescription("Sweep the full graph in one pass and return called production functions and methods without an exact/static attributed test edge. The MCP server checks freshness before this call and refreshes in the current requested analysis mode; precise graphs separately type-resolve compiling test packages. Exact direct selectors and local method values suppress only their resolved symbol, avoiding same-name receiver conflation. CHA interface targets remain visible with test_resolution=possible and possible_test_count instead of silently satisfying exact coverage; test_resolution=none means no attributed or bounded-possible test target was found. This is static attribution, not runtime coverage proof. Read-only; no side effects. WHEN TO USE: During test census or pre-release hardening. Distinct from gograph_orphans (zero production callers) and replaces N sequential gograph_tests calls. NOT TO USE: For running tests or proving branch execution. RETURNS: JSON array sorted by caller_count descending with name, kind, file, line, caller_count, package, test_resolution, and optional possible_test_count; empty when every called symbol has an exact/static or historical parser-attributed test edge."),
		mcp.WithString("pkg", mcp.Description("Optional package name substring to filter results (e.g. 'cli', 'search')")),
		mcp.WithInteger("top", mcp.Description("Limit results to top N by caller count (0 = all, default)")),
	)
	addTool(untestedTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if newG, err := rebuild(); err == nil {
			g = newG
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("failed to refresh graph: %v", err)), nil
		}
		results := search.Untested(g)

		// Parse optional filters from MCP arguments.
		pkg := ""
		top := 0
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			if v, ok := args["pkg"].(string); ok {
				pkg = v
			}
			var err error
			top, err = integerArg(args, "top", top)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
		}

		if pkg != "" {
			pkgLower := strings.ToLower(pkg)
			var filtered []search.UntestedResult
			for _, r := range results {
				if strings.Contains(strings.ToLower(r.PackageName), pkgLower) ||
					strings.Contains(strings.ToLower(r.File), pkgLower) {
					filtered = append(filtered, r)
				}
			}
			results = filtered
		}
		if top > 0 && len(results) > top {
			results = results[:top]
		}

		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	// Tool: gograph_doc
	docTool := mcp.NewTool("gograph_doc",
		mcp.WithDescription("Fetch Go documentation for a package, stdlib symbol, or third-party symbol by running `go doc <query>`. The handler does not query the graph, though the project-scoped MCP server must already have started with a usable artifact or buildable Go source. Filesystem-shaped queries are rejected, and the command is refused for source-tree links the Go toolchain may inspect across the selected root plus its effective module root, or the workspace root and member trees; .git and .gograph are excluded from that preflight. It also refuses a special recognized Go build input, linked/non-regular Go tool metadata (go.mod, go.sum, go.work, go.work.sum, or vendor/modules.txt), or a workspace member outside the workspace directory. Each applicable member directory, go.mod, and optional go.sum is validated first. Dependency and toolchain resolution remain open-world under the user's Go environment. WHEN TO USE: When a call chain reaches code outside the project. NOT TO USE: For project-internal symbols (use gograph_source or gograph_context). RETURNS: A one-element JSON array containing {query, output}, where output is the raw go doc text; an error when the query or repository input is unsafe, the symbol is not found, or go is unavailable."),
		mcp.WithString("query", mcp.Required(), mcp.Description("The go doc query string. Examples: 'fmt.Errorf', 'net/http.HandleFunc', 'io.Reader', 'github.com/jackc/pgx/v5.Conn.QueryRow'")),
	)
	addTool(docTool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		q, _ := query["query"].(string)
		if q == "" {
			return mcp.NewToolResultError("query is required"), nil
		}
		if err := scanner.ValidateGoDocQuery(q); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		docRoot, err := scanner.SourceValidationRoot(graphRoot(g))
		if err != nil {
			return mcp.NewToolResultError("cannot determine repository validation root: " + err.Error()), nil
		}
		if err := scanner.ValidateToolchainSourceInputs(docRoot); err != nil {
			return mcp.NewToolResultError("refusing to run the Go toolchain with unsafe repository source or metadata: " + err.Error()), nil
		}

		cmd := exec.Command("go", "doc", q)
		cmd.Dir = docRoot
		out, err := cmd.Output()
		if err != nil {
			var exitErr *exec.ExitError
			errMsg := err.Error()
			if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
				errMsg = strings.TrimSpace(string(exitErr.Stderr))
			}
			return mcp.NewToolResultError(errMsg), nil
		}
		text := strings.TrimSpace(string(out))
		type docResult struct {
			Query  string `json:"query"`
			Output string `json:"output"`
		}
		data, err := json.MarshalIndent([]docResult{{Query: q, Output: text}}, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}

func sanitizeGitRef(ref string) error {
	if ref == "" || strings.HasPrefix(ref, "-") || !safeGitRef.MatchString(ref) {
		return fmt.Errorf("invalid git reference: contains unsafe characters or is empty")
	}
	return nil
}
