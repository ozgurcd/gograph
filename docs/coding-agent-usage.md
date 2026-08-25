# gograph for Coding Agents

How `gograph` helps coding agents (Claude Code, Cursor, Copilot, Gemini, Codeium, Antigravity, etc.) work effectively in Go repositories.

## The problem gograph solves

Coding agents typically explore a repo by reading raw files and grepping. This is fine for small projects but becomes expensive in larger Go codebases:

- **Context burn** — each `Read` of a 500-line file fills the context window with bodies the agent doesn't need just to learn that a function exists.
- **Repeated first-orientation** — answering "where does X live?" or "what calls Y?" can require several text searches and source reads.
- **Missed structure** — agents read files in isolation and miss the package layout, import graph, and call relationships that a human would skim from a directory tree.
- **Stale mental model** — after edits, the agent's earlier reads no longer reflect reality.

`gograph` produces a static, AST-derived map of the repo so the agent can answer structural questions from one small file (`.gograph/GRAPH_REPORT.md`) instead of dozens of file reads.

## What it gives the agent

A single command (`gograph build .`) emits `graph.json` plus nine focused Markdown reports under `.gograph/`:

| Artifact | Use |
|---|---|
| `GRAPH_REPORT.md` + split reports | Human + agent readable summaries for symbols, dependencies, routes, SQL, errors, configuration, concurrency, and tests. |
| `graph.json` | Machine-readable full graph — dependencies, packages, files, structs, interfaces, funcs, methods, imports, call edges, env reads, SQL queries, errors, concurrency primitives, test edges. |

*Note: Use `gograph build . --precise` for type-checked interface, CHA, and SSA enrichment. Add `--tags=integration` (or another validated comma-separated tag list) when tagged files and tests must be part of that graph; an explicit value replaces `GOFLAGS -tags`, while omitting it preserves inherited behavior. SSA bodies are limited to selected repository packages rather than the transitive dependency closure; imported types and local external-call references remain available. That production enrichment requires compilable, build-selected packages; if type/load analysis fails or omits an indexed non-test file, gograph warns, retains the AST graph, and records `precise_fallback`, unless a fresh successful precise artifact already covers the same sources. Test packages are resolved separately: broken tests produce `typed_partial` test attribution without downgrading successful production precision. Typed-only test targets are recomputed on incremental builds rather than restored as parser facts. Persisted graph JSON over 512 MiB is rejected before allocation; rerun `gograph build` to reconstruct it from source. On constrained hosts, `gograph build . --precise --memory-mode=low --max-memory=1GiB` preserves precision while prioritizing lower heap use through aggressive reclamation and a soft Go runtime memory target; it is not a hard RSS cap and may use more GC CPU.*

And query commands the agent can invoke without re-parsing:

Start an agent session with `gograph doctor --json` so an older PATH-resolved
or shadowed installation is visible before any MCP result is trusted.

```sh
gograph query <term>            # symbol/package/file/import/call substring search (works great for finding specific test names!)
gograph focus <package>         # isolate context for a specific package
gograph callers <function|Interface.Method> [--no-tests] [--depth N] [--exact] # who calls it; interface-qualified queries expand precise CHA targets
gograph callees <function> [--no-tests] [--depth N] # what it calls; --depth 2-10 expands N hops down
gograph implementers <interface> # which structs implement an interface
gograph interfaces <struct>     # which interfaces a struct satisfies (precise if --precise used)
gograph fields <struct>         # extract fields and types of a struct
gograph source <symbol>         # extract the indexed declaration's source block
gograph impact <symbol>         # find downstream callers (blast radius)
gograph impact --uncommitted    # find blast radius of all uncommitted code changes
gograph impact --since main     # blast radius of all symbols changed since main (PR-level)
gograph orphans                 # functions unreachable via BFS from main/init, test, route, and eligible public roots — stricter than a 0-call check
gograph routes                  # extract all HTTP REST API routes
gograph imports <pkg>           # trace external/internal package usage
gograph sql [term]              # map raw SQL queries to their execution functions; optionally filter by keyword/table
gograph errors                  # custom error variables and panics mapped to their source
gograph embeds <struct>         # find which structs embed a target struct
gograph public <pkg>            # list only the exported API surface of a package
gograph envs [term]             # list indexed environment reads
gograph concurrency [term]      # map goroutines, channel sends, mutexes, waitgroups, sync.Once
gograph tests [symbol]          # direct attributed test calls (compatibility mode)
gograph tests <symbol> --transitive [--exact-only] [--package name] # every reaching test with path/depth
gograph coverage <TestFunc> [--exact-only] [--package name] # transitive product symbols one test statically reaches
gograph identity <symbol-or-stable-id> [--package name] # print or re-resolve canonical symbol identity
gograph path <from> <to>        # shortest call chain between two symbols (BFS traversal)
gograph stale                   # check selected files and build context vs graph.json
gograph stats                   # compact index health summary: schema/build/production precision/test-resolution status and graph counts
gograph godobj                  # find god-object struct candidates (default thresholds)
gograph godobj --methods 10 --fields 12 --calls 30 --top 5  # custom thresholds
gograph complexity              # cyclomatic complexity for all functions, highest first
gograph complexity "Run"        # complexity for a specific function by name
gograph coupling                # package fan-in, fan-out, and instability table
gograph coupling "internal/auth" # filter to a specific package
gograph coupling --internal-only # exclude standard-library and third-party packages
# --- COMPOSED AGENT WORKFLOWS ---
gograph context "ValidateToken" --exact # node + source + callers + callees + tests + role in ONE call
gograph context --uncommitted    # context for ALL uncommitted symbols bundled — replaces 5-8 sequential calls after plan --uncommitted
gograph explain "ValidateToken"  # LLM-ready narrative: role, complexity, SQL, env, routes, interfaces (use to understand purpose)
gograph hotspot                  # top 10 most-called functions (focus study here first)
gograph hotspot --top 20         # expand the hotspot window
gograph deps "internal/auth"     # direct import dependencies of a package
gograph deps "internal/auth" --transitive  # full transitive import closure
gograph dependents "internal/auth"  # all packages that import this package (inverse of deps — run before any package refactor)
gograph plan <symbol>            # generate an operational change plan for a symbol
gograph plan <symbol> --with-context  # plan + full context for every inspect_first symbol in ONE call
gograph plan --uncommitted       # generate a change plan for all uncommitted changes
gograph risk <symbol>            # evaluate change risk profile (blast radius, complexity, tests, SQL/env)
gograph risk --uncommitted       # evaluate risk profile for all uncommitted changes
gograph changes                  # new/modified/deleted symbols since trusted persisted graph;
                                 # deleted also means absent from current safe selected inventory
gograph changes --git <ref>      # symbols in files changed since a git ref (MODIFIED only; e.g. --git main, --git HEAD~5, --git v1.4.50)
gograph errorflow "parse failed" --no-tests  # trace error path to entry points, excluding test references
gograph trace "parse failed"     # alias for errorflow (kept for compatibility)
gograph flow --no-tests          # potential HTTP/JSON/env paths to SQL, process, filesystem, or outbound HTTP sinks
gograph diagram                  # Mermaid architecture diagram of package dependency graph [--group-by package|module|service|file] [--max-depth N] [--include-stdlib]
gograph check                    # run static policy checks (.gograph/checks.json): boundaries, api_drift, max_arity, max_complexity, test_coverage
gograph check --uncommitted      # include uncommitted code in check scope
gograph check --since main       # include api_drift against a Git ref
# or: gograph check --since path/to/baseline.json
gograph mutate "User.Status"     # find mutations of a specific struct field; Type.Field filters same-named fields on other types. Ordinary local assignments are excluded. IncDec/augmented and indirect atomic/sync/wrapper/channel mutations require --precise. Indirect results identify the mutating method.
gograph arity --min 5            # find functions with many arguments (long parameter list smell)
gograph skeleton                 # output the whole repository's API signatures (bodies stripped)
gograph constructors <struct>    # find factory functions returning a named struct
gograph literals <struct>        # all Foo{...} composite literal sites — run before adding/removing a required field
gograph usages <type>            # indexed param/return/field/interface-method uses — review before changing an interface
gograph returnusage <function>   # how each caller uses the return value (discarded/assigned/partially_ignored/returned/passed) — run before changing a return signature
gograph schema <table>           # find structs mapped to a database table or schema via tags
gograph globals <pkg>            # find pkg-level vars, consts, and functions mutating them
gograph implementers <interface> --test-only  # find structs implementing an interface in test or mock files
gograph mocks <interface>        # alias for implementers --test-only (kept for compatibility)
gograph fixtures <pkg>           # find test helper structs and functions in test files
gograph endpoint <route>         # full vertical slice for one HTTP endpoint: handler, call chain, SQL, env reads [--depth N] [--json]
gograph capabilities             # print token-optimized AI agent cheat sheet
gograph wiki [--output dir]      # generate llm-wiki/ — curated, machine-first markdown pages (overview, architecture,
                                 # hotspots, routes, env, errors, concurrency, per-package docs, api-surface).
                                 # Run once per session for generated codebase orientation.
                                 # Default output: ./llm-wiki/  (add llm-wiki/ to .gitignore)
gograph summary                  # single-call briefing: top 3 hotspots + worst instability + highest complexity +
                                 # orphan count + god-object count. Replaces 5 separate tool calls. [--json]
gograph untested                 # called production symbols without an exact transitive test path. Open interface
                                 # candidates remain test_resolution=possible. Flags: [--pkg <name>] [--top N]
                                 # [--exclude <repository-relative-glob>]... [--wide] [--json]
gograph doctor                   # diagnose duplicate/shadowed PATH installations without executing alternates [--json]
gograph doc <pkg[.Symbol]>       # go doc wrapper: signature + doc comment for any stdlib or third-party symbol.
                                 # No graph required. Rejects filesystem-shaped queries and refuses unsafe
                                 # descendant links, Go tool metadata, and unconfined workspace members before go doc;
                                 # dependencies are open-world.
                                 # Use when following call chains outside the project.
                                 # Examples: gograph doc fmt.Errorf   gograph doc io.Reader
                                 #           gograph doc net/http.HandleFunc  [--json]
gograph mcp [path] [--persist-refresh] [--tags=integration[,tag...]] [--memory-mode=low] [--max-memory=1GiB]
                                 # stdio MCP server; optional successful-refresh publication
                                 # memory options match CLI build/refresh semantics
gograph httpcalls [term]         # all outbound net/http calls (Get, Post, PostForm, Head); filter by method or URL substring
gograph add-claude-plugin        # install MCP server + CLAUDE.md rules + PreToolUse hook (Claude Desktop & Claude Code)
gograph hook-guard               # PreToolUse hook binary — blocks indexed-repository Go symbol greps (invoked automatically by Claude Code)
# --- CI ENFORCEMENT ---
gograph gate                     # read regular non-linked project-root .gograph.yml and fail CI on threshold violations
# --- SNAPSHOTS ---
gograph snapshot save <name>     # capture current architectural metrics under a label
gograph snapshot diff <name>     # compare current graph against a saved snapshot (shows improved / WORSE per metric)
gograph snapshot list            # list all saved snapshots
gograph snapshot drop <name>     # delete a named snapshot
```

