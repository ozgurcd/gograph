# gograph for Coding Agents

How `gograph` helps coding agents (Claude Code, Cursor, Copilot, Gemini, Codeium, Antigravity, etc.) work effectively in Go repositories.

## The problem gograph solves

Coding agents typically explore a repo by reading raw files and grepping. This is fine for small projects but becomes expensive in larger Go codebases:

- **Context burn** — each `Read` of a 500-line file fills the context window with bodies the agent doesn't need just to learn that a function exists.
- **Slow first-orientation** — answering "where does X live?" or "what calls Y?" can take 5–15 tool calls of grep + read.
- **Missed structure** — agents read files in isolation and miss the package layout, import graph, and call relationships that a human would skim from a directory tree.
- **Stale mental model** — after edits, the agent's earlier reads no longer reflect reality.

`gograph` produces a static, AST-derived map of the repo so the agent can answer structural questions from one small file (`.gograph/GRAPH_REPORT.md`) instead of dozens of file reads.

## What it gives the agent

A single command (`gograph build .`) emits two artifacts under `.gograph/`:

| Artifact | Use |
|---|---|
| `GRAPH_REPORT.md` | Human + agent readable summary: external dependencies (Tech Stack), package list, entry points, top files by symbol/call density, top symbols by outgoing calls, env vars read, full import graph. |
| `graph.json` | Machine-readable full graph — dependencies, packages, files, structs, interfaces, funcs, methods, imports, call edges, env reads, SQL queries, errors, concurrency primitives, test edges. |

*Note: Use `gograph build . --precise` for type-checked interface analysis and more precise call edges (requires compilable code).*

And query commands the agent can invoke without re-parsing:

