// Package cli wires together the CLI commands.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/build"
	"go/token"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ozgurcd/gograph/internal/baseline"
	"github.com/ozgurcd/gograph/internal/buildctx"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/mcp"
	"github.com/ozgurcd/gograph/internal/parser"
	"github.com/ozgurcd/gograph/internal/precise"
	"github.com/ozgurcd/gograph/internal/rootfind"
	"github.com/ozgurcd/gograph/internal/scanner"
	"github.com/ozgurcd/gograph/internal/search"
	"github.com/ozgurcd/gograph/internal/session"
	"github.com/ozgurcd/gograph/internal/sourcefs"
	"github.com/ozgurcd/gograph/internal/wiki"
)

const outputDir = ".gograph"
const graphFile = ".gograph/graph.json"
const reportFile = ".gograph/GRAPH_REPORT.md"
const symFile = ".gograph/graph-symbols.md"
const depsFile = ".gograph/graph-deps.md"
const routesFile = ".gograph/graph-routes.md"
const sqlFile = ".gograph/graph-sql.md"
const errorsFile = ".gograph/graph-errors.md"
const configFile = ".gograph/graph-config.md"
const concFile = ".gograph/graph-concurrency.md"
const testsFile = ".gograph/graph-tests.md"

const (
	exitSuccess = 0
	exitError   = 1
	exitStale   = 2
)

// Version is set at build time via -ldflags; defaults to "dev" for local builds.
var Version = "dev"

// Run is the entrypoint called from main.
func Run(args []string) int {
	if len(args) == 0 {
		printHelp()
		return 0
	}

	// Strip global flags before dispatch; set the package-level flags.
	// Pre-scan JSON so failures raised while parsing an earlier global flag
	// still honor a --json that appears later in the invocation.
	jsonMode = requestsJSON(args)
	filesOnlyMode = false
	mermaidMode = false
	var intention string
	filtered := args[:0]
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--help", "-h":
			if len(args) > 1 && args[0] != "--help" && args[0] != "-h" {
				printCommandHelp(args[0])
			} else {
				printHelp()
			}
			return 0
		case "--json":
			jsonMode = true
		case "--files-only":
			filesOnlyMode = true
		case "--mermaid":
			mermaidMode = true
		case "-i", "--intention":
			if i+1 < len(args) {
				intention = args[i+1]
				i++ // skip the value
			} else {
				return failCommand(commandFromArgs(args), "Error: --intention/-i flag requires a value")
			}
		default:
			if strings.HasPrefix(a, "-i=") {
				intention = strings.TrimPrefix(a, "-i=")
			} else if strings.HasPrefix(a, "--intention=") {
				intention = strings.TrimPrefix(a, "--intention=")
			} else {
				filtered = append(filtered, a)
			}
		}
	}
	args = filtered

	if err := validateOutputModes(args); err != nil {
		return failCommand(commandFromArgs(args), err.Error())
	}

	// Bare `gograph --mermaid` (no subcommand) → architecture overview diagram.
	if len(args) == 0 {
		if mermaidMode {
			return runDiagram(nil)
		}
		printHelp()
		return 0
	}

	// Enforce active session constraints
	nonAnalytical := map[string]bool{
		"session":           true,
		"--session":         true,
		"mcp":               true,
		"build":             true,
		"add-claude-plugin": true,
		"hook-guard":        true,
		"version":           true,
		"help":              true,
		"-h":                true,
		"--help":            true,
		"-v":                true,
		"--version":         true,
		"stale":             true,
		"stats":             true,
		"capabilities":      true,
		"wiki":              true,
		"doc":               true,
	}

	if !nonAnalytical[args[0]] {
		activeID, err := session.GetActiveSessionID()
		if err != nil {
			return failCommandf(args[0], "Error reading active session metadata: %v", err)
		}
		if activeID != "" {
			if intention == "" {
				return failCommandf(args[0], "Error: Active session %q requires an intention. Please supply the --intention (-i) flag stating your technical rationale.", activeID)
			}
		}
	}

	startTime := time.Now()
	exitCode := dispatch(args)
	elapsed := time.Since(startTime)

	// Log command telemetry
	if args[0] != "session" && args[0] != "--session" && args[0] != "mcp" {
		status := commandTelemetryStatus(args[0], exitCode)
		_ = session.LogCommand(args[0], args[1:], intention, elapsed, status)
	}

	return exitCode
}

func commandTelemetryStatus(command string, exitCode int) string {
	if exitCode == exitSuccess || (command == "stale" && exitCode == exitStale) {
		return "success"
	}
	return "failure"
}

// dispatch routes subcommands to their implementation.
func dispatch(args []string) int {
	switch args[0] {
	case "session", "--session":
		return runSessionWithJSONErrors(args[1:])
	case "build":
		return runBuild(args[1:])
	case "query":
		return runQuery(args[1:])
	case "focus":
		return runFocus(args[1:])
	case "node":
		return runNode(args[1:])
	case "source":
		return runSource(args[1:])
	case "public":
		return runPublic(args[1:])
	case "fields":
		return runFields(args[1:])
	case "embeds":
		return runEmbeds(args[1:])
	case "imports":
		return runImports(args[1:])
	case "callers":
		return runCallers(args[1:])
	case "callees":
		return runCallees(args[1:])
	case "impact":
		return runImpact(args[1:])
	case "implementers":
		return runImplementers(args[1:])
	case "envs":
		return runEnvs(args[1:])
	case "interfaces":
		return runInterfaces(args[1:])
	case "concurrency":
		return runConcurrency(args[1:])
	case "tests":
		return runTests(args[1:])
	case "routes":
		return runRoutes()
	case "sql":
		return runSQL(args[1:])
	case "errors":
		return runErrors(args[1:])
	case "errorflow":
		return runErrorFlow(args[1:])
	case "flow":
		return runFlow(args[1:])
	case "path":
		return runPath(args[1:])
	case "stale":
		return runStale()
	case "stats":
		return runStats()
	case "summary":
		return runSummary()
	case "untested":
		return runUntested(args[1:])
	case "doc":
		return runDoc(args[1:])
	case "wiki":
		return runWiki(args[1:])
	case "orphans":
		return runOrphans()
	case "godobj":
		return runGodObj(args[1:])
	case "skeleton":
		return runSkeleton()
	case "mutate":
		return runMutate(args[1:])
	case "trace":
		return runTrace(args[1:])
	case "arity":
		return runArity(args[1:])
	case "complexity":
		return runComplexity(args[1:])
	case "diagram":
		return runDiagram(args[1:])
	case "coupling":
		return runCoupling(args[1:])
	case "context":
		return runContext(args[1:])
	case "hotspot":
		return runHotspot(args[1:])
	case "deps":
		return runDeps(args[1:])
	case "dependents":
		return runDependents(args[1:])
	case "changes":
		return runChanges(args[1:])
	case "capabilities":
		return runCapabilities()
	case "mcp":
		return runMCP(args[1:])
	case "constructors":
		return runConstructors(args[1:])
	case "literals":
		return runLiterals(args[1:])
	case "usages":
		return runUsages(args[1:])
	case "returnusage":
		return runReturnUsage(args[1:])
	case "schema":
		return runSchema(args[1:])
	case "globals":
		return runGlobals(args[1:])
	case "mocks":
		return runMocks(args[1:])
	case "fixtures":
		return runFixtures(args[1:])
	case "boundaries":
		return runBoundaries(args[1:])
	case "endpoint":
		return runEndpoint(args[1:])
	case "explain":
		return runExplain(args[1:])
	case "plan":
		return runPlan(args[1:])
	case "review":
		return runReview(args[1:])
	case "risk":
		return runRisk(args[1:])
	case "api", "contract":
		return runAPI(args[1:])
	case "check":
		return runCheck(args[1:])
	case "gate":
		return runGate(args[1:])
	case "snapshot":
		return runSnapshot(args[1:])
	case "add-claude-plugin":
		if err := installPlugin(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to install plugin: %v\n", err)
			return 1
		}
		return 0
	case "hook-guard":
		return runHookGuard()
	case "httpcalls":
		return runHTTPCalls(args[1:])
	case "help", "--help", "-h":
		printHelp()
		return 0
	case "version", "--version", "-v":
		fmt.Printf("gograph version v%s\n", Version)
		return 0
	default:
		if jsonMode {
			return failCommandf(args[0], "unknown command: %s", args[0])
		}
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		printHelp()
		return 1
	}
}

func runCapabilities() int {
	fmt.Println(`gograph: AST-aware Repository Navigation Tool for AI Agents

━━━ READ FIRST ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
If this repository contains llm-wiki/, read its curated context pages before
writing code or running analysis:

  llm-wiki/index.md         → index of all wiki pages
  llm-wiki/project.md       → project identity, non-goals, correctness model
  llm-wiki/agent-rules.md   → binding wiki workflow and governance rules
  llm-wiki/agent-contract.md → session lifecycle and tool selection contract

If generated pages are missing: gograph build . --precise && gograph wiki

━━━ PREREQUISITE ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Repository analysis commands read from .gograph/graph.json. Build it once before
using them. The capabilities, help, version, and external 'doc' commands do not
need an index:

  gograph build .            fast, tolerates broken code — use during development
  gograph build . --precise  type-checked CHA/SSA; records precise or precise_fallback
                               unless a failed retry can retain a fresh precise artifact

After build: graph.json + Markdown reports are written to .gograph/.
The .gograph/ ignore entry is appended to the Git repository root .gitignore
when available; outside Git, the build target .gitignore is used.
If no Go files are found, or none can be parsed, build exits before replacing artifacts.
Partial builds record parse failures and selection/security warnings in graph.json.
  gograph stats   → counts plus complete/partial build and ast/precise/fallback status
  gograph stale   → checks source selection, build context, and content digests;
                    exits 0 (up to date), 1 (error), or 2 (stale)

CLI graph-backed analysis uses the last trusted persisted graph, written by a manual build or an
opt-in MCP publication. The MCP server checks source freshness and newer
persisted graphs per call. After edits it rebuilds in the current requested
mode, so precise analysis is recomputed.

Repository source and persisted-index reads are confined beneath the selected
root: linked or special recognized Go build inputs are excluded, linked or
non-regular go.mod/go.sum/go.work/go.work.sum/vendor/modules.txt metadata is
rejected before toolchain use, and applicable workspace members must stay
beneath the workspace directory with their directory and metadata validated.
Precise analysis and doc reject source-tree links the Go toolchain may inspect
across the selected root plus its effective module root, or the workspace root
and member trees; .git and .gograph are excluded from that preflight;
graph.json must be a regular file beneath a real .gograph directory, and
graphs with a missing or unsupported source-policy marker are rebuild-required.
Serialized graph roots are ignored. Use the current binary when analyzing
untrusted repositories; older binaries do not enforce this source-confinement contract.

Repository-controlled session, snapshot, boundary, gate-init, and generated
wiki destinations use rooted regular-file operations and refuse descendant
links that could redirect reads, writes, or cleanup outside their selected root.

MCP refreshes stay in memory by default. Starting
  gograph mcp [path] --persist-refresh
publishes each confirmed-fresh startup build or refresh as graph.json plus nine
reports. It keeps one latest state (not a branch cache), does not edit .gitignore,
and returns publication failures to the triggering tool for a later retry.

━━━ COMMON WORKFLOWS ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  Start of any session         → summary  (top hotspots + worst instability + highest complexity + orphan/god-obj counts in ONE call)
  Onboard to unfamiliar repo   → hotspot, skeleton, focus <pkg>
  Find where X is defined      → query <term>  then  source <sym> to read body
  Understand a symbol (raw)    → context <sym>  (callers+callees+source+tests in one call)
  Understand all changed syms  → context --uncommitted  (all contexts bundled — use after plan --uncommitted)
  Understand a symbol (deep)   → explain <sym>  (role, complexity, SQL, env, routes, interfaces)
  Before editing any symbol    → plan <sym>     (callers, tests, SQL/env/route risk)
  After editing, before commit → build . --precise  then  review --uncommitted
  Before a package refactor    → dependents <pkg>  (every consumer of this package)
  Full blast radius of change  → impact <sym>  or  impact --uncommitted  or  impact --since <ref>
  PR / branch scope review     → changes --git main
  HTTP endpoint deep-dive      → endpoint <handler>  (route + call chain + SQL + env)
  Error root-cause trace       → errorflow <err_str>
  Security source/sink scan    → flow [term] [--source kind] [--sink kind]
  Dead code sweep              → orphans
  Test coverage gaps (codebase) → untested  (callers but zero test edges — one sweep, sorted by risk)
  External symbol signature    → doc <pkg.Symbol>  (stdlib/third-party — no graph required)
  API breaking-change check    → api --since <ref>
  CI enforcement               → gate, check --since <ref>

━━━ WHEN TO USE WHAT ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
FINDING THINGS — three different scopes:
  query <term>      broad: searches symbol names, file paths, package names, import paths, call sites
  node <sym>        exact: AST metadata for one named symbol (kind, file, line, signature, doc)
  source <sym>      body: extracts the actual source code block — use instead of reading the file

CALL GRAPH — two different depths:
  callers/callees <sym> [--depth N]   bounded: 1 hop (default) up to 10 — use for focused exploration
  impact <sym>                         unbounded: full BFS to ALL transitive callers — can be large on hotspots
  Call-graph symbol selectors accept short names ("Validate" — fuzzy substring),
    concrete package-qualified dot notation, and fully-qualified IDs where advertised.
    callers also accepts an interface method ("Repository.Delete"), expanding every
    recorded precise implementer and returning a shared source site once. Precise IDs
    and interface expansion require a --precise build for full effect.

SYMBOL UNDERSTANDING — two different outputs:
  context <sym>   structured data: node + source + callers + callees + tests — fast, token-efficient
  explain <sym>   narrative: role classification, prod vs test split, complexity, SQL, env, routes, interfaces
                  → use context when you need lists to act on; use explain when you need to understand purpose

PACKAGE RELATIONS — three different questions:
  deps <pkg>           what does this package import? (outgoing)
  dependents <pkg>     what imports this package? (incoming) — essential before refactoring a package
  imports <path>       which files import this specific import path? — for tracing one external dependency

STRUCT / TYPE — five different angles:
  fields <struct>        what fields does this struct have?
  embeds <struct>        which structs embed this struct?
  constructors <struct>  which functions return this struct? (New*, factory functions)
  literals <struct>      where is this struct initialized as Foo{...}? (run before adding a required field)
  implementers <iface>   which structs satisfy this interface?
  interfaces <struct>    which interfaces does this struct satisfy? (inverse of implementers)
  usages <type>          where is this type used? (param/return types, struct fields, iface methods)
                         → use before changing any interface or type — shows the full blast radius

PACKAGE vs SYMBOL scope:
  focus <pkg>    everything in a package: files, all symbols, internal calls, imports
  public <pkg>   exported symbols only: the package's API surface
  context <sym>  one symbol only: deep slice of a single function/struct/interface

ERRORS — two different questions:
  errors              where are all errors defined and returned in the codebase?
  errorflow <term>    how does this specific error reach the HTTP layer? (definition → return sites → entry point)

SECURITY FLOW — potential untrusted data paths:
  flow [term]         HTTP request / decoded JSON / environment → SQL query text,
                      process execution, filesystem path, or outbound HTTP target
  --source / --sink   restrict to one documented source or sink kind
  --no-tests          production-only scan (tests are included by default)
  --config <path>     sanitizer-return policy; defaults to .gograph/flow.json when present

━━━ OUTPUT FORMAT ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Query and composed-analysis commands document JSON individually. Files-only is
implemented by query, focus, node, public, fields, embeds, imports, callers,
callees, impact, implementers, envs, interfaces, concurrency, tests, routes,
sql, errors, flow, orphans, mutate, constructors, literals, usages,
returnusage, schema, globals, mocks, fixtures, boundaries, httpcalls, and
dependents. Empty files-only results write zero lines. Mermaid is limited to
the graph-oriented commands listed below. Request only one output mode at a time:

  (default)       [kind] Name — detail  (file:line)  — one result per line
  --json          {"schema_version":"1","command":"...","status":"ok",
                   "query":"...","count":N,"results":[...]}
  --files-only    flat deduplicated list of file paths — use for checklists
  --mermaid       visual dependency/call diagrams in Mermaid format
                  (supported by deps, dependents, coupling, callers, callees, path, impact, endpoint)

For supported commands, use --json for structured pipelines and --files-only
when you only need involved file paths. Operational commands remain text except
that session audit also accepts --json and returns its native audit object.

━━━ STATIC ANALYSIS LIMITATIONS ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Know these before trusting results:

  Interface dispatch    default AST graphs may miss dynamic targets. Precise CHA keeps every
                        valid named in-repository implementation as a possible target and accepts
                        callers Interface.Method, but can over-approximate runtime reachability.
                        Promoted methods use hidden traversal-only wrapper edges. Reflection,
                        unsafe, plugins, unresolved function values, test-only packages,
                        unnamed concrete types, and module-external implementations can remain incomplete.
  errorflow             heuristic AST traversal — NOT SSA/data-flow. Useful for navigation,
                        not proof. Confidence rating (HIGH/MEDIUM) is a heuristic estimate.
  flow                  interprocedural, path-insensitive AST taint analysis with call/return
                        matching across at most 16 nested repository calls. Findings are review
                        leads, not proof; globals, reflection, arbitrary heap aliases, and some
                        dynamic calls are not modeled. Use a precise build for stronger method/
                        interface targets. External calls lower confidence.
                        Sanitizers apply to return values only.
  endpoint              constant Gin/Echo/Fiber Group() and Chi Route() prefixes are
                        composed. Dynamic prefixes remain best-effort; search those by
                        known suffix or handler symbol.
  impact / skeleton     can produce very large output on hotspot symbols or large repos.
                        Use callers --depth N for bounded traversal instead of impact.
  CLI snapshot results  reflect the last trusted persisted graph. Run 'gograph stale' first;
                        exit status encodes result (0 = up to date, 1 = error, 2 = stale).
  MCP analysis          checks source freshness and newer graph.json artifacts per call;
                        edits preserve/retry the requested precision. stale/default
                        changes/stats inspect trusted persisted graph.json, or the startup
                        auto-build fallback when no usable artifact exists. --persist-refresh
                        can publish successful refreshes for later CLI/server processes.
  Subdirectory safe     graph-backed query commands auto-discover the project root
                        (walks up to the nearest .gograph/ directory). No need to cd
                        back to the repo root before running plan or review.

━━━ COMMANDS ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
AGENT WORKFLOW RULES (CRITICAL):
1. BEFORE editing: run 'gograph plan <symbol>' — callers, tests, SQL/env/route risk in one call
2. AFTER editing:  run 'gograph build . --precise' then 'gograph review --uncommitted'

INDEXING:
build . [--precise]  : parse AST, stage graph.json + reports, commit graph.json last
                       Honors Go build constraints and cmd/go package-directory rules;
                       skips generated, module-ignored, and Git-ignored sources.
stale                : check source selection, build context, and content digests
                       exit 0 = up to date, 1 = error, 2 = stale
stats                : schema/build/precision health, parse failures, and symbol/call/route counts

QUERY COMMANDS:
boundaries [--config] : verify package architecture constraints using boundaries.json
boundaries --create   : auto-generate a baseline boundaries.json from the current repo
callees <fn> [--no-tests] [--depth N]: what fn calls (depth=1 direct; --depth 2+ expands N hops, max 10)
callers <fn> [--no-tests] [--depth N] [--exact]: who calls fn (depth=1 direct; max 10)
  complexity [sym]     : cyclomatic complexity estimate per function (highest first;
                         source unreadable/unparseable is retained as UNKNOWN/-1)
concurrency [str]    : goroutine spawns, channel sends, and typed sync calls
coupling [pkg] [--include-stdlib] [--internal-only]
                     : fan-in, fan-out, and instability per package
diagram [--group-by package|module|service|file] [--max-depth N] [--include-stdlib]
                     : Mermaid architecture diagram of package dependency graph
embeds <struct>      : structs embedding this struct
envs [str]           : os.Getenv/os.LookupEnv/supported Viper Get* reads
errors [term] [--no-tests] : error constructors, sentinels, and panic sites
fields <struct>      : fields/types of struct
focus <pkg>          : all files, symbols, calls, imports for one package
godobj               : god-object struct candidates (--methods N --fields N --calls N --top N)
impact <sym>         : full transitive blast radius — WARNING: can be large on hotspot symbols
impact --uncommitted : blast radius of all uncommitted changes
impact --since <ref> : blast radius of all symbols changed since a git ref (e.g. main, HEAD~5)
implementers <iface> [--test-only] : structs implementing iface (--test-only = test/mock files only)
imports <path>       : files importing a specific import path
interfaces <struct>  : interfaces satisfied by this struct (inverse of implementers)
node <sym>           : AST metadata for one symbol (kind, file, line, signature, doc)
orphans              : symbols unreachable via BFS from main/init, test, route, and eligible public roots
path <from> <to>     : shortest call chain between two symbols (BFS)
public <pkg>         : exported symbols only
query <term...>      : broad OR search — symbols, files, packages, imports, call sites
routes               : all HTTP REST routes. Annotates unresolvable handlers.
source <sym>         : confined exact source for function/method/struct/interface/
                       type/variable/constant — USE THIS instead of reading files
sql [term]           : raw SQL queries mapped to their functions; optional keyword/table filter
tests <sym>          : test functions exercising this symbol

TOKEN SAVERS (COMPOSED COMMANDS — each replaces 3-8 separate calls):
api --since <ref|graph.json>
                     : breaking API/contract changes since a Git ref or saved graph;
                       saved graphs must be regular files inside the project root,
                       have no linked path component, and carry the exact current
                       source-policy marker; their serialized root is ignored
arity [--min 5]      : functions with too many arguments
changes              : symbols modified/new/deleted since the trusted persisted graph;
                       deleted includes files absent from the safe selected inventory
changes --git <ref>  : symbols in files changed since a git ref (e.g. main, HEAD~5, v1.4.50)
constructors <struct>: factory functions returning this struct
literals <struct>    : composite literal sites Foo{...} — run before adding/removing a required field
usages <type>        : where a type appears in signatures and fields (param/return/field/iface method)
returnusage <fn>     : how each caller uses the return value of fn (discarded/assigned/returned/passed)
risk <sym>           : risk evaluation — blast radius, complexity, tests, SQL/env (0-100 score + verdict)
risk --uncommitted   : risk evaluation for all uncommitted changes
context <sym> [--limit N] [--exact]: node+source+callers+callees+tests — raw structured data
context --uncommitted    : context for ALL uncommitted symbols in one call (replaces 5-8 sequential context calls)
                           NOTE: every context response now includes 'role' (architectural classification)
dependents <pkg>     : packages that import this package (run before any package refactor)
deps <pkg> [--transitive]: import dependency tree (add --transitive for full BFS closure)
endpoint <handler>   : route + handler + full call chain + SQL + env reads
                       INPUT: handler symbol name (always works) or flat route string (flat routers only)
errorflow <term> [--no-tests]: error definition → return sites → likely HTTP entry point path
flow [term] [--source kind] [--sink kind] [--config path] [--no-tests]
                     : potential HTTP/JSON/env input paths to SQL, process, file, or HTTP sinks
                       source: http_request | decoded_json | environment
                       sink: sql_query | process_execution | filesystem | outbound_http
                       tests are included by default; sanitizer policy is evaluated at query time
                       flow.json: {"sanitizers":[{"function":"pkg.Clean","for":["filesystem"]}]}
explain <sym>        : narrative summary — role, complexity, SQL, env, routes, interfaces, tests
                       (use explain for understanding; use context for raw data to act on)
fixtures <pkg>       : test helper structs and functions in test files
globals <pkg>        : package-level vars, consts, and functions mutating them
hotspot [--top N] [--include-tests]
                     : functions ranked by incoming call count — study these first
mocks <iface>        : alias for 'implementers --test-only' (kept for compatibility)
mutate <field>       : functions that mutate a struct field. Use Type.Field to avoid same-name
                       fields on other structs. Covers assignments; ++/+= and indirect method,
                       atomic/sync/channel mutations require --precise. Indirect rows show via=<method>.
plan <sym> [--with-context]
                     : change plan — callers, tests, SQL/env/route risk, public API impact
plan --uncommitted [--with-context]
                     : change plan for all currently uncommitted modified symbols
review <sym>         : post-edit review — test coverage, complexity, risk profile
review --uncommitted : post-edit review for all uncommitted changes
risk <sym>           : change risk profile — blast radius, complexity, test coverage, SQL/env dependencies
risk --uncommitted   : change risk profile for all uncommitted changes
schema <table>       : structs mapped to a DB table via struct tags
skeleton             : full repository API signatures with bodies stripped — WARNING: large on big repos
trace <err_str>      : alias for errorflow (kept for compatibility)
doc <pkg[.Symbol]>  : "go doc <query>" — signature + doc comment for any stdlib or third-party symbol.
                       No graph required. Examples: doc fmt.Errorf  doc net/http.HandleFunc  doc io.Reader
                       doc github.com/jackc/pgx/v5.Conn.QueryRow
                       Filesystem-shaped queries are rejected. Runs the local Go toolchain after
                       rejecting source-tree links across the selected root plus its effective
                       module root, or the workspace root and member trees (.git/.gograph excluded),
                       and validating Go tool metadata plus confined workspace members;
                       dependency resolution remains open-world.
httpcalls [term]     : all outbound HTTP client calls via net/http (Get, Post, PostForm, Head).
                       Filter by method or URL substring.
summary              : hotspots + worst instability + top complexity + reachability-orphan/god-object counts
untested [--pkg <n>] [--top N] : production functions with callers but zero test edges — coverage gaps
                       sorted by caller count (highest risk first). Replaces N 'tests <sym>' calls.
check [--config p] [--uncommitted] [--since ref|graph.json]
                     : static policy checks (boundaries, API drift, changed-route/export tests,
                       test coverage, orphans, globals, arity, and complexity);
                       relative/default config paths are project-confined; absolute
                       config is an explicit regular local file; saved baselines
                       must be regular non-linked in-project files with the exact
                       current source-policy marker and cannot supply the trusted root
gate                 : CI/CD enforcement against regular, non-linked project-root
                       .gograph.yml; delta gates use the previous persisted graph
gate init            : exclusively create a regular .gograph.yml; refuses links/overwrite
snapshot <subcmd>    : confined architectural metric snapshots (save, diff, list, drop)
mcp [path] [--persist-refresh] : start MCP server over stdio; refreshes stay in memory by default
                                 opt-in publishes one latest graph/report set without editing .gitignore
gograph session <action>     : start/end audit sessions (create [word], end, audit, cleanup)
                               storage is confined to regular .gograph session entries
                               NOTE: MCP tool calls (gograph_plan, gograph_review) are
                               now correctly recorded in session audit counters.
add-claude-plugin    : install Claude Desktop MCP config + shared rules + Claude Code hook;
                       Claude Code MCP registration uses the printed 'claude mcp add' command
hook-guard           : PreToolUse hook — blocks grep on Go symbols, redirects to gograph`)
	return 0
}