## Claude Code / Claude Desktop Integration

Running `gograph add-claude-plugin` performs three installation steps in one command:

| Step | What it does | Where |
|---|---|---|
| **MCP server** | Registers gograph so Claude has native tool access | `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) |
| **CLAUDE.md rules** | Injects steering instructions Claude reads at session start | `~/.claude/CLAUDE.md` |
| **PreToolUse hook** | Intercepts broad `grep`/`rg` Go-symbol searches in indexed repositories and suggests structural tools | `~/.claude/hooks/gograph-guard.sh` + `~/.claude/settings.json` |

### How the hook works

The hook (`gograph hook-guard`) is invoked automatically by Claude Code before every `Bash` tool call. It:

1. Reads the tool call JSON from stdin.
2. Checks if the command is `grep` or `rg`.
3. Resolves parsed search paths against the payload's `cwd` (falling back to the hook process working directory when `cwd` is absent) and checks each path for a real `.gograph` ancestor.
4. If at least one effective target belongs to an indexed repository, the search can cover Go files, and every non-exempt pattern branch is either one Go identifier or an identifier-only alternation (3+ ASCII identifier characters per branch) → **blocks** with exit code `2` and tells Claude which `gograph` tool to use instead.
5. Otherwise → **allows** with exit code `0`.

**Allowed through (not blocked):**
- Searches whose effective targets have no `.gograph` ancestor, including non-Go repositories in multi-root workspaces
- Searches targeting only non-Go files (`*.yaml`, `*.md`, `*.sql`, etc.)
- Comment/doc-only searches (TODO, FIXME, HACK, etc.)
- Searches targeting only `docs/`, `.github/`, `testdata/`, or `migrations/`
- Patterns containing non-identifier regex or literal text. In particular,
  literal-pipe patterns in fixed-string mode and escaped pipes in `grep -E`/`rg`
  remain text searches.

**Blocked and redirected:**
```bash
grep -r "ValidateToken" .        # → gograph_query "ValidateToken"
rg "UserService" -g '*.go' .     # → gograph_context "UserService"
grep -rn "runCheck" .            # → gograph_callers "runCheck"
grep -rn 'LoadUser\|SaveUser' .   # basic grep alternation → LoadUser first
rg 'LoadUser|SaveUser' -g '*.go'  # ripgrep alternation → LoadUser first
```

Index detection follows the actual parsed targets rather than only `cwd`. A
command launched from an unindexed repository is still steered when it searches
an indexed sibling, while a command launched from an indexed repository is
allowed when all of its search targets are unindexed.

Alternation follows the selected search dialect: basic `grep` uses `\|`, while
`grep -E` and `rg` use bare `|`. The hook parses direct `grep`/`rg` arguments,
option values, quotes, and the first shell pipeline stage; malformed or dynamic
shell syntax fails open.

The hook is steering, not a correctness boundary. If graph precision is
`ast`/`precise_fallback`, a result is ambiguous, or a known call is missing,
verify with `gopls` or a targeted source/text search and disclose that fallback.
Literal, comment, generated/non-indexed, and non-Go searches belong in text
search from the start.

## Concrete agent workflows

### Recommended agent workflow:
- Session start: read `llm-wiki/index.md` → `llm-wiki/project.md` → `llm-wiki/agent-rules.md` → `llm-wiki/agent-contract.md` (if these maintained pages exist). Treat `agent-rules.md` as protected policy and use the repository's governed draft workflow for proposed changes.
- If generated pages are missing: `gograph build . --precise && gograph wiki`
- Graph freshness: `gograph stats` + `gograph stale`
- Understand a symbol: `gograph context <symbol>` (raw data) or `gograph explain <symbol>` (narrative — use when you need to understand purpose and architecture)
- Before editing: `gograph plan <symbol>` (callers, tests, SQL/env/route risk)
- Before a package refactor: `gograph dependents <pkg>` (indexed import consumers)
- After editing: `gograph build . --precise`, then `gograph review --uncommitted`, followed by the repository's required tests and checks
- Before done: `gograph check --uncommitted`
- If API-facing changes exist: `gograph check --since main` (or `master`)
*(Note: A Git baseline must exist locally. A baseline path ending in `.json`
loads a saved graph instead. Saved graphs must be regular files inside the
selected project, with no linked path component and the exact current
source-policy marker. Their serialized `root` is ignored in favor of the
selected project root.)*

### Config Example for .gograph/checks.json:
```json
{
  "checks": {
    "boundaries": "error",
    "api_drift": "warn",
    "require_tests_for_changed_routes": "error",
    "require_tests_for_changed_exported_symbols": "warn",
    "test_coverage": "warn",
    "no_orphans": "error",
    "new_globals": "warn",
    "max_arity": {
      "level": "warn",
      "value": 6
    },
    "max_complexity": {
      "level": "warn",
      "value": 20
    }
  },
  "boundaries_config": ".gograph/boundaries.json",
  "baseline": "master"
}
```
*(Note: The `"baseline"` property accepts a local Git branch/tag/commit such as
`"main"` or `"v1.0"`, or a saved graph path ending in `.json`, for checks that
compare against earlier state. Saved graphs have the same in-project,
regular-file, no-linked-component, current-source-policy requirements as
`--since`; their serialized root is not trusted.)*

### 1. Onboarding to an unfamiliar repo
Instead of `ls -R` + reading 10 random files, the agent reads `.gograph/GRAPH_REPORT.md` and immediately knows: packages, entry points, hottest files, hottest symbols, what imports what.

### 2. "Where is X implemented?"
`gograph query X` returns file:line locations for matching symbols, packages, files, imports, and call sites — typically one tool call vs. several `grep` rounds.

### 3. Impact analysis before a refactor
`gograph callers SomeFunc` lists indexed call sites with source locations and
the recorded call expression. Combined with `callees`, it provides a static
blast-radius starting point before editing. Use `--no-tests` (`gograph callers
SomeFunc --no-tests`) to filter indexed test callers. Dynamic calls, external
implementations, and incomplete/fallback analysis still require verification.

### 4. Configuration / secrets surface
`gograph envs` lists recognized `os.Getenv`, `os.LookupEnv`, and supported
configuration-read sites with file, line, and enclosing function. Filter by
name: `gograph envs DATABASE`.

### 5. Interface satisfaction discovery
`gograph interfaces Worker` uses duck-typing to show which interfaces `Worker` satisfies without running the compiler. Essential when mocking a service layer for tests.

### 6. Concurrency audit
`gograph concurrency` shows recognized goroutine, channel, and typed `sync`
operations in indexed files. Filter: `gograph concurrency goroutine` or
`gograph concurrency mutex`.

### 7. Test coverage lookup
`gograph tests ValidateToken` shows direct statically mapped test calls.
`gograph tests ValidateToken --transitive` answers the broader reverse question
in one pass: every test with a stable-ID path to the product symbol, including
router/callback intermediates and exact/possible propagation. Neither form is a
runtime-execution claim.

For the inverse question, `gograph coverage TestValidateToken` returns the
transitive product-symbol set attributed to that one test, with stable IDs and
`exact`/`possible` propagation. Use `--exact-only` for claims that must exclude
uncertain dispatch. `gograph identity <symbol>` prints or re-resolves a stable
ID for cross-document references. The optional package qualifier disambiguates
an in-package test from an external `foo_test` package when their graph IDs
collide. These remain static graph claims, not proof that a test or branch
executed.

### 8. Call chain pathfinding
`gograph path CreateUser sql` performs BFS over the call graph to find the shortest path between two symbols. Example output:
```
Call path: CreateUser → sql
  1. [path] CreateUser — calls UserService.Create (handlers/user.go:42)
  2. [path] UserService.Create — calls db.ExecContext (service/user.go:88)
  3. [path] db.ExecContext (service/user.go:88)
