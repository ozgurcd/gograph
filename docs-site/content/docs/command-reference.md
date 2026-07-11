---
title: "Command Reference"
weight: 2
description: "Detailed documentation for every gograph CLI command."
---

This reference documents every command available in the `gograph` CLI, compiled directly from the production source code.

---

## Indexing & Core Commands

### build
```bash
gograph build [path] [--precise]
```
Walks and parses a Go repository. Generates the structured graph at `.gograph/graph.json` and nine targeted Markdown reports in `.gograph/`.
Adds `.gograph/` to the Git repository root `.gitignore` when available; outside Git, falls back to the build target `.gitignore`.
Git-ignored files and directories use the same exclusion policy in `build`, `stale`, and `changes`. If no Go files are found or none parse successfully, exits without replacing existing artifacts. Partial parse failures are recorded in `graph.json`, which is committed with an atomic rename.
- **Arguments**: `path` (optional, defaults to `.`)
- **Flags**: 
  - `--precise`: Attempts type-checked CHA/SSA enrichment. Enrichment needs compilable packages; on failure gograph warns and still publishes the AST graph.

### stale
```bash
gograph stale
```
Checks if any `.go` source files in the repository are newer than `.gograph/graph.json`. Returns a list of files that have been modified since the last build.

### stats
```bash
gograph stats
```
Provides a zero-parse index health summary derived entirely from `.gograph/graph.json`. Extremely fast.
- **Output fields**:
  - `schema_version`
  - `generated_at`
  - `packages`
  - `files`
  - `symbols`
  - `calls`
  - `imports`
  - `routes`
  - `sqls`
  - `env_reads`
  - `test_edges`
  - `build_status` (`complete`, `partial`, or `unknown` for older graphs)
  - `scanned_files`
  - `parsed_files`
  - `parse_failures`

---

## Search & Navigation

### query
```bash
gograph query <term...>
```
Performs a broad, case-insensitive substring search across multiple entities.
- **Scans**: Symbol names, file paths, package names, import paths, and call sites.
- **Logic**: Performs OR-matching if multiple terms are provided.

### focus
```bash
gograph focus <package>
```
Extracts targeted package orientation context.
- **Output**: Returns all files, symbols, internal calls, and dependencies of the specified package.

### node
```bash
gograph node <name>
```
Displays detailed AST metadata for a single named symbol, package, or file.
- **Output fields**: Kind, file, line, signature, comments/docstrings, and struct fields.

### source
```bash
gograph source <name>
```
Extracts the exact raw source code block for a symbol (function, method, struct, or interface) from the filesystem using the graph's location data.
- **Note**: This is the preferred way for AI agents to view symbol declarations and bodies, avoiding reading entire files.

---

## Call Graph Commands

### callers
```bash
gograph callers <function> [--no-tests] [--depth N] [--exact]
```
Finds all callers of a target function or method.
- **Flags**:
  - `--no-tests`: Filters out test files from caller results.
  - `--depth N`: Traverses the call graph upwards up to `N` hops (from 1 to 10). Useful for scoped neighborhood analysis. Defaults to `1` (direct callers).
  - `--exact`: Requires exact symbol-name or fully-qualified-ID matching.

### callees
```bash
gograph callees <function> [--no-tests] [--depth N]
```
Finds all functions or methods called from within the target function.
- **Flags**:
  - `--no-tests`: Filters out calls within test files.
  - `--depth N`: Traverses the call graph downwards up to `N` hops (from 1 to 10). Defaults to `1` (direct callees).

### impact
```bash
gograph impact <symbol>
gograph impact --uncommitted
gograph impact --since <ref>
```
Calculates the transitive upstream blast radius (all functions that eventually call the target).
- **Options**:
  - `<symbol>`: Performs impact analysis for a specific function.
  - `--uncommitted`: Computes the blast radius for all currently modified uncommitted symbols.
  - `--since <ref>`: Computes the blast radius for all symbols changed since the specified git reference (e.g., `main`, `v1.4.50`).

### path
```bash
gograph path <from> <to>
```
Calculates and prints the shortest call chain (BFS path) between two symbols, verifying reachability.

### orphans
```bash
gograph orphans
```
Finds dead code candidates using BFS from `main`/`init`, test/benchmark/fuzz roots, registered routes, and eligible externally callable exports. Exports under `internal/` are not roots; dead chains are reported even when their members call one another.

---

## Interfaces & Types

