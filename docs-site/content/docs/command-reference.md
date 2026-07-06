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
If no Go files are found after ignore filtering, exits without writing artifacts.
- **Arguments**: `path` (optional, defaults to `.`)
- **Flags**: 
  - `--precise`: Enables type-checked Class Hierarchy Analysis (CHA) using Go's type-checker to resolve dynamic interface dispatches to concrete caller/callee relationships. Slower, requires code to be compilable.

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
gograph callers <function> [--no-tests] [--depth N]
```
Finds all callers of a target function or method.
- **Flags**:
  - `--no-tests`: Filters out test files from caller results.
  - `--depth N`: Traverses the call graph upwards up to `N` hops (from 1 to 10). Useful for scoped neighborhood analysis. Defaults to `1` (direct callers).

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
Calculates the transitive downstream blast radius (all functions that eventually call the target).
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
Finds dead code candidates. Runs a full BFS reachability analysis starting from the entry points (e.g., `main`, registered HTTP routes, and exported package APIs) to identify entirely unreachable functions.

---

## Interfaces & Types

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
gograph sql
```
Extracts and maps raw SQL string queries to the functions that execute them.

### errors
```bash
gograph errors [--no-tests]
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

---

## Composed Token Saver Commands

These compound commands are optimized for AI agent consumption to prevent sequential tool execution round-trips, significantly saving context tokens and reducing latency.

### context
```bash
gograph context <symbol> [--limit N]
gograph context --uncommitted
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
gograph plan --uncommitted
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

---

## Code Quality & Verification

### check
```bash
gograph check [--config]
gograph check --uncommitted
gograph check --since <ref>
```
Executes static policy checks against package boundaries, API drift, and test requirements.
- **Options**:
  - `--config`: Custom checks using `.gograph/checks.json`.
  - `--uncommitted`: Restricts policy validation to uncommitted modified files.
  - `--since <ref>`: Validates changes introduced since a git reference.

### gate
```bash
gograph gate
```
Enforces CI/CD quality gates. Reads the `.gograph.yml` configuration and fails the build (returns exit code > 0) if thresholds (e.g. cyclomatic complexity, orphan counts, test coverage ratios) are violated.

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
- **Labels**: `LOW` (0-4), `MEDIUM` (5-9), `HIGH` (10-19), `VERY HIGH` (20+).

### coupling
```bash
gograph coupling [package]
```
Calculates Fan-In, Fan-Out, and Instability metrics for all packages or a target package.
- **Formula**: `Instability = FanOut / (FanIn + FanOut)`. A value of `0` means extremely stable (nothing depends on external elements); `1` means fully unstable (pure consumer).

### hotspot
```bash
gograph hotspot [--top N]
```
Identifies structural hotspots by ranking functions by their incoming call count (fan-in). Essential to identify high-risk parts of the codebase. Defaults to `--top 10`.

### skeleton
```bash
gograph skeleton
```
Outputs the entire repository's API signatures with their function/method bodies stripped. Useful for full structural orientation.

### mutate
```bash
gograph mutate <field>
```
Finds every function or method that assigns a value to a specific struct field.

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
- **Auto-Build**: If `.gograph/graph.json` does not exist when starting, automatically triggers `gograph build` to ensure the server starts successfully.

### add-claude-plugin
```bash
gograph add-claude-plugin
```
Installs the gograph MCP tool as a Claude plugin. Configures a smart `PreToolUse` hook in `.git/hooks/` and sets up `CLAUDE.md` workspace rules.

### hook-guard
```bash
gograph hook-guard
```
Called by the `PreToolUse` hook. Intercepts incoming agent tool calls over `stdin`. If it detects the agent executing a raw `grep` or `find` over Go symbol names, it blocks the tool call and instructs the agent to use `gograph` instead to prevent hallucination.