```
This lets an agent inspect whether the indexed graph contains a candidate path
from an HTTP handler to a SQL call without first reading every intermediate
file. It does not prove that the path executes at runtime.

### 9. Graph freshness check
`gograph stale` compares the selected-file inventory, effective Go build context, and SHA-256 source-content digests with the persisted graph. Use `gograph stale --tags=integration` for a graph built with that explicit selection; without the option it resolves inherited `GOFLAGS`. It detects byte edits even when mtimes are preserved, plus added, deleted, newly inactive, and newly active source files. The newest mtime remains diagnostic only. It displays the graph age, changed files, and whether the build context changed, then tells the agent to re-run `gograph build .` when needed. It uses the trusted directory from which `graph.json` was loaded—not its serialized `root` metadata—so results are identical from repository subdirectories. Agents should run this before any structural analysis.

On rebuild, gograph reparses every selected file in a changed package and
reuses parser-owned records for unchanged packages. `gograph stats` exposes
the resulting `reused_files` and `rebuilt_packages` counts. Precise mode keeps
this AST reuse but recomputes repository-wide type/CHA/SSA enrichment for
cross-package correctness.

The command uses the same tri-state exit contract in text and JSON modes: `0`
means the graph is current, `2` means it is stale, and `1` means an operational
or JSON serialization error. Exit `2` is an expected freshness result, not a failed check.
Scripts—especially those using `set -e`—must branch explicitly:

```sh
set -e
if gograph stale; then
  echo "Graph is current."
else
  status=$?
  case "$status" in
    2) gograph build . ;;
    *) exit "$status" ;;
  esac
fi
```

Avoid `gograph stale || gograph build .`: that also rebuilds after exit `1` and
can mask a real freshness-check failure.

### 10. Reading Internal Implementations (Mock Stubs, Algorithms)
When you need to read an indexed method body (for example, to check whether a
mock repository has a `panic("not implemented")` stub), or inspect an indexed
interface declaration, start with `gograph source` rather than locating it with
a broad text search.

Simply run:
`gograph source NotificationSender`
It extracts the matching declaration's source block. If names are ambiguous,
use receiver notation or a fully-qualified ID; if the declaration is absent or
analysis fell back, verify with `gopls` or a targeted source search.

### 11. Reachability-based dead code
`gograph orphans` performs a BFS from runtime roots (`main`, `init`), test/benchmark/fuzz roots, HTTP handlers, and exported symbols that can serve as external roots (excluding exports confined under `internal/`). A function called only by other dead code is also reported.

### 11. God-object detection
`gograph godobj` scans the graph for struct types that exceed configurable thresholds across three dimensions: method count, field count, and total outgoing calls from their methods. It produces a ranked, severity-labeled list so an agent can quickly identify candidates for refactoring.

Thresholds are all overridable:
```
gograph godobj --methods 10 --fields 12 --calls 30 --top 5
```
Example output:
```
God Object Candidates (methods>5, fields>8, calls>15):

[HIGH    ] AuthService — 18 methods, 6 fields, 42 outgoing calls  (internal/auth/service.go:12)
[MEDIUM  ] Server — 11 methods, 14 fields, 28 outgoing calls  (internal/server/server.go:8)
[LOW     ] Config — 7 methods, 22 fields, 9 outgoing calls  (internal/config/config.go:3)
```
Results are best-effort — data structs with many fields but no methods are expected in well-structured Go code and can be tuned out by raising `--fields`.

### 12. Cyclomatic complexity
`gograph complexity` estimates the cyclomatic complexity of every function in the graph, sorted highest-first. Each branch-inducing construct (`if`, `for`, `range`, `switch case`, `select case`, `&&`, `||`) increments the score by 1, starting at 1.

Labels follow McCabe thresholds:
| Score | Label |
|-------|-------|
| 1–5   | LOW |
| 6–10  | MEDIUM |
| 11–20 | HIGH |
| > 20  | VERY HIGH |

Filter to a specific function: `gograph complexity "ValidateToken"`

Example output:
```
Cyclomatic Complexity (sorted highest first):

[VERY HIGH] score=23   Run  (internal/cli/cli.go:36)
[MEDIUM   ] score=10   runGodObj  (internal/cli/cli.go:783)
[LOW      ] score=3    loadGraph  (internal/cli/cli.go:220)
```
An agent can use this to identify risky functions before a refactor and prioritize test coverage.

### 13. Package coupling
`gograph coupling` computes three metrics for every package:
- **Fan-out** — how many distinct packages this package imports (measures dependency breadth)
- **Fan-in** — how many distinct packages import this package (blast radius of changes)
- **Instability** — `FanOut / (FanIn + FanOut)`, range [0.0–1.0]
  - `0.0` = maximally stable (nothing it depends on changes)
  - `1.0` = maximally unstable (depends on many things, nothing depends on it)

Filter to a specific package: `gograph coupling "internal/auth"`

Example output:
```
Package Coupling (sorted by instability, highest first):

Package                                                  FanOut   FanIn  Instability
----------------------------------------------------------------------------------
cli                                                          14       0  1.00
search                                                        9       0  1.00
graph                                                         3       8  0.27
```

### 15. Symbol context bundle (primary token saver)
`gograph context <symbol>` is a composed command that bundles the following
indexed evidence into one response:
- **Node** — kind, file, line, signature, doc string
- **Source** — the raw function body extracted from the source file
- **Callers** — functions with retained call edges to this symbol
- **Callees** — retained call edges from this symbol
- **Tests** — test functions that exercise this symbol

Without this command, an agent needs 4–5 separate tool calls to gather the same information.

Example:
```
gograph context "ValidateToken"
```
```
=== CONTEXT: ValidateToken ===

--- NODE ---
[function] ValidateToken — func ValidateToken(token string) (bool, error)  (internal/auth/validator.go:42)

--- SOURCE ---
// internal/auth/validator.go::ValidateToken (internal/auth/validator.go:42-67)
func ValidateToken(token string) (bool, error) { ... }

--- CALLERS (3) ---
[caller] HandleLogin — calls ValidateToken  (internal/api/handler.go:88)
...

--- CALLEES (5) ---
[callee] jwt.Parse — called by ValidateToken  (internal/auth/validator.go:45)
...

--- TESTS (2) ---
[test] TestValidateToken  (internal/auth/validator_test.go:12)
```

### 16. Change plan generation (Safe Edits)
While `context` is used to *understand* code, `gograph plan <symbol>` is used to *safely edit* code. It aggregates multiple primitives (`impact`, `tests`, `routes`, `sql`, `envs`) into a single actionable checklist. 

Instead of an agent making 5 separate tool calls to check if a function touches SQL or breaks an HTTP route, `gograph plan` gives you everything in one shot:
```
gograph plan "ValidateToken"
```
```
Change plan for ValidateToken

1. Read first:
   - internal/auth/validator.go:42 ValidateToken
   - internal/auth/service.go:88 AuthService.Login
   - internal/api/login.go:53 HandleLogin

2. Update likely affected tests:
   - internal/auth/validator_test.go
   - internal/api/login_test.go

3. Risk:
   - Public API: yes
   - Called by HTTP route: POST /login
   - Reads env: JWT_SECRET
   - Touches SQL: no
```
Agents should **always** run `gograph plan` before editing a symbol to avoid breaking downstream callers or missing test updates. It can also be run for all uncommitted changes using `gograph plan --uncommitted`.

### 17. Change review (Post-Edit Verification)
`gograph review <symbol>` (or `gograph review --uncommitted`) acts as the final gate *after* you have made code changes, but *before* you commit them.

It aggregates the current AST state of the modified files and generates a completion report, answering critical safety questions:
- What exactly changed?
- Which of the modified symbols lack mapped tests? (Highlights coverage gaps)
- Did complexity increase? (Flags functions that exceeded the McCabe threshold)
- Did the public API or HTTP route surface change?
- What are the downstream execution risks? (Did you accidentally introduce an `os.Getenv` or a SQL query into a tight loop?)

Example:
```
gograph review --uncommitted
```
```
Code Review for Uncommitted Changes

Analyzed 2 modified symbols.

1. What changed?
   - internal/auth/validator.go:42 ValidateToken (function)
   - internal/auth/service.go:88 AuthService.Login (method)

2. Which changed symbols lack mapped tests?
   - AuthService.Login

3. Complexity & Architectural Risk (Current State)
   - [HIGH COMPLEXITY] ValidateToken: score=12

4. Did public API or route surface change?
   - [PUBLIC API] ValidateToken
   - [PUBLIC API] Login
   - [HTTP ROUTE] POST /login -> Login

5. Downstream Execution Risks (What do these changes touch?)
   - Reads Environment Variables: JWT_SECRET
   - Touches SQL: false
   - Emits Custom Errors/Panics: true
   - Uses Concurrency Primitives: false