### fields
```bash
gograph fields <struct>
```
Lists a struct's fields, types, and tags.

### embeds
```bash
gograph embeds <struct>
```
Finds structs that anonymously embed the named type.

### implementers
```bash
gograph implementers <interface> [--test-only]
```
Finds structs that implement the named interface (duck-typing).
- **Flags**:
  - `--test-only`: Restricts results strictly to structs defined in test or mock files.

### interfaces
```bash
gograph interfaces <struct>
```
Duck-type checker. Finds all interfaces in the codebase that the specified struct implements.

### constructors
```bash
gograph constructors <struct>
```
Finds factory and constructor functions that return the named struct (e.g., `NewClient`, `New*`).

### literals
```bash
gograph literals <struct>
```
Finds every place where the struct is initialized using a composite literal (`StructName{...}`). Essential to run before adding or removing a required field to know exactly which sites will break.

### returnusage
```bash
gograph returnusage <function>
```
Traces how each caller handles the return values of the specified function.
- **Labels**: `discarded`, `assigned`, `partially_ignored`, `returned`, or `passed`. Run this before refactoring signatures to find callers that silently ignore return values.

### usages
```bash
gograph usages <type>
```
Finds every place where a named type appears in parameter/return lists, struct fields, or interface methods. Essential for tracing the impact of a type change.

### schema
```bash
gograph schema <table>
```
Finds structs mapped to a database table or schema via struct tags (e.g. `db:"..."`, `gorm:"..."`).

### globals
```bash
gograph globals <pkg>
```
Finds all package-level variables and constants, as well as functions that mutate them.

### mocks
```bash
gograph mocks <interface>
```
Alias for `implementers <interface> --test-only`. Kept for compatibility.

### fixtures
```bash
gograph fixtures <pkg>
```
Finds test helper structs and test functions within test files in a specific package.

---

## Packages & Dependencies

### deps
```bash
gograph deps <pkg> [--transitive]
```
Finds the direct import dependencies of a package.
- **Flags**:
  - `--transitive`: Calculates the full transitive closure of package imports (BFS).

### dependents
```bash
gograph dependents <pkg>
```
Finds all packages in the repository that import the specified package (the inverse of `deps`). Deduplicated by package. Highly recommended to run before package-level refactoring.

### changes
```bash
gograph changes
gograph changes --git <ref>
```
Without `--git`, compares current source against the last persisted graph and reports new, modified, and deleted symbols. Git-ref mode reports symbols in changed files relative to the ref; full NEW/DELETED classification requires a baseline graph comparison.

### imports
```bash
gograph imports <pkg>
```
Finds all source files in the repository that import a specific external or internal import path.

### public
```bash
gograph public <pkg>
```
Lists only the exported (public) API symbols (types, functions, variables, constants) of a package.

---

## Extraction Commands

### routes
```bash
gograph routes
```
Extracts all HTTP REST API routes found in the codebase (handles Gin, Chi, Echo, and net/http literals).

### sql
```bash
gograph sql [term]
```
Extracts and maps raw SQL string queries to the functions that execute them. The optional term filters by SQL keyword or table-name substring.

### errors
```bash
gograph errors [term] [--no-tests]
```
Lists custom error variables (declared using `errors.New`, `fmt.Errorf`, etc.) and panic statements mapped to their source locations.

### envs
```bash
gograph envs [term]
```
Lists every `os.Getenv` or `viper.Get*` read in the codebase, with file and line. Optional substring filter by key name.

### concurrency
```bash
gograph concurrency [term]
```
Maps goroutine spawns (`go func`), channel operations, mutex locks, `WaitGroups`, and `sync.Once` usage.

### httpcalls
```bash
gograph httpcalls [term]
```
Lists package-level `net/http` client calls (`Get`, `Post`, `PostForm`, `Head`). The optional filter matches method, URL, or function context.

### tests
```bash
gograph tests [symbol]
```
Lists attributed test edges, optionally filtered to one symbol. This is static attribution, not runtime coverage.

---

## Composed Token Saver Commands

These compound commands are optimized for AI agent consumption to prevent sequential tool execution round-trips, significantly saving context tokens and reducing latency.

### context
```bash
gograph context <symbol> [--limit N] [--exact]
gograph context --uncommitted [--limit N]
```
Gathers all essential structural details for a symbol or uncommitted changes in a single call.
- **Output**: Node AST details, exact source code, caller list, callee list, test list, and its calculated architectural `role` classification.
- **Flags**:
  - `--uncommitted`: Bundles the full context for *all* currently uncommitted modified symbols into one response.