func runBuild(args []string) int {
	root := "."
	preciseMode := false
	var filteredArgs []string
	for _, a := range args {
		if a == "--precise" {
			preciseMode = true
		} else {
			filteredArgs = append(filteredArgs, a)
		}
	}
	if len(filteredArgs) > 0 {
		root = filteredArgs[0]
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error resolving path: %v\n", err)
		return 1
	}

	fmt.Printf("gograph build: scanning %s\n", absRoot)

	buildConfig, configErr := resolveBuildConfig(absRoot)
	previous, _ := loadGraph(absRoot)
	g, err := buildGraphWithConfig(absRoot, buildConfig, configErr, previous)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error building graph: %v\n", err)
		return 1
	}
	if preciseMode {
		fmt.Println("  running type-checked precision analysis (this may take a moment)...")
		if err := enrichGraphPreciselyWithConfig(absRoot, g, buildConfig, configErr); err != nil {
			fmt.Fprintf(os.Stderr, "warning: precise enrichment failed: %v\n", err)
		}
	}
	sortGraph(g)

	if err := writeGitignore(absRoot); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update .gitignore: %v\n", err)
	}

	jsonPath := filepath.Join(absRoot, graphFile)
	publication, err := publishGraphArtifacts(absRoot, g, manualArtifactPublication)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error publishing graph artifacts: %v\n", err)
		return 1
	}
	g = publication.Graph

	fmt.Printf("  packages: %d  files: %d  symbols: %d  calls: %d\n",
		len(g.Packages), len(g.Files), len(g.Symbols), len(g.Calls))
	if publication.Published {
		fmt.Printf("  wrote %s\n", jsonPath)
		fmt.Printf("  wrote %d markdown reports to %s/\n", graphReportCount, outputDir)
	} else {
		fmt.Printf("  kept existing richer artifact %s\n", jsonPath)
	}
	return 0
}

func BuildGraph(absRoot string) (*graph.Graph, error) {
	buildConfig, configErr := resolveBuildConfig(absRoot)
	previous, _ := loadGraph(absRoot)
	return buildGraphWithConfig(absRoot, buildConfig, configErr, previous)
}

func buildGraphWithConfig(absRoot string, buildConfig buildctx.Config, configErr error, previousGraphs ...*graph.Graph) (*graph.Graph, error) {
	files, selectionFingerprint, walkErrs := scanner.WalkWithConfigAndFingerprint(absRoot, buildConfig)
	if configErr != nil {
		walkErrs = append([]error{fmt.Errorf("using default Go build context: %w", configErr)}, walkErrs...)
	}
	buildMetadata := &graph.BuildMetadata{
		ScannedFiles:            len(files),
		Precision:               graph.PrecisionAST,
		SourcePolicyVersion:     graph.CurrentSourcePolicyVersion,
		AnalysisCacheVersion:    graph.CurrentAnalysisCacheVersion,
		BuildContextFingerprint: selectionFingerprint,
	}
	for _, e := range walkErrs {
		fmt.Fprintf(os.Stderr, "  warning: %v\n", e)
		buildMetadata.Warnings = append(buildMetadata.Warnings, e.Error())
	}
	fmt.Fprintf(os.Stderr, "  found %d Go files to parse\n", len(files))
	if len(files) == 0 {
		return nil, fmt.Errorf("no Go files in %s", absRoot)
	}

	g := &graph.Graph{
		Version:     graph.Version,
		GeneratedAt: time.Now().UTC(),
		Root:        absRoot,
		Build:       buildMetadata,
	}

	if deps, err := parseDependencies(absRoot); err == nil {
		g.Dependencies = deps
	}

	// Pre-compute the module-rooted import path for each package directory.
	// This is read from go.mod once per unique directory and cached so that
	// the import path is available when generating stable symbol IDs below.
	dirToImportPath := make(map[string]string)
	for _, path := range files {
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			continue
		}
		dir := filepath.Dir(rel)
		if _, seen := dirToImportPath[dir]; !seen {
			dirToImportPath[dir] = bestEffortImportPath(absRoot, dir, buildConfig)
		}
	}

	pkgMap := make(map[string]*graph.PackageNode)
	sourceReader, err := sourcefs.Open(absRoot)
	if err != nil {
		return nil, fmt.Errorf("opening repository source root: %w", err)
	}
	defer func() { _ = sourceReader.Close() }()

	type selectedSource struct {
		path   string
		rel    string
		dir    string
		digest string
	}
	selected := make([]selectedSource, 0, len(files))
	currentFiles := make(map[string]selectedSource, len(files))
	for _, path := range files {
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			rel = path
		}
		rel = filepath.Clean(rel)
		dir := filepath.Dir(rel)
		source, err := sourceReader.ReadFile(rel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: read %s: %v\n", rel, err)
			buildMetadata.Failures = append(buildMetadata.Failures, graph.BuildFailure{File: rel, Error: err.Error()})
			continue
		}
		item := selectedSource{path: path, rel: rel, dir: dir, digest: graph.SourceDigest(source)}
		selected = append(selected, item)
		currentFiles[rel] = item
	}

	var previous *graph.Graph
	if len(previousGraphs) > 0 {
		previous = previousGraphs[0]
	}
	reuseEligible := previous != nil && previous.UsesCurrentSourcePolicy() &&
		previous.Build.AnalysisCacheVersion == graph.CurrentAnalysisCacheVersion &&
		previous.Build.BuildContextFingerprint == selectionFingerprint
	previousFiles := make(map[string]graph.FileNode)
	var reusableResults map[string]*parser.FileResult
	changedDirs := make(map[string]bool)
	if reuseEligible {
		reusableResults = indexReusableFileAnalysis(previous)
		for _, file := range previous.Files {
			rel := graphFileRelative(absRoot, file.Path)
			previousFiles[rel] = file
			if _, exists := currentFiles[rel]; !exists || file.ContentDigest == "" {
				changedDirs[filepath.Dir(rel)] = true
			}
		}
		for _, failure := range previous.Build.Failures {
			changedDirs[filepath.Dir(graphFileRelative(absRoot, failure.File))] = true
		}
		for rel, item := range currentFiles {
			old, exists := previousFiles[rel]
			if !exists || old.ContentDigest != item.digest {
				changedDirs[item.dir] = true
			}
		}
	} else {
		for _, item := range selected {
			changedDirs[item.dir] = true
		}
	}
	for dir := range changedDirs {
		if _, exists := dirToImportPath[dir]; exists {
			buildMetadata.RebuiltPackages++
		}
	}

	fset := token.NewFileSet()
	for _, item := range selected {
		pkgImportPath := dirToImportPath[item.dir]
		if reuseEligible && !changedDirs[item.dir] {
			if result, exists := reusableResults[item.rel]; exists {
				result.File.ContentDigest = item.digest
				appendFileResult(g, result)
				buildMetadata.ParsedFiles++
				buildMetadata.ReusedFiles++
				addPackageFile(pkgMap, item.dir, pkgImportPath, result.File.PackageName, item.rel)
				continue
			}
		}
		source, err := sourceReader.ReadFile(item.rel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: read %s: %v\n", item.rel, err)
			buildMetadata.Failures = append(buildMetadata.Failures, graph.BuildFailure{File: item.rel, Error: err.Error()})
			continue
		}
		result, err := parser.ParseSource(fset, item.path, source, item.rel, pkgImportPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: %v\n", err)
			buildMetadata.Failures = append(buildMetadata.Failures, graph.BuildFailure{File: item.rel, Error: err.Error()})
			continue
		}
		result.File.ContentDigest = item.digest
		buildMetadata.ParsedFiles++
		appendFileResult(g, result)
		addPackageFile(pkgMap, item.dir, pkgImportPath, result.File.PackageName, item.rel)
	}
	if buildMetadata.ParsedFiles == 0 {
		firstFailure := "unknown parse failure"
		if len(buildMetadata.Failures) > 0 {
			firstFailure = buildMetadata.Failures[0].Error
		}
		return nil, fmt.Errorf("none of %d Go files parsed successfully: %s", len(files), firstFailure)
	}
	buildMetadata.Complete = len(buildMetadata.Failures) == 0 && len(buildMetadata.Warnings) == 0

	pkgKeys := make([]string, 0, len(pkgMap))
	for k := range pkgMap {
		pkgKeys = append(pkgKeys, k)
	}
	sort.Strings(pkgKeys)
	for _, k := range pkgKeys {
		g.Packages = append(g.Packages, *pkgMap[k])
	}

	filterPotentialCalls(g)
	sortGraph(g)
	return g, nil
}

func appendFileResult(g *graph.Graph, result *parser.FileResult) {
	g.Files = append(g.Files, result.File)
	g.Symbols = append(g.Symbols, result.Symbols...)
	g.Imports = append(g.Imports, result.Imports...)
	g.Calls = append(g.Calls, result.Calls...)
	g.EnvReads = append(g.EnvReads, result.Env...)
	g.Routes = append(g.Routes, result.Routes...)
	g.SQLs = append(g.SQLs, result.SQLs...)
	g.Errors = append(g.Errors, result.Errors...)
	g.Concurrency = append(g.Concurrency, result.Concurrency...)
	g.TestEdges = append(g.TestEdges, result.TestEdges...)
	g.Mutations = append(g.Mutations, result.Mutations...)
	g.Literals = append(g.Literals, result.Literals...)
	g.HTTPCalls = append(g.HTTPCalls, result.HTTPCalls...)
	g.FlowFunctions = append(g.FlowFunctions, result.FlowFunctions...)
}

func addPackageFile(packages map[string]*graph.PackageNode, dir, importPath, packageName, rel string) {
	if _, ok := packages[dir]; !ok {
		packages[dir] = &graph.PackageNode{
			ID:                   dir,
			Name:                 packageName,
			ImportPathBestEffort: importPath,
			Dir:                  dir,
		}
	}
	packages[dir].Files = append(packages[dir].Files, rel)
}

func graphFileRelative(root, path string) string {
	if filepath.IsAbs(path) {
		if rel, err := filepath.Rel(root, path); err == nil {
			return filepath.Clean(rel)
		}
	}
	return filepath.Clean(path)
}