```
If you are an agent making autonomous edits, run `gograph build . --precise`
followed by `gograph review --uncommitted` as a final static review step. Then
run the repository's tests and required checks; gograph review alone cannot
prove that no regression was introduced.

### 18. Change Risk Evaluation (gograph risk)
`gograph risk <symbol>` (or `gograph risk --uncommitted`) evaluates the risk profile of a set of changes or a target symbol. It calculates a normalized 0–100 risk score and provides a verdict: `SAFE` (0–30), `REVIEW` (31–70), or `DANGER` (>70).

It combines multiple structural metrics into a single weighted score:
- **Blast Radius** (Max 30 points): $3 \times \text{transitive callers}$ (BFS).
- **Cyclomatic Complexity** (Max 25 points): $2 \times (\text{complexity} - 1)$ of the function.
- **Test Coverage** (Max 20 points): 0 if $\ge 2$ tests, 10 if $1$ test, 20 if $0$ tests.
- **Public API** (Max 10 points): 10 if exported, 0 if private.
- **SQL downstream** (Max 10 points): 10 if SQL is used downstream.
- **Environment variables downstream** (Max 5 points): 5 if env vars are read downstream.

Example:
```bash
gograph risk RunAudit
```
```
Risk Report for RunAudit
Verdict: SAFE (Score: 23/100)

Metrics:
  - Blast Radius: 2 callers (6/30)
  - Complexity: 6 (10/25)
  - Tests: 2 (0/20)
  - Exported API: Yes (10/10)
  - Touches SQL: No (0/10)
  - Touches Env: No (0/5)
```

### 19. Error Flow Tracing
`gograph errorflow <error-string|ErrSymbol>` is a powerful backend diagnostic command that maps the lifecycle of an error up to the HTTP layer.

`gograph trace` is an alias for `errorflow` kept for compatibility — always prefer `errorflow` directly. `errorflow` searches for:
1. **Definition sites**: Where the sentinel error is declared (`var ErrInvalidToken = errors.New(...)`).
2. **Return/wrap sites**: Where the error string is created or wrapped (`fmt.Errorf("... %w", ErrInvalidToken)`).
3. **Upward Paths**: It traverses the AST call graph upwards until it hits an entrypoint (like an HTTP route or `main`).

**⚠️ Important Disclaimer:** `gograph errorflow` uses a pure **AST (Abstract Syntax Tree) call-graph heuristic**. It does **NOT** use SSA (Static Single Assignment) or data-flow/taint tracking. This means it is highly useful for navigating likely error paths, but it cannot mathematically prove that an error flows to a specific route if it is swallowed by complex middleware or interface indirection. The command assigns a `HIGH`, `MEDIUM`, or `LOW` confidence rating to each path based on its findings.

Example:
```bash
gograph errorflow ErrInvalidToken
```

### Security flow analysis

`gograph flow [term]` follows potential untrusted data across assignments, return values, and repository function calls. It recognizes typed HTTP request/framework contexts, decoded JSON/framework binding targets, and environment/config reads as sources. It reports paths into SQL query text, `os/exec` arguments, filesystem paths, and outbound HTTP targets.

```bash
gograph flow --no-tests
gograph flow --source decoded_json --sink sql_query --no-tests
gograph flow "CreateUser" --json
```

Test files are included by default; use `--no-tests` for a production-only review. Text output shows severity, confidence, and path steps. `--json` returns structured findings, and `--files-only` returns the deduplicated source and sink files.

Sanitizer policy is read from `.gograph/flow.json` at query time, so policy edits do not require `gograph build`:

```json
{
  "sanitizers": [
    { "function": "security.CleanPath", "for": ["filesystem"] },
    { "function": "security.ValidateURL", "for": ["outbound_http"] }
  ]
}
```

Use `--config <path>` for another JSON file inside the graph root. Omitting `for` applies a sanitizer to all sink kinds. `function` accepts the call spelling or a fully-qualified symbol ID; use the fully-qualified form when names collide. Sanitizers describe trusted return values; a function that returns only `bool` or `error` does not sanitize the unchanged input.

This is interprocedural, path-insensitive static analysis with call/return matching across up to 16 nested repository calls. Default graphs resolve direct local/imported functions; run `gograph build . --precise` for stronger method/interface targets. It does not model reflection, globals, arbitrary heap aliases, or every dynamic call. Unresolved external transformations lower confidence. Treat every result as a source-review lead, not proof of exploitability.

### 20. Hotspot ranking
`gograph hotspot [--top N]` ranks all functions by how many call sites depend on them (fan-in). The top hotspots are the most load-bearing code in the codebase — the functions an agent must understand before making any structural change.

```
gograph hotspot --top 5
```
```
Hotspot Functions (top 5, sorted by incoming calls):

  1.  42     calls  loadGraph  (internal/cli/cli.go:220)
  2.  38     calls  sortResults  (internal/search/search.go:198)
  3.  28     calls  formatResults  (internal/mcp/server.go:322)
```
An agent onboarding to a new repo should always run `hotspot` before reading any files, to know where to focus.

### 21. HTTP Endpoint Vertical Slice
`gograph endpoint <route>` answers the question every developer asks when entering a new codebase or reviewing a PR: **"what actually happens when this endpoint is called?"**

It composes in one command:
1. **Route resolution** — finds the `HTTPRoute` whose method+path matches the query
2. **Handler symbol** — locates the handler function in the symbol graph
3. **Full callee chain** — BFS downstream through call edges (default depth: 5 hops)
4. **SQL emitted** — all SQL queries touched by any symbol in the chain
5. **Env reads** — all environment variables read within the chain

**Input formats accepted:**
```bash
gograph endpoint "POST /api/users"   # exact method + path
gograph endpoint "/users"            # path fragment (matches all methods)
gograph endpoint "CreateUser"        # handler symbol name directly (RECOMMENDED — see below)
```

**Example output:**
```
ROUTE    POST /api/users
HANDLER  CreateUser  (internal/api/users.go:42)

CALL CHAIN
  1  CreateUser          → ValidateUserInput, hashPassword, userRepo.Save
  2  ValidateUserInput   → validateEmail, validatePassword
  3  userRepo.Save       → db.ExecContext

SQL
  [internal/repo/user.go:87] INSERT INTO users (email, password_hash) VALUES ($1, $2)

ENV READS
  DATABASE_URL

LIMITATIONS
  ⚠  Call chain uses heuristic AST call-graph, not SSA data-flow.
  ⚠  Calls through interfaces or dynamic dispatch may not appear.
```

**Flags:**
- `--depth N` — BFS depth for call chain (default: 5)
- `--json` — machine-readable JSON output

---

#### Grouped Routes (Gin, Echo, Chi, Fiber)

gograph composes constant route prefixes across Gin, Echo, and Fiber
`.Group("...")` assignments and chains, plus nested Chi
`.Route("...", func(r Router) { ... })` closures. Chi's title-case verb methods
such as `Get` and `Delete` are normalized to HTTP verbs.

**Flat and grouped routing:**
```go
v1 := router.Group("/api/v1")
users := v1.Group("/users")
users.POST("/", CreateUser)             // recorded: POST /api/v1/users/
users.GET("/:id", GetUser)              // recorded: GET /api/v1/users/:id

router.Route("/admin", func(r Router) {
    r.Get("/audit", Audit)               // recorded: GET /admin/audit
})
```

The prefix expression itself must be a string literal. Dynamically computed
prefixes such as `router.Group(prefixFromConfig)` cannot be reconstructed
without executing application code; those routes retain their known literal
suffix. Query a constant final path when available, or use the handler symbol
for dynamic cases:

```bash
gograph routes
gograph endpoint "POST /api/v1/users/"
gograph endpoint "CreateUser"
```

---

#### 📦 Inline (Anonymous) Handler Source

When a route is registered with an anonymous closure:

```go
router.POST("/users/bulk", func(c *gin.Context) {
    // logic here
})
```

gograph records this as `<inline handler at line N>` and **captures the full function source** at build time using `go/printer`. The source is stored in `graph.json` as `inline_body` on the route entry — no file I/O is needed at query time.

The `endpoint` command displays it directly:

```
ROUTE    POST /users/bulk
HANDLER  <inline handler at line 578>  (internal/api/router.go:578)

HANDLER SOURCE (inline closure)

  func(c *gin.Context) {
      ids := c.QueryArray("id")
      // ...
  }

LIMITATIONS
  ⚠  Handler is an inline closure — no symbol name in the graph. ...
  ⚠  Call chain uses heuristic AST call-graph, not SSA data-flow.
```

**Important:** `inline_body` is captured during `gograph build`. If you see `Source not available`, run `gograph build .` to rebuild the graph with this feature.

The call chain is **not traceable** for inline handlers because they have no symbol name in the graph. The source display is the substitute.


### 17. Dependency trees

`gograph deps <package>` shows the direct import dependencies of a package. Adding `--transitive` expands this to the full import closure via BFS.

```
gograph deps "internal/cli"
gograph deps "internal/cli" --transitive
```
Output:
```
Package: cli

Direct imports (14):
  encoding/json
  github.com/ozgurcd/gograph/internal/graph
  ...

Transitive imports (24):
  ...