### explain
```bash
gograph explain <symbol>
```
Synthesizes AST data into a rich, prompt-ready natural language prose narrative.
- **Output details**: Symbol purpose, Prod vs. Test split, McCabe cyclomatic complexity rating, SQL queries used, Environment variables read, matching HTTP routes, interface satisfaction, and an opinionated role classification (e.g., HTTP handler, orchestrator, utility).

### endpoint
```bash
gograph endpoint <route> [--depth N] [--json] [--include-tests]
```
Generates a complete vertical slice report for a single HTTP endpoint.
- **Inputs**: Handler symbol name (always works), route path fragment (e.g. `/users`), or route pattern (`POST /api/users`).
- **Composes**: Route definition + handler function + full downstream callee chain (BFS, default depth 5) + database SQL queries + env vars read.

### errorflow
```bash
gograph errorflow <term> [--no-tests]
```
Traces the lifetime of an error up to the HTTP/entrypoint layer.
- **Algorithm**: Resolves the error's declaration site, return/wrapping locations (including `%w` format strings), and traverses the call graph upwards to find entry points.
- **Flags**:
  - `--no-tests`: Excludes test-file callers from the trace.

### trace
```bash
gograph trace <term> [--no-tests]
```
Alias for `errorflow`. Kept for compatibility.

### plan
```bash
gograph plan <symbol> [--with-context]
gograph plan --uncommitted [--with-context]
```
Generates a comprehensive change-impact plan prior to editing.
- **Output**: Affected callers, relevant tests to run after editing, and specific risks (SQL writes, environment reads, public API drift).
- **Flags**:
  - `--with-context`: Inlines the complete `context` for every symbol listed in the plan, avoiding sequential lookup calls.
  - `--uncommitted`: Generates a joint change plan for all currently modified uncommitted symbols.

### review
```bash
gograph review <symbol>
gograph review --uncommitted
```
Performs post-edit verification.
- **Output**: Code changes, complexity drift, test coverage status, and a risk evaluation.

### risk
```bash
gograph risk <symbol>
gograph risk --uncommitted
```
Combines blast radius, complexity, attributed tests, public API status, and SQL/env dependencies into a 0-100 risk score and verdict.

### summary
```bash
gograph summary
```
Returns top hotspots, worst package instability, highest complexity, reachability-orphan count, and god-object count in one call.

### untested
```bash
gograph untested [--pkg name] [--top N]
```
Ranks called production functions that have no attributed test edge. This is distinct from unreachable-code detection and from runtime coverage.

---

## Code Quality & Verification

### check
```bash
gograph check [--config path]
gograph check --uncommitted
gograph check --since <ref>
```
Executes static policy checks against package boundaries, API drift, changed-route and changed-export test requirements, exported-symbol test coverage, unreachable symbols, new globals, arity, and complexity. Git baselines are extracted to a temporary directory; route checks use handler identity and detect body-only changes from Git changed files.
- **Options**:
  - `--config path`: Use a custom checks JSON file instead of `.gograph/checks.json`.
  - `--uncommitted`: Includes uncommitted changed-symbol/file context in checks that use change scope.
  - `--since <ref>`: Validates changes introduced since a git reference.

### gate
```bash
gograph gate
gograph gate init
```
Enforces CI/CD quality gates. Reads the project-root `.gograph.yml` configuration and fails closed if `graph.json` is stale. A current graph then exits non-zero when configured complexity, instability, god-object, reachability-orphan, or coupling thresholds are violated. `gate init` writes a commented template and refuses to overwrite an existing file.

### api
```bash
gograph api --since <ref>
```
Builds a validated temporary graph from a Git reference and reports exported API/contract additions, removals, and changes. `contract` is a compatibility alias.

### snapshot
```bash
gograph snapshot save <name>
gograph snapshot diff <name>
gograph snapshot list
gograph snapshot drop <name>
```
Architectural metric snapshots. Captures the current codebase metrics (symbol count, coupling, orphans) to allow comparison before/after a refactoring.

### boundaries
```bash
gograph boundaries [--config]
gograph boundaries --create
```
Enforces package modularity boundaries.
- **Options**:
  - `--config`: Evaluates package import relationships against `boundaries.json`.
  - `--create`: Autogenerates a starting `boundaries.json` mapping based on the current package architecture imports.