```sh
gograph query <term>            # symbol/package/file/import/call substring search (works great for finding specific test names!)
gograph focus <package>         # isolate context for a specific package
gograph callers <function> [--no-tests] [--depth N] # who calls it; --depth 2-10 expands N hops up the call graph
gograph callees <function> [--no-tests] [--depth N] # what it calls; --depth 2-10 expands N hops down
gograph implementers <interface> # which structs implement an interface
gograph interfaces <struct>     # which interfaces a struct satisfies (precise if --precise used)
gograph fields <struct>         # extract fields and types of a struct
gograph source <symbol>         # extract exact source code of a symbol (USE THIS instead of grep to read function bodies, mock stubs, or full interface definitions)
gograph impact <symbol>         # find downstream callers (blast radius)
gograph impact --uncommitted    # find blast radius of all uncommitted code changes
gograph impact --since main     # blast radius of all symbols changed since main (PR-level)
gograph orphans                 # functions unreachable from any entry point via BFS (main, routes, exports) — stricter than 0-call check
gograph routes                  # extract all HTTP REST API routes
gograph imports <pkg>           # trace external/internal package usage
gograph sql                     # map raw SQL queries to their execution functions
gograph errors                  # custom error variables and panics mapped to their source
gograph embeds <struct>         # find which structs embed a target struct
gograph public <pkg>            # list only the exported API surface of a package
gograph envs [term]             # list every environment variable read in the codebase
gograph concurrency [term]      # map goroutines, channels, mutexes, waitgroups, sync.Once
gograph tests [symbol]          # find which test functions exercise a named symbol
gograph path <from> <to>        # shortest call chain between two symbols (BFS traversal)
gograph stale                   # check if graph.json is out of date vs source files
gograph stats                   # compact index health summary: schema version, build timestamp, counts of packages/files/symbols/calls/routes/SQL/env/test edges
gograph godobj                  # find god-object struct candidates (default thresholds)
gograph godobj --methods 10 --fields 12 --calls 30 --top 5  # custom thresholds
gograph complexity              # cyclomatic complexity for all functions, highest first
gograph complexity "Run"        # complexity for a specific function by name
gograph coupling                # package fan-in, fan-out, and instability table
gograph coupling "internal/auth" # filter to a specific package
# --- PRIMARY TOKEN SAVERS ---
gograph context "ValidateToken"  # node + source + callers + callees + tests + role in ONE call (use for raw structured data)
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
gograph changes                  # new/modified/deleted symbols since last build
gograph changes --git <ref>      # symbols in files changed since a git ref (MODIFIED only; e.g. --git main, --git HEAD~5, --git v1.4.50)
gograph errorflow "parse failed" --no-tests  # trace error path to entry points, excluding test references
gograph trace "parse failed"     # alias for errorflow (kept for compatibility)
gograph diagram                  # Mermaid architecture diagram of package dependency graph [--group-by package|module|service|file] [--max-depth N] [--include-stdlib]
gograph check                    # run static policy checks (.gograph/checks.json): boundaries, api_drift, max_arity, max_complexity, test_coverage
gograph check --uncommitted      # include uncommitted code in check scope
gograph check --since main       # include api_drift baseline against a git ref
gograph mutate "User.Status"     # find functions that mutate a specific struct field (covers direct assignments, IncDec/augmented (++, +=), and indirect mutations through method calls: atomic.*/sync.Map/sync.Mutex stdlib mutators, user-defined wrapper methods detected by SSA, and channel sends. Indirect detection requires --precise build. Results carry `via=<method>` in Detail when indirect.)
gograph arity --min 5            # find functions with many arguments (long parameter list smell)
gograph skeleton                 # output the whole repository's API signatures (bodies stripped)
gograph constructors <struct>    # find factory functions returning a named struct
gograph literals <struct>        # all Foo{...} composite literal sites — run before adding/removing a required field
gograph usages <type>            # every place a type appears in param/return/field/iface-method — run before changing an interface
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
                                 # Run once per session for zero-cost codebase orientation.
                                 # Default output: ./llm-wiki/  (add llm-wiki/ to .gitignore)
gograph summary                  # single-call briefing: top 3 hotspots + worst instability + highest complexity +
                                 # orphan count + god-object count. Replaces 5 separate tool calls. [--json]
gograph untested                 # production functions with callers but zero test edges. One sweep replaces N
                                 # 'gograph tests <sym>' calls. Flags: [--pkg <name>] [--top N] [--json]
gograph doc <pkg[.Symbol]>       # go doc wrapper: signature + doc comment for any stdlib or third-party symbol.
                                 # No graph required. Use when following call chains outside the project.
                                 # Examples: gograph doc fmt.Errorf   gograph doc io.Reader
                                 #           gograph doc net/http.HandleFunc  [--json]
gograph mcp <path>               # runs an MCP server over stdio
gograph httpcalls [term]         # all outbound net/http calls (Get, Post, PostForm, Head); filter by method or URL substring
gograph add-claude-plugin        # install MCP server + CLAUDE.md rules + PreToolUse hook (Claude Desktop & Claude Code)
gograph hook-guard               # PreToolUse hook binary — reads tool call JSON from stdin, blocks Go symbol greps (invoked automatically by Claude Code)
# --- CI ENFORCEMENT ---
gograph gate                     # fail CI if any .gograph.yml threshold is violated (complexity, instability, orphans, coupling)
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
| **PreToolUse hook** | Intercepts `grep`/`rg` on Go symbols and redirects to gograph | `~/.claude/hooks/gograph-guard.sh` + `~/.claude/settings.json` |

### How the hook works

The hook (`gograph hook-guard`) is invoked automatically by Claude Code before every `Bash` tool call. It:

1. Reads the tool call JSON from stdin.
2. Checks if the command is `grep` or `rg`.
3. If targeting `.go` files and the search pattern looks like a Go identifier (PascalCase/camelCase, 3+ chars) → **blocks** with exit code `2` and tells Claude which `gograph` tool to use instead.
4. Otherwise → **allows** with exit code `0`.

**Allowed through (not blocked):**
- Searches explicitly targeting non-Go files (`*.yaml`, `*.md`, `*.sql`, etc.)
- Comment/doc searches (TODO, FIXME, HACK, etc.)
- Searches in `docs/`, `.github/`, `testdata/`, `migrations/`
- Patterns that don't look like Go identifiers (short strings, regex with special chars)

**Blocked and redirected:**
```bash
grep -r "ValidateToken" .        # → gograph_query "ValidateToken"
rg "UserService" --include=*.go  # → gograph_context "UserService"
grep -rn "runCheck" .            # → gograph_callers "runCheck"
```

## Concrete agent workflows

### Recommended agent workflow:
- Session start: read `llm-wiki/README.md` → `llm-wiki/project.md` → `llm-wiki/rules.md` → `llm-wiki/agent-contract.md` (if llm-wiki/ exists)
- If generated pages are missing: `gograph build . --precise && gograph wiki`
- Graph freshness: `gograph stats` + `gograph stale`
- Understand a symbol: `gograph context <symbol>` (raw data) or `gograph explain <symbol>` (narrative — use when you need to understand purpose and architecture)
- Before editing: `gograph plan <symbol>` (callers, tests, SQL/env/route risk)
- Before a package refactor: `gograph dependents <pkg>` (every consumer)
- After editing: `gograph review --uncommitted`
- Before done: `gograph check --uncommitted`
- If API-facing changes exist: `gograph check --since main` (or `master`)
*(Note: The baseline ref must exist locally.)*

### Config Example for .gograph/checks.json:
```json
{
  "checks": {
    "boundaries": "error",
    "api_drift": "warn",
    "require_tests_for_changed_routes": "error",
    "require_tests_for_changed_exported_symbols": "warn",
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
*(Note: The `"baseline"` property defines the Git branch (e.g., `"main"`, `"master"`, or a release tag like `"v1.0"`) that `gograph check` compares against when calculating `api_drift`. If your repository uses `main`, update this value accordingly!)*

### 1. Onboarding to an unfamiliar repo
Instead of `ls -R` + reading 10 random files, the agent reads `.gograph/GRAPH_REPORT.md` and immediately knows: packages, entry points, hottest files, hottest symbols, what imports what.

### 2. "Where is X implemented?"
`gograph query X` returns file:line locations for matching symbols, packages, files, imports, and call sites — typically one tool call vs. several `grep` rounds.

### 3. Impact analysis before a refactor
`gograph callers SomeFunc` lists every call site without the agent having to grep all `.go` files. It **returns the exact line of code** so the agent can immediately see the arguments passed to the function. Combined with `callees`, the agent can reason about blast radius before editing. Use the `--no-tests` flag (`gograph callers SomeFunc --no-tests`) to instantly filter out test callers when checking production usage.

### 4. Configuration / secrets surface
`gograph envs` lists every `os.Getenv` / `os.LookupEnv` / `viper.GetString` site with file, line, and enclosing function — one command vs. grepping every file. Filter by name: `gograph envs DATABASE`.

### 5. Interface satisfaction discovery
`gograph interfaces Worker` uses duck-typing to show which interfaces `Worker` satisfies without running the compiler. Essential when mocking a service layer for tests.

### 6. Concurrency audit
`gograph concurrency` shows every goroutine spawn, channel send, mutex lock, WaitGroup, and `sync.Once.Do` across the codebase. Filter: `gograph concurrency goroutine` or `gograph concurrency mutex`.

### 7. Test coverage lookup
`gograph tests ValidateToken` instantly shows which `Test*` functions exercise `ValidateToken` — no grepping test files needed.

### 8. Call chain pathfinding
`gograph path CreateUser sql` performs BFS over the call graph to find the shortest path between two symbols. Example output:
```
Call path: CreateUser → sql
  1. [path] CreateUser — calls UserService.Create (handlers/user.go:42)
  2. [path] UserService.Create — calls db.ExecContext (service/user.go:88)
  3. [path] db.ExecContext (service/user.go:88)
```
This lets an agent confirm whether an HTTP handler actually reaches a given SQL call without reading every file in between.

### 9. Graph freshness check
`gograph stale` compares `graph.json`'s `generated_at` timestamp against the `mtime` of every `.go` file. It displays the graph age, the newest source file, and its modification time. If any source file is newer, it lists the changed files and tells the agent to re-run `gograph build .`. Agents should run this before any structural analysis.

### 10. Reading Internal Implementations (Mock Stubs, Algorithms)
When you need to read the actual body of a method (e.g., to check if a mock repository has a `panic("not implemented")` stub), or when you need to see the **full list of method signatures in an interface**, **do not use `grep` to find the line number.** 

Simply run:
`gograph source NotificationSender`
It will instantly extract and print the entire source block for that specific method or interface, bypassing the need for manual file reads.

### 11. Reachability-based dead code
`gograph orphans` performs a BFS from all entry points (`main()`, exported functions, HTTP handlers) and flags any function or method never reached. This is stricter than a simple 0-incoming-calls check — a function called only by other dead code is also reported.

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
`gograph context <symbol>` is the single highest-impact token-saving command. It bundles the following into one response:
- **Node** — kind, file, line, signature, doc string
- **Source** — the raw function body extracted from the source file
- **Callers** — every function that calls this symbol
- **Callees** — every function this symbol calls
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
If you are an agent making autonomous edits, you must always run `gograph build . --precise` followed by `gograph review --uncommitted` as your final step to verify no regressions were introduced.

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

#### ⚠️ Critical Limitation: Route-Grouping (Gin, Echo, Chi, Fiber)

**This limitation affects the majority of production Go HTTP services.**

gograph resolves HTTP routes by reading the **literal string** passed as the first argument to router registration calls (`.GET()`, `.POST()`, `.PUT()`, etc.). It does **not** track variable assignments or chain `.Group()` calls.

**Flat routing (works correctly):**
```go
router.POST("/api/users", CreateUser)   // recorded: POST /api/users ✅
router.GET("/api/users/:id", GetUser)   // recorded: GET /api/users/:id ✅
```

**Grouped routing (prefix is lost):**
```go
v1 := router.Group("/api/v1")           // variable — not recorded
users := v1.Group("/users")             // chained variable — not recorded
users.POST("/", CreateUser)             // recorded: POST /  ❌ (prefix lost)
users.GET("/:id", GetUser)              // recorded: GET /:id  ❌ (prefix lost)
```

In a codebase that uses `Group()`, searching `gograph endpoint "POST /api/v1/users"` **returns no results** because that string never appears as a literal in the AST. The assembled path only exists at runtime.

**This affects:** Gin (`router.Group`), Echo (`e.Group`), Chi (`r.Route`), Fiber (`app.Group`), and any framework using route group composition.

#### ✅ Recommended Usage Pattern

**Always prefer handler symbol name over route pattern.** The handler name is always a literal identifier in the AST, regardless of how routing is organized:

```bash
# PREFERRED — works with ALL routing styles (flat, grouped, nested)
gograph endpoint "CreateUser"
gograph endpoint "GetUserByID"
gograph endpoint "DeleteOrder"

# CONDITIONAL — only works if the path is a flat literal (no Group() prefix)
gograph endpoint "POST /api/users"
gograph endpoint "/api/users"
```

**Workflow for grouped routers:**
```bash
# Step 1: find all registered routes and their handler names
gograph routes

# Step 2: note the handler name for the route you care about
# Output: POST /  → handler: CreateUser

# Step 3: query by handler name
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
This tells an agent exactly which packages will be affected if `cli` changes, without requiring it to follow import chains manually.

### 18. Change detection
`gograph changes` compares every source file's modification time against `graph.json`'s `generated_at` timestamp and reports:
- **MODIFIED** — symbols in files that changed since the last build
- **NEW** — top-level declarations in changed files not recorded in the graph
- **DELETED** — symbols whose source files no longer exist

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
Changes since graph build (2026-05-09 14:00:00 UTC):

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
`gograph api --since <ref>` compares the public-facing contract and integration surface of the Go codebase against a baseline git reference.

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
`gograph` exposes a registered MCP tool suite for the highest-value agent workflows directly to the agent as executable tools, bypassing the need for terminal commands.

MCP agents should call `gograph_capabilities` first when they need to discover available gograph tools and recommended workflows.

### Registered MCP Tools

The current tool suite includes:
- **`gograph_capabilities`**: Discover available tools and workflows.
- **`gograph_stale`**: Check whether `.gograph/graph.json` is outdated relative to Go source files. Returns JSON with `is_stale`, `graph_age`, `newest_source_mtime`, `newest_source_file`, and `changed_files[]`.
- **`gograph_session_create`**: Start a telemetry audit session for tracking agent compliance and tool success metrics.
- **`gograph_session_end`**: End the active telemetry session cleanly and write end-of-session logs.
- **`gograph_session_audit`**: Review and grade agent compliance (Plan rule, Review rule, Composability/Efficiency) and tool success rates.
- **`gograph_session_cleanup`**: Delete all stale inactive session telemetry logs to keep the repository clean.
- **`gograph_query`**
- **`gograph_focus`**
- **`gograph_callers`**
- **`gograph_callees`**
- **`gograph_implementers`**
- **`gograph_fields`**
- **`gograph_source`**
- **`gograph_orphans`**
- **`gograph_impact`**: Blast radius analysis. Supports three modes: single symbol, `uncommitted=true` for uncommitted changes, and `since=<ref>` for all changes since a git ref.
- **`gograph_boundaries`**: Verifies package architecture constraints. Returns structured output.
- **`gograph_api`**: Compares public-facing contract and integration surface drift against a baseline git reference.
- **`gograph_routes`**: Extract all HTTP REST API routes found in the codebase. Annotates routes using unresolvable factory handlers (e.g. `promhttp.Handler()`) with `[dynamic handler]` in the detail, setting `DynamicHandler: true`.
- **`gograph_node`**: AST metadata for a symbol: kind, file, line, signature, doc comment. Lighter than `gograph_source` when you only need metadata.
- **`gograph_path`**: Shortest BFS call chain between two symbols. Use to confirm whether a handler actually reaches a given function.
- **`gograph_changes`**: Symbols modified/added/deleted since last build. With `git_ref`, returns symbols in files changed since that ref (MODIFIED only).
- **`gograph_context`**: Bundles node details, callers, callees, tests, and source code into one compact structured response.
- **`gograph_plan`**: Pre-edit planning. Highlights likely affected tests, routes, env reads, SQL touches, and public API impact. Set `with_context=true` to bundle full context for every `inspect_first` symbol — eliminates follow-up `context` calls.
- **`gograph_review`**: Post-edit review. Summarizes what changed and its risk profile in a structured JSON payload.
- **`gograph_risk`**: Risk evaluation. Combines blast radius, complexity, test coverage, and SQL/env dependencies into a 0–100 risk score and verdict (SAFE/REVIEW/DANGER). Supports `symbol` or `uncommitted=true`.
- **`gograph_errorflow`**: Traces likely error paths up to entry points (HTTP routes or CLI commands). (*Limitation: Uses heuristic static call-graph and AST reference analysis, not SSA data-flow tracking.*)
- **`gograph_imports`**
- **`gograph_envs`**: All `os.Getenv`/`os.LookupEnv` reads in the codebase. Filter by key name substring.
- **`gograph_endpoint`**: Full vertical slice for one HTTP route: handler, BFS call chain (default depth 5), SQL, and env reads. Query by route pattern, path fragment, or handler name. Accepts `depth` parameter.
- **`gograph_interfaces`**: Interfaces satisfied by a named struct — inverse of `gograph_implementers`. Use before refactoring a method to know which contracts break.
- **`gograph_mutate`**: All assignment sites for a named struct field (direct, IncDec, and indirect via method calls). Use before adding field validation.
- **`gograph_tests`**: Test functions that exercise a named symbol. Omit symbol to list all test edges.
- **`gograph_sql`**
- **`gograph_errors`**
- **`gograph_embeds`**
- **`gograph_public`**
- **`gograph_constructors`**
- **`gograph_literals`**: Find all composite-literal initialization sites for a named struct. Run before adding a required field — every site returned will break at compile time.
- **`gograph_usages`**: Find every place a named type appears in function signatures (param/return), struct fields, and interface method signatures. Run before changing an interface to see the full consumption blast radius.
- **`gograph_returnusage`**: Show how each caller uses the return value of a function (discarded/assigned/partially_ignored/returned/passed). Run before changing a return signature to find callers that silently discard values.
- **`gograph_arity`**: Find functions with too many arguments. Optional `min` parameter (default: 5).
- **`gograph_complexity`**: Cyclomatic complexity per function, sorted highest first. Labels: LOW/MEDIUM/HIGH/VERY HIGH (McCabe thresholds: 5/10/20). Filter by symbol name substring.
- **`gograph_coupling`**: Fan-in, fan-out, and instability per package. Instability = FanOut/(FanIn+FanOut). Range [0,1]. Filter to a specific package.
- **`gograph_deps`**: Import dependency tree of a package. Set `transitive=true` for full BFS closure.
- **`gograph_dependents`**: All packages that import the named package (inverse of `gograph_deps`). Essential before any package-level refactor.
- **`gograph_hotspot`**: Functions ranked by fan-in (incoming call count). High fan-in = highest-risk change target. Optional `top` parameter (default: 10).
- **`gograph_concurrency`**: Map goroutine spawns, channel operations, mutex locks, WaitGroups, and sync.Once. Optional filter by kind.
- **`gograph_fixtures`**: Find test helper structs and functions in test files for a package.
- **`gograph_godobj`**: Find god-object struct candidates. Optional thresholds: `methods`, `fields`, `calls`, `top`.
- **`gograph_skeleton`**: Full repository API signatures with bodies stripped. Large output — use on small repos or targeted packages.
- **`gograph_schema`**
- **`gograph_globals`**
- **`gograph_mocks`**
- **`gograph_explain`**: LLM-ready architectural summary. Synthesizes callers (prod vs test), callees, complexity, SQL, env, routes, concurrency, test coverage, interface satisfaction, and an opinionated role classification into one structured narrative.
- **`gograph_stats`**: Compact index health summary. Returns schema version, build timestamp, and counts of packages, files, symbols, calls, imports, routes, SQL queries, env reads, and test edges. Use this as a quick sanity check at the start of any analysis session.
- **`gograph_trace`**: Alias for `gograph_errorflow`. Kept for backward compatibility — prefer `gograph_errorflow` directly.
- **`gograph_diagram`**: Mermaid architecture diagram of the repository package dependency graph. Parameters: `group_by` (package/module/service/file), `max_depth` (0=unlimited), `include_stdlib` (bool). Use for onboarding or communicating package structure.
- **`gograph_check`**: Run static policy checks (boundaries, api_drift, max_arity, max_complexity, test_coverage, no_orphans). Parameters: `since` (git ref for api_drift baseline), `uncommitted` (bool), `config` (path to checks.json). Returns structured JSON with status (pass/warn/fail), findings, and summary counts. Use during PR review or pre-commit analysis. For CI enforcement with non-zero exit code, use CLI `gograph gate` instead.
- **`gograph_wiki`**: Generate the `llm-wiki/` directory of machine-first markdown pages from the static graph. Pages: overview, architecture, hotspots, routes, env, errors, concurrency, api-surface, and one file per internal package. Optional `output` parameter to override directory. Run once per session for zero-cost orientation. Returns a JSON manifest of written page filenames.
- **`gograph_summary`**: Single-call codebase briefing: top 3 hotspots, worst instability package, highest complexity function, orphan count, and god-object count. Replaces 5 separate tool calls.
- **`gograph_untested`**: Production functions with callers but zero test edges — coverage gaps invisible to orphans or per-symbol test queries. Optional `pkg` filter. Replaces N `gograph_tests` calls.
- **`gograph_doc`**: `go doc` wrapper — signature + doc comment for any stdlib or third-party symbol. **No graph required.** Use when a call chain leads outside the project. Examples: `fmt.Errorf`, `io.Reader`.
- **`gograph_httpcalls`**: All outbound HTTP client calls via `net/http` (Get, Post, PostForm, Head) in the codebase. Filter by method or URL substring.

> **Note — CLI-only commands (no MCP equivalent):** `build`, `gate`, `snapshot`, `add-claude-plugin`, `hook-guard`, `mcp`, `version`, `help` are intentionally CLI-only. `gate` and `snapshot` are CI/CD tools that write files or exit non-zero — unsuitable for MCP. `session` commands have MCP equivalents (`gograph_session_*`).

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
   This writes `.gograph/graph.json` and `.gograph/GRAPH_REPORT.md`, and adds `.gograph/` to the Git repository root `.gitignore` non-destructively. If no Go files are found after ignore filtering, the build exits without writing artifacts.

3. **Tell the agent to use it.** You don't need a huge instruction template anymore. Just add this to `CLAUDE.md`, `.cursorrules`, `.github/copilot-instructions.md`, or whatever file your agent reads:

   > Before answering architecture or repository questions, run `gograph capabilities` and follow the instructions it prints.

   The `gograph capabilities` command will output a token-optimized cheat sheet of commands and tell the agent everything it needs to know to stop grepping and start using the graph.

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
*Note: Session commands (`session create`, `session end`, `session audit`), MCP routing servers (`mcp`), help commands, `capabilities`, `wiki`, and `doc` are exempt from intention enforcement.*

### 3. Ending a Session
Once the agent finishes its work, the session is cleanly ended:
```sh
gograph session end
# Output: Session "implement_refactor_20260530_200840" successfully ended.
```

### 4. Telemetry Log Architecture (Append-Only JSONL)
All commands executed during an active session are logged inside `.gograph/sessions/session_<session_id>.jsonl`. 
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

`gograph` is intentionally narrow — important for any tool a coding agent will run autonomously:

- **No network** — no API calls, no telemetry, no embeddings service. The built-in MCP server runs entirely over local standard input/output (`stdio`), so no network ports are ever opened.
- **No code execution** — purely static AST parsing. Never runs `go test`, `go build`, `go list`, or any code from the target repo.
- **No secret-bearing files** — only `.go` files are opened. `.env`, `*.key`, `*.pem`, `*.crt`, kubeconfig, tfstate, etc. are never read.
- **No file contents in output** — the graph stores structural metadata (names, kinds, line numbers, edges), not source bodies.
- **Generated files skipped** — `.pb.go`, `_generated.go`, files with `// Code generated` headers are excluded so they don't pollute the map.
- **AI agent worktrees excluded** — `.claude/`, `.cursor/`, `.agents/` directories are skipped entirely by the scanner, preventing duplicate symbols from AI agent worktrees (e.g. `.claude/worktrees/agent-*/`). Directories listed in `.gitignore` are also skipped via `git check-ignore`.
- **Subdirectory aware** — all query commands auto-discover the project root by walking up to the nearest `.gograph/` directory. Agents do not need to `cd` back to the repo root before running `plan`, `review`, or any other query.
- **Non-destructive** — output files are mode `0640`; the Git repository root `.gitignore` is appended to, never overwritten.

The agent gains a structural view of the repo without gaining any new attack surface or data-exfiltration vector.

## Cost / token comparison (rough)

For a mid-sized Go service (~50 files, ~300 symbols):

| Approach | Approximate tool calls | Approximate tokens consumed |
|---|---|---|
| Grep + read raw files to answer "what calls IssueToken?" | 6–12 | 8k–25k |
| `gograph callers IssueToken` | 1 | <500 |
| Initial repo orientation (read 8 files) | 8+ | 30k–60k |
| Read `GRAPH_REPORT.md` once | 1 | 2k–6k |

Numbers vary by repo, but the order-of-magnitude win is consistent: structural questions stop competing with implementation reads for context window space.

## Limitations the agent should know about

- **Go only.** No multi-language parsing.
- **Call edges are best-effort text form** from the AST — no type resolution, so overloaded names and method receivers may collide. Treat `callers`/`callees` results as a starting point, not ground truth. **Workaround:** when you need to disambiguate same-named methods/functions or query symbols cleanly:
  1. Use standard Go **package-qualified dot-notation** (e.g. `service.GenerateRequest`, `graph.Graph` or `graph.Graph.Build`). All query commands support package-qualified dot notation dynamically.
  2. For precise target matching with no same-name conflation, pass the fully-qualified symbol ID (e.g., `gograph callers 'github.com/foo/bar/internal/auth::(*Service).Validate'`). The same FQ-ID syntax works for `callees`, `impact`, and `path` (both endpoints). Requires `--precise` mode at build time.
- **No cross-repo / module-external edges.** External dependencies are extracted from `go.mod` to summarize the tech stack, but call edges into third-party packages are not resolved.
- **Snapshot, not live.** The graph reflects the state at the last `gograph build` run. Re-run after structural edits.

## TL;DR

`gograph` turns "agent re-reads the repo every conversation" into "agent reads one map file, then issues targeted queries." For Go projects worked on by coding agents, it materially reduces context cost and improves structural accuracy, without adding any network, execution, or data-leak risk.