```
This reports packages connected through the indexed import graph so the agent
can prioritize review without following each import manually.

### 18. Change detection
`gograph changes` compares every selected source file's content digest against the persisted graph and reports:
- **MODIFIED** — symbols in files whose bytes differ from the persisted graph
- **NEW** — top-level declarations in changed files not recorded in the graph
- **DELETED** — symbols whose recorded files are absent from the current safely
  selected inventory: gone, ignored, build-inactive, or unsafe to read

`gograph changes --git <ref>` extends this to git-ref-based scoping:
- Runs `git diff --name-only <ref>` to get the list of changed files
- Returns **MODIFIED** symbols from those files (NEW/DELETED require a full baseline build)
- Accepts any valid git ref: branch name, tag, or commit SHA (e.g. `--git main`, `--git HEAD~5`, `--git v1.4.50`)
- Ref is validated against a positive allowlist to prevent injection

This allows an agent in an iterative session to see exactly what changed without re-reading files or re-running `gograph build`.

```
gograph changes
```
```
Changes since persisted graph (2026-05-09 14:00:00 UTC):

Modified files (2):
  internal/auth/validator.go
  internal/api/handler.go

Affected symbols: 3 modified, 1 new, 0 deleted

[NEW     ] RefreshToken  (internal/auth/validator.go:71)
[MODIFIED] ValidateToken  (internal/auth/validator.go:42)
[MODIFIED] HandleLogin  (internal/api/handler.go:88)
```

### 20. Architecture Boundary Enforcement
You can configure `gograph` to actively enforce clean architecture by defining boundaries in `.gograph/boundaries.json`:
```json
{
  "layers": [
    {
      "name": "domain",
      "packages": ["internal/domain/**"],
      "may_import": []
    },
    {
      "name": "handler",
      "packages": ["internal/handler/**"],
      "may_import": ["internal/service/**", "internal/domain/**"]
    }
  ]
}
```
Run the enforcement check:
```bash
gograph boundaries
```
*If a violation is found (e.g., `handler` imports `internal/repository` directly), it will exit with code 1 and print the exact file that violated the rule. Extremely useful for CI/CD or Agent workflows!*

### 21. API / Contract Drift
`gograph api --since <ref|graph.json>` compares the public-facing contract and
integration surface of the Go codebase against either a local Git reference or
a saved graph path ending in `.json`. A saved graph must be a regular file
inside the selected project, have no linked path component, and carry the exact
current source-policy marker. Its serialized root is replaced with the selected
project root before use.

It identifies structural changes that may break callers, clients, mocks, tests, or coding agents, focusing on:
1. Exported Go API drift (signature changes)
2. Interface drift
3. Struct / JSON contract drift
4. HTTP route surface drift

Example:
```bash
gograph api --since main
```
*Note: Contract drift is based on static AST and graph comparison. It identifies likely breaking surface changes, but it does not prove runtime compatibility.*
*Tip: Run `gograph build . --precise` before `gograph api --since main` for best results.*

### 22. Native Execution via MCP
Agents that support the Model Context Protocol (like Claude Desktop, Cursor, and Antigravity) can run `gograph` as a native MCP server:
```json
{
  "mcpServers": {
    "gograph": {
      "command": "gograph",
      "args": ["mcp", "/path/to/repo"]
    }
  }
}
```

MCPB-capable clients can obtain the same local stdio server from the preview
official Registry under `io.github.ozgurcd/gograph`. Registry installation
prompts for `/path/to/repo`; it is separate from Homebrew, `go install`, and
the Claude Code marketplace plugin. Six bundles cover macOS, Linux, and
Windows on amd64 and arm64, but current Registry metadata cannot select CPU
architecture portably. Choose the matching asset filename, or use the manual
configuration above. See [Official MCP Registry and MCPB
Distribution](mcp-registry.md).

`gograph` exposes its repository query, analysis, and workflow suite directly
as project MCP tools, with workspace status/query/path/impact on the separate
workspace server. The complete CLI-only lifecycle surface is `build`,
`validate`, `doctor`, `gate`, `snapshot`, plugin/hook installation,
project/workspace MCP startup, workspace build/member refresh, help, and
version. In particular, `doctor` inspects the invoking machine's executable and
`PATH`, not repository graph state.

MCP agents should call `gograph_capabilities` first when they need to discover available gograph tools and recommended workflows.

At startup, the MCP server loads a regular repository-confined persisted graph with the exact current source-policy marker, or creates an in-memory AST graph when the artifact is missing, unreadable, unsafe, or unsupported. Source-analysis tools compare source-content digests and the build/module fingerprint per call, adopt a newer persisted precise graph, and reparse changed packages in memory while reusing unchanged package AST records; rebuild failures and precise fallbacks are returned visibly. A precise or precise-fallback session still re-attempts repository-wide CHA/SSA after an edit instead of silently serving AST-only analysis. If the same sources already have a fresh successful precise artifact, a failed retry retains it rather than publishing a fallback. `gograph_stale`, default `gograph_changes`, and `gograph_stats` inspect trusted persisted `graph.json` when available, or the startup auto-build fallback when no usable artifact exists.

Refreshes are in-memory by default. Starting the server as
`gograph mcp [path] --persist-refresh` opts into writing or overwriting
`.gograph/graph.json` and the nine Markdown reports after each confirmed-fresh
refresh. The MCP path does not update `.gitignore`, so configure the ignore
entry separately. Publication retains one latest state, not a branch-aware
cache. If startup has to auto-build, it publishes that graph before serving and
fails startup if publication fails. A later tool-triggered failure returns an
error; the fresh in-memory graph remains pending and the server retries the
write on a later refresh-capable call without rebuilding it. Writers coordinate
through `.gograph/.artifacts.lock`. Fixed plugin and MCPB configurations omit
the flag. Reports are replaced first and `graph.json` is replaced last as the
publication marker. Same-directory replacement is atomic on Unix-like systems
but is not guaranteed atomic by Go on non-Unix platforms; the complete
ten-file bundle is not a single atomic filesystem transaction. The lock file
remains as separate operational state.

For constrained hosts, add `--memory-mode=low --max-memory=1GiB` to the MCP
startup command. The policy applies to startup analysis and every later
refresh, and `gograph_capabilities` reports its requested/effective byte
targets. The limit is a soft Go runtime memory target rather than a hard process/RSS cap.

Default `gograph_changes` compares source with the persisted graph. With
persistence enabled, a successful refresh advances that baseline, so the edits
just published normally disappear from the default changes result. Use
`git_ref` when the comparison should remain anchored to a Git revision.

MCP annotations describe each tool's functional contract: analysis tools are
read-only by default. If an audit session is active, non-session MCP calls also
append local observational command telemetry; this does not include query
results or MCP arguments. MCP tools do not expose or enforce the CLI's
`--intention` field, so those observational records have an empty intention.
In addition,
`gograph_session_create` and `gograph_session_end` mutate telemetry state;
`gograph_boundaries_create` writes configuration; `gograph_session_cleanup`
is destructive; and `gograph_wiki` writes or overwrites documentation. `gograph_doc`
is open-world because the local Go toolchain follows the user's module environment. Audit output is rendered to request-local buffers,
so parallel MCP traffic never replaces process-global stdout.
When `--persist-refresh` is enabled, refresh-capable analysis tools are marked
non-read-only because a request may publish artifacts.

### Registered MCP Tools

The current suite registers 67 MCP endpoints: 63 query, analysis, and workflow tools plus four session lifecycle tools. The live `gograph_capabilities` payload is tested against the server registry. The optional `mermaid=true` parameter on `callers`, `callees`, `impact`, `endpoint`, `dependents`, `deps`, `path`, and `coupling` returns the same Markdown-fenced Mermaid presentation as CLI `--mermaid`; absent or false, each tool retains its normal response format.
- **`gograph_capabilities`**: Discover available tools and workflows.
- **`gograph_stale`**: Check whether trusted persisted `.gograph/graph.json`, or the startup fallback when no usable artifact exists, is outdated relative to selected Go source content digests or the effective build context. The newest mtime fields are diagnostic only. Returns JSON with `is_stale`, `graph_age`, `newest_source_mtime`, `newest_source_file`, `changed_files[]`, and `build_context_changed`.
- **`gograph_session_create`**: Start a telemetry audit session using a strictly validated ID and regular, repository-confined state under `.gograph/sessions/`; linked storage is refused.
- **`gograph_session_end`**: End the active session using the same rooted regular-file boundary and remove its active pointer.
- **`gograph_session_audit`**: Review and grade agent compliance and tool success rates. IDs accept only letters, digits, and underscore; logs must be regular repository-confined files.
- **`gograph_session_cleanup`**: Delete stale inactive regular session logs without following linked directories or files; the active log is preserved.
- **`gograph_query`**: Accepts `term` or a `terms` array; multiple terms use the CLI's OR semantics.
- **`gograph_focus`**
- **`gograph_callers`**: Supports `depth` (1-10), `no_tests`, exact matching, and optional Mermaid presentation, equivalent to the CLI traversal options. In a precise graph, `Interface.Method` resolves through every recorded implementer while returning a shared source call site once.
- **`gograph_callees`**: Supports `depth` (1-10), `no_tests`, and optional Mermaid presentation, equivalent to the CLI traversal options.
- **`gograph_implementers`**
- **`gograph_fields`**
- **`gograph_source`**: Repository-confined source for a named function, method, struct, interface, type, variable, or constant. It errors only when the symbol is absent or no matching block can be read safely; an ambiguous query may return its safe matches.
- **`gograph_orphans`**
- **`gograph_impact`**: Blast radius analysis. Supports three modes: single symbol, `uncommitted=true` for uncommitted changes, and `since=<ref>` for all changes since a git ref. `mermaid=true` applies to all three modes.
- **`gograph_boundaries`**: Verifies package architecture constraints from a regular, non-linked file inside the selected project. Returns structured output.
- **`gograph_boundaries_create`**: Creates a regular repository-rooted baseline boundary config, rejecting linked ancestors/finals and overwrite; equivalent to CLI `boundaries --create`.
- **`gograph_api`**: Compares public-facing contract and integration surface
  drift against a Git reference or a saved graph path ending in `.json`. Saved
  graphs must be regular non-linked files inside the selected project, require
  the exact current source-policy marker, and cannot supply the trusted root.
- **`gograph_routes`**: Extract all HTTP REST API routes found in the codebase. Constant nested Gin/Echo/Fiber Group prefixes and Chi Route closure prefixes are composed into final paths; dynamic prefix expressions remain best-effort suffixes. Routes using unresolvable factory handlers (e.g. `promhttp.Handler()`) are annotated with `[dynamic handler]`, setting `DynamicHandler: true`.
- **`gograph_node`**: AST metadata for a symbol: kind, file, line, signature, doc comment. Lighter than `gograph_source` when you only need metadata.
- **`gograph_path`**: Shortest BFS call chain between two symbols. Use to confirm whether a handler actually reaches a given function; accepts `mermaid=true` for visual output.
- **`gograph_changes`**: Symbols modified/added/deleted since the trusted persisted graph, or the startup fallback when no usable artifact exists. Deleted includes files absent from the current safely selected inventory. With `git_ref`, returns symbols in files changed since that ref (MODIFIED only).
- **`gograph_context`**: Bundles node details, callers, callees, tests, source code, and top-level architectural role into one compact structured response shared with CLI JSON. `node` retains the first match for compatibility, `nodes[]` preserves all ambiguous matches, test names and `test_results[]` preserve test evidence, and `source_error` reports a non-fatal source read failure. Supports exact matching and uncommitted mode.
- **`gograph_plan`**: Pre-edit planning. Highlights likely affected tests, routes, env reads, SQL touches, and public API impact. Set `with_context=true` to bundle full context for every `inspect_first` symbol — eliminates follow-up `context` calls.
- **`gograph_review`**: Post-edit review. Summarizes what changed and its risk profile in a structured JSON payload.
- **`gograph_risk`**: Risk evaluation. Combines blast radius, complexity, test coverage, and SQL/env dependencies into a 0–100 risk score and verdict (SAFE/REVIEW/DANGER). Supports `symbol` or `uncommitted=true`.
- **`gograph_errorflow`**: Traces likely error paths up to entry points (HTTP routes or CLI commands). CLI JSON, this tool, and `gograph_trace` share definition sites, return sites, paths, test names, and structured test rows. (*Limitation: Uses heuristic static call-graph and AST reference analysis, not SSA data-flow tracking.*)
- **`gograph_flow`**: Potential source-to-sink security paths with severity, confidence, and path steps. Optional parameters: `term`, `source`, `sink`, `config`, and `no_tests`. Source kinds are `http_request`, `decoded_json`, and `environment`; sink kinds are `sql_query`, `process_execution`, `filesystem`, and `outbound_http`. Uses `.gograph/flow.json` by default when present.
- **`gograph_imports`**
- **`gograph_envs`**: All `os.Getenv`/`os.LookupEnv` and supported Viper `Get*` reads in the codebase. Filter by key name substring.
- **`gograph_endpoint`**: Full vertical slice for one HTTP route: handler, BFS call chain (default depth 5; clamped to 1-20), SQL, and env reads. Query by route pattern, path fragment, or handler name. `include_tests` includes routes registered in `_test.go` files; `mermaid` selects flowchart output.
- **`gograph_interfaces`**: Interfaces satisfied by a named struct — inverse of `gograph_implementers`. Use before refactoring a method to know which contracts break.
- **`gograph_mutate`**: Struct-field and package-global mutation sites. AST mode reports direct assignments; precise mode adds `++`/`+=`, pointer aliases, atomic/sync/wrapper calls, and channel evidence. Use before adding field validation or changing shared state.
- **`gograph_tests`**: Direct attributed test calls by default. Set `transitive=true` with `symbol` to receive `gograph.tests.v1`, listing every reaching test with exact/possible resolution, depth, and a representative stable-ID path. Optional `exact_only` filters uncertain paths and `package` disambiguates the selected product symbol. CLI uses the equivalent `--transitive`, `--exact-only`, and `--package` flags.
- **`gograph_coverage`**: Transitive product symbols statically reachable from one unambiguous test, with stable-ID paths and exact/possible propagation. Parameters: required `test`; optional `exact_only` and exact `package` disambiguator. Equivalent to CLI `coverage`.
- **`gograph_identity`**: Resolve an exact symbol spelling or stable ID. Returns exact, ambiguous, or not_found without silently choosing a candidate; optional `package` disambiguates an external-test collision. Equivalent to CLI `identity`.
- **`gograph_sql`**: SQL literals mapped to their executing functions. Optional `term` filters by keyword or table-name substring.
- **`gograph_errors`**: Error constructors, sentinels, and panic sites; supports a term filter and `no_tests`.
- **`gograph_embeds`**
- **`gograph_public`**
- **`gograph_constructors`**
- **`gograph_literals`**: Find all composite-literal initialization sites for a named struct. Run before adding a required field — every site returned will break at compile time.
- **`gograph_usages`**: Find every place a named type appears in function signatures (param/return), struct fields, and interface method signatures. Run before changing an interface to see the full consumption blast radius.
- **`gograph_returnusage`**: Show how each caller uses the return value of a function (discarded/assigned/partially_ignored/returned/passed). Run before changing a return signature to find callers that silently discard values.
- **`gograph_arity`**: Find functions with too many arguments. Optional `min` parameter (default: 5).
- **`gograph_complexity`**: Cyclomatic complexity per function, sorted highest first. Labels: LOW/MEDIUM/HIGH/VERY HIGH (McCabe thresholds: 5/10/20); source that cannot be read or parsed safely is retained as UNKNOWN with score -1. Filter by symbol name substring.
- **`gograph_coupling`**: Fan-in, fan-out, and instability per package. Instability = FanOut/(FanIn+FanOut). Range [0,1]. Supports package filtering, `include_stdlib`, `internal_only`, and `mermaid`.
- **`gograph_deps`**: Import dependency tree of a package. Set `transitive=true` for full BFS closure or `mermaid=true` for a diagram.
- **`gograph_dependents`**: All packages that import the named package (inverse of `gograph_deps`). Essential before any package-level refactor; accepts `mermaid=true`.
- **`gograph_hotspot`**: Functions ranked by fan-in (incoming call count). High fan-in = highest-risk change target. Optional `top` parameter (default: 10); `include_tests` adds test-file call edges.
- **`gograph_concurrency`**: Map goroutine spawns, channel sends, and calls on mutex/RWMutex, WaitGroup, and sync.Once. Channel receives and `select` statements are not indexed. Optional filter by kind.
- **`gograph_fixtures`**: Find test helper structs and functions in test files for a package.
- **`gograph_godobj`**: Find god-object struct candidates. Optional thresholds: `methods`, `fields`, `calls`, `top`.
- **`gograph_skeleton`**: Full repository API signatures with bodies stripped. Large output — use on small repos or targeted packages.
- **`gograph_schema`**
- **`gograph_globals`**
- **`gograph_mocks`**
- **`gograph_explain`**: LLM-ready architectural summary. Synthesizes callers (prod vs test), callees, complexity, SQL, env, routes, concurrency, test coverage, interface satisfaction, and an opinionated role classification into one structured narrative.
- **`gograph_stats`**: Compact trusted persisted-index health summary, or startup-fallback health when no usable artifact exists. Returns schema version, build timestamp, complete/partial build status, production analysis precision (`ast`, `precise`, or `precise_fallback`), test-call resolution (`ast_heuristic`, `typed_complete`, or `typed_partial`), parsed/scanned file counts, parse-failure count, and graph entity counts including persisted flow functions. Use this as a quick sanity check at the start of any analysis session.
- **`gograph_trace`**: Alias for `gograph_errorflow`. Kept for backward compatibility — prefer `gograph_errorflow` directly.
- **`gograph_diagram`**: Mermaid architecture diagram of the repository package dependency graph. Parameters: `group_by` (package/module/service/file), `max_depth` (0=unlimited), `include_stdlib` (bool). Use for onboarding or communicating package structure.
- **`gograph_check`**: Run static policy checks (`boundaries`, `api_drift`, `require_tests_for_changed_routes`, `require_tests_for_changed_exported_symbols`, `test_coverage`, `no_orphans`, `new_globals`, `max_arity`, and `max_complexity`). Parameters: `since` (Git ref or saved graph path ending in `.json`), `uncommitted` (bool), `config` (path to checks.json). Default/relative config is confined to a regular non-linked project file; an absolute config explicitly selects a regular local file. Saved graphs must be regular non-linked files inside the selected project, require the exact current source-policy marker, and cannot supply the trusted root. CLI and MCP share one validated baseline builder. Changed-route checks map by handler identity and include body-only changes. Returns structured JSON with status, findings, and summary counts. For CI enforcement with non-zero exit codes, use CLI `gograph gate`, which reads only a regular non-linked project-root `.gograph.yml`.
- **`gograph_wiki`**: Generate the `llm-wiki/` directory of machine-first markdown pages from the static graph. Pages: overview, architecture, hotspots, routes, env, errors, concurrency, api-surface, and one file per internal package. A relative `output` is anchored to the selected project and rejects linked ancestors; an absolute path is an explicit local destination whose final directory must be real. Generated descendants and writes remain rooted beneath the chosen output. Run once per session for zero-cost orientation. Returns a JSON manifest of written page filenames.
- **`gograph_summary`**: Single-call codebase briefing: top 3 hotspots, worst instability package, highest complexity function, orphan count, and god-object count. Replaces 5 separate tool calls.
- **`gograph_untested`**: Called production functions and methods without an exact transitive path from any test. Precise direct calls and proof-backed concrete interface receivers are exact; open CHA/parser paths propagate `possible` to descendants. Rows include `stable_id`, `test_resolution`, and `possible_test_count`. `typed_partial` stats mean the census remains an upper bound. Optional `pkg`, `top`, and repository-relative `exclude[]` filters; CLI also supports `--wide` for full stable IDs. Replaces N forward coverage or direct-test probes.
- **`gograph_doc`**: `go doc` wrapper — signature + doc comment for any stdlib or third-party package/symbol. The handler does not query the graph, though the project-scoped MCP server must already have started with a usable artifact or buildable Go source. Rejects filesystem-shaped queries and refuses source-tree links `cmd/go` may inspect across the selected root plus its effective module root, or the workspace root and member trees, special recognized Go build inputs, or linked/non-regular `go.mod`, `go.sum`, `go.work`, `go.work.sum`, and `vendor/modules.txt` metadata before invoking the toolchain; `.git` and `.gograph` are excluded from the source-tree walk. Applicable workspace members must remain beneath the workspace directory, with each directory, `go.mod`, and optional `go.sum` validated first; dependency resolution remains open-world. Returns a one-element JSON array with `query` and raw-text `output`. Use when a call chain leads outside the project. Examples: `fmt.Errorf`, `io.Reader`.
- **`gograph_httpcalls`**: All outbound HTTP client calls via `net/http` (Get, Post, PostForm, Head) in the codebase. Filter by method or URL substring.

The separate workspace server started by `gograph workspace mcp` exposes four
additional read-only tools, each backed by the same native implementation as
its CLI operation:

- **`gograph_workspace_status`** ↔ `gograph workspace status`
- **`gograph_workspace_query`** ↔ `gograph workspace query`
- **`gograph_workspace_path`** ↔ `gograph workspace path`
- **`gograph_workspace_impact`** ↔ `gograph workspace impact`

> **CLI/MCP parity:** Every repository query, analysis, and workflow command, including boundary baseline creation, has a project-MCP equivalent. The normal mapping is `<command>` → `gograph_<command>`; `contract`, `boundaries --create`, and session actions use the special mappings in the [complete transport matrix](../docs-site/content/docs/command-reference.md#cli--mcp-transport-matrix). The intentional CLI-only surface is `build`, `validate`, `doctor`, `gate`, `snapshot`, `add-claude-plugin`, `hook-guard`, project/workspace MCP startup, workspace build/member refresh, `version`, and `help`; these publish artifacts, inspect/configure the host installation, enforce process exit status, or start the host integration. Presentation remains transport-specific: CLI output flags map to MCP parameters or structured content rather than byte-identical output.

## Recommended project setup

1. **Build the binary once per machine:**
   ```sh
   make build && cp bin/gograph /usr/local/bin/gograph
   ```

2. **Generate the graph in the target repo:**
   ```sh
   cd /path/to/your-go-repo
   gograph build .
   ```
   This writes `.gograph/graph.json` plus nine Markdown reports and adds `.gograph/` to the Git repository root `.gitignore` non-destructively. If no Go files are found or no file parses successfully, the build exits without replacing artifacts. Partial failures are listed in graph build metadata. Artifacts are staged under a local writer lock, reports are renamed first, and `graph.json` is synced and renamed last as the commit marker; same-directory replacement is atomic on Unix-like systems but is not guaranteed atomic by Go on non-Unix platforms, and the full bundle is not one atomic transaction.

3. **Tell the agent to use it.** You don't need a huge instruction template anymore. Just add this to `CLAUDE.md`, `.cursorrules`, `.github/copilot-instructions.md`, or whatever file your agent reads:

   > Before answering architecture or repository questions, run `gograph capabilities` and follow the instructions it prints.

   The `gograph capabilities` command prints the current command surface and
   workflow guidance. Use gograph for supported structural questions, `gopls`
   for live compiler-backed operations, and targeted text search for literals,
   comments, generated/non-indexed files, and verification fallbacks.

4. **Optional — refresh on demand.** Have the agent run `gograph build .` after creating/renaming/removing symbols, or wire it into a `pre-commit` / `Makefile` target.

## Workflow Telemetry & Audit Sessions

`gograph` includes a high-fidelity workflow logging and session tracking engine. This system allows developers, teams, and CI/CD pipelines to audit agent behaviors, ensure compliance with agent workflow rules, and track command success/failure telemetry.

### 1. Activating a Session
A session is started using the `session create` subcommand. You can supply an optional custom word which will be incorporated into the session ID along with a timestamp:
```sh
gograph session create implement_refactor
# Output: Session "implement_refactor_20260530_200840" successfully created and activated.
```
If the custom word is omitted, `gograph` will generate a short, random, unique identifier:
```sh
gograph session create
# Output: Session "session_d3f45a_20260530_200840" successfully created and activated.
```
Only one session can be active at a time per workspace. The active session pointer is tracked in `.gograph/active_session.json`.

### 2. Mandatory Intention Enforcement
When a session is active, the AI agent is **required** to state its technical rationale using the `--intention` or `-i` flag for every analytical command:
```sh
gograph stale -i "Check if the graph is stale before analyzing the codebase"
```
If the agent fails to supply `-i` when a session is active, `gograph` blocks execution and returns a structured exit code `1` with an error message:
```
Error: Active session "implement_refactor_20260530_200840" requires an intention. Please supply the --intention (-i) flag stating your technical rationale.
```
*Note: Session commands, `mcp`, `build`, `doctor`, `stale`, `stats`, `capabilities`, `wiki`, `doc`, plugin/hook setup, help, and version are exempt from intention enforcement. Other analytical commands require an intention while a session is active.*

### 3. Ending a Session
Once the agent finishes its work, the session is cleanly ended:
```sh
gograph session end
# Output: Session "implement_refactor_20260530_200840" successfully ended.
```

### 4. Telemetry Log Architecture (Append-Only JSONL)
CLI and MCP analysis invocations during an active session are logged inside `.gograph/sessions/session_<session_id>.jsonl`. Session lifecycle/MCP startup calls and successful `hook-guard` checks are excluded.
To ensure architectural cleanliness and avoid heavy I/O operations or disk bloat, **raw query results are never logged**. Only telemetry metadata is captured:
- Command name and arguments
- Technical intention (`-i` / `--intention`)
- Latency (execution time in milliseconds)
- Exit status (`success` or `failure`)

Example log format:
```json
{"type":"session_start","session_id":"my_refactor_20260530_200840","created_at":"2026-05-30T20:08:40-04:00"}
{"type":"command","timestamp":"2026-05-30T20:08:48-04:00","command":"stale","args":[],"intention":"Check graph status","execution_ms":57,"status":"success"}
{"type":"session_end","ended_at":"2026-05-30T20:08:54-04:00","status":"completed"}
```

### 5. Auditing and Compliance Scoring
You can audit the agent's work at any time during or after a session using the `session audit` command:
```sh
gograph session audit [session_id]
```
If `session_id` is omitted, the tool automatically scans `.gograph/sessions/` and audits the **most recent** session.

The audit report provides:
1. **Agent Compliance Score & Grade**:
   - **Plan Rule compliance (35%)**: Did the agent run `plan <symbol>` before modifying code?
   - **Review Rule compliance (35%)**: Did the agent run `review --uncommitted` after making edits?
   - **Efficiency / Composability (30%)**: Did the agent use composed token-saving commands (like `context <symbol>`) instead of running dozens of verbose raw queries (like `node`, `callers`, `callees`)?
   - **Overall Grade**: Scores are mapped to academic grades (A: Highly Compliant, B: Good, C: Needs Improvement, F: Non-Compliant).
2. **Success Rate**: The percentage of successfully completed commands versus failed ones.
3. **Actionable Recommendations**: If the agent missed core rules, the report details exactly how to instruct or steer the agent to improve its efficiency.

#### ⚠️ Strict Agent Access Ban
To preserve the integrity of the audit pipeline and prevent the agent from parsing or altering logs:
> [!IMPORTANT]
> **AI Coding Agents are strictly forbidden from listing, reading, or parsing files inside the `.gograph/sessions/` directory.**

### 6. Session Cleanup
To prevent `.gograph/sessions/` from growing indefinitely and accumulating stale session metadata, you can trigger a cleanup at any time:
```sh
gograph session cleanup
```
*Note: If an active session is currently running, `session cleanup` safely skips deleting the active session's `.jsonl` log file to prevent telemetry corruption.*

---

## Why this is safe to give an agent

`gograph` is intentionally narrow, but its actual I/O boundary matters:

- **No target-code execution** — gograph never runs the repository's binaries, tests, or application entry points. Default indexing uses Go AST parsing but asks the installed Go toolchain for effective build/module context. Precise mode additionally type-loads repository packages, and `doc` invokes `go doc`; dependency/toolchain resolution follows the user's configured module cache and network policy and remains open-world.
- **Local stdio MCP transport** — the server opens no listening port and sends no data to a gograph service. Optional session telemetry is local metadata under `.gograph/sessions/`; raw query results are not logged.
- **Project metadata reads** — in addition to `.go` files, gograph reads regular `go.mod`, `go.sum`, `go.work`, `go.work.sum`, and `vendor/modules.txt` metadata, `.gitignore`, Git state, `.gograph/graph.json`, and user-selected gograph JSON/YAML configs, including `.gograph/flow.json`. It does not intentionally read `.env`, key, certificate, kubeconfig, or tfstate files.
- **Targeted source output** — `source` and `context` return requested Go source, and inline route-handler bodies are stored in `graph.json` so endpoint analysis can return them. Other graph data is structural metadata.
- **Generated files skipped** — `.pb.go`, `_generated.go`, files with `// Code generated` headers are excluded so they don't pollute the map.
- **Repository-confined source** — descendant symlinks and special files for extensions recognized by `go/build` are reported and excluded. AST and query-time source reads use a repository-rooted handle, reject symlink path components, and accept only regular Go files. Linked/non-regular `go.mod`, `go.sum`, `go.work`, `go.work.sum`, and `vendor/modules.txt` inputs are rejected before gograph or the Go toolchain reads them. Applicable `go.work use` members must stay beneath their workspace directory; each member directory, `go.mod`, and optional `go.sum` is preflighted. Persisted `graph.json` is read through the same descendant-link boundary; publication rejects a linked or non-directory `.gograph`. An explicitly symlinked repository root remains supported. Graphs with a missing or unsupported source-policy marker must be rebuilt, and their serialized root is never authority. Saved baseline graphs are likewise confined to regular non-linked files inside the selected project and require the exact marker. Default/relative check configs, flow configs, boundaries, and gate config are also rooted regular-file reads. Older binaries should not be used for untrusted repositories. Precise repository package loading and `go doc` run only after a preflight that rejects source-tree links and special recognized build inputs `cmd/go` may inspect across the selected root plus its effective module root, or the workspace root and member trees; `.git` and `.gograph` are excluded from that walk, and `doc` also rejects filesystem-shaped queries.
- **Repository-confined mutations** — session state, snapshots, boundary creation, gate initialization, relative wiki output, graph artifacts, locks, and the automatic `.gitignore` update use project-rooted checks and reject linked/special targets. Session and snapshot names are strictly validated. An absolute check config or wiki output is an explicit operator-selected local location; generated wiki descendants remain confined beneath its real output directory.
- **Inactive and ignored paths excluded** — Go build constraints, GOOS/GOARCH filenames, cgo state, cmd/go's hidden/underscore/`testdata` package-directory rules, Go 1.26 module ignore directives, generated sources, `.claude/`, `.cursor/`, `.agents/`, and Git-ignored paths are excluded consistently. The same scanner policy powers build, stale, and changes.
- **Subdirectory aware** — graph-backed query commands auto-discover the project root by walking up to the nearest `.gograph/` directory. Agents do not need to `cd` back to the repo root before running `plan` or `review`; `doc` instead uses its enclosing Go module/source-validation root.
- **Publication ordering** — output files are mode `0640`; writers require a real `.gograph` directory, stage under a local lock, replace reports first, then sync and rename `graph.json` last as the commit marker. Same-directory replacement is atomic on Unix-like systems but is not guaranteed atomic by Go on non-Unix platforms. The full ten-file bundle is not one atomic filesystem transaction, and `.artifacts.lock` remains as separate operational state. CLI `build` appends to the Git repository root `.gitignore` without overwriting it.
- **Optional MCP publication** — `--persist-refresh` uses the analyzed
  project's `.gograph/` directory but deliberately does not modify
  `.gitignore`. Writers coordinate through `.gograph/.artifacts.lock`. It is
  disabled in generated and bundled configurations.