// indexReusableFileAnalysis reconstructs parser-owned records in one linear
// pass. Precise-only records are intentionally omitted; precise enrichment is
// subsequently recomputed across the complete repository graph.
func indexReusableFileAnalysis(previous *graph.Graph) map[string]*parser.FileResult {
	results := make(map[string]*parser.FileResult, len(previous.Files))
	for _, file := range previous.Files {
		rel := filepath.Clean(file.Path)
		file.ID = rel
		file.Path = rel
		results[rel] = &parser.FileResult{File: file}
	}
	lookup := func(path string) *parser.FileResult {
		return results[filepath.Clean(path)]
	}
	for _, symbol := range previous.Symbols {
		result := lookup(symbol.File)
		if result == nil {
			continue
		}
		symbol.InterfaceMethods = cloneStringMap(symbol.DeclaredInterfaceMethods)
		symbol.DeclaredInterfaceMethods = cloneStringMap(symbol.DeclaredInterfaceMethods)
		result.Symbols = append(result.Symbols, symbol)
	}
	for _, edge := range previous.Imports {
		if result := lookup(edge.FromFile); result != nil {
			result.Imports = append(result.Imports, edge)
		}
	}
	for _, edge := range previous.Calls {
		if result := lookup(edge.File); result != nil && !edge.Precise {
			edge.CalleeSymbolID = ""
			edge.Synthetic = false
			result.Calls = append(result.Calls, edge)
		}
	}
	for _, edge := range previous.EnvReads {
		if result := lookup(edge.File); result != nil {
			result.Env = append(result.Env, edge)
		}
	}
	for _, edge := range previous.Routes {
		if result := lookup(edge.File); result != nil {
			result.Routes = append(result.Routes, edge)
		}
	}
	for _, edge := range previous.SQLs {
		if result := lookup(edge.File); result != nil {
			result.SQLs = append(result.SQLs, edge)
		}
	}
	for _, edge := range previous.Errors {
		if result := lookup(edge.File); result != nil {
			result.Errors = append(result.Errors, edge)
		}
	}
	for _, edge := range previous.Concurrency {
		if result := lookup(edge.File); result != nil {
			result.Concurrency = append(result.Concurrency, edge)
		}
	}
	for _, edge := range previous.TestEdges {
		if result := lookup(edge.File); result != nil {
			result.TestEdges = append(result.TestEdges, edge)
		}
	}
	for _, edge := range previous.Mutations {
		if result := lookup(edge.File); result != nil && !edge.Precise {
			result.Mutations = append(result.Mutations, edge)
		}
	}
	for _, edge := range previous.Literals {
		if result := lookup(edge.File); result != nil {
			result.Literals = append(result.Literals, edge)
		}
	}
	for _, edge := range previous.HTTPCalls {
		if result := lookup(edge.SourceFile); result != nil {
			result.HTTPCalls = append(result.HTTPCalls, edge)
		}
	}
	for _, item := range previous.FlowFunctions {
		if result := lookup(item.File); result != nil {
			result.FlowFunctions = append(result.FlowFunctions, item)
		}
	}
	return results
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

// enrichGraphPreciselyWithConfig records the outcome in the graph even when
// enrichment fails. Callers may continue using the AST graph, but the durable
// fallback state makes that downgrade visible and preserves the request to
// retry precise analysis after a future source edit.
func enrichGraphPreciselyWithConfig(absRoot string, g *graph.Graph, buildConfig buildctx.Config, configErr error) error {
	if g.Build == nil {
		g.Build = &graph.BuildMetadata{}
	}
	var err error
	if configErr != nil {
		err = fmt.Errorf("build context resolution failed: %w", configErr)
	} else {
		err = precise.EnrichWithConfig(absRoot, g, buildConfig)
	}
	if err != nil {
		g.Build.Precision = graph.PrecisionFallback
		g.Build.Warnings = append(g.Build.Warnings, "precise enrichment failed: "+err.Error())
		return err
	}
	g.Build.Precision = graph.PrecisionPrecise
	return nil
}

// buildPreciseGraph is the in-memory MCP builder for a graph whose persisted
// policy requested precise analysis. A failed enrichment retains the marked
// fallback graph for diagnostics but returns an error so MCP analysis cannot
// silently serve AST-only results.
func buildPreciseGraph(absRoot string) (*graph.Graph, error) {
	buildConfig, configErr := resolveBuildConfig(absRoot)
	previous, _ := loadGraph(absRoot)
	g, err := buildGraphWithConfig(absRoot, buildConfig, configErr, previous)
	if err != nil {
		return nil, err
	}
	if err := enrichGraphPreciselyWithConfig(absRoot, g, buildConfig, configErr); err != nil {
		fmt.Fprintf(os.Stderr, "warning: precise MCP refresh fell back to AST analysis: %v\n", err)
		return g, fmt.Errorf("precise MCP refresh failed; graph is precise_fallback: %w", err)
	}
	sortGraph(g)
	return g, nil
}

func resolveBuildConfig(absRoot string) (buildctx.Config, error) {
	return buildctx.ResolveOrDefault(context.Background(), absRoot)
}

func precisionFallbackError(g *graph.Graph) error {
	if g == nil || g.Build.EffectivePrecision() != graph.PrecisionFallback {
		return nil
	}
	reason := "type-checked enrichment did not complete"
	for i := len(g.Build.Warnings) - 1; i >= 0; i-- {
		const prefix = "precise enrichment failed: "
		if strings.HasPrefix(g.Build.Warnings[i], prefix) {
			reason = strings.TrimPrefix(g.Build.Warnings[i], prefix)
			break
		}
	}
	return fmt.Errorf("precise analysis unavailable (graph is precise_fallback): %s", reason)
}

func filterPotentialCalls(g *graph.Graph) {
	callables := make(map[string]bool)
	for _, symbol := range g.Symbols {
		if symbol.Kind == graph.KindFunction || symbol.Kind == graph.KindMethod {
			callables[symbol.Name] = true
		}
	}

	filtered := g.Calls[:0]
	for _, call := range g.Calls {
		if call.Potential && !callables[potentialCallName(call.CalleeRaw)] {
			continue
		}
		call.Potential = false
		filtered = append(filtered, call)
	}
	g.Calls = filtered
}

func potentialCallName(raw string) string {
	if idx := strings.LastIndex(raw, "."); idx >= 0 {
		raw = raw[idx+1:]
	}
	if idx := strings.Index(raw, "["); idx >= 0 {
		raw = raw[:idx]
	}
	return raw
}

// printResults prints []Result in text or JSON mode.
// cmd is the command name; query is the search term (may be empty).
// emptyMsg is printed when results are empty in text mode.
// Returns the exit code (always 0 for empty results).
func printResults(cmd, query string, results []search.Result, emptyMsg string) int {
	if jsonMode {
		return PrintJSON(okEnvelope(cmd, query, results, len(results)))
	}
	if filesOnlyMode {
		seenFiles := make(map[string]bool)
		for _, r := range results {
			if r.File != "" && !seenFiles[r.File] {
				fmt.Println(r.File)
				seenFiles[r.File] = true
			}
		}
		return 0
	}
	if len(results) == 0 {
		fmt.Println(emptyMsg)
		return 0
	}
	for _, r := range results {
		fmt.Println(r.String())
	}
	return 0
}

func runQuery(args []string) int {
	if len(args) == 0 {
		return failCommand("query", "usage: gograph query <term...>")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("query", err.Error())
	}
	results := search.Query(g, args)
	return printResults("query", strings.Join(args, " "), results, "no results")
}

func runFocus(args []string) int {
	if !hasSingleTarget(args) {
		return failCommand("focus", "usage: gograph focus <package>")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("focus", err.Error())
	}
	results := search.Focus(g, args[0])
	return printResults("focus", args[0], results, fmt.Sprintf("no focus data found for package %q", args[0]))
}

func runNode(args []string) int {
	if len(args) == 0 {
		return failCommand("node", "usage: gograph node <name>")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("node", err.Error())
	}
	results := search.Node(g, strings.Join(args, " "))
	return printResults("node", strings.Join(args, " "), results, "no results")
}

func runCallers(args []string) int {
	if len(args) == 0 {
		return failCommand("callers", "usage: gograph callers <function-or-method-name> [--no-tests] [--depth N] [--exact]")
	}
	includeTests := true
	depth := 1
	exactMatch := false
	var termParts []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--no-tests":
			includeTests = false
		case "--exact":
			exactMatch = true
		case "--depth":
			value, err := parseIntegerFlag(args, &i)
			if err != nil {
				return failCommand("callers", err.Error())
			}
			depth = value
			if depth < 1 {
				depth = 1
			} else if depth > 10 {
				depth = 10
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				return failCommandf("callers", "unknown flag: %s", args[i])
			}
			termParts = append(termParts, args[i])
		}
	}
	if !hasSingleTarget(termParts) {
		return failCommand("callers", "usage: gograph callers <function-or-method-name> [--no-tests] [--depth N] [--exact]")
	}
	term := strings.Join(termParts, " ")
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("callers", err.Error())
	}
	if mermaidMode {
		fmt.Println(search.CallersToMermaid(g, term, depth, includeTests, exactMatch))
		return 0
	}
	var results []search.Result
	if depth > 1 {
		results = search.CallersDepth(g, term, depth, includeTests, exactMatch)
	} else {
		results = search.Callers(g, term, includeTests, exactMatch)
	}
	return printResults("callers", term, results, "no callers found")
}

func runCallees(args []string) int {
	if len(args) == 0 {
		return failCommand("callees", "usage: gograph callees <function-or-method-name> [--no-tests] [--depth N]")
	}
	includeTests := true
	depth := 1
	var termParts []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--no-tests":
			includeTests = false
		case "--depth":
			value, err := parseIntegerFlag(args, &i)
			if err != nil {
				return failCommand("callees", err.Error())
			}
			depth = value
			if depth < 1 {
				depth = 1
			} else if depth > 10 {
				depth = 10
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				return failCommandf("callees", "unknown flag: %s", args[i])
			}
			termParts = append(termParts, args[i])
		}
	}
	if !hasSingleTarget(termParts) {
		return failCommand("callees", "usage: gograph callees <function-or-method-name> [--no-tests] [--depth N]")
	}
	term := strings.Join(termParts, " ")
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("callees", err.Error())
	}
	if mermaidMode {
		fmt.Println(search.CalleesToMermaid(g, term, depth, includeTests))
		return 0
	}
	var results []search.Result
	if depth > 1 {
		results = search.CalleesDepth(g, term, depth, includeTests)
	} else {
		results = search.Callees(g, term, includeTests, false)
	}
	return printResults("callees", term, results, "no callees found")
}

func runImplementers(args []string) int {
	return runImplementersCommand("implementers", args)
}

func runImplementersCommand(command string, args []string) int {
	testOnly := false
	var termParts []string
	for _, a := range args {
		if a == "--test-only" {
			testOnly = true
		} else if strings.HasPrefix(a, "-") {
			return failCommandf(command, "unknown flag: %s", a)
		} else {
			termParts = append(termParts, a)
		}
	}
	if !hasSingleTarget(termParts) {
		return failCommand(command, "Usage: gograph implementers <interface> [--test-only]")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommandf(command, "failed to load graph: %v", err)
	}
	iface := termParts[0]
	if testOnly {
		results := search.Mocks(g, iface)
		return printResults(command, iface, results, fmt.Sprintf("No test/mock structs found implementing '%s'.", iface))
	}
	results := search.Implementers(g, iface)
	return printResults(command, iface, results, fmt.Sprintf("No structs found implementing '%s'.", iface))
}

func runEnvs(args []string) int {
	if !hasOptionalTarget(args) {
		return failCommand("envs", "usage: gograph envs [term]")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommandf("envs", "failed to load graph: %v", err)
	}
	term := ""
	if len(args) > 0 {
		term = args[0]
	}
	results := search.Envs(g, term)
	return printResults("envs", term, results, "No environment variable reads found.")
}

func runInterfaces(args []string) int {
	if !hasSingleTarget(args) {
		return failCommand("interfaces", "Usage: gograph interfaces <struct>")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommandf("interfaces", "failed to load graph: %v", err)
	}
	results := search.Interfaces(g, args[0])
	return printResults("interfaces", args[0], results, fmt.Sprintf("No interfaces found satisfied by '%s'.", args[0]))
}

func runConcurrency(args []string) int {
	if !hasOptionalTarget(args) {
		return failCommand("concurrency", "usage: gograph concurrency [term]")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommandf("concurrency", "failed to load graph: %v", err)
	}
	term := ""
	if len(args) > 0 {
		term = args[0]
	}
	results := search.Concurrency(g, term)
	return printResults("concurrency", term, results, "No concurrency primitives found.")
}

func runTests(args []string) int {
	if !hasOptionalTarget(args) {
		return failCommand("tests", "usage: gograph tests [symbol]")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommandf("tests", "failed to load graph: %v", err)
	}
	term := ""
	if len(args) > 0 {
		term = args[0]
	}
	results := search.Tests(g, term)
	emptyMsg := "No test edges found."
	if term != "" {
		emptyMsg = fmt.Sprintf("No test functions found exercising '%s'.", term)
	}
	return printResults("tests", term, results, emptyMsg)
}

// graphRefresher keeps MCP analysis fresh without losing the analysis mode the
// user selected. Precise and precise_fallback graphs both carry a durable
// precise request, while explicit current-policy AST graphs refresh as
// AST-only. Missing/unsupported-policy persisted graphs are rejected before a
// refresher is constructed.
//
// A manual `gograph build --precise` may publish a newer graph while an MCP
// server is already running. The artifact is checked by file metadata before
// it is parsed, then a successfully loaded replacement is treated as the later
// publication even when overlapping builds make its build-start GeneratedAt
// earlier. A precise-requested server never adopts an AST-only artifact: it
// keeps the richer graph when source is unchanged, or recomputes precise
// analysis in memory when source changed.
func graphRefresher(
	initial *graph.Graph,
	root string,
	buildAST func(string) (*graph.Graph, error),
	buildPrecise func(string) (*graph.Graph, error),
	publishers ...func(*graph.Graph) (graphPublication, error),
) func() (*graph.Graph, error) {
	latest := initial
	artifactPath := filepath.Join(root, graphFile)
	artifactInfo := graphArtifactInfo(artifactPath)
	var publisher func(*graph.Graph) (graphPublication, error)
	if len(publishers) > 0 {
		publisher = publishers[0]
	}
	pendingPublication := false

	publishLatest := func() (*graph.Graph, error) {
		if publisher == nil {
			return latest, nil
		}
		publication, err := publisher(latest)
		if err != nil {
			pendingPublication = true
			return nil, fmt.Errorf("persisting refreshed graph: %w", err)
		}
		if publication.Graph == nil {
			pendingPublication = true
			return nil, errors.New("persisting refreshed graph returned no graph")
		}
		latest = publication.Graph
		pendingPublication = false
		// Do not treat our own same-directory replacement as a later external build on
		// the next tool call.
		artifactInfo = graphArtifactInfo(artifactPath)
		return latest, nil
	}

	return func() (*graph.Graph, error) {
		currentArtifactInfo := graphArtifactInfo(artifactPath)
		if graphArtifactChanged(artifactInfo, currentArtifactInfo) {
			if persisted, err := loadGraph(root); err == nil {
				// Advance the observed artifact only after a successful load. A
				// transient read or decode failure remains retryable on the next call.
				artifactInfo = currentArtifactInfo
				if shouldAdoptPersistedGraph(latest, persisted) {
					latest = persisted
					pendingPublication = false
				}
			}
		}

		if latest != nil && !search.Stale(latest, root).IsStale {
			if err := precisionFallbackError(latest); err != nil {
				return nil, err
			}
			if pendingPublication {
				return publishLatest()
			}
			return latest, nil
		}

		// Source can move while AST/CHA analysis is running. Recheck the graph
		// before serving it and retry once; a continuously changing tree fails
		// visibly instead of returning a known-stale result.
		const maxAttempts = 2
		var stale search.StaleResult
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			build := buildAST
			if latest != nil && latest.Build.PreciseRequested() {
				build = buildPrecise
			}
			refreshed, err := build(root)
			if refreshed != nil {
				latest = refreshed
			}
			if err != nil {
				return nil, err
			}
			if latest == nil {
				return nil, errors.New("graph refresh returned no graph")
			}
			stale = search.Stale(latest, root)
			if !stale.IsStale {
				if err := precisionFallbackError(latest); err != nil {
					return nil, err
				}
				return publishLatest()
			}
		}
		return nil, fmt.Errorf("graph remained stale after %d refresh attempts (%d freshness changes)", maxAttempts, stale.ChangeCount())
	}
}

func graphArtifactInfo(path string) os.FileInfo {
	// The repository root itself may have been explicitly supplied through a
	// symlink, but .gograph and graph.json must be real descendant entries.
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || parent.Mode()&os.ModeSymlink != 0 || !parent.IsDir() {
		return nil
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil
	}
	return info
}

func graphArtifactChanged(previous, current os.FileInfo) bool {
	if previous == nil || current == nil {
		return previous != current
	}
	// writeJSON publishes by same-directory rename. SameFile detects that replacement
	// even on filesystems whose timestamp granularity and equal-sized payloads
	// would make a modtime+size check ambiguous.
	return !os.SameFile(previous, current) ||
		!previous.ModTime().Equal(current.ModTime()) ||
		previous.Size() != current.Size()
}

func shouldAdoptPersistedGraph(current, persisted *graph.Graph) bool {
	if persisted == nil {
		return false
	}
	if current == nil {
		return true
	}
	if current.Build.PreciseRequested() && !persisted.Build.PreciseRequested() {
		return false
	}
	// This predicate is called only after graphArtifactChanged observed a new
	// publication. GeneratedAt is assigned at build start, so it cannot order
	// overlapping builds: one may start first and publish last. Trust the
	// successfully decoded replacement here, then let the caller's source
	// staleness check decide whether it needs another in-memory rebuild.
	return true
}

type mcpOptions struct {
	Root           string
	PersistRefresh bool
}

func parseMCPArgs(args []string) (mcpOptions, error) {
	options := mcpOptions{Root: "."}
	rootSet := false
	positionalOnly := false
	for _, argument := range args {
		if !positionalOnly {
			switch {
			case argument == "--":
				positionalOnly = true
				continue
			case argument == "--persist-refresh":
				options.PersistRefresh = true
				continue
			case strings.HasPrefix(argument, "--persist-refresh="):
				value := strings.TrimPrefix(argument, "--persist-refresh=")
				parsed, err := strconv.ParseBool(value)
				if err != nil {
					return mcpOptions{}, fmt.Errorf("invalid --persist-refresh value %q: %w", value, err)
				}
				options.PersistRefresh = parsed
				continue
			case strings.HasPrefix(argument, "-"):
				return mcpOptions{}, fmt.Errorf("unknown mcp option %q", argument)
			}
		}
		if rootSet {
			return mcpOptions{}, fmt.Errorf("mcp accepts at most one project path")
		}
		options.Root = argument
		rootSet = true
	}
	return options, nil
}