### complexity
```bash
gograph complexity [symbol]
```
Displays McCabe cyclomatic complexity for all functions, sorted highest first. Optional substring filter by symbol name.
- **Labels**: `LOW` (1-5), `MEDIUM` (6-10), `HIGH` (11-20), `VERY HIGH` (21+).

### coupling
```bash
gograph coupling [package] [--include-stdlib] [--internal-only]
```
Calculates Fan-In, Fan-Out, and Instability metrics for all packages or a target package.
- **Formula**: `Instability = FanOut / (FanIn + FanOut)`. `0` means no outgoing dependencies; `1` means no incoming dependents. Isolated packages report `n/a`.

### diagram
```bash
gograph diagram [--group-by package|module|service|file] [--max-depth N] [--include-stdlib]
```
Generates a Mermaid architecture diagram. Bare `gograph --mermaid` is shorthand for the package overview.

### hotspot
```bash
gograph hotspot [--top N] [--include-tests]
```
Identifies structural hotspots by ranking functions by their incoming call count (fan-in). Essential to identify high-risk parts of the codebase. Defaults to `--top 10` and excludes test-file call edges unless `--include-tests` is set.

### godobj
```bash
gograph godobj [--methods N] [--fields N] [--calls N] [--top N]
```
Ranks structs that exceed any enabled method, field, or outgoing-call threshold; combined excess determines severity.

### skeleton
```bash
gograph skeleton
```
Outputs the entire repository's API signatures with their function/method bodies stripped. Useful for full structural orientation.

### mutate
```bash
gograph mutate <field|Type.Field>
```
Finds struct-field and package-global mutations. `Type.Field` filters same-named fields on unrelated types, and ordinary local assignments are excluded. An explicit precise build adds `++`/`+=`, pointer-alias, atomic/sync/wrapper, and channel mutations.

### arity
```bash
gograph arity [--min N]
```
Finds functions with excessive parameter counts. Defaults to `--min 5`.

---

## Agent Integration

### capabilities
```bash
gograph capabilities
```
Prints the token-optimized AI agent cheat sheet detailing common workflows and commands. Useful for bootstrapping context in an LLM system prompt.

### mcp
```bash
gograph mcp [path]
```
Starts a Model Context Protocol (MCP) server over `stdio`, exposing all gograph capabilities as native tools for integration with AI clients (e.g., Claude Code, Cursor).
- **Freshness**: If `graph.json` is missing, startup builds an in-memory AST graph. Source-analysis tools check freshness per call and rebuild only after edits; MCP `stale`, default `changes`, and `stats` inspect the persisted snapshot. A precise graph is preserved until source changes.
- **Parity**: 60 query, analysis, and workflow capabilities have CLI equivalents; four additional endpoints manage sessions.

### wiki
```bash
gograph wiki [--output dir]
```
Generates machine-first `llm-wiki/` pages from the graph. This writes or overwrites files in the selected directory.

### doc
```bash
gograph doc <pkg[.Symbol]>
```
Runs `go doc` in the current module. No graph is required. The local Go toolchain follows the user's module/cache/network policy.

### session
```bash
gograph session create [word]
gograph session end
gograph session audit [session_id]
gograph session cleanup
```
Manages local workflow metadata under `.gograph/sessions/`. Raw query results are not logged.

### add-claude-plugin
```bash
gograph add-claude-plugin
```
Registers Claude Desktop MCP configuration, injects shared `~/.claude/CLAUDE.md` rules, and installs `~/.claude/hooks/gograph-guard.sh`. Claude Code MCP registration still requires the `claude mcp add` command printed by the installer. Partial installation exits non-zero.

### hook-guard
```bash
gograph hook-guard
```
Called by the Claude Code `PreToolUse` hook. Intercepts incoming tool-call JSON over `stdin`; blocks likely `grep`/`rg` Go-symbol searches with exit code 2 and allows non-Go/comment searches.

### version and help
```bash
gograph version
gograph help
```
Print the build version or the complete CLI help contract.

## Output Modes

Query/composed commands support `--json` using the envelope keys `schema_version`, `command`, `status`, `query`, `count`, and `results`. Result-list queries support `--files-only`. Mermaid output is limited to graph-oriented commands. Operational commands (`build`, `wiki`, `gate`, `snapshot`, sessions, installation, help, and version) remain text.