- **Optional low-memory execution** — CLI builds, explicit workspace member
  refreshes, and project MCP refreshes accept `--memory-mode=low` with an
  optional `--max-memory=<size>`. This changes GC and phase scheduling only;
  it never silently removes precise analysis facts to satisfy the soft target.

The agent gains a local structural view without a hosted gograph dependency; normal filesystem and local toolchain permissions still apply.

## Measuring workflow cost

Do not assume a fixed token reduction. Repository size, selected command,
client envelopes, tokenizer, follow-up reads, cache state, and whether the
static result answers the task all affect cost.

For a defensible comparison, choose representative tasks and manually reviewed
expected answers. Record actual tool calls, model tokens, wall-clock time,
false positives, false negatives, fallback searches, and task success. The
controlled harness in `scripts/benchmark.go` verifies declared structural
evidence and records exact commands, complete raw output, process calls, payload
size, and repeated timing. It deliberately does not infer model-token cost or
end-to-end agent success; see `docs/benchmarking.md` for its fixture,
methodology, and limitations.

## Limitations the agent should know about

- **Go only.** No multi-language parsing.
- **Default call edges are best-effort AST evidence.** Without precise enrichment, overloaded names and method receivers may collide and interface dispatch can be unresolved. Treat `callers`/`callees` results as navigation evidence, not runtime proof. **Workaround:** when you need to disambiguate same-named methods/functions or query symbols cleanly:
  1. Use standard Go **package-qualified dot-notation** (e.g. `service.GenerateRequest`, `graph.Graph` or `graph.Graph.Build`) with call-graph commands that advertise symbol selectors. `callers` also accepts `Interface.Method` on a precise graph.
  2. For precise target matching with no same-name conflation, pass the fully-qualified symbol ID (e.g., `gograph callers 'github.com/foo/bar/internal/auth::(*Service).Validate'`). The same FQ-ID syntax works for `callees`, `impact`, and `path` (both endpoints). Requires `--precise` mode at build time.