func prepareMCPGraph(options mcpOptions) (*graph.Graph, string, error) {
	root := options.Root
	if root == "." {
		root = rootfind.FindRoot()
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, "", fmt.Errorf("resolving path: %w", err)
	}

	g, err := loadGraph(absRoot)
	if err != nil {
		// Graph does not exist yet — build it automatically so Claude Desktop
		// works without requiring a manual "gograph build ." step first.
		fmt.Fprintf(os.Stderr, "graph unavailable or rebuild required, building automatically for %s...\n", absRoot)
		g, err = BuildGraph(absRoot)
		if err != nil {
			return nil, "", fmt.Errorf("auto-building graph: %w", err)
		}
		if options.PersistRefresh {
			publication, publishErr := publishGraphArtifacts(absRoot, g, refreshArtifactPublication)
			if publishErr != nil {
				return nil, "", fmt.Errorf("persisting auto-built graph: %w", publishErr)
			}
			g = publication.Graph
		}
	}
	absRoot = graphRoot(g)
	return g, absRoot, nil
}

func runMCP(args []string) int {
	options, err := parseMCPArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid mcp arguments: %v\n", err)
		return 1
	}
	g, absRoot, err := prepareMCPGraph(options)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to prepare MCP graph: %v\n", err)
		return 1
	}

	var publishers []func(*graph.Graph) (graphPublication, error)
	if options.PersistRefresh {
		publishers = append(publishers, func(candidate *graph.Graph) (graphPublication, error) {
			return publishGraphArtifacts(absRoot, candidate, refreshArtifactPublication)
		})
	}
	rebuild := graphRefresher(g, absRoot, BuildGraph, buildPreciseGraph, publishers...)
	buildBaseline := func(ctx context.Context, ref string) (*graph.Graph, error) {
		return baseline.Build(ctx, absRoot, ref, BuildGraph)
	}

	if err := mcp.Serve(g, rebuild, BuildGraph, buildBaseline, Version, mcp.ServerOptions{PersistRefresh: options.PersistRefresh}); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		return 1
	}
	return 0
}

func loadGraph(root string) (*graph.Graph, error) {
	// When root is "." (the default for graph-backed query commands), discover
	// the actual gograph project root by walking upward. This lets commands such
	// as plan and review work from subdirectories.
	if root == "." {
		root = rootfind.FindRoot()
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving path: %w", err)
	}
	jsonPath := filepath.Join(absRoot, graphFile)
	reader, err := sourcefs.Open(absRoot)
	if err != nil {
		return nil, fmt.Errorf("cannot open repository root %s: %w", absRoot, err)
	}
	defer func() { _ = reader.Close() }()
	data, err := reader.ReadRegularFile(graphFile)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s — run `gograph build` first: %w", jsonPath, err)
	}
	var g graph.Graph
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("parsing graph.json: %w", err)
	}
	if !g.UsesCurrentSourcePolicy() {
		return nil, fmt.Errorf("graph.json has a missing or unsupported repository source policy — run `gograph build` to rebuild it safely")
	}
	// The caller-selected load location is the trust boundary. Even a persisted
	// Root value that names the same directory (for example ".") must not remain
	// relative, because later MCP operations may run from another working
	// directory and would otherwise re-anchor graph-derived source reads there.
	g.Root = absRoot
	return &g, nil
}

func sameDirectory(recordedRoot, loadedRoot string) bool {
	if !filepath.IsAbs(recordedRoot) {
		recordedRoot = filepath.Join(loadedRoot, recordedRoot)
	}
	recordedInfo, err := os.Stat(recordedRoot)
	if err != nil || !recordedInfo.IsDir() {
		return false
	}
	loadedInfo, err := os.Stat(loadedRoot)
	return err == nil && loadedInfo.IsDir() && os.SameFile(recordedInfo, loadedInfo)
}

// graphRoot returns the trusted repository root assigned when graph.json was
// loaded or the graph was built. A serialized Root field is never filesystem
// authority. Query commands can be invoked from any descendant directory, so
// filesystem and Git operations must not re-anchor to the process working
// directory.
func graphRoot(g *graph.Graph) string {
	if g != nil && g.Root != "" {
		if root, err := filepath.Abs(g.Root); err == nil {
			return filepath.Clean(root)
		}
		return filepath.Clean(g.Root)
	}
	root := rootfind.FindRoot()
	if absRoot, err := filepath.Abs(root); err == nil {
		return absRoot
	}
	return filepath.Clean(root)
}

func writeGitignore(root string) error {
	worktreeRoot := gitWorktreeRoot(root)
	giPath := filepath.Join(worktreeRoot, ".gitignore")
	const entry = ".gograph/"
	reader, err := sourcefs.Open(worktreeRoot)
	if err != nil {
		return fmt.Errorf("open Git worktree root: %w", err)
	}
	existing, err := reader.ReadRegularFile(".gitignore")
	_ = reader.Close()
	exists := true
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read .gitignore safely: %w", err)
		}
		exists = false
		existing = nil
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == entry {
			return nil
		}
	}
	flags := os.O_APPEND | os.O_WRONLY
	if !exists {
		flags |= os.O_CREATE | os.O_EXCL
	}
	expected, lstatErr := os.Lstat(giPath)
	if lstatErr != nil && !os.IsNotExist(lstatErr) {
		return fmt.Errorf("inspect .gitignore before append: %w", lstatErr)
	}
	if lstatErr == nil && !expected.Mode().IsRegular() {
		return fmt.Errorf("refusing unsafe .gitignore %s: mode %s is not a regular file", giPath, expected.Mode())
	}
	f, err := os.OpenFile(giPath, flags, 0o640)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	actual, err := f.Stat()
	if err != nil {
		return err
	}
	if !actual.Mode().IsRegular() || expected != nil && !os.SameFile(expected, actual) {
		return fmt.Errorf("refusing .gitignore that changed during open: %s", giPath)
	}
	prefix := "\n"
	if len(existing) == 0 {
		prefix = ""
	}
	_, err = fmt.Fprintf(f, "%s%s\n", prefix, entry)
	return err
}

func gitWorktreeRoot(root string) string {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return root
	}
	gitRoot := strings.TrimSpace(string(out))
	if gitRoot == "" {
		return root
	}
	return gitRoot
}

func parseDependencies(absRoot string) ([]graph.Dependency, error) {
	moduleFiles, err := sourcefs.Open(absRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = moduleFiles.Close() }()
	data, err := moduleFiles.ReadRegularFile("go.mod")
	if err != nil {
		return nil, err
	}

	var deps []graph.Dependency
	lines := strings.Split(string(data), "\n")
	inRequire := false

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		if line == "require (" {
			inRequire = true
			continue
		}

		if inRequire && line == ")" {
			inRequire = false
			continue
		}

		if inRequire || strings.HasPrefix(line, "require ") {
			line = strings.TrimPrefix(line, "require ")
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				deps = append(deps, graph.Dependency{
					Module:  parts[0],
					Version: parts[1],
				})
			}
		}
	}

	sort.Slice(deps, func(i, j int) bool {
		return deps[i].Module < deps[j].Module
	})

	return deps, nil
}

func bestEffortImportPath(absRoot, relDir string, config buildctx.Config) string {
	absDir := absRoot
	if relDir != "." && relDir != "" {
		absDir = filepath.Join(absRoot, relDir)
	}
	if config.ModulesEnabled() && config.ModuleRoot() != "" {
		modulePath := config.ModulePath()
		moduleRoot := canonicalExistingPath(config.ModuleRoot())
		packageDir := canonicalExistingPath(absDir)
		if modulePath != "" {
			if relative, relErr := filepath.Rel(moduleRoot, packageDir); relErr == nil && pathWithinRoot(relative) {
				if relative == "." {
					return modulePath
				}
				return pathpkg.Join(modulePath, filepath.ToSlash(relative))
			}
		}
	}

	buildContext := config.BuildContext()
	if pkg, _ := buildContext.ImportDir(absDir, build.FindOnly); pkg != nil && pkg.ImportPath != "" && pkg.ImportPath != "." {
		return pkg.ImportPath
	}
	return pseudoImportPath(absDir)
}