- **Precise interface dispatch remains conservative unless the receiver is proven.** SSA devirtualizes a call only when its interface value is constructed from one concrete dynamic type; merely seeing one implementation in the repository is insufficient. Other invokes retain all named in-repository CHA candidates. Promoted concrete methods use a traversal-only wrapper edge. Reflection, `unsafe`, plugins, unresolved function values, test-only packages, unnamed concrete types, and module-external implementations can still cause omissions.
- **Test-call attribution has its own capability.** AST graphs use selector-name heuristics. Precise builds type-resolve compiling tests: direct selectors, local method values, and single-assignment non-escaping concrete interface locals are exact, while open interface candidates stay `possible`. Broken or omitted test packages produce `typed_partial` without weakening production precision. Reverse-transitive tests and `untested` propagate uncertainty through descendants; even exact static attribution is not runtime coverage proof.
- **Graph call counts are edge counts.** In a precise graph, one interface call expression contributes one edge per retained concrete target, and promoted-method forwarding can add explicitly marked synthetic traversal edges. Current caller/callee call-site output hides forwarding edges and deduplicates parallel targets for presentation. Content digests, analysis-cache markers, parser/precise provenance, and reuse counters are also additive v2 fields. A legacy graph without digests uses mtime freshness until rebuilt and is not eligible for parser-record reuse. Older v2 binaries can decode the additive fields but may count or display synthetic records as ordinary edges, so use the current binary with newly generated precise graphs.
- **Repository graphs do not resolve module-external edges by themselves.** External dependencies are extracted from `go.mod`; a configured workspace can resolve eligible facts between member repositories through its separate overlay, while third-party call targets remain unresolved.
- **Security flow is a conservative heuristic.** It is path-insensitive, uses field/root approximation, and matches call/return context for at most 16 nested repository calls. Default graphs have weaker method/interface resolution than precise graphs. Reflection, globals, arbitrary heap aliases, and unresolved dynamic calls can cause misses or false positives. External-call propagation is marked low confidence. A finding is not an exploitability claim.
- **CLI snapshot vs MCP refresh.** CLI graph-backed analysis reflects the last trusted published graph. MCP source-analysis tools compare source digests plus the build/module fingerprint and newer persisted graphs, then reparse changed packages in memory using the current requested mode. Precise mode still recomputes repository-wide CHA/SSA. A precise refresh that falls back fails the analysis request visibly; MCP `stale`, default `changes`, and `stats` inspect the trusted persisted snapshot, or the startup fallback when no usable artifact exists. `--persist-refresh` advances that snapshot after successful refreshes, so default `changes` uses the new state as its baseline. It stores only the latest artifact set and does not avoid a rebuild when switching back to a previously analyzed branch.

## TL;DR

`gograph` gives coding agents a persisted Go repository map and targeted
structural queries. It can reduce repeated exploration while keeping graph
artifacts local and avoiding target-program execution, but its results must be
interpreted according to the analysis mode and limitations above.