func canonicalExistingPath(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

func pathWithinRoot(relative string) bool {
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func pseudoImportPath(dir string) string {
	if absolute, err := filepath.Abs(dir); err == nil {
		dir = absolute
	}
	valid := strings.Map(func(char rune) rune {
		const illegal = `!"#$%&'()*,:;<=>?[\]^{|}` + "`\uFFFD"
		if !unicode.IsGraphic(char) || unicode.IsSpace(char) || strings.ContainsRune(illegal, char) {
			return '_'
		}
		return char
	}, filepath.ToSlash(filepath.Clean(dir)))
	return pathpkg.Join("_", valid)
}

func sortGraph(g *graph.Graph) {
	g.Calls = dedupeCalls(g.Calls)
	sort.Slice(g.Files, func(i, j int) bool { return g.Files[i].Path < g.Files[j].Path })
	sort.Slice(g.Packages, func(i, j int) bool { return g.Packages[i].ID < g.Packages[j].ID })
	sort.Slice(g.Symbols, func(i, j int) bool {
		if g.Symbols[i].ID != g.Symbols[j].ID {
			return g.Symbols[i].ID < g.Symbols[j].ID
		}
		if g.Symbols[i].File != g.Symbols[j].File {
			return g.Symbols[i].File < g.Symbols[j].File
		}
		return g.Symbols[i].Line < g.Symbols[j].Line
	})
	sort.Slice(g.Imports, func(i, j int) bool {
		if g.Imports[i].FromFile != g.Imports[j].FromFile {
			return g.Imports[i].FromFile < g.Imports[j].FromFile
		}
		if g.Imports[i].ImportPath != g.Imports[j].ImportPath {
			return g.Imports[i].ImportPath < g.Imports[j].ImportPath
		}
		return g.Imports[i].Alias < g.Imports[j].Alias
	})
	sort.Slice(g.Calls, func(i, j int) bool {
		if g.Calls[i].File != g.Calls[j].File {
			return g.Calls[i].File < g.Calls[j].File
		}
		if g.Calls[i].Line != g.Calls[j].Line {
			return g.Calls[i].Line < g.Calls[j].Line
		}
		if g.Calls[i].Column != g.Calls[j].Column {
			return g.Calls[i].Column < g.Calls[j].Column
		}
		if g.Calls[i].CallerSymbolID != g.Calls[j].CallerSymbolID {
			return g.Calls[i].CallerSymbolID < g.Calls[j].CallerSymbolID
		}
		if g.Calls[i].CalleeRaw != g.Calls[j].CalleeRaw {
			return g.Calls[i].CalleeRaw < g.Calls[j].CalleeRaw
		}
		if g.Calls[i].CalleeSymbolID != g.Calls[j].CalleeSymbolID {
			return g.Calls[i].CalleeSymbolID < g.Calls[j].CalleeSymbolID
		}
		if g.Calls[i].Synthetic != g.Calls[j].Synthetic {
			return !g.Calls[i].Synthetic
		}
		return g.Calls[i].ReturnUsage < g.Calls[j].ReturnUsage
	})
	sort.Slice(g.EnvReads, func(i, j int) bool {
		if g.EnvReads[i].Key != g.EnvReads[j].Key {
			return g.EnvReads[i].Key < g.EnvReads[j].Key
		}
		if g.EnvReads[i].File != g.EnvReads[j].File {
			return g.EnvReads[i].File < g.EnvReads[j].File
		}
		return g.EnvReads[i].Line < g.EnvReads[j].Line
	})
	sort.Slice(g.Dependencies, func(i, j int) bool {
		if g.Dependencies[i].Module != g.Dependencies[j].Module {
			return g.Dependencies[i].Module < g.Dependencies[j].Module
		}
		return g.Dependencies[i].Version < g.Dependencies[j].Version
	})
	sort.Slice(g.Routes, func(i, j int) bool {
		if g.Routes[i].File != g.Routes[j].File {
			return g.Routes[i].File < g.Routes[j].File
		}
		if g.Routes[i].Line != g.Routes[j].Line {
			return g.Routes[i].Line < g.Routes[j].Line
		}
		if g.Routes[i].Method != g.Routes[j].Method {
			return g.Routes[i].Method < g.Routes[j].Method
		}
		return g.Routes[i].Path < g.Routes[j].Path
	})
	sort.Slice(g.SQLs, func(i, j int) bool {
		if g.SQLs[i].File != g.SQLs[j].File {
			return g.SQLs[i].File < g.SQLs[j].File
		}
		if g.SQLs[i].Line != g.SQLs[j].Line {
			return g.SQLs[i].Line < g.SQLs[j].Line
		}
		return g.SQLs[i].Query < g.SQLs[j].Query
	})
	sort.Slice(g.Errors, func(i, j int) bool {
		if g.Errors[i].File != g.Errors[j].File {
			return g.Errors[i].File < g.Errors[j].File
		}
		if g.Errors[i].Line != g.Errors[j].Line {
			return g.Errors[i].Line < g.Errors[j].Line
		}
		return g.Errors[i].Message < g.Errors[j].Message
	})
	sort.Slice(g.Concurrency, func(i, j int) bool {
		if g.Concurrency[i].File != g.Concurrency[j].File {
			return g.Concurrency[i].File < g.Concurrency[j].File
		}
		if g.Concurrency[i].Line != g.Concurrency[j].Line {
			return g.Concurrency[i].Line < g.Concurrency[j].Line
		}
		return g.Concurrency[i].Kind < g.Concurrency[j].Kind
	})
	sort.Slice(g.TestEdges, func(i, j int) bool {
		if g.TestEdges[i].File != g.TestEdges[j].File {
			return g.TestEdges[i].File < g.TestEdges[j].File
		}
		if g.TestEdges[i].Line != g.TestEdges[j].Line {
			return g.TestEdges[i].Line < g.TestEdges[j].Line
		}
		if g.TestEdges[i].TestFunc != g.TestEdges[j].TestFunc {
			return g.TestEdges[i].TestFunc < g.TestEdges[j].TestFunc
		}
		return g.TestEdges[i].Target < g.TestEdges[j].Target
	})
	sort.Slice(g.Implements, func(i, j int) bool {
		if g.Implements[i].InterfaceID != g.Implements[j].InterfaceID {
			return g.Implements[i].InterfaceID < g.Implements[j].InterfaceID
		}
		if g.Implements[i].ConcreteID != g.Implements[j].ConcreteID {
			return g.Implements[i].ConcreteID < g.Implements[j].ConcreteID
		}
		if g.Implements[i].Interface != g.Implements[j].Interface {
			return g.Implements[i].Interface < g.Implements[j].Interface
		}
		return g.Implements[i].Concrete < g.Implements[j].Concrete
	})
	sort.Slice(g.Mutations, func(i, j int) bool {
		if g.Mutations[i].File != g.Mutations[j].File {
			return g.Mutations[i].File < g.Mutations[j].File
		}
		if g.Mutations[i].Line != g.Mutations[j].Line {
			return g.Mutations[i].Line < g.Mutations[j].Line
		}
		if g.Mutations[i].Function != g.Mutations[j].Function {
			return g.Mutations[i].Function < g.Mutations[j].Function
		}
		if g.Mutations[i].Field != g.Mutations[j].Field {
			return g.Mutations[i].Field < g.Mutations[j].Field
		}
		return g.Mutations[i].Via < g.Mutations[j].Via
	})
	sort.Slice(g.Literals, func(i, j int) bool {
		if g.Literals[i].File != g.Literals[j].File {
			return g.Literals[i].File < g.Literals[j].File
		}
		if g.Literals[i].Line != g.Literals[j].Line {
			return g.Literals[i].Line < g.Literals[j].Line
		}
		return g.Literals[i].TypeName < g.Literals[j].TypeName
	})
	sort.Slice(g.HTTPCalls, func(i, j int) bool {
		if g.HTTPCalls[i].SourceFile != g.HTTPCalls[j].SourceFile {
			return g.HTTPCalls[i].SourceFile < g.HTTPCalls[j].SourceFile
		}
		if g.HTTPCalls[i].SourceLine != g.HTTPCalls[j].SourceLine {
			return g.HTTPCalls[i].SourceLine < g.HTTPCalls[j].SourceLine
		}
		if g.HTTPCalls[i].Method != g.HTTPCalls[j].Method {
			return g.HTTPCalls[i].Method < g.HTTPCalls[j].Method
		}
		return g.HTTPCalls[i].URL < g.HTTPCalls[j].URL
	})
	sort.Slice(g.FlowFunctions, func(i, j int) bool {
		if g.FlowFunctions[i].ID != g.FlowFunctions[j].ID {
			return g.FlowFunctions[i].ID < g.FlowFunctions[j].ID
		}
		return g.FlowFunctions[i].File < g.FlowFunctions[j].File
	})
}

func dedupeCalls(calls []graph.CallEdge) []graph.CallEdge {
	type key struct {
		callerID, callerName, calleeRaw, calleeID, file, usage string
		line, column                                           int
		synthetic                                              bool
	}
	seen := make(map[key]bool, len(calls))
	deduped := calls[:0]
	for _, call := range calls {
		k := key{
			callerID:   call.CallerSymbolID,
			callerName: call.CallerName,
			calleeRaw:  call.CalleeRaw,
			calleeID:   call.CalleeSymbolID,
			file:       call.File,
			usage:      call.ReturnUsage,
			line:       call.Line,
			column:     call.Column,
			synthetic:  call.Synthetic,
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		deduped = append(deduped, call)
	}
	return deduped
}

const helpText = `gograph — local AST-based Go repository context indexer for AI agents

USAGE
  gograph <command> [arguments] [output flags]

OUTPUT FLAGS
  --json                     Structured JSON for query and composed-analysis commands.
                             Operational commands such as build, wiki, gate, snapshot,
                             plugin installation, help, and version remain text; session
                             audit is the raw-JSON exception.
  --files-only               Flat, deduplicated paths for supported result-list commands;
                             empty results write zero lines.
  --mermaid                  Output visual dependency/call diagrams in Mermaid format.
                             Supported by: deps, dependents, coupling, callers, callees,
                             path, impact, and endpoint.
                             Bare form (no subcommand): gograph --mermaid → architecture
                             overview diagram (shorthand for 'diagram').
                             Request only one of --json, --files-only, or --mermaid.
  -i, --intention <msg>      Explain the technical rationale for executing the command.
                             MANDATORY for all analytical commands when a session is active.

INDEXING
  build [path]               Walk and parse a Go repository. Generates graph.json
                             and 9 targeted Markdown reports in .gograph/.
                             Adds .gograph/ to the Git repository root .gitignore
                             when available; outside Git, uses the target .gitignore.
                             If no files parse successfully, exits without replacing artifacts.
                             Parse failures and selection/security warnings are recorded
                             in graph.json build metadata and make status partial.
                             Run after any major code change. Default path: .
                             Supports --precise to perform type-checked Class
                             Hierarchy/SSA enrichment. If enrichment fails, warns,
                             publishes the AST graph and records precise_fallback, unless
                             a fresh precise artifact from the same sources is retained.
                             Inactive build-constrained files, cmd/go wildcard-excluded
                             directories, generated sources, go.mod ignore paths, AI
                             worktrees, and Git-ignored paths are automatically skipped.
  stale                      Check selected files, build context, and content digests.
                             Exit 0 when current, 2 when stale, and 1 on error;
                             --json uses the same exit contract.
                             Agents should run this before structural analysis.
  stats                      Compact index health summary: schema version, build
                             timestamp, and counts of packages, files, symbols,
                             calls, imports, routes, SQL queries, env reads, and
                             test edges, flow functions, build completeness, analysis
                             precision (ast/precise/precise_fallback), parser reuse,
                             rebuilt-package count, and parse failures. Zero
                             re-parsing — reads graph.json only.

AGENT WORKFLOW RULES (CRITICAL)
  1. BEFORE editing: ALWAYS run 'gograph plan <symbol>' to understand the impact,
     mapped tests, and execution risks (SQL/Env/Routes) of your target.
  2. AFTER editing: ALWAYS run 'gograph build . --precise' followed by 'gograph review --uncommitted'
     to verify test coverage, complexity, and that no unintended risks were introduced.

SEARCH & NAVIGATION
  query <term...>            Search across symbols, packages, files, imports, and
                             call sites. Case-insensitive, OR logic across terms.
  focus <package>            Show all symbols, imports, and call edges for one
                             package. Token-efficient alternative to reading files.
  node <name>                Show indexed AST metadata for a symbol, package, or file.
  source <name>              Extract confined raw source for functions, methods,
                             structs, interfaces, types, variables, and constants.
  public <package>           List only the exported (public) API of a package.
  fields <struct>            List all fields and types of a struct.
  embeds <struct>            Find which structs embed the given struct.
  imports <pkg>              Find all files importing a given package path.
  mutate <field>             Find functions that mutate a specific struct field.
                             Use Type.Field to disambiguate fields with the same name.
                             Catches direct assignments (s.f = x), IncDec/augmented
                             (s.f++, s.f += 1), and indirect mutations through
                             method calls — atomic.*/sync.Map/sync.Mutex/sync.RWMutex
                             /sync.WaitGroup/sync.Once stdlib mutators, user-defined
                             wrapper methods that write to receiver fields (detected
                             via SSA), and channel sends (s.ch <- x). Indirect rows
                             show "via <method-name>" in Detail. The ++/+= and
                             indirect-mutation cases require a --precise build.
  arity [--min 5]            Find functions with many arguments (long parameter list smell).
  skeleton                   Output the whole repository's API signatures with bodies stripped.

CALL GRAPH
  callers <function> [--no-tests] [--depth N] [--exact]
                             Find functions that call a target function or interface
                             method; precise interface queries expand every recorded
                             implementation and return a shared source site once.
                             --depth 2-10 expands N hops up (callers-of-callers).
  callees <function> [--no-tests] [--depth N]    find functions that a target function calls; --depth 2-10 expands N hops down
  impact <name>              Full upstream blast radius (all transitive callers).
  impact --uncommitted       Blast radius of all uncommitted modified symbols.
  impact --since <ref>       Blast radius of all symbols changed since a git ref (e.g. main, HEAD~5).
                             Composes changes --git <ref> + impact into one call.
  path <from> <to>           Shortest call chain between two symbols (BFS).
                             For callers/callees/impact/path: the symbol argument can be a short
                             name ("Validate" — fuzzy substring), concrete dot-notation,
                             interface notation ("Repository.Delete" for callers), or a fully-qualified ID
                             ("pkg/path::(*S).Validate" — exact match, no same-name conflation).
                             Use the FQ form to disambiguate overloads. Requires --precise build.
  trace <err_str>            Find the origin of an error and trace backwards to entry points.
  orphans                    Functions unreachable from runtime/test/route/public entry
                             points via BFS, including dead chains with internal callers.
                             Precise reachability traverses every interface target edge.

INTERFACES & TYPES
  implementers <interface> [--test-only]
                             Structs that implement the named interface (duck-typing).
                             --test-only limits results to structs defined in test/mock files.
  interfaces <struct>        Interfaces satisfied by the named struct (duck-typing).
  constructors <struct>      Find factory functions returning the named struct.
  literals <struct>          Find composite-literal initialization sites (Foo{...}) for a struct.
                             Run before adding a required field to know every site that will break.
  returnusage <function>     Show how each caller uses the return value of a function.
                             Labels: discarded, assigned, partially_ignored, returned, passed.
                             Run before changing a return signature — finds callers that silently
                             discard a value that will carry different semantics after the change.
  usages <type>              Find every place a type is referenced in a function signature
                             (param or return type), struct field, or interface method signature.
                             Run before changing an interface — shows the full consumption blast radius.
  schema <table>             Find structs mapped to a database table/schema via tags.
  globals <pkg>              Find pkg-level vars, consts, and mutators.
  mocks <interface>          Alias for 'implementers --test-only'. Kept for compatibility.
  fixtures <pkg>             Find test helper structs and functions in test files.

CODE QUALITY
  check [--config]           Run static policy checks using .gograph/checks.json.
  check --uncommitted        Run checks, including uncommitted code.
  check --since <ref|graph.json>
                             Run checks against a Git ref or saved graph baseline.
                             Saved graphs must be regular files inside the project
                             root, have no linked component, and carry the exact
                             current source-policy marker; serialized roots are ignored.
  gate                       Run CI/CD enforcement checks against a regular,
                             non-linked project-root .gograph.yml.
                             Fails before evaluation when graph.json is stale, then fails
                             if any configured threshold is violated.
                             Orphan/coupling deltas use the immediately preceding persisted
                             graph embedded automatically by publication; first build skips them.
  gate init                  Exclusively create a regular .gograph.yml template;
                             refuses links and existing entries.
  snapshot <subcmd>          Capture and diff architectural metrics (save, diff, list, drop).
                             Subcommands: save <name>, diff <name>, list, drop <name>.
                             Snapshot files/directories must be real repository entries.
  boundaries [--config]      Verify package architecture constraints using boundaries.json.
  boundaries --create        Auto-generate a baseline boundaries.json from the current repo;
                             refuses linked output paths and overwrite.
  flow [term] [--source kind] [--sink kind] [--config path] [--no-tests]
                             Find potential untrusted-data paths to SQL query text,
                             process execution, filesystem access, or outbound HTTP.
                             Sources: http_request, decoded_json, environment.
                             Sinks: sql_query, process_execution, filesystem, outbound_http.
                             Tests are included by default; --no-tests excludes them.
                             Optional return-value sanitizer policy: .gograph/flow.json.
                             Policy changes apply without rebuilding graph.json.
                             Schema: {"sanitizers":[{"function":"pkg.Clean",
                               "for":["filesystem"]}]}; omit "for" for all sinks.
  complexity [symbol]        Cyclomatic complexity per function, highest first.
                             Filter by symbol name substring. Labels: LOW / MEDIUM /
                             HIGH / VERY HIGH (McCabe thresholds: 5 / 10 / 20), or
                             UNKNOWN with score -1 when source cannot be read or parsed safely.
  diagram [--group-by package|module|service|file] [--max-depth N] [--include-stdlib]
                             Architecture overview diagram in Mermaid format.
                             --group-by package (default): one node per import path.
                             --group-by module: collapse to top-level dir group
                               (internal, cmd, pkg…); external deps → module root.
                             --group-by service: two-segment groups (internal/auth,
                               cmd/server…) — between package and module granularity.
                             --group-by file: file → imported package edges.
                             --max-depth N: BFS N levels from entry packages (those
                               nothing else imports). 0 = unlimited (default).
                             Shorthand: gograph --mermaid (no subcommand).
  coupling [package] [--include-stdlib] [--internal-only]
                             Fan-in, fan-out, and instability per package.
                             Instability = FanOut / (FanIn + FanOut). Range [0,1].
                             By default excludes stdlib packages; --internal-only also
                             excludes third-party dependencies.
  context <symbol> [--limit N] [--exact]
                             Bundle node+source+callers+callees+tests+role in one call.
                             'role' is a lightweight architectural classification (HTTP handler,
                             data access, orchestrator, coordinator, utility, entry point, internal).
  context --uncommitted      Context for all uncommitted modified symbols in one call.
                             Replaces 5-8 sequential 'context <sym>' calls after 'plan --uncommitted'.
  explain <symbol>           LLM-ready architectural narrative for a symbol.
                             Synthesizes callers (prod vs test split), callees,
                             complexity, SQL, env, routes, concurrency, tests,
                             interface satisfaction, and an opinionated role
                             classification into one prompt-ready text block.
  hotspot [--top N] [--include-tests]
                             Rank functions by incoming call count (fan-in).
                             Shows the most-depended-on code to study first.
                             Default: --top 10 with test-file call edges excluded.
  summary                    Single-call codebase briefing: hotspots, coupling,
                             complexity, reachability-orphan count, and god objects.
  untested [--pkg name] [--top N]
                             Called production functions with no attributed test edge.
  endpoint <route>           Full vertical slice for one HTTP endpoint (depth 1-20; supports --mermaid).
                             Composes: route resolution + handler symbol +
                             full callee chain (BFS, default depth 5) + SQL
                             emitted + env vars read. [--depth N] [--json|--mermaid]
                             [--include-tests]
                             Input: route pattern ("POST /api/users"), path
                             fragment ("/users"), or handler symbol name.
                             ROUTE-GROUPING LIMITATION: gograph reads route
                             paths from AST literals only. Grouped routers
                             (Gin Group, Echo Group, Chi) concatenate paths
                             at runtime — the prefix is not a literal and is
                             NOT recorded. Searching "POST /api/v1/users"
                             fails in a grouped codebase.
                             WORKAROUND: always prefer handler symbol name:
                               gograph endpoint "CreateUser"  (always works)
                             To find handler names: gograph routes
  deps <pkg> [--transitive]  Direct import dependencies of a package.
                             Add --transitive for the full closure (BFS).
  dependents <pkg>           Packages that import the named package (inverse of deps).
                             Essential before any package-level refactor.
  changes                    Symbols modified/added/deleted since the trusted persisted graph.
                             Deleted includes files absent from the current safely selected
                             inventory (gone, ignored, build-inactive, or unsafe).
  changes --git <ref>        Symbols in files changed since a git ref (MODIFIED
                             only). Useful for PR review and release scoping.
                             NEW and DELETED detection requires a full baseline
                             build. Ref must match [A-Za-z0-9._/\-~^]+.
                             Examples: --git main  --git HEAD~5  --git v1.4.50
  godobj [flags]             God-object struct candidates scored by method count,
                             field count, and outgoing calls.
                             Flags: --methods N  --fields N  --calls N  --top N
                             Defaults: --methods 5  --fields 8  --calls 15  --top 10
  plan <symbol> [--with-context]
                             Generate an operational change plan (callers, tests, risk profile)
                             before editing a symbol. --with-context bundles each inspect-first symbol.
  plan --uncommitted [--with-context]
                             Generate a change plan for all currently uncommitted modified symbols.
  review <symbol>            Generate a post-edit final review report for a modified symbol.
  review --uncommitted       Generate a post-edit final review report for all uncommitted changes.
  risk <symbol>              Evaluate change risk profile (blast radius, complexity, test coverage, SQL/env).
  risk --uncommitted         Evaluate risk profile for all uncommitted changes.
  api --since <ref|graph.json>
                             Identify API/contract changes since a Git ref or saved graph baseline.
                             Saved graphs must be regular files inside the project
                             root, have no linked component, and carry the exact
                             current source-policy marker; serialized roots are ignored.
                             Run 'gograph build . --precise' before this for best results.

EXTRACTION
  routes                     All HTTP REST API routes and their handler functions. Annotates unresolvable handlers.
  sql [term]                 Raw SQL queries mapped to the functions that run them.
                             Filter by SQL keyword or table-name substring.
  httpcalls [term]           All outbound HTTP client calls via net/http.
                             Filter by method or URL substring.
  errorflow <term> [--no-tests]
                             Trace likely error paths up to entry points (AST heuristic, NO SSA).
                             --no-tests excludes test-file references from related-test collection.
  trace <term> [--no-tests]  Alias for errorflow. Kept for compatibility.
  errors [term] [--no-tests] errors.New/fmt.Errorf/sentinel/panic sites mapped to source.
  envs [term]                Every os.Getenv / os.LookupEnv / supported Viper Get* read.
  concurrency [term]         Goroutine spawns, channel sends, and typed Mutex/RWMutex/
                             WaitGroup/Once calls (not receives or select statements).
  tests [symbol]             Test functions that exercise a named symbol.

AGENT INTEGRATION
  capabilities               Token-optimized cheat sheet for AI agents. Run this
                             first so the agent knows how to use gograph.
  wiki [--output <dir>]      Generate llm-wiki/ — machine-first markdown pages
                             from the static graph. Covers: overview, architecture,
                             hotspots, routes, env, errors, concurrency, api-surface,
                             and one file per internal package. Run once per session
                             for zero-cost orientation. Default: ./llm-wiki/.
                             Relative output is anchored to the graph root and rejects
                             linked components. Absolute output explicitly selects a
                             real local directory; generated paths stay beneath it.
                             Add llm-wiki/ to .gitignore.
  doc <pkg[.Symbol]>         Run 'go doc' for a stdlib or third-party package/symbol.
                             No graph required. Rejects filesystem-shaped queries and
                             source-tree links across the selected root plus its effective module
                             root, or the workspace root and member trees (.git/.gograph excluded);
                             validates regular Go tool metadata and workspace members confined
                             beneath the workspace; dependency
                             resolution follows the user's Go environment.
  mcp [path] [--persist-refresh]
                             Start a Model Context Protocol server over stdio.
                             Exposes graph queries as native tools for AI clients;
                             adopts newer precise graphs and preserves precision on refresh.
                             --persist-refresh is opt-in and publishes the latest successful
                             refresh to .gograph; it is not a multi-branch graph cache.
  session <action> [word]    Manage telemetry & audit sessions. Actions:
                             - create [unique_word]: Starts an audit session.
                             - end: Ends the active session.
                             - audit [session_id]: Audits and scores agent compliance & success.
                             - cleanup: Deletes stale inactive session log files.
                             Session IDs and regular files are confined beneath the
                             project's real .gograph/sessions directory.
                             NOTE: MCP gograph_plan/gograph_review calls are now
                             counted correctly in audit totals.
  add-claude-plugin          Install gograph as a Claude MCP plugin. Also injects
                             CLAUDE.md steering rules and a smart PreToolUse hook
                             that redirects Go symbol greps to gograph tools.
                             Claude Code MCP registration still requires the printed
                             'claude mcp add' command. Partial installation exits non-zero.
  hook-guard                 PreToolUse hook invoked by Claude Code. Reads a JSON
                             tool call from stdin; blocks grep on Go symbols and
                             suggests the equivalent gograph command. Exit 0 = allow,
                             exit 2 = block. Not intended for direct human use.

OTHER
  version, -v                Print version.
  help, -h                   Show this help.

OUTPUTS (after 'build')
  .gograph/graph.json        Machine-readable graph (JSON), committed by same-directory rename.
  .gograph/GRAPH_REPORT.md   Master index report.
  .gograph/graph-symbols.md  .gograph/graph-routes.md
  .gograph/graph-sql.md      .gograph/graph-concurrency.md
  .gograph/graph-tests.md    .gograph/graph-deps.md
  .gograph/graph-errors.md   .gograph/graph-config.md
  .gitignore                 Appends .gograph/ at Git repository root when available.
`

func printHelp() {
	fmt.Print(helpText)
}

func printCommandHelp(cmd string) {
	lines := strings.Split(helpText, "\n")
	found := false
	for _, line := range lines {
		if !found && strings.HasPrefix(line, "  "+cmd) && (len(line) == len("  "+cmd) || line[len("  "+cmd)] == ' ' || line[len("  "+cmd)] == '[') {
			found = true
			fmt.Println("USAGE")
			descStart := strings.Index(line[2:], "  ")
			if descStart != -1 {
				usagePart := line[2 : 2+descStart]
				descPart := strings.TrimSpace(line[2+descStart:])
				fmt.Printf("  gograph %s\n\nDESCRIPTION\n  %s\n", strings.TrimSpace(usagePart), descPart)
			} else {
				fmt.Printf("  gograph %s\n\nDESCRIPTION\n", strings.TrimSpace(line))
			}
		} else if found {
			if strings.HasPrefix(line, "                             ") || strings.HasPrefix(line, "    ") {
				fmt.Printf("  %s\n", strings.TrimSpace(line))
			} else {
				break
			}
		}
	}
	if !found {
		printHelp()
	}
}

func runFlow(args []string) int {
	options := search.FlowOptions{IncludeTests: true}
	usage := "usage: gograph flow [term] [--source http_request|decoded_json|environment] [--sink sql_query|process_execution|filesystem|outbound_http] [--config path] [--no-tests]"
	fail := func(message string) int {
		return failCommand("flow", message)
	}

	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch argument {
		case "--no-tests":
			options.IncludeTests = false
		case "--source", "--sink", "--config":
			if index+1 >= len(args) {
				return fail(fmt.Sprintf("%s requires a value\n%s", argument, usage))
			}
			index++
			switch argument {
			case "--source":
				options.Source = args[index]
			case "--sink":
				options.Sink = args[index]
			case "--config":
				options.ConfigPath = args[index]
			}
		default:
			switch {
			case strings.HasPrefix(argument, "--source="):
				options.Source = strings.TrimPrefix(argument, "--source=")
			case strings.HasPrefix(argument, "--sink="):
				options.Sink = strings.TrimPrefix(argument, "--sink=")
			case strings.HasPrefix(argument, "--config="):
				options.ConfigPath = strings.TrimPrefix(argument, "--config=")
			case strings.HasPrefix(argument, "-"):
				return fail(fmt.Sprintf("unknown flow flag: %s\n%s", argument, usage))
			case options.Term == "":
				options.Term = argument
			default:
				return fail(usage)
			}
		}
	}

	g, err := loadGraph(".")
	if err != nil {
		return fail(err.Error())
	}
	results, err := search.Flow(g, options)
	if err != nil {
		return fail(err.Error())
	}
	if jsonMode {
		return PrintJSON(okEnvelope("flow", options.Term, results, len(results)))
	}
	if filesOnlyMode {
		seen := make(map[string]bool)
		for _, result := range results {
			for _, path := range []string{result.Source.File, result.Sink.File} {
				if path != "" && !seen[path] {
					fmt.Println(path)
					seen[path] = true
				}
			}
		}
		return 0
	}
	if len(results) == 0 {
		fmt.Println("No potential untrusted-data flows found.")
		return 0
	}

	fmt.Printf("Potential security flows (%d):\n\n", len(results))
	for _, result := range results {
		fmt.Println(result.String())
		for _, step := range result.Path {
			fmt.Printf("  %-13s %s: %s  (%s:%d)\n", step.Kind, step.Function, step.Detail, step.File, step.Line)
		}
		fmt.Println()
	}
	return 0
}

// runPath finds the shortest call chain between two symbols via BFS.
func runPath(args []string) int {
	if len(args) != 2 || args[0] == "" || args[1] == "" || strings.HasPrefix(args[0], "-") || strings.HasPrefix(args[1], "-") {
		return failCommand("path", "usage: gograph path <from-symbol> <to-symbol>")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("path", err.Error())
	}
	chain := search.Path(g, args[0], args[1], true)
	if len(chain) == 0 {
		if jsonMode {
			return PrintJSON(okEnvelope("path", args[0]+" -> "+args[1], nil, 0))
		}
		if filesOnlyMode {
			return 0
		}
		fmt.Printf("No call path found from %q to %q.\n", args[0], args[1])
		return 0
	}
	if jsonMode {
		return PrintJSON(okEnvelope("path", args[0]+" -> "+args[1], chain, len(chain)))
	}
	if mermaidMode {
		fmt.Println(search.PathToMermaid(chain))
		return 0
	}
	fmt.Printf("Call path: %s → %s\n", args[0], args[1])
	for i, step := range chain {
		fmt.Printf("  %d. %s\n", i+1, step.String())
	}
	return 0
}

// runStale checks whether graph.json is out of date relative to source files.
func runStale() int {
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("stale", err.Error())
	}
	sr := search.Stale(g, graphRoot(g))
	if jsonMode {
		return staleJSONExitCode(
			PrintJSON(okEnvelope("stale", "", sr, sr.ChangeCount())),
			sr.IsStale,
		)
	}
	if !sr.IsStale {
		fmt.Printf("Graph is up to date (generated: %s).\n", sr.GraphAge)
		return exitSuccess
	}
	fmt.Printf("Graph is STALE (generated: %s).\n", sr.GraphAge)
	if sr.BuildContextChanged {
		fmt.Println("  effective Go build context changed")
	}
	if len(sr.ChangedFiles) > 0 {
		fmt.Printf("  %d selected file(s) changed:\n", len(sr.ChangedFiles))
		for _, f := range sr.ChangedFiles {
			fmt.Printf("    %s\n", f)
		}
	}
	fmt.Println("Run `gograph build .` to refresh.")
	return exitStale
}

func staleJSONExitCode(printCode int, isStale bool) int {
	if printCode != exitSuccess {
		return exitError
	}
	if isStale {
		return exitStale
	}
	return exitSuccess
}

func runStats() int {
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("stats", err.Error())
	}
	st := search.Stats(g)
	if jsonMode {
		return PrintJSON(okEnvelope("stats", "", st, 1))
	}
	fmt.Printf("schema_version : %s\n", st.SchemaVersion)
	fmt.Printf("generated_at   : %s\n", st.GeneratedAt)
	fmt.Printf("precision      : %s\n", st.Precision)
	fmt.Printf("packages       : %d\n", st.Packages)
	fmt.Printf("files          : %d\n", st.Files)
	fmt.Printf("symbols        : %d\n", st.Symbols)
	fmt.Printf("calls          : %d\n", st.Calls)
	fmt.Printf("imports        : %d\n", st.Imports)
	fmt.Printf("routes         : %d\n", st.Routes)
	fmt.Printf("sqls           : %d\n", st.SQLs)
	fmt.Printf("env_reads      : %d\n", st.EnvReads)
	fmt.Printf("test_edges     : %d\n", st.TestEdges)
	fmt.Printf("flow_functions : %d\n", st.FlowFunctions)
	fmt.Printf("build_status   : %s\n", st.BuildStatus)
	if st.BuildStatus != "unknown" {
		fmt.Printf("parsed_files   : %d/%d\n", st.ParsedFiles, st.ScannedFiles)
		fmt.Printf("reused_files   : %d\n", st.ReusedFiles)
		fmt.Printf("rebuilt_pkgs   : %d\n", st.RebuiltPackages)
		fmt.Printf("parse_failures : %d\n", st.ParseFailures)
	}
	return 0
}

// runOrphans uses reachability analysis to find truly unreachable symbols.
func runOrphans() int {
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("orphans", err.Error())
	}
	results := search.ReachableOrphans(g)
	return printResults("orphans", "", results, "No unreachable symbols found.")
}

// runGodObj detects god-object struct candidates using configurable thresholds.
// Flags: --methods N, --fields N, --calls N, --top N
func runGodObj(args []string) int {
	p := search.DefaultGodObjectParams()

	// Parse --key value pairs manually (no external flag lib).
	for i := 0; i < len(args); i++ {
		flag := args[i]
		switch flag {
		case "--methods":
		case "--fields":
		case "--calls":
		case "--top":
		default:
			return failCommandf("godobj", "unknown flag: %s", flag)
		}
		value, err := parseIntegerFlag(args, &i)
		if err != nil {
			return failCommand("godobj", err.Error())
		}
		switch flag {
		case "--methods":
			p.MinMethods = value
		case "--fields":
			p.MinFields = value
		case "--calls":
			p.MinCalls = value
		case "--top":
			p.Top = value
		}
	}

	g, err := loadGraph(".")
	if err != nil {
		return failCommand("godobj", err.Error())
	}

	candidates := search.GodObjects(g, p)
	if jsonMode {
		return PrintJSON(okEnvelope("godobj", "", candidates, len(candidates)))
	}
	if len(candidates) == 0 {
		fmt.Printf("No god-object candidates found (methods>%d, fields>%d, calls>%d).\n",
			p.MinMethods, p.MinFields, p.MinCalls)
		return 0
	}

	fmt.Printf("God Object Candidates (methods>%d, fields>%d, calls>%d):\n\n",
		p.MinMethods, p.MinFields, p.MinCalls)
	for _, c := range candidates {
		fmt.Printf("[%-8s] %s — %d methods, %d fields, %d outgoing calls  (%s:%d)\n",
			c.Severity, c.Name, c.MethodCount, c.FieldCount, c.OutgoingCalls, c.File, c.Line)
	}
	return 0
}

func runArity(args []string) int {
	minArgs := 5
	for i := 0; i < len(args); i++ {
		if args[i] != "--min" {
			return failCommandf("arity", "unknown argument: %s", args[i])
		}
		value, err := parseIntegerFlag(args, &i)
		if err != nil {
			return failCommand("arity", err.Error())
		}
		minArgs = value
	}

	g, err := loadGraph(".")
	if err != nil {
		return failCommand("arity", err.Error())
	}
	results := search.Arity(g, minArgs)
	if jsonMode {
		return PrintJSON(okEnvelope("arity", "", results, len(results)))
	}

	if len(results) == 0 {
		fmt.Printf("No functions found with >= %d arguments.\n", minArgs)
		return 0
	}

	fmt.Printf("Functions with %d+ arguments:\n", minArgs)
	for _, r := range results {
		fmt.Printf("  %s (%s:%d) - %s\n", r.Name, r.File, r.Line, r.Detail)
	}
	return 0
}

// runComplexity estimates cyclomatic complexity for matching functions.
func runComplexity(args []string) int {
	if !hasOptionalTarget(args) {
		return failCommand("complexity", "usage: gograph complexity [symbol]")
	}
	term := ""
	if len(args) > 0 {
		term = args[0]
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("complexity", err.Error())
	}
	results := search.Complexity(g, term)
	if jsonMode {
		return PrintJSON(okEnvelope("complexity", term, results, len(results)))
	}
	if len(results) == 0 {
		if term != "" {
			fmt.Printf("No functions found matching %q.\n", term)
		} else {
			fmt.Println("No functions found in graph.")
		}
		return 0
	}
	fmt.Printf("Cyclomatic Complexity (sorted highest first):\n\n")
	for _, r := range results {
		fmt.Printf("[%-9s] score=%-4d %s  (%s:%d)\n",
			r.Label, r.Score, r.Symbol, r.File, r.Line)
	}
	return 0
}

// runCoupling shows package fan-in, fan-out, and instability metrics.
func runCoupling(args []string) int {
	term := ""
	opts := search.CouplingOptions{}
	internalOnly := false
	for _, a := range args {
		switch a {
		case "--include-stdlib":
			// Keep stdlib packages in the report. Default is to exclude
			// them — users asking about *their* code's coupling almost
			// never care about fmt/strings/etc. coupling.
			opts.IncludeStdlib = true
		case "--internal-only":
			internalOnly = true
		default:
			if strings.HasPrefix(a, "-") {
				return failCommandf("coupling", "unknown flag: %s", a)
			}
			if term != "" {
				return failCommandf("coupling", "unexpected argument: %s", a)
			}
			term = a
		}
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("coupling", err.Error())
	}
	if internalOnly {
		opts.ModuleOnly = search.ReadModulePath(graphRoot(g))
	}
	results := search.Coupling(g, term, opts)
	if jsonMode {
		return PrintJSON(okEnvelope("coupling", term, results, len(results)))
	}
	if mermaidMode {
		fmt.Println(search.CouplingToMermaid(g, term, opts))
		return 0
	}
	if len(results) == 0 {
		if term != "" {
			fmt.Printf("No packages found matching %q.\n", term)
		} else {
			fmt.Println("No package import edges found in graph.")
		}
		return 0
	}
	fmt.Printf("Package Coupling (sorted by instability, highest first):\n\n")
	fmt.Printf("%-55s  %6s  %6s  %s\n", "Package", "FanOut", "FanIn", "Instability")
	fmt.Printf("%s\n", strings.Repeat("-", 82))
	for _, r := range results {
		instStr := fmt.Sprintf("%.2f", r.Instability)
		if r.Instability < 0 {
			instStr = "n/a"
		}
		fmt.Printf("%-55s  %6d  %6d  %s\n", r.Package, r.FanOut, r.FanIn, instStr)
	}
	return 0
}

// runDiagram generates a high-level architecture overview diagram of the repository.
func runDiagram(args []string) int {
	groupBy := "package"
	maxDepth := 0
	includeStdlib := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--include-stdlib":
			includeStdlib = true
		case a == "--group-by" && i+1 < len(args):
			i++
			groupBy = args[i]
		case strings.HasPrefix(a, "--group-by="):
			groupBy = strings.TrimPrefix(a, "--group-by=")
		case a == "--max-depth" && i+1 < len(args):
			i++
			maxDepth, _ = strconv.Atoi(args[i])
		case strings.HasPrefix(a, "--max-depth="):
			maxDepth, _ = strconv.Atoi(strings.TrimPrefix(a, "--max-depth="))
		}
	}
	g, err := loadGraph(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	diagram := search.DiagramToMermaid(g, groupBy, maxDepth, includeStdlib)
	// Count nodes by counting label definitions (lines containing `["`).
	nodeCount := strings.Count(diagram, "[\"")
	if nodeCount > 30 {
		fmt.Fprintf(os.Stderr, "warning: diagram has %d nodes — may be hard to read.\n", nodeCount)
		fmt.Fprintf(os.Stderr, "  Try --max-depth 2, a coarser --group-by level, or:\n")
		fmt.Fprintf(os.Stderr, "  gograph focus <package>   for a per-package file view\n")
	}
	fmt.Println(diagram)
	return 0
}

// runContext bundles node+source+callers+callees+tests for a symbol in one call.
func runContext(args []string) int {
	if len(args) == 0 {
		return failCommand("context", "usage: gograph context <symbol> [--limit N] [--exact]\n       gograph context --uncommitted [--limit N]")
	}

	uncommitted := false
	limit := 0
	exactMatch := false
	var termParts []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--uncommitted":
			uncommitted = true
		case "--exact":
			exactMatch = true
		case "--limit", "-n":
			value, err := parseIntegerFlag(args, &i)
			if err != nil {
				return failCommand("context", err.Error())
			}
			limit = value
		default:
			if strings.HasPrefix(a, "-") {
				return failCommandf("context", "unknown flag: %s", a)
			}
			termParts = append(termParts, a)
		}
	}
	if uncommitted && len(termParts) > 0 {
		return failCommand("context", "context --uncommitted cannot be combined with a symbol")
	}
	if !uncommitted && !hasSingleTarget(termParts) {
		return failCommand("context", "usage: gograph context <symbol> [--limit N] [--exact]\n       gograph context --uncommitted [--limit N]")
	}

	g, err := loadGraph(".")
	if err != nil {
		return failCommand("context", err.Error())
	}
	root := graphRoot(g)

	if uncommitted {
		syms, err := search.UncommittedSymbols(g)
		if err != nil {
			return failCommand("context", err.Error())
		}
		if len(syms) == 0 {
			if jsonMode {
				return PrintJSON(okEnvelope("context", "--uncommitted", nil, 0))
			}
			fmt.Println("No uncommitted modified symbols found.")
			return 0
		}
		var results []*search.ContextResult
		var payloads []search.ContextPayload
		for _, sym := range syms {
			if r := search.Context(g, root, sym, false); r != nil {
				results = append(results, r)
				payloads = append(payloads, search.NewContextPayload(sym, r))
			}
		}
		if jsonMode {
			return PrintJSON(okEnvelope("context", "--uncommitted", payloads, len(payloads)))
		}
		fmt.Printf("=== CONTEXT: %d uncommitted symbol(s) ===\n\n", len(results))
		for _, r := range results {
			printContextResult(r, limit)
		}
		return 0
	}

	if len(termParts) == 0 {
		return failCommand("context", "usage: gograph context <symbol> [--limit N] [--exact]\n       gograph context --uncommitted [--limit N]")
	}
	term := strings.Join(termParts, " ")
	result := search.Context(g, root, term, exactMatch)
	if result == nil {
		if jsonMode {
			return PrintJSON(okEnvelope("context", term, nil, 0))
		}
		fmt.Printf("No symbol found matching %q.\n", term)
		return 0
	}
	if jsonMode {
		return PrintJSON(okEnvelope("context", term, search.NewContextPayload(term, result), 1))
	}
	fmt.Printf("=== CONTEXT: %s ===\n\n", term)
	printContextResult(result, limit)
	return 0
}

func printContextResult(result *search.ContextResult, limit int) {
	if len(result.Node) > 0 {
		fmt.Println("--- NODE ---")
		for _, r := range result.Node {
			fmt.Println(r.String())
		}
		if result.Role != "" {
			fmt.Printf("role: %s\n", result.Role)
		}
		fmt.Println()
	}

	if result.Source != "" {
		fmt.Println("--- SOURCE ---")
		fmt.Println(result.Source)
	} else if result.SourceErr != nil {
		fmt.Printf("(source unavailable: %v)\n\n", result.SourceErr)
	}

	if limit > 0 && len(result.Callers) > limit {
		fmt.Printf("--- CALLERS (showing %d of %d) ---\n", limit, len(result.Callers))
		for _, r := range result.Callers[:limit] {
			fmt.Println(r.String())
		}
		fmt.Printf("... and %d more callers.\n\n", len(result.Callers)-limit)
	} else if len(result.Callers) > 0 {
		fmt.Printf("--- CALLERS (%d) ---\n", len(result.Callers))
		for _, r := range result.Callers {
			fmt.Println(r.String())
		}
		fmt.Println()
	}

	if limit > 0 && len(result.Callees) > limit {
		fmt.Printf("--- CALLEES (showing %d of %d) ---\n", limit, len(result.Callees))
		for _, r := range result.Callees[:limit] {
			fmt.Println(r.String())
		}
		fmt.Printf("... and %d more callees.\n\n", len(result.Callees)-limit)
	} else if len(result.Callees) > 0 {
		fmt.Printf("--- CALLEES (%d) ---\n", len(result.Callees))
		for _, r := range result.Callees {
			fmt.Println(r.String())
		}
		fmt.Println()
	}

	if len(result.Tests) > 0 {
		fmt.Printf("--- TESTS (%d) ---\n", len(result.Tests))
		for _, r := range result.Tests {
			fmt.Println(r.String())
		}
		fmt.Println()
	}
}

// runHotspot ranks functions by incoming call count.
func runHotspot(args []string) int {
	top := 10
	includeTests := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--top":
			value, err := parseIntegerFlag(args, &i)
			if err != nil {
				return failCommand("hotspot", err.Error())
			}
			top = value
		case "--include-tests":
			// Count call edges from *_test.go files. Default-off because
			// test infrastructure tends to dominate hotspot rankings in
			// test-heavy codebases (e.g. baseReq with 100+ callers from
			// table-driven tests). Production-fan-in is more useful for
			// "where is this codebase concentrated" questions.
			includeTests = true
		default:
			return failCommandf("hotspot", "unknown argument: %s", args[i])
		}
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("hotspot", err.Error())
	}
	results := search.Hotspot(g, top, includeTests)
	if jsonMode {
		return PrintJSON(okEnvelope("hotspot", "", results, len(results)))
	}
	if len(results) == 0 {
		fmt.Println("No hotspot data found (no call edges in graph).")
		return 0
	}
	label := fmt.Sprintf("top %d", top)
	if top == 0 {
		label = "all"
	}
	fmt.Printf("Hotspot Functions (%s, sorted by incoming calls):\n\n", label)
	for i, r := range results {
		fmt.Printf("%3d.  %-6d calls  %s  (%s:%d)\n", i+1, r.IncomingCalls, r.Name, r.File, r.Line)
	}
	return 0
}

// runDeps shows direct (and optionally transitive) imports for a package.
func runDeps(args []string) int {
	if len(args) == 0 {
		return failCommand("deps", "usage: gograph deps <package> [--transitive]")
	}
	pkg := args[0]
	if pkg == "" || strings.HasPrefix(pkg, "-") {
		return failCommand("deps", "usage: gograph deps <package> [--transitive]")
	}
	transitive := false
	for _, a := range args[1:] {
		switch a {
		case "--transitive":
			transitive = true
		default:
			return failCommandf("deps", "unknown argument: %s", a)
		}
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("deps", err.Error())
	}
	result := search.Deps(g, pkg, transitive)
	if result == nil {
		if jsonMode {
			return PrintJSON(okEnvelope("deps", pkg, nil, 0))
		}
		fmt.Printf("No package found matching %q.\n", pkg)
		return 0
	}
	if jsonMode {
		return PrintJSON(okEnvelope("deps", pkg, result, len(result.Direct)+len(result.Transitive)))
	}
	if mermaidMode {
		fmt.Println(search.DepsToMermaid(g, result))
		return 0
	}
	fmt.Printf("Package: %s\n\nDirect imports (%d):\n", result.Package, len(result.Direct))
	for _, imp := range result.Direct {
		fmt.Printf("  %s\n", imp)
	}
	if transitive {
		fmt.Printf("\nTransitive imports (%d):\n", len(result.Transitive))
		for _, imp := range result.Transitive {
			fmt.Printf("  %s\n", imp)
		}
	}
	return 0
}

// runDependents lists all packages that import the named package.
func runDependents(args []string) int {
	if !hasSingleTarget(args) {
		return failCommand("dependents", "usage: gograph dependents <package>")
	}
	pkg := args[0]
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("dependents", err.Error())
	}
	results := search.Dependents(g, pkg)
	if jsonMode {
		return PrintJSON(okEnvelope("dependents", pkg, results, len(results)))
	}
	if mermaidMode {
		fmt.Println(search.DependentsToMermaid(pkg, results))
		return 0
	}
	return printResults("dependents", pkg, results, fmt.Sprintf("No packages found that import %q.", pkg))
}

// runChanges reports symbols modified/added/deleted since the persisted graph,
// or — when --git <ref> is provided — symbols in files changed since that git ref.
func runChanges(args []string) int {
	// Parse --git <ref> flag.
	var gitRef string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--git":
			if i+1 >= len(args) {
				return failCommand("changes", "--git requires a value")
			}
			i++
			gitRef = args[i]
		default:
			return failCommandf("changes", "unknown argument: %s", args[i])
		}
	}

	g, err := loadGraph(".")
	if err != nil {
		return failCommand("changes", err.Error())
	}
	root := graphRoot(g)

	// --- git-ref mode ---
	if gitRef != "" {
		result, err := search.ChangesByGitRef(g, root, gitRef)
		if err != nil {
			return failCommand("changes", err.Error())
		}
		if jsonMode {
			return PrintJSON(okEnvelope("changes", gitRef, result, len(result.ChangedFiles)+len(result.Symbols)))
		}
		if len(result.ChangedFiles) == 0 && len(result.Symbols) == 0 {
			fmt.Printf("No Go file changes detected since %s.\n", gitRef)
			return 0
		}
		fmt.Printf("Changes since %s (git-ref mode — MODIFIED only):\n\n", gitRef)
		if len(result.ChangedFiles) > 0 {
			fmt.Printf("Modified files (%d):\n", len(result.ChangedFiles))
			for _, f := range result.ChangedFiles {
				fmt.Printf("  %s\n", f)
			}
			fmt.Println()
		}
		fmt.Printf("Affected symbols: %d modified\n", len(result.Symbols))
		fmt.Println("Note: NEW and DELETED detection requires a full baseline build from that ref.")
		fmt.Println()
		for _, sym := range result.Symbols {
			fmt.Printf("[MODIFIED] %s  (%s:%d)\n", sym.Name, sym.File, sym.Line)
		}
		return 0
	}

	// --- default mode: content digests vs graph.json ---
	result := search.Changes(g, root)
	if jsonMode {
		return PrintJSON(okEnvelope("changes", "", result, len(result.ChangedFiles)+len(result.Symbols)))
	}

	if len(result.ChangedFiles) == 0 && len(result.Symbols) == 0 {
		fmt.Printf("No changes detected (graph generated: %s).\n",
			result.GraphAge.Format("2006-01-02 15:04:05 UTC"))
		return 0
	}

	fmt.Printf("Changes since persisted graph (%s):\n\n",
		result.GraphAge.Format("2006-01-02 15:04:05 UTC"))

	if len(result.ChangedFiles) > 0 {
		fmt.Printf("Modified files (%d):\n", len(result.ChangedFiles))
		for _, f := range result.ChangedFiles {
			fmt.Printf("  %s\n", f)
		}
		fmt.Println()
	}

	counts := map[search.ChangeStatus]int{}
	for _, s := range result.Symbols {
		counts[s.Status]++
	}
	fmt.Printf("Affected symbols: %d modified, %d new, %d deleted\n\n",
		counts[search.ChangeModified], counts[search.ChangeNew], counts[search.ChangeDeleted])

	for _, sym := range result.Symbols {
		switch sym.Status {
		case search.ChangeNew:
			fmt.Printf("[NEW     ] %s  (%s:%d)\n", sym.Name, sym.File, sym.Line)
		case search.ChangeDeleted:
			fmt.Printf("[DELETED ] %s  (%s)\n", sym.Name, sym.File)
		case search.ChangeModified:
			fmt.Printf("[MODIFIED] %s  (%s:%d)\n", sym.Name, sym.File, sym.Line)
		}
	}
	return 0
}

// runSource extracts the raw source code of a named symbol.
func runSource(args []string) int {
	if !hasSingleTarget(args) {
		return failCommand("source", "usage: gograph source <name>")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("source", err.Error())
	}
	term := args[0]
	root := graphRoot(g)
	src, err := search.Source(g, root, term)
	if err != nil {
		return failCommandf("source", "source: %v", err)
	}
	if jsonMode {
		return PrintJSON(okEnvelope("source", term, src, 1))
	}
	fmt.Println(src)
	return 0
}

// runPublic lists only the exported (public) API of a package.
func runPublic(args []string) int {
	if !hasSingleTarget(args) {
		return failCommand("public", "usage: gograph public <package>")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("public", err.Error())
	}
	results := search.Public(g, args[0])
	return printResults("public", args[0], results, fmt.Sprintf("No exported symbols found for package %q.", args[0]))
}

// runFields lists all fields and types of a struct.
func runFields(args []string) int {
	if !hasSingleTarget(args) {
		return failCommand("fields", "usage: gograph fields <struct>")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("fields", err.Error())
	}
	results := search.Fields(g, args[0])
	return printResults("fields", args[0], results, fmt.Sprintf("No fields found for struct %q.", args[0]))
}

// runEmbeds finds which structs embed the given struct.
func runEmbeds(args []string) int {
	if !hasSingleTarget(args) {
		return failCommand("embeds", "usage: gograph embeds <struct>")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("embeds", err.Error())
	}
	results := search.Embeds(g, args[0])
	return printResults("embeds", args[0], results, fmt.Sprintf("No structs found embedding %q.", args[0]))
}

// runImports finds all files importing a given package path.
func runImports(args []string) int {
	if !hasSingleTarget(args) {
		return failCommand("imports", "usage: gograph imports <pkg>")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("imports", err.Error())
	}
	results := search.ExternalImports(g, args[0])
	return printResults("imports", args[0], results, fmt.Sprintf("No files found importing %q.", args[0]))
}

// runImpact traverses the call graph backwards to find all symbols that eventually call the target.
func runImpact(args []string) int {
	if len(args) == 0 {
		return failCommand("impact", "usage: gograph impact <symbol>\n       gograph impact --uncommitted\n       gograph impact --since <ref>")
	}
	if args[0] == "--uncommitted" && len(args) != 1 {
		return failCommand("impact", "impact --uncommitted does not accept additional arguments")
	}
	if args[0] == "--since" && len(args) != 2 {
		return failCommand("impact", "usage: gograph impact --since <ref>")
	}
	if strings.HasPrefix(args[0], "-") && args[0] != "--uncommitted" && args[0] != "--since" {
		return failCommandf("impact", "unknown flag: %s", args[0])
	}
	if args[0] != "--uncommitted" && args[0] != "--since" && !hasSingleTarget(args) {
		return failCommand("impact", "usage: gograph impact <symbol>\n       gograph impact --uncommitted\n       gograph impact --since <ref>")
	}

	g, err := loadGraph(".")
	if err != nil {
		return failCommand("impact", err.Error())
	}

	if args[0] == "--uncommitted" {
		return runImpactUncommitted(g)
	}

	if args[0] == "--since" {
		return runImpactSince(g, args[1])
	}

	term := args[0]
	if mermaidMode {
		fmt.Println(search.ImpactToMermaid(g, term, true))
		return 0
	}
	results := search.Impact(g, term, true)
	return printResults("impact", term, results, fmt.Sprintf("No callers found in blast radius of %q.", args[0]))
}

// runImpactUncommitted parses git diff to find modified symbols, then computes their blast radius.
func runImpactUncommitted(g *graph.Graph) int {
	modifiedSymbolNames, err := search.UncommittedSymbols(g)
	if err != nil {
		return failCommand("impact", err.Error())
	}

	if len(modifiedSymbolNames) == 0 {
		return printResults("impact", "--uncommitted", nil, "No uncommitted modified symbols found in the graph.")
	}

	if mermaidMode {
		fmt.Println(search.ImpactMultipleToMermaid(g, modifiedSymbolNames, true))
		return 0
	}
	reason := fmt.Sprintf("downstream impact of uncommitted changes (%d symbols)", len(modifiedSymbolNames))
	results := search.ImpactMultiple(g, modifiedSymbolNames, reason, true)
	return printResults("impact", "--uncommitted", results, "No callers found in blast radius of uncommitted changes.")
}

// runImpactSince computes the blast radius of all symbols changed since a git ref.
func runImpactSince(g *graph.Graph, ref string) int {
	root := graphRoot(g)
	changes, err := search.ChangesByGitRef(g, root, ref)
	if err != nil {
		return failCommand("impact", err.Error())
	}
	if len(changes.Symbols) == 0 {
		return printResults("impact", "--since "+ref, nil, fmt.Sprintf("No Go symbol changes found since %q.", ref))
	}
	names := make([]string, 0, len(changes.Symbols))
	for _, s := range changes.Symbols {
		names = append(names, s.Name)
	}
	if mermaidMode {
		fmt.Println(search.ImpactMultipleToMermaid(g, names, true))
		return 0
	}
	reason := fmt.Sprintf("downstream impact of changes since %s (%d symbols)", ref, len(names))
	results := search.ImpactMultiple(g, names, reason, true)
	return printResults("impact", "--since "+ref, results, fmt.Sprintf("No callers found in blast radius of changes since %q.", ref))
}

// runRoutes lists all HTTP REST API routes and their handler functions.
func runRoutes() int {
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("routes", err.Error())
	}
	results := search.Routes(g)
	return printResults("routes", "", results, "No HTTP routes found.")
}

// runSQL lists raw SQL queries mapped to the functions that run them.
func runSQL(args []string) int {
	if !hasOptionalTarget(args) {
		return failCommand("sql", "usage: gograph sql [term]")
	}
	term := ""
	if len(args) > 0 {
		term = args[0]
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("sql", err.Error())
	}
	results := search.SQL(g, term)
	return printResults("sql", term, results, "No SQL queries found.")
}

// runErrors lists custom error variables and panics mapped to their source.
func runErrors(args []string) int {
	term := ""
	includeTests := true
	filtered := args[:0]
	for _, a := range args {
		if a == "--no-tests" {
			includeTests = false
		} else if strings.HasPrefix(a, "-") {
			return failCommandf("errors", "unknown flag: %s", a)
		} else {
			filtered = append(filtered, a)
		}
	}
	if len(filtered) > 0 && !hasSingleTarget(filtered) {
		return failCommand("errors", "usage: gograph errors [term] [--no-tests]")
	}
	if len(filtered) > 0 {
		term = filtered[0]
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("errors", err.Error())
	}
	results := search.Errors(g, term, includeTests)
	return printResults("errors", term, results, "No custom errors or panics found.")
}

// runHTTPCalls lists all detected HTTP client calls in the graph.
func runHTTPCalls(args []string) int {
	if !hasOptionalTarget(args) {
		return failCommand("httpcalls", "usage: gograph httpcalls [term]")
	}
	term := ""
	if len(args) > 0 {
		term = args[0]
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("httpcalls", err.Error())
	}
	results := search.HTTPCalls(g, term)
	return printResults("httpcalls", term, results, "No HTTP client calls found.")
}

// runSkeleton prints a stripped skeleton of the repository structure.
func runSkeleton() int {
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("skeleton", err.Error())
	}
	skeleton := search.Skeleton(g)
	if jsonMode {
		return PrintJSON(okEnvelope("skeleton", "", skeleton, 1))
	}
	fmt.Println(skeleton)
	return 0
}

// runMutate finds functions that mutate the given struct field.
func runMutate(args []string) int {
	if !hasSingleTarget(args) {
		return failCommand("mutate", "usage: gograph mutate <Field>")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("mutate", err.Error())
	}
	results := search.Mutate(g, args[0])
	return printResults("mutate", args[0], results, "No mutations found for that field.")
}

// runTrace traces an error string backwards from entry points.
func runTrace(args []string) int {
	return runErrorFlowCommand("trace", args)
}

func runErrorFlow(args []string) int {
	return runErrorFlowCommand("errorflow", args)
}

func runErrorFlowCommand(command string, args []string) int {
	noTests := false
	var termParts []string
	for _, a := range args {
		if a == "--no-tests" {
			noTests = true
		} else {
			termParts = append(termParts, a)
		}
	}
	if len(termParts) == 0 {
		return failCommand(command, "Usage: gograph errorflow <error-string|ErrSymbol> [--no-tests]")
	}

	g, err := loadGraph(".")
	if err != nil {
		return failCommand(command, err.Error())
	}

	term := strings.Join(termParts, " ")
	report := search.ErrorFlow(g, term, !noTests)

	if jsonMode {
		payload := search.NewErrorFlowPayload(report)
		return PrintJSON(okEnvelope(command, term, payload, payload.Count()))
	}

	fmt.Printf("ErrorFlow Report for %q\n", term)
	fmt.Println("==================================================")
	fmt.Println("⚠️  DISCLAIMER: Likely error path based on static call graph and AST references.")
	fmt.Println("   Highly useful for navigation, not proof. No SSA/data-flow tracking performed.")
	fmt.Println("==================================================")
	fmt.Println()

	if len(report.DefinitionSites) > 0 {
		fmt.Println("1. Definition Sites:")
		for _, r := range report.DefinitionSites {
			fmt.Printf("   - %s (%s:%d) -> %s\n", r.Name, r.File, r.Line, r.Detail)
		}
		fmt.Println()
	}

	if len(report.ReturnSites) > 0 {
		fmt.Println("2. Return / Wrap / Check Sites:")
		for _, r := range report.ReturnSites {
			fmt.Printf("   - %s (%s:%d) -> %s\n", r.Name, r.File, r.Line, r.Detail)
		}
		fmt.Println()
	}

	if len(report.Paths) > 0 {
		fmt.Println("3. Likely Route / Entrypoint Paths:")
		for i, p := range report.Paths {
			confidence := "MEDIUM"
			if len(report.DefinitionSites) > 0 {
				confidence = "HIGH"
			}
			fmt.Printf("   Path %d [Confidence: %s] (Originates in %s):\n", i+1, confidence, p.Error.Function)
			for j, step := range p.Path {
				fmt.Printf("      %d. %s (%s:%d) - %s\n", j+1, step.Name, step.File, step.Line, step.Detail)
			}
			fmt.Println()
		}
	} else {
		fmt.Println("3. Likely Route / Entrypoint Paths:\n   - No complete path to an HTTP route or main entrypoint found.")
		fmt.Println()
	}

	if len(report.RelatedTests) > 0 {
		fmt.Println("4. Related Tests:")
		for _, r := range report.RelatedTests {
			fmt.Printf("   - %s (%s:%d) -> %s\n", r.Name, r.File, r.Line, r.Detail)
		}
		fmt.Println()
	}

	return 0
}

func runConstructors(args []string) int {
	if !hasSingleTarget(args) {
		return failCommand("constructors", "Usage: gograph constructors <struct>")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommandf("constructors", "Error loading graph: %v", err)
	}
	results := search.Constructors(g, args[0])
	return printResults("constructors", args[0], results, fmt.Sprintf("No constructors found for struct '%s'.", args[0]))
}

func runUsages(args []string) int {
	if !hasSingleTarget(args) {
		return failCommand("usages", "usage: gograph usages <TypeName>")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("usages", err.Error())
	}
	results := search.Usages(g, args[0])
	if jsonMode {
		return PrintJSON(okEnvelope("usages", args[0], results, len(results)))
	}
	return printResults("usages", args[0], results, fmt.Sprintf("No usage sites found for type %q.", args[0]))
}

func runReturnUsage(args []string) int {
	if !hasSingleTarget(args) {
		return failCommand("returnusage", "usage: gograph returnusage <function>")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("returnusage", err.Error())
	}
	results := search.ReturnUsages(g, args[0])
	if jsonMode {
		return PrintJSON(okEnvelope("returnusage", args[0], results, len(results)))
	}
	return printResults("returnusage", args[0], results, fmt.Sprintf("No call sites found for %q.", args[0]))
}

func runLiterals(args []string) int {
	if !hasSingleTarget(args) {
		return failCommand("literals", "usage: gograph literals <struct>")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("literals", err.Error())
	}
	results := search.Literals(g, args[0])
	if jsonMode {
		return PrintJSON(okEnvelope("literals", args[0], results, len(results)))
	}
	return printResults("literals", args[0], results, fmt.Sprintf("No literal sites found for struct %q.", args[0]))
}

func runSchema(args []string) int {
	if !hasSingleTarget(args) {
		return failCommand("schema", "Usage: gograph schema <table>")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommandf("schema", "Error loading graph: %v", err)
	}
	results := search.Schema(g, args[0])
	return printResults("schema", args[0], results, fmt.Sprintf("No struct found mapped to table '%s'.", args[0]))
}

func runGlobals(args []string) int {
	term := ""
	if hasSingleTarget(args) {
		term = args[0]
	} else {
		return failCommand("globals", "Usage: gograph globals <package>")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommandf("globals", "Error loading graph: %v", err)
	}
	results := search.Globals(g, term)
	return printResults("globals", term, results, "No globals or mutators found.")
}

func runMocks(args []string) int {
	return runImplementersCommand("mocks", append([]string{"--test-only"}, args...))
}

func runFixtures(args []string) int {
	if !hasSingleTarget(args) {
		return failCommand("fixtures", "Usage: gograph fixtures <package>")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommandf("fixtures", "Error loading graph: %v", err)
	}
	results := search.Fixtures(g, args[0])
	return printResults("fixtures", args[0], results, fmt.Sprintf("No fixtures found for package '%s'.", args[0]))
}

func runBoundaries(args []string) int {
	configPath := ".gograph/boundaries.json"
	createMode := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return failCommand("boundaries", "--config requires a value")
			}
			i++
			configPath = args[i]
		case "--create":
			createMode = true
		default:
			return failCommandf("boundaries", "unknown argument: %s", args[i])
		}
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommandf("boundaries", "Error loading graph: %v", err)
	}

	if createMode {
		if err := search.CreateBoundaries(g, configPath); err != nil {
			return failCommandf("boundaries", "Failed to create boundaries: %v", err)
		}
		fmt.Printf("Successfully created baseline boundaries at %s\n", configPath)
		return 0
	}

	results, err := search.Boundaries(g, configPath)
	if err != nil {
		return failCommandf("boundaries", "Boundaries error: %v", err)
	}
	code := printResults("boundaries", configPath, results, "No boundary violations found. Architecture is clean!")
	if len(results) > 0 {
		// Exit with non-zero if violations exist (useful for CI/CD)
		return 1
	}
	return code
}

func runEndpoint(args []string) int {
	if len(args) == 0 {
		return failCommand("endpoint", `Usage: gograph endpoint <route-pattern|handler-symbol> [--depth N] [--json|--mermaid] [--include-tests]

Examples:
  gograph endpoint "POST /api/users"   # route pattern (flat routers only)
  gograph endpoint "/users"             # path fragment
  gograph endpoint "CreateUser"         # handler symbol (works with ALL routing styles)

Flags:
  --depth N         BFS depth for call chain, clamped to 1-20 (default: 5)
  --include-tests   include routes registered in *_test.go files (excluded by default)
  --json            machine-readable JSON output`)
	}

	depth := 5
	query := args[0]
	includeTests := false // tests excluded by default, consistent with other commands
	if query == "" || strings.HasPrefix(query, "-") {
		return failCommandf("endpoint", "missing endpoint query before flag %s", query)
	}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--include-tests":
			includeTests = true
		case "--depth":
			value, err := parseIntegerFlag(args, &i)
			if err != nil {
				return failCommand("endpoint", err.Error())
			}
			depth = value
		default:
			return failCommandf("endpoint", "unknown argument: %s", args[i])
		}
	}
	if depth < 1 {
		depth = 1
	} else if depth > 20 {
		depth = 20
	}

	g, err := loadGraph(".")
	if err != nil {
		return failCommandf("endpoint", "failed to load graph: %v", err)
	}

	slices := search.Endpoint(g, query, depth, includeTests)
	if len(slices) == 0 {
		if jsonMode {
			return PrintJSON(okEnvelope("endpoint", query, slices, 0))
		}
		fmt.Printf("No matching HTTP routes found for %q\n\n", query)
		fmt.Println("Possible reasons:")
		fmt.Println("  1. The route does not exist — run 'gograph routes' to see all registered routes.")
		fmt.Println("  2. The route uses a dynamically computed group prefix that static analysis cannot resolve.")
		fmt.Println("     Constant Gin/Echo/Fiber Group() and Chi Route() prefixes are composed automatically.")
		fmt.Println("")
		fmt.Println("Fix: search by handler symbol name instead of route pattern:")
		fmt.Printf("  gograph endpoint \"<HandlerFunctionName>\"\n")
		fmt.Println("")
		fmt.Println("To find the handler name for a route, run: gograph routes")
		return 0
	}

	if mermaidMode {
		fmt.Println(search.EndpointToMermaid(slices))
		return 0
	}

	if jsonMode {
		return PrintJSON(okEnvelope("endpoint", query, slices, len(slices)))
	}

	for _, s := range slices {
		fmt.Printf("ROUTE    %s\n", s.Route)
		fmt.Printf("HANDLER  %s  (%s:%d)\n", s.Handler, s.HandlerFile, s.HandlerLine)

		if s.IsInline {
			fmt.Println()
			if s.InlineBody != "" {
				fmt.Println("HANDLER SOURCE (inline closure)")
				fmt.Println()
				// Indent each line for readability
				for _, line := range strings.Split(s.InlineBody, "\n") {
					fmt.Printf("  %s\n", line)
				}
				fmt.Println()
			} else {
				// InlineBody is empty only if the graph was built before this feature.
				// Direct the user to rebuild.
				fmt.Println("NOTE: Handler is an inline closure (anonymous function).")
				fmt.Printf("      Source not available — run 'gograph build .' to capture it.\n")
				fmt.Printf("      Navigate manually: %s  line %d\n", s.HandlerFile, s.HandlerLine)
				fmt.Println()
			}
			fmt.Println("LIMITATIONS")
			for _, l := range s.Limitations {
				fmt.Printf("  ⚠  %s\n", l)
			}
			fmt.Println()
			continue
		}

		fmt.Println()

		if len(s.CallChain) > 0 {
			fmt.Println("CALL CHAIN")
			for _, step := range s.CallChain {
				location := ""
				if step.File != "" {
					location = fmt.Sprintf("  (%s:%d)", step.File, step.Line)
				}
				calleeStr := ""
				if len(step.Callees) > 0 {
					calleeStr = "  → " + strings.Join(step.Callees, ", ")
				}
				fmt.Printf("  %d  %-40s%s%s\n", step.Depth, step.Symbol, calleeStr, location)
			}
			fmt.Println()
		}

		if len(s.SQL) > 0 {
			fmt.Println("SQL")
			for _, sq := range s.SQL {
				fmt.Printf("  [%s:%d] %s\n", sq.File, sq.Line, sq.Query)
			}
			fmt.Println()
		}

		if len(s.EnvReads) > 0 {
			fmt.Println("ENV READS")
			for _, e := range s.EnvReads {
				fmt.Printf("  %s\n", e)
			}
			fmt.Println()
		}

		fmt.Println("LIMITATIONS")
		for _, l := range s.Limitations {
			fmt.Printf("  ⚠  %s\n", l)
		}
		fmt.Println()
	}
	return 0
}

type planContextJSON = search.ContextPayload

func newPlanContextJSON(symbol string, result *search.ContextResult) planContextJSON {
	return search.NewContextPayload(symbol, result)
}

// runPlan generates an operational change plan for one or more symbols or for uncommitted changes.
func runPlan(args []string) int {
	if len(args) == 0 {
		return failCommand("plan", "usage: gograph plan <symbol> [--with-context]\n       gograph plan --uncommitted [--with-context]")
	}

	withContext := false
	var filtered []string
	for _, a := range args {
		if a == "--with-context" {
			withContext = true
		} else {
			filtered = append(filtered, a)
		}
	}
	args = filtered
	if len(args) != 1 || args[0] == "" || (strings.HasPrefix(args[0], "-") && args[0] != "--uncommitted") {
		return failCommand("plan", "usage: gograph plan <symbol> [--with-context]\n       gograph plan --uncommitted [--with-context]")
	}

	g, err := loadGraph(".")
	if err != nil {
		return failCommand("plan", err.Error())
	}

	var symbolNames []string
	var title string

	if args[0] == "--uncommitted" {
		symbolNames, err = search.UncommittedSymbols(g)
		if err != nil {
			return failCommand("plan", err.Error())
		}
		if len(symbolNames) == 0 {
			if jsonMode {
				return PrintJSON(okEnvelope("plan", "Uncommitted Changes", nil, 0))
			}
			fmt.Println("No uncommitted modified symbols found in the graph.")
			return 0
		}
		title = "Uncommitted Changes"
	} else {
		symbolNames = []string{args[0]}
		title = symbolNames[0]
	}

	plan := search.Plan(g, symbolNames, title)
	root := ""
	var rawContexts []*search.ContextResult
	var contexts []planContextJSON
	if withContext && len(plan.ReadFirst) > 0 {
		root, _ = filepath.Abs(rootfind.FindRoot())
		for _, sym := range plan.ReadFirst {
			_, result := search.ContextForPlanResult(g, root, sym)
			if result == nil {
				continue
			}
			rawContexts = append(rawContexts, result)
			contexts = append(contexts, newPlanContextJSON(sym.Name, result))
		}
	}

	if jsonMode {
		if withContext {
			return PrintJSON(okEnvelope("plan", title, map[string]any{
				"plan":             plan,
				"inspect_contexts": contexts,
			}, 1))
		}
		return PrintJSON(okEnvelope("plan", title, plan, 1))
	}

	fmt.Print(plan.String())

	if withContext && len(rawContexts) > 0 {
		fmt.Println("\n=== INSPECT_FIRST CONTEXTS ===")
		for i, result := range rawContexts {
			fmt.Printf("\n=== CONTEXT: %s ===\n\n", contexts[i].Symbol)
			printContextResult(result, 0)
		}
	}
	return 0
}

// runReview generates a post-edit checklist for modified symbols.
func runReview(args []string) int {
	if len(args) != 1 || args[0] == "" || (strings.HasPrefix(args[0], "-") && args[0] != "--uncommitted") {
		return failCommand("review", "usage: gograph review <symbol> OR gograph review --uncommitted")
	}

	g, err := loadGraph(".")
	if err != nil {
		return failCommand("review", err.Error())
	}

	var symbolNames []string
	var title string

	if args[0] == "--uncommitted" {
		symbolNames, err = search.UncommittedSymbols(g)
		if err != nil {
			return failCommand("review", err.Error())
		}
		if len(symbolNames) == 0 {
			if jsonMode {
				return PrintJSON(okEnvelope("review", "Uncommitted Changes", nil, 0))
			}
			fmt.Println("No uncommitted modified symbols found in the graph.")
			return 0
		}
		title = "Uncommitted Changes"
	} else {
		symbolNames = []string{args[0]}
		title = args[0]
	}

	report := search.Review(g, symbolNames, title)

	if jsonMode {
		return PrintJSON(okEnvelope("review", title, report, 1))
	}

	fmt.Print(report.String())
	return 0
}

func runRisk(args []string) int {
	if len(args) != 1 || args[0] == "" || (strings.HasPrefix(args[0], "-") && args[0] != "--uncommitted") {
		return failCommand("risk", "usage: gograph risk <symbol> OR gograph risk --uncommitted")
	}

	g, err := loadGraph(".")
	if err != nil {
		return failCommand("risk", err.Error())
	}

	var symbolNames []string
	var title string

	if args[0] == "--uncommitted" {
		symbolNames, err = search.UncommittedSymbols(g)
		if err != nil {
			return failCommand("risk", err.Error())
		}
		if len(symbolNames) == 0 {
			if jsonMode {
				return PrintJSON(okEnvelope("risk", "Uncommitted Changes", &search.RiskReport{
					Title:   "Uncommitted Changes",
					Message: "No uncommitted modified symbols found in the graph.",
				}, 0))
			}
			fmt.Println("No uncommitted modified symbols found in the graph.")
			return 0
		}
		title = "Uncommitted Changes"
	} else {
		symbolNames = []string{args[0]}
		title = symbolNames[0]
	}

	report := search.Risk(g, symbolNames, title)

	if jsonMode {
		return PrintJSON(okEnvelope("risk", title, report, len(report.Results)))
	}

	fmt.Print(report.String())
	return 0
}

func runExplain(args []string) int {
	if !hasSingleTarget(args) {
		return failCommand("explain", "usage: gograph explain <symbol>")
	}
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("explain", err.Error())
	}
	term := args[0]
	result := search.Explain(g, term)
	if result == nil {
		if jsonMode {
			return PrintJSON(okEnvelope("explain", term, nil, 0))
		}
		fmt.Printf("No symbol found matching %q.\n", term)
		return 0
	}
	if jsonMode {
		return PrintJSON(okEnvelope("explain", term, result, 1))
	}
	fmt.Printf("=== EXPLAIN: %s ===\n\n%s\n", result.Symbol, result.Narrative)
	return 0
}

// runWiki generates the llm-wiki/ directory from the static graph.
// Usage: gograph wiki [--output <dir>]
func runWiki(args []string) int {
	outputDir := "llm-wiki"
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--output" {
			outputDir = args[i+1]
		}
	}

	g, err := loadGraph(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	gen := wiki.New(g)
	pages, err := gen.Generate(outputDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wiki:", err)
		return 1
	}

	written := 0
	for _, p := range pages {
		if p.Content != "" {
			fmt.Printf("  wrote  %s/%s\n", outputDir, p.Filename)
			written++
		}
	}
	fmt.Printf("\nDone. %d page(s) written to %s/\n", written, outputDir)
	return 0
}

// runSummary prints a dense, single-call codebase briefing combining the five
// most useful orientation queries: top hotspots, worst instability package,
// highest-complexity function, orphan count, and god-object count.
// Replaces: hotspot + coupling + orphans + complexity + godobj (5 tool calls → 1).
func runSummary() int {
	g, err := loadGraph(".")
	if err != nil {
		return failCommand("summary", err.Error())
	}

	hotspots := search.Hotspot(g, 3, false)
	coupling := search.Coupling(g, "", search.CouplingOptions{})
	complexity := search.Complexity(g, "")
	orphanList := search.ReachableOrphans(g)
	godObjs := search.GodObjects(g, search.DefaultGodObjectParams())
	stats := search.Stats(g)

	if jsonMode {
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
		return PrintJSON(okEnvelope("summary", "", res, 1))
	}

	fmt.Printf("CODEBASE SUMMARY  (%d symbols, %d packages)\n", stats.Symbols, stats.Packages)
	fmt.Println()

	// Hotspots
	if len(hotspots) == 0 {
		fmt.Println("Hotspots:           (no call edges)")
	} else {
		names := make([]string, len(hotspots))
		for i, h := range hotspots {
			names[i] = fmt.Sprintf("%s (%dx)", h.Name, h.IncomingCalls)
		}
		fmt.Printf("Hotspots:           %s\n", strings.Join(names, ", "))
	}

	// Worst instability
	if len(coupling) == 0 {
		fmt.Println("Worst instability:  (no coupling data)")
	} else {
		c := coupling[0]
		fmt.Printf("Worst instability:  %s (%.2f)\n", c.Package, c.Instability)
	}

	// Highest complexity
	if len(complexity) == 0 {
		fmt.Println("Highest complexity: (no data)")
	} else {
		c := complexity[0]
		fmt.Printf("Highest complexity: %s (score=%d, %s)\n", c.Symbol, c.Score, c.Label)
	}

	// Orphans and God Objects
	fmt.Printf("Orphans:            %d unreachable symbols\n", len(orphanList))
	fmt.Printf("God objects:        %d\n", len(godObjs))

	return 0
}

// runUntested finds production functions and methods that have at least one
// non-test caller but zero test edges — the coverage gap that neither
// 'orphans' (zero callers) nor 'tests <sym>' (per-symbol lookup) surfaces
// efficiently at codebase scale.
//
// Usage: gograph untested [--pkg <name>] [--top N]
func runUntested(args []string) int {
	pkg := ""
	top := 0
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--pkg":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return failCommand("untested", "--pkg requires a value")
			}
			pkg = args[i+1]
			i++
		case "--top":
			value, err := parseIntegerFlag(args, &i)
			if err != nil {
				return failCommand("untested", err.Error())
			}
			top = value
		default:
			return failCommandf("untested", "unknown argument: %s", args[i])
		}
	}

	g, err := loadGraph(".")
	if err != nil {
		return failCommand("untested", err.Error())
	}

	results := search.Untested(g)

	// Filter by package if requested.
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

	// Apply --top limit.
	if top > 0 && len(results) > top {
		results = results[:top]
	}

	if jsonMode {
		return PrintJSON(okEnvelope("untested", "", results, len(results)))
	}

	if len(results) == 0 {
		fmt.Println("No untested functions found — all called symbols have test coverage.")
		return 0
	}

	label := "all"
	if top > 0 {
		label = fmt.Sprintf("top %d", top)
	}
	fmt.Printf("Untested Functions (%s, sorted by caller count):\n\n", label)
	fmt.Printf("%-40s  %-12s  %6s  %s\n", "FUNCTION", "PACKAGE", "CALLERS", "FILE")
	fmt.Println(strings.Repeat("-", 90))
	for _, r := range results {
		name := r.Name
		if len(name) > 38 {
			name = name[:35] + "..."
		}
		pkg := r.PackageName
		if len(pkg) > 10 {
			// Show just the last segment for readability.
			if i := strings.LastIndex(pkg, "/"); i >= 0 {
				pkg = pkg[i+1:]
			}
		}
		fmt.Printf("%-40s  %-12s  %6d  %s:%d\n", name, pkg, r.CallerCount, r.File, r.Line)
	}
	return 0
}

// runDoc runs `go doc <query>` and surfaces the output — provides signatures,
// doc comments, and method listings for any stdlib or third-party symbol that
// gograph's graph does not index (external packages).
//
// Usage: gograph doc <pkg.Symbol>
// Examples:
//
//	gograph doc fmt.Errorf
//	gograph doc net/http.HandleFunc
//	gograph doc github.com/jackc/pgx/v5.Conn.QueryRow
//	gograph doc io.Reader
func runDoc(args []string) int {
	if !hasSingleTarget(args) {
		return failCommand("doc", `usage: gograph doc <pkg[.Symbol]>
examples:
  gograph doc fmt.Errorf
  gograph doc net/http.HandleFunc
  gograph doc github.com/jackc/pgx/v5.Conn.QueryRow`)
	}

	query := args[0]
	if err := scanner.ValidateGoDocQuery(query); err != nil {
		return failCommandf("doc", "%v", err)
	}
	docRoot, err := scanner.SourceValidationRoot(".")
	if err != nil {
		return failCommandf("doc", "cannot determine repository validation root: %v", err)
	}
	if err := scanner.ValidateToolchainSourceInputs(docRoot); err != nil {
		return failCommandf("doc", "refusing to run the Go toolchain with unsafe repository source or metadata: %v", err)
	}

	cmd := exec.Command("go", "doc", query)
	cmd.Dir = docRoot
	out, err := cmd.Output()

	type docResult struct {
		Query  string `json:"query"`
		Output string `json:"output"`
	}

	if err != nil {
		// `go doc` writes helpful errors to stderr; surface them.
		var exitErr *exec.ExitError
		errMsg := err.Error()
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			errMsg = strings.TrimSpace(string(exitErr.Stderr))
		}
		return failCommand("doc", errMsg)
	}

	text := strings.TrimSpace(string(out))
	if jsonMode {
		return PrintJSON(okEnvelope("doc", "", []docResult{{Query: query, Output: text}}, 1))
	}
	fmt.Println(text)
	return 0
}
