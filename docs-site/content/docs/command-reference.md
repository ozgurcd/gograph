---
title: "Command Reference"
weight: 2
description: "Detailed documentation for every gograph CLI command."
---

This reference documents every command available in the `gograph` CLI, compiled directly from the production source code.

---

## Global Flags

Global flags may appear before or after the command:

- `--json`: Structured output for query/composed-analysis commands and
  workspace build, status, query, path, and impact.
- `--files-only`: Deduplicated paths for the commands listed under Output
  Modes; an empty result writes zero lines.
- `--mermaid`: Mermaid output for `callers`, `callees`, `impact`,
  `endpoint`, `dependents`, `deps`, `path`, and `coupling`. Bare
  `gograph --mermaid` is shorthand for `gograph diagram`.
- `-i <message>` / `--intention <message>`: Technical rationale recorded with
  CLI command telemetry. It is mandatory for analytical commands while an
  audit session is active; session, MCP startup, build, installation, help,
  version, doctor, stale, stats, capabilities, wiki, and doc commands do not require it.

Request only one of `--json`, `--files-only`, or `--mermaid`; unsupported or
conflicting output flags fail instead of being silently ignored.

---

## Indexing & Core Commands

### build
```bash
gograph build [path] [--precise] [--strict] [--tags=integration[,tag...]] [--memory-mode=low] [--max-memory=1GiB]
```
Walks and parses a Go repository. Generates the structured graph at `.gograph/graph.json` and nine targeted Markdown reports in `.gograph/`.
Adds `.gograph/` to the Git repository root `.gitignore` when available; outside Git, falls back to the build target `.gitignore`.
The update accepts only an absent or regular `.gitignore`; a symlink or special
file is refused without modifying its target.
The scanner honors the effective Go build context, including build tags, platform filenames, cgo, test-file constraints, cmd/go package-directory rules, and module ignore directives. Git-ignored files and directories use the same exclusion policy in `build`, `stale`, and `changes`. Linked directories plus linked/special files for extensions recognized by `go/build` are reported and excluded, while an explicitly symlinked repository root remains supported. Unrelated regular-file or dangling links with non-Go extensions, such as YAML configuration and TSV fixtures, are not Go tool inputs and do not block precise analysis. Linked/non-regular `go.mod`, `go.sum`, `go.work`, `go.work.sum`, and `vendor/modules.txt` metadata is rejected before toolchain use. Applicable `go.work use` members may be sibling modules beneath the nearest real Git checkout; without that boundary they remain confined beneath the workspace directory. Nested Git boundaries are not crossed, and member directories, `go.mod`, and optional `go.sum` are validated before `cmd/go`. AST parsing uses repository-confined source bytes. If no Go files are found or none parse successfully, exits without replacing existing artifacts. Partial status includes parse failures and selection/security warnings recorded in `graph.json`.

Graph/report publication waits up to 30 seconds for
`.gograph/.artifacts.lock`, then stages and syncs all ten files. The nine
reports are renamed first and `graph.json` is renamed last as the commit
marker. Publication refuses a linked or non-directory `.gograph`, and readers
accept only a regular repository-confined `graph.json`. An existing
`.artifacts.lock` must also be a regular file. Same-directory
replacement is atomic on Unix-like systems but is not
guaranteed atomic by Go on non-Unix platforms, and the bundle is not one atomic
filesystem transaction; a crash can leave reports ahead of the previous
`graph.json`. The lock file remains as separate operational state. A failed
`build --precise` retry keeps an existing fresh precise artifact for the same
selected sources instead of publishing a downgrade.

Every successfully parsed file stores a SHA-256 digest. A later build reparses
all selected files in each changed package directory and reuses parser-owned
records for unchanged packages. This package boundary avoids mixing old and
new declarations within one Go package. `--precise` benefits from the reused
AST base, but type loading and CHA/SSA enrichment still run repository-wide so
cross-package method sets and dispatch targets remain correct.
- **Arguments**: `path` (optional, defaults to `.`)
- **Flags**: 
	- `--tags=<tag[,tag...]>`: Selects additional Go build tags for the whole
    graph, including test files and precise package loading. The flag is
    repeatable; values are validated, deduplicated, and canonicalized. An
    explicit value replaces any `-tags` inherited from `GOFLAGS`, matching
    `cmd/go` precedence. Without this option, existing `GOFLAGS` behavior is
    unchanged. The selected context is part of the graph fingerprint.
  - `--precise`: Attempts type-checked CHA/SSA enrichment after a repository preflight that rejects source-tree links `cmd/go` may inspect across the selected root plus its effective module root, or the workspace root and member trees; `.git` and `.gograph` are excluded from that walk. It also rejects special recognized build inputs, unsafe workspace members, and linked/non-regular module/workspace metadata entries. Enrichment needs compilable, build-selected production packages; on unsafe input, failure, or an incomplete non-test package load gograph warns and publishes the unchanged AST graph unless a fresh successful precise artifact already covers the same safely selected sources. Graph metadata records `precise`, `precise_fallback`, or `ast`. Precise interface calls retain one parallel call edge per valid named in-repository target; promoted methods add an explicitly marked traversal-only forwarding edge. A separate typed pass resolves compiling test packages without making test compilation a prerequisite for production precision. Broken or omitted test packages produce `typed_partial` test-call attribution rather than a production precision fallback.
  - `--strict`: Requires `--precise`. Precise enrichment failure keeps the same
    publication/retention behavior and diagnostic metadata, then exits non-zero.
    Without this flag, precise fallback keeps its compatible exit-zero behavior.
  - **Graph v2 compatibility**: Precision/column/synthetic, content-digest,
    analysis-cache, parser/precise provenance, and reuse-count fields remain
    additive. Graphs without the exact current source-policy marker are
    deliberately rebuild-required because their source-derived data cannot be
    trusted. A legacy graph without digests retains mtime-based freshness until
    one current build upgrades it, but is never eligible for parser-record
    reuse. Older v2 binaries can decode newly written graphs but do not enforce
    repository source confinement and may count or display synthetic forwarding
    records as ordinary calls; use the current binary for untrusted repositories
    and new graphs.
  - **Artifact memory safety**: Whole graph, saved-baseline, validation, MCP
    reload, and workspace-overlay JSON reads reject artifacts larger than 512
    MiB before allocation. Query-only commands return rebuild guidance. A build
    treats an oversized previous graph as unusable, reconstructs parser facts
    from source, and recomputes typed-only test targets before publication.
  - **SSA scope**: Precise production SSA bodies cover the selected repository
    packages rather than the transitive dependency closure. Imported types and
    local references to external calls remain available; dependency-body call
    graphs and their source-less synthetic wrapper edges are not persisted.
  - `--memory-mode=low`: Preserves the same analysis and output semantics while
    prioritizing lower heap use through aggressive garbage collection and phase-boundary memory
    reclamation, and a selective transactional precise-enrichment copy.
  - `--max-memory=<size>`: Adds a soft Go-runtime memory target and requires low
    mode. Integer decimal and binary sizes such as `1GB`, `768MiB`, and `1GiB`
    are accepted. This is not a hard RSS cap: mapped files, the binary,
    cgo, and Go toolchain subprocesses are outside or may exceed the target.
    Gograph does not silently reduce precision when the target is too small.

### stale
```bash
gograph stale [--tags=integration[,tag...]] [--json]
```
Compares the selected-file inventory, effective Go build context, and SHA-256 source-content digests with `.gograph/graph.json`. Modification times remain diagnostic fields only. It reports added, deleted, newly active, newly inactive, and byte-modified selected files plus build-context changes.
Use the same `--tags` value supplied to `build` when checking an explicitly
tagged artifact. Without it, `stale` continues to resolve inherited `GOFLAGS`.
Text output reports the persisted graph's source, freshness, completeness, and
precision. JSON adds the same full `gograph.graph-state.v1` object as top-level
`graph_state`, including refresh and persistence outcomes.

Text and JSON modes use the same exit contract:

- `0`: the graph is current
- `2`: the graph is stale and should be rebuilt
- `1`: an operational/serialization error occurred, including a missing or
  unsupported source-policy marker that requires rebuilding

When using `set -e`, put the command in an `if` condition and branch explicitly
on status `2`. Do not use `gograph stale || gograph build .`, because that also
rebuilds on status `1` and can hide the original error.

### stats
```bash
gograph stats [--json]
```
Provides a source-parse-free index health summary derived from the persisted
`.gograph/graph.json`. The CLI does not refresh before reporting it.
Text adds `artifact_source` and `freshness`; JSON adds top-level
`gograph.graph-state.v1`. Successful repository graph-backed CLI JSON commands
use this same additive field without changing their existing `results` value.
- **Output fields**:
  - `schema_version`
  - `generated_at`
  - `precision` (`ast`, `precise`, or `precise_fallback`)
  - `test_call_resolution` (`ast_heuristic`, `typed_complete`, or
    `typed_partial`)
  - `packages`
  - `files`
  - `symbols`
  - `calls`
    - Counts graph edges. One precise interface call expression contributes one edge per valid named in-repository CHA target; promoted-method wrappers can add synthetic traversal-only forwarding edges that are hidden from call-site output.
  - `imports`
  - `routes`
  - `sqls`
  - `env_reads`
  - `test_edges`
  - `flow_functions`
  - `build_status` (`complete` or `partial`)
  - `scanned_files` and `parsed_files` in JSON; text renders
    `parsed_files` as `parsed/scanned`
  - `reused_files` and `rebuilt_packages` in JSON; text renders the latter as
    `rebuilt_pkgs`
  - `parse_failures`

### validate

```bash
gograph validate --repo PATH --binding-json JSON --json
```

Evaluates one strict `gograph.binding.v1` structural predicate against the
existing trusted repository graph and emits exactly one
`gograph.validation.v1` document. It never builds or refreshes an artifact.
Supported predicates are `symbol_exists`, `package_imports`,
`call_edge_exists`, and `type_implements`. Exit status is `0` for pass, `1`
for an evaluated fail, and `2` when the request cannot be evaluated or is
invalid. This process-status machine-validation adapter is CLI-only; MCP
callers use the corresponding query/evidence tools rather than a validation
predicate endpoint. See the [Machine Validation Contract](https://github.com/ozgurcd/gograph/blob/main/docs/machine-validation-contract.md)
for the schemas, freshness rules, and completeness requirements.

---

## Search & Navigation

### explore
```bash
gograph explore <term...> [--compact|--deep] [--limit N] [--exact] [--json]
```
Performs bounded first-call discovery by composing ranked broad lexical matches
with one explicitly disclosed selected symbol's node metadata, source, direct
callers, direct callees, attributed tests, and exact identity-resolved
transitive upstream impact. Possible dispatch edges are excluded from this
bounded impact section. Use focused `impact` for broader fallback traversal.

- **Selection**: `selection_basis` is `direct_symbol_match`,
  `ranked_lexical_match`, or `none`; `ambiguous` remains true when the selected
  symbol resolves to several nodes. Question-like input is tokenized
  lexically and is not interpreted by a model.
- **Response modes**: Standard mode preserves the original response and
  defaults to 10 rows. `--compact` defaults to 5 rows and keeps ranked matches,
  selected node/role, full totals, and `omitted_sections` while omitting source,
  caller, callee, test, and impact bodies. `--deep` defaults to 25 rows, retains
  the standard response, and adds bounded depth-3 exact identity
  callers/callees, selected package context, and explanation. Compact and deep
  are mutually exclusive.
- **Bounds**: An explicit `--limit` overrides the mode default, applies
  independently to every returned list, and is clamped to 1-100. `totals`
  reports complete section sizes and `truncated_sections` identifies every
  bounded section. Deep also carries its own totals and explanation truncation
  metadata. Use the focused `query`, `context`, or `impact` command when a
  complete section is required.
- **Deep precision**: Deep traversal excludes possible dispatch and crosses
  synthetic forwarders transparently. Caller/callee rows carry `stable_id`,
  call-site provenance, and declaration location when the target is indexed.
  An ambiguous selected symbol omits the deep expansion until an exact
  fully-qualified identity is supplied.
- **Exact mode**: `--exact` requires exact resolution for selected-symbol context;
  broad lexical matches remain visible but are not promoted automatically.
- **JSON/MCP parity**: CLI `--json` places the native
  `gograph.explore.v1` value in its generic `results` envelope. MCP
  `gograph_explore` returns that same native value as JSON text, with required
  `query`; optional `limit` and `exact`; and mutually exclusive typed `compact`
  and `deep` booleans.

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
Extracts exact raw source blocks for a function, method, struct, interface,
type, variable, or constant using the graph's location data. Reads are rooted
at the analyzed repository and accept only regular `.go` files without symlink
path components. An ambiguous name may return its safely readable matches; the
command errors when no matching block can be read safely.
- **Note**: This is the preferred way for AI agents to view symbol declarations and bodies, avoiding reading entire files.

---

## Call Graph Commands

### callers
```bash
gograph callers <function> [--no-tests] [--depth N] [--exact] [--mermaid]
```
Finds all callers of a target function or method.
- Interface-qualified names such as `Repository.Delete`, including methods inherited from embedded interfaces, resolve through every recorded precise implementer. If several targets correspond to one invocation, the source expression is returned once. Records with a known column report it as well as the line; records without a column remain line-only.
- **Flags**:
  - `--no-tests`: Filters out test files from caller results.
  - `--depth N`: Traverses the call graph upwards up to `N` hops (from 1 to 10). Useful for scoped neighborhood analysis. Defaults to `1` (direct callers).
  - `--exact`: Requires exact symbol-name or fully-qualified-ID matching.

### callees
```bash
gograph callees <function> [--no-tests] [--depth N] [--mermaid]
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
  - `--mermaid`: Returns the blast radius as a Mermaid flowchart.

### path
```bash
gograph path <from> <to> [--json|--mermaid]
```
Calculates the best indexed call chain between two symbols. Competing paths are
ranked deterministically by exact before possible, then shorter paths,
production before tests, typed resolution before heuristics, and a canonical
relationship key. CLI and `gograph_path` use the same implementation.

### orphans
```bash
gograph orphans
```
Finds dead code candidates using BFS from `main`/`init`, test/benchmark/fuzz roots, registered routes, and eligible externally callable exports. Exports under `internal/` are not roots; dead chains are reported even when their members call one another. Precise reachability follows every retained interface target, not an arbitrary single implementation.

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
Finds helper types, factory functions, and other non-test symbols within
`*_test.go` files in a specific package. Functions named `Test*`, `Benchmark*`,
or `Example*` are excluded.

---

## Packages & Dependencies

### deps
```bash
gograph deps <pkg> [--transitive] [--mermaid]
```
Finds the direct import dependencies of a package.
- **Flags**:
  - `--transitive`: Calculates the full transitive closure of package imports (BFS).

### dependents
```bash
gograph dependents <pkg> [--mermaid]
```
Finds all packages in the repository that import the specified package (the inverse of `deps`). Deduplicated by package. Highly recommended to run before package-level refactoring.

### changes
```bash
gograph changes
gograph changes --git <ref>
```
Without `--git`, compares current source against the last trusted persisted
graph and reports new, modified, and deleted symbols. `DELETED` means the
recorded file is absent from the current safely selected inventory: it may be
gone, ignored, inactive under the effective build context, or unsafe to read.
Git-ref mode reports symbols in changed files relative to the ref; full
NEW/DELETED classification requires a baseline graph comparison.

### imports
```bash
gograph imports <pkg>
```
Finds all source files in the repository that import a specific external or internal import path.

### public
```bash
gograph public <pkg>
```
Lists the exported (public) functions, methods, types/interfaces, variables, and
constants of a package.

---

## Extraction Commands

### routes
```bash
gograph routes [term] [--module MODULE] [--include-tests] [--limit N] [--cursor CURSOR]
```
Returns a deterministic `gograph.routes.v1` page of HTTP REST API routes from
Gin, Chi, Echo, Fiber, and net/http-style registration calls. Production routes
are included by default; `--include-tests` adds registrations from `_test.go`.
The optional term matches method/path, handler, or file, and `--module` accepts
an exact module path/directory or a unique directory basename. Pages default to
100 rows, accept 1-200, and stay within a 64 KiB compact-JSON budget; use the
returned cursor with unchanged filters to continue a truncated census.
CLI `--files-only` follows every page locally and emits the complete
deduplicated file set; normal text and JSON return one bounded page.

Constant nested Gin/Echo/Fiber `Group` prefixes and Chi `Route` closure prefixes
are composed into final paths. Dynamically computed prefix expressions remain
unresolved, so those routes retain their known literal suffix. Variadic
Gin/Fiber registration uses the final argument as the terminal handler. Echo
uses `path, handler, middleware...`, and gograph retains that ordering. Handler
factories remain marked dynamic when their returned closure cannot be
statically resolved.

### sql
```bash
gograph sql [term]
```
Extracts and maps raw SQL string queries to the functions that execute them. The optional term filters by SQL keyword or table-name substring.

### errors
```bash
gograph errors [term] [--no-tests]
```
Lists indexed `errors.New`/`fmt.Errorf` calls, sentinel declarations, and `panic`
calls mapped to their source locations.

### envs
```bash
gograph envs [term]
```
Lists every `os.Getenv`, `os.LookupEnv`, or supported Viper `Get*` read in the
codebase, with file and line. Optional substring filter by key name.

### concurrency
```bash
gograph concurrency [term]
```
Maps goroutine spawns (`go func`), channel sends, and calls on mutex/RWMutex,
`WaitGroup`, and `sync.Once`. Channel receives and `select` statements are not
indexed.

### httpcalls
```bash
gograph httpcalls [term]
```
Lists package-level `net/http` client calls (`Get`, `Post`, `PostForm`, `Head`). The optional filter matches method, URL, or function context.

### flow
```bash
gograph flow [term] [--source http_request|decoded_json|environment] [--sink sql_query|process_execution|filesystem|outbound_http] [--config path] [--no-tests]
```
Finds potential paths from untrusted inputs to security-sensitive operations. Test files are included by default; `--no-tests` limits analysis to production files.

- **Sources**:
  - `http_request`: parameters typed as `*net/http.Request` or recognized Gin, Echo, Fiber, and fasthttp contexts.
  - `decoded_json`: `encoding/json.Unmarshal`, `encoding/json.NewDecoder(...).Decode`, Go 1.27's `encoding/json/v2` decode functions (`Unmarshal`, `UnmarshalRead`, and `UnmarshalDecode`), and recognized framework binding methods.
  - `environment`: `os.Getenv`, `os.LookupEnv`, and supported Viper package reads.
- **Sinks**:
  - `sql_query`: the query-text argument to `Query`, `QueryRow`, `Exec`, `Prepare`, and `Raw` variants. Parameter values are not treated as query text.
  - `process_execution`: command and argument values passed to `os/exec.Command` or `CommandContext`.
  - `filesystem`: path arguments to common `os` file operations.
  - `outbound_http`: URL/request arguments passed to package-level `net/http` calls, request constructors, and `Do` methods.
- **Output**: Severity, confidence, source and sink locations, and source-to-sink path steps. `--json` returns structured `FlowResult` objects; `--files-only` returns deduplicated source and sink files.
- **Sanitizers**: The command automatically reads `.gograph/flow.json` when present. `--config` selects another JSON file inside the graph root. Policies apply to return values and may be sink-scoped:

  ```json
  {
    "sanitizers": [
      { "function": "security.CleanPath", "for": ["filesystem"] },
      { "function": "security.ValidateURL", "for": ["outbound_http"] }
    ]
  }
  ```

  Omit `for` to apply a sanitizer to every sink kind. `function` accepts the call spelling or a fully-qualified symbol ID; use the fully-qualified form for duplicate names. Validators that only return `bool` or `error` do not sanitize the original value.
- **Limitations**: Interprocedural and path-insensitive, with call/return matching across at most 16 nested repository calls. Default graphs resolve direct local/imported functions; run `build . --precise` for stronger method/interface targets. Reflection, globals, arbitrary heap aliases, and unresolved dynamic calls may be missed or over-approximated. Unresolved external transformations lower confidence. Findings require source review and do not prove exploitability.

### tests
```bash
gograph tests [symbol] [--json]
gograph tests <symbol> --transitive [--exact-only] [--package name] [--json]
```
Default mode lists direct attributed test edges for compatibility. Transitive
mode requires one product symbol and returns `gograph.tests.v1`: every test with
a representative stable-ID path to that symbol, path depth, and `exact` or
`possible` resolution. `--exact-only` removes paths containing parser/CHA
uncertainty, and `--package` disambiguates a same-named product symbol. The MCP
equivalent uses `transitive`, `exact_only`, and `package`. Static attribution is
not runtime or branch coverage proof.

### coverage
```bash
gograph coverage <test-function-or-stable-id> [--exact-only] [--package name] [--json]
```
Returns the transitive product functions and methods statically reachable from
one test. Every row carries a canonical stable ID, representative path, depth,
and `exact` or `possible` resolution. Any uncertain parser/CHA edge degrades
the remainder of that path to `possible`; `--exact-only` omits those rows.
Same-named tests in multiple packages return an ambiguity instead of merging
their results. When an in-package test and external `foo_test` package share the
same graph ID, `--package` (MCP `package`) selects the exact Go package name.
This is static attribution, not runtime or branch coverage.

### identity
```bash
gograph identity <symbol-or-stable-id> [--package name] [--json]
```
Prints and re-resolves canonical symbol IDs. IDs use module import path plus
receiver/name, so line shifts and file moves inside the same package do not
change them. Package/module moves, receiver changes, and renames do. Ambiguous
short names return every candidate and never select one silently. The optional
package qualifier handles colliding in-package/external-test IDs without using
file/line as identity.

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
- **JSON/MCP shape**: CLI JSON and `gograph_context` keep the first match in
  `node` for compatibility, preserve every ambiguous match in `nodes[]`, expose
  `role`, return both test names and structured `test_results[]`, and report a
  non-fatal source read failure in `source_error`.
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
gograph endpoint <route> [--depth N] [--include-tests] [--json|--mermaid]
```
Generates a complete vertical slice report for a single HTTP endpoint.
- **Inputs**: Handler symbol name, route path fragment (e.g. `/users`), or route pattern (`POST /api/users`). Constant grouped prefixes are resolved; for a dynamic group prefix, use the known suffix or handler symbol.
- **Composes**: Route definition + handler function + full downstream callee chain (BFS, default depth 5) + database SQL queries + env vars read.
- **Flags**: `--depth` is clamped to 1-20; `--include-tests` includes routes registered in `_test.go` files; `--mermaid` returns a fenced flowchart instead of the normal text/JSON presentation.

### errorflow
```bash
gograph errorflow <term> [--no-tests]
```
Traces the lifetime of an error up to the HTTP/entrypoint layer.
- **Algorithm**: Resolves the error's declaration site, return/wrapping locations (including `%w` format strings), and traverses the call graph upwards to find entry points.
- **JSON/MCP shape**: CLI JSON, MCP `gograph_errorflow`, and `trace` share
  `definitions`, return `sites`, propagation `paths`, test names, structured
  `test_results`, and the static-analysis limitation.
- **Flags**:
  - `--no-tests`: Excludes test-file callers from the trace.

### trace
```bash
gograph trace <term> [--no-tests]
```
Alias for `errorflow`. Kept for compatibility and returns the same structured
payload under JSON/MCP.

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
gograph untested [--pkg name] [--top N] [--exclude glob]... [--wide] [--json]
```
Ranks called production functions and methods without an exact transitive path
from any test. Direct selectors, local method values, and proof-backed concrete
interface receivers can be exact. Open CHA/parser paths propagate `possible` to
their descendants, which remain in the result with `test_resolution=possible`
and `possible_test_count`; `test_resolution=none` means no bounded path was
found. JSON and MCP rows include canonical `stable_id`. `--wide` prints that
identity without truncation. `typed_partial` means the result remains an upper
bound. This is static attribution, not runtime coverage.
Each `--exclude` is matched lexically against the repository-relative source
path; use `prefix/**` for all descendants. The MCP equivalent is the `exclude`
string array on `gograph_untested`.

---

## Code Quality & Verification

### check
```bash
gograph check [--config path]
gograph check --uncommitted
gograph check --since <ref|graph.json>
```
Executes static policy checks against package boundaries, API drift, changed-route and changed-export test requirements, exported-symbol test coverage, unreachable symbols, new globals, arity, and complexity. Git baselines are extracted to a temporary directory; a path ending in `.json` instead loads a saved graph baseline. Route checks use handler identity and detect body-only changes from Git changed files.

Saved graph baselines must be regular, non-linked files (including no linked
ancestor) inside the selected project and carry the exact current repository
source-policy marker. Their serialized root is ignored in favor of the trusted
project load location. Rebuild or replace an unsupported baseline before use.
- **Options**:
  - `--config path`: Use a custom checks JSON file instead of `.gograph/checks.json`. A default or relative path is confined to a regular non-linked file beneath the selected project; an absolute path explicitly selects a regular local file.
  - `--uncommitted`: Includes uncommitted changed-symbol/file context in checks that use change scope.
  - `--since <ref|graph.json>`: Validates changes against a Git reference or a
    saved graph baseline.

### gate
```bash
gograph gate
gograph gate init
```
Enforces CI/CD quality gates. Reads only a regular, non-linked project-root `.gograph.yml` and fails closed if `graph.json` is stale. A current graph then exits non-zero when configured complexity, instability, god-object, reachability-orphan, or coupling thresholds are violated. `gate init` exclusively creates the same regular project-root path and rejects links, special files, and existing entries.

Thresholds are configured only in `.gograph.yml`; `gate` does not accept
per-threshold CLI flags. Run `gograph gate init`, review and commit the generated
configuration, then run `gograph build . --precise` followed by `gograph gate`
in CI. Orphan and new-coupling limits compare with the immediately preceding
persisted graph and are skipped when that baseline is absent. Package-boundary
rules are a separate `gograph boundaries` check.

### api
```bash
gograph api --since <ref|graph.json>
```
Builds a validated temporary graph from a Git reference, or loads a saved graph
whose path ends in `.json`, and reports exported API/contract additions,
removals, and changes. Saved graph files must be regular, non-linked entries
inside the selected project with the exact current repository source-policy
marker; their serialized root is ignored. `contract` is a compatibility alias.

### snapshot
```bash
gograph snapshot save <name>
gograph snapshot diff <name>
gograph snapshot list
gograph snapshot drop <name>
```
Architectural metric snapshots stored under `.gograph/snapshots/`. Each entry
captures symbol count, reachability-orphan count, god objects, maximum
complexity, average instability, and coupling edges for before/after comparison.
The directory must be real and snapshot entries regular and non-linked. `save`
may overwrite an existing regular snapshot of the same name; `drop` removes
only the validated named regular file.

### boundaries
```bash
gograph boundaries [--config path]
gograph boundaries --create [--config path]
```
Enforces package modularity boundaries.
- **Options**:
  - `--config path`: Evaluates package import relationships against an
    in-project, regular, non-linked `boundaries.json` (default:
    `.gograph/boundaries.json`). Absolute and repository-relative in-project
    paths are accepted.
  - `--create`: Exclusively creates a starting boundary map at that path from
    current package imports. Only real parent directories are created; links,
    special files, traversal outside the graph root, and overwrite are refused.

### complexity
```bash
gograph complexity [symbol]
```
Displays McCabe cyclomatic complexity for all functions, sorted highest first. Optional substring filter by symbol name.
- **Labels**: `LOW` (1-5), `MEDIUM` (6-10), `HIGH` (11-20), `VERY HIGH` (21+), or `UNKNOWN` with score `-1` when repository source cannot be read or parsed safely.

### coupling
```bash
gograph coupling [package] [--include-stdlib] [--internal-only] [--mermaid]
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
gograph skeleton [--json]
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

## Federated Workspaces

A regular `.gograph-workspace.yml` establishes a workspace root. Every member
must be a unique relative descendant without symlink traversal. Each member's
`.gograph/graph.json` remains authoritative and independently fingerprinted;
the deterministic `.gograph/workspace.json` contains only scoped
cross-repository ownership, resolution, and HTTP-contract facts.
`gograph workspace --help` lists the complete command family and prints a
minimal valid manifest. Repository-local `go.work` sibling-module authority
inside a Git checkout does not widen this manifest's member-path boundary.

### workspace build

```bash
gograph workspace build [path] [--refresh-members] [--tags=integration[,tag...]] [--memory-mode=low] [--max-memory=1GiB] [--json]
```

Without `--refresh-members`, reads member graphs and writes only the workspace
overlay. Missing, stale, incomplete, incompatible, or insufficiently precise
members fail the build without replacing the previous overlay.
`--refresh-members` explicitly permits sequential writes in member repositories
and is not transactional. Its JSON reports `refresh_plan`,
`refresh_attempted`, `refresh_succeeded`, and `refresh_failed`, including
before/after artifact fingerprints, so a failure after earlier successful
member writes is visible.
When member refresh is enabled, the repository build memory options apply
sequentially to every refreshed member with the same soft-limit semantics.
`--tags` selects the same member build context for validation and every
explicit refresh. Use the same tag selection for subsequent workspace status,
query, path, impact, and MCP startup; a different selection is correctly
reported as stale rather than silently mixing graphs.

### workspace status

```bash
gograph workspace status [path] [--tags=integration[,tag...]] [--json]
```

Reports `complete`, `partial`, or `cannot_evaluate`, with each member's
availability, freshness, exact artifact/source/build-context fingerprints,
analysis mode, capabilities (including independent
`test_call_resolution`), advisory repository revision/dirty state, and
diagnostics. Overlay status includes presence, freshness, input fingerprint,
external exact-byte artifact fingerprint, resolver versions, and diagnostics.
The advisory Git probes disable repository-configured filesystem monitors and
optional index locking.

### workspace query

```bash
gograph workspace query [--scope id] [--workspace path] [--tags=integration[,tag...]] <term...> [--json]
```

Searches symbols, packages, modules, and first-class HTTP contracts in the
selected scope. `repository:symbol` is presentation/query syntax only;
persisted identities are structured.

### workspace path

```bash
gograph workspace path [--scope id] [--workspace path] [--tags=integration[,tag...]] [--include-possible] <from> <to> [--json]
```

Finds the best path over the selected member graphs plus the workspace overlay.
Competing paths rank exact before ambiguous before possible, then shorter,
production before tests, typed resolution before heuristics, and fewer
cross-repository transitions when otherwise equivalent. A canonical final
tie-breaker makes results independent of edge iteration order. Cross-repository
Go resolutions materialize as ordinary `calls`; HTTP clients and handlers
connect through a contract as `calls_http` then `serves_http`.

### workspace impact

```bash
gograph workspace impact [--scope id] [--workspace path] [--tags=integration[,tag...]] [--include-possible] <target> [--json]
```

Finds transitive incoming dependencies in the same virtual graph. Path and
impact traverse only `exact` evidence by default. `--include-possible` opts
into both `ambiguous` and `possible` evidence for exploration.

### workspace mcp

```bash
gograph workspace mcp [path] [--tags=integration[,tag...]]
```

Starts a separate read-only workspace MCP server with exactly four tools:
`gograph_workspace_status`, `gograph_workspace_query`,
`gograph_workspace_path`, and `gograph_workspace_impact`. For each operation,
the MCP JSON text is exactly the native value placed in the CLI `--json`
envelope's `results` field, including scope selection, ordering, and traversal
semantics. MCP cannot refresh member graphs or publish the overlay.

Multiple scopes require `--scope` unless the manifest defines `default_scope`
or contains exactly one scope. Duplicate module or logical HTTP ownership is
an error within a scope unless shared HTTP ownership is explicit; duplicates
in mutually exclusive scopes are allowed. Workspace changes, snapshots, RPC,
topics, and shared-schema resolution are outside workspace v1.

---

## CLI / MCP Transport Matrix

Every repository query, analysis, and workflow capability has a project-MCP
equivalent. The standard mapping is CLI `<command>` to MCP
`gograph_<command>`. The complete shipped inventory is listed here so neither
transport has an undocumented analytical feature.

| Capability group | CLI ↔ project MCP |
|---|---|
| Core | `capabilities` ↔ `gograph_capabilities`; `stale` ↔ `gograph_stale`; `stats` ↔ `gograph_stats` |
| Search/navigation | `query` ↔ `gograph_query`; `focus` ↔ `gograph_focus`; `node` ↔ `gograph_node`; `source` ↔ `gograph_source`; `public` ↔ `gograph_public` |
| Calls/reachability | `callers` ↔ `gograph_callers`; `callees` ↔ `gograph_callees`; `impact` ↔ `gograph_impact`; `path` ↔ `gograph_path`; `orphans` ↔ `gograph_orphans` |
| Types/tests | `implementers` ↔ `gograph_implementers`; `interfaces` ↔ `gograph_interfaces`; `fields` ↔ `gograph_fields`; `embeds` ↔ `gograph_embeds`; `constructors` ↔ `gograph_constructors`; `literals` ↔ `gograph_literals`; `usages` ↔ `gograph_usages`; `returnusage` ↔ `gograph_returnusage`; `schema` ↔ `gograph_schema`; `globals` ↔ `gograph_globals`; `mocks` ↔ `gograph_mocks`; `fixtures` ↔ `gograph_fixtures`; `identity` ↔ `gograph_identity` |
| Packages/changes | `imports` ↔ `gograph_imports`; `deps` ↔ `gograph_deps`; `dependents` ↔ `gograph_dependents`; `changes` ↔ `gograph_changes` |
| Extraction | `routes` ↔ `gograph_routes`; `sql` ↔ `gograph_sql`; `errors` ↔ `gograph_errors`; `envs` ↔ `gograph_envs`; `concurrency` ↔ `gograph_concurrency`; `httpcalls` ↔ `gograph_httpcalls`; `flow` ↔ `gograph_flow`; `tests` ↔ `gograph_tests`; `coverage` ↔ `gograph_coverage` |
| Composed workflows | `explore` ↔ `gograph_explore`; `context` ↔ `gograph_context`; `plan` ↔ `gograph_plan`; `review` ↔ `gograph_review`; `risk` ↔ `gograph_risk`; `errorflow` ↔ `gograph_errorflow`; `trace` ↔ `gograph_trace`; `endpoint` ↔ `gograph_endpoint`; `explain` ↔ `gograph_explain`; `summary` ↔ `gograph_summary`; `untested` ↔ `gograph_untested` |
| Quality/policy | `api`/`contract` ↔ `gograph_api`; `boundaries` ↔ `gograph_boundaries`; `boundaries --create` ↔ `gograph_boundaries_create`; `check` ↔ `gograph_check`; `complexity` ↔ `gograph_complexity`; `coupling` ↔ `gograph_coupling`; `diagram` ↔ `gograph_diagram`; `hotspot` ↔ `gograph_hotspot`; `godobj` ↔ `gograph_godobj`; `skeleton` ↔ `gograph_skeleton`; `mutate` ↔ `gograph_mutate`; `arity` ↔ `gograph_arity` |
| Documentation/toolchain | `wiki` ↔ `gograph_wiki`; `doc` ↔ `gograph_doc` |
| Session lifecycle | `session create` ↔ `gograph_session_create`; `session end` ↔ `gograph_session_end`; `session audit` ↔ `gograph_session_audit`; `session cleanup` ↔ `gograph_session_cleanup` |

That is 64 CLI-equivalent project capabilities plus four session tools: 68
tools on `gograph mcp`. The separate `gograph workspace mcp` server adds these
four exact pairs:

| Workspace CLI | Workspace MCP |
|---|---|
| `workspace status` | `gograph_workspace_status` |
| `workspace query` | `gograph_workspace_query` |
| `workspace path` | `gograph_workspace_path` |
| `workspace impact` | `gograph_workspace_impact` |

The only CLI operations without callable MCP equivalents are process-, host-,
CI-, or artifact-lifecycle operations: `build`, `validate`, `doctor`, `gate`,
`snapshot`, `add-claude-plugin`, `hook-guard`, project/workspace MCP startup,
`workspace build`/member refresh, and help. The standalone `version` command
has no dedicated MCP tool, but `gograph_capabilities` exposes the running server
version. CLI flags/envelopes and typed MCP arguments/content are transport-specific; paired operations share
their functional implementation and documented filters, ordering, evidence,
and result semantics.

---

## Agent Integration

### doctor
```bash
gograph doctor [--json]
```
Inspects the running executable and every distinct `gograph` executable found
on `PATH`. It reports duplicate installations, a running/PATH mismatch, and
older stable binaries when versions are safely comparable. Alternate binaries
are read through Go build metadata and are never executed. `--json` emits the
versioned `gograph.doctor.v1` document. When run inside a Go repository it also
reports graph availability/freshness, analysis mode, AST/precision/call
resolution capabilities, artifact fingerprint, and the exact freshness or
build-context diagnostic. Development, dirty, and prerelease builds are not
ordered against stable releases. Doctor remains intentionally CLI-only because
it diagnoses the host process and installation selected before MCP startup.

### capabilities
```bash
gograph capabilities
```
Prints the token-optimized AI agent cheat sheet detailing common workflows and
commands. The MCP counterpart, `gograph_capabilities`, also includes a top-level
`version` identifying the running server binary, so MCP-only clients can record
the analysis instrument. Useful for bootstrapping context in an LLM system prompt.

### mcp
```bash
gograph mcp [path] [--persist-refresh] [--tags=integration[,tag...]] [--memory-mode=low] [--max-memory=1GiB]
```
Starts a Model Context Protocol (MCP) server over `stdio`, exposing gograph's
query, analysis, and workflow capabilities as native tools for integration with
AI clients (e.g., Claude Code, Cursor).
- **Freshness**: If `graph.json` is missing, unreadable, linked through a descendant path, or has a missing/unsupported source-policy marker, startup builds a safe in-memory AST graph. Source-analysis tools compare selected source digests plus the build/module fingerprint and newer persisted artifacts per call, then reparse changed packages while reusing unchanged package AST records. Every refresh-backed result preserves its existing text and adds `gograph.mcp-result.v1` structured content plus `_meta.gograph_graph_state`. The nested `gograph.graph-state.v1` independently reports persisted/in-memory source, current/stale freshness, complete/partial parsing, AST/precise/fallback analysis, refresh outcome, and persistence outcome; each operation owns its bounded diagnostic. Failed precise enrichment can serve a marked current in-memory fallback; an ordinary refresh failure can serve the last trusted stale graph. Degraded states are not silently published. MCP `stale`, default `changes`, and `stats` inspect the trusted persisted snapshot, or the startup in-memory fallback when no usable artifact exists, and attach the exact state they inspect.
- **Build tags**: `--tags` uses the same validated selection and precedence as
  CLI `build`. Startup analysis, incremental AST rebuilds, precise refreshes,
  temporary Git baselines, and optional artifact publication all retain that
  selection. `gograph_capabilities.analysis_build_context` reports requested
  and effective tags.
- **Environment binding**: A persisted graph is valid only under its recorded
  effective source/build selection. Start MCP with the same `GOWORK`,
  `GOFLAGS`, and explicit `--tags`; a mismatch is stale and must refresh
  successfully or return a diagnostic rather than serving incompatible facts;
  stale-on-error never bypasses this boundary.
  `gograph doctor --json` reports the same diagnostic before server startup.
- **Persistence**: Refreshes remain in memory by default. `--persist-refresh`
  writes or overwrites `.gograph/graph.json` and the nine Markdown reports only
  after a successful, confirmed-fresh refresh. It does not update `.gitignore`
  and keeps one latest state rather than a per-branch cache. A publication
  failure during startup auto-build prevents the server from starting. A later
  publication failure serves the fresh in-memory result with
  `persistence.outcome=failed` and is retried on another refresh-capable call
  without rebuilding. Writers
  wait up to 30 seconds on `.gograph/.artifacts.lock`. Reports are renamed
  first and `graph.json` last as the commit marker. Same-directory replacement
  is atomic on Unix-like systems but is not guaranteed atomic by Go on non-Unix
  platforms; the complete bundle is not one atomic transaction, and the lock
  file remains as separate operational state.
- **Memory policy**: `--memory-mode=low` and `--max-memory` apply the same
  correctness-preserving runtime policy to startup analysis and every later
  MCP refresh. `gograph_capabilities` reports the requested and effective byte
  targets plus the soft-limit caveat.
- **Changes baseline**: Default `gograph_changes` compares against persisted
  `graph.json`. Successful refresh publication advances that baseline, so use
  `git_ref` when the comparison must remain anchored to a Git revision.
- **Mermaid**: Set `mermaid: true` on `gograph_callers`, `gograph_callees`,
  `gograph_impact`, `gograph_endpoint`, `gograph_dependents`, `gograph_deps`,
  `gograph_path`, or `gograph_coupling`. The tool returns Mermaid flowchart
  text instead of its normal response.
- **Parity**: 64 query, analysis, and workflow commands have corresponding MCP
  endpoints; four additional endpoints manage sessions (68 endpoints total).
  MCP uses typed tool arguments rather than CLI global flags, and some
  not-found and status results have different transport-level presentation.
  `gograph_explore` and CLI `explore --json` share the native
  `gograph.explore.v1` value; only the CLI adds its generic envelope.
- **Audit telemetry**: Read-only annotations describe the functional analysis
  contract. While an audit session is active, non-session MCP calls append
  local command/status telemetry without arguments or query results.
  MCP has no `intention` tool parameter and does not enforce the CLI session
  requirement, so those records use an empty intention.

### wiki
```bash
gograph wiki [--output dir]
```
Generates machine-first `llm-wiki/` pages from the graph. A relative output is
rooted at the analyzed project and may not traverse or follow a linked
descendant; an absolute output explicitly selects another local root. The
selected output must be a real directory. Generated directories and regular
page writes are confined beneath it, links/special entries are refused, and
existing regular pages may be overwritten. After successful generation,
obsolete `packages/*.md` files are removed only when they carry Gograph's
generated package-page signature. Custom files, `packages/README.md`, links,
special entries, and oversized unknown pages are preserved.

### doc
```bash
gograph doc <pkg[.Symbol]>
```
Runs `go doc` with the user's Go environment. In workspace-auto mode the
working-tree preflight starts at the nearest enclosing workspace; otherwise it
starts at the nearest enabled module (or project/start fallback), while an
explicit `GOWORK` selection is validated separately. No graph is required. Gograph
rejects absolute/relative filesystem-shaped queries and flags, then refuses
`go doc` when source-tree links `cmd/go` may inspect exist across the selected
root plus its effective module root, or the workspace root and member trees;
`.git` and `.gograph` are excluded from that walk. It also refuses when
`go.mod`, `go.sum`, `go.work`, `go.work.sum`, `vendor/modules.txt`, or a
recognized Go build input is non-regular. Applicable `go.work` members may be
sibling modules beneath the nearest real Git checkout; without that boundary
they remain confined beneath the workspace directory. Each directory,
`go.mod`, and optional `go.sum` is validated first. Unrelated non-Go
regular-file or dangling links do not block the Go-tool preflight.
Package/symbol queries such as `fmt.Errorf`,
`net/http.HandleFunc`, and `github.com/jackc/pgx/v5.Conn.QueryRow` remain valid.
The local Go toolchain and dependency resolution follow the user's
module/cache/network policy and are therefore open-world.
The MCP `gograph_doc` response is a one-element JSON array containing
`{"query": "...", "output": "..."}`; `output` holds the raw `go doc` text.
The handler itself does not query the graph, but the project-scoped MCP server
must already have started with a usable artifact or buildable Go source.

### session
```bash
gograph session create [word]
gograph session end
gograph session audit [session_id]
gograph session cleanup
```
Manages local workflow metadata under `.gograph/sessions/`. Session IDs contain
only letters, digits, and underscores. The `.gograph`/sessions directories must
be real, and pointers/logs must be regular non-linked entries. Audit reads and
cleanup are confined to the project; cleanup removes only validated inactive
regular logs. CLI analytical commands fail closed when active-pointer metadata
is unsafe or corrupt. Raw query results are not logged.

### add-claude-plugin
```bash
gograph add-claude-plugin
```
Registers Claude Desktop MCP configuration, injects shared `~/.claude/CLAUDE.md` rules, and installs `~/.claude/hooks/gograph-guard.sh`. Claude Code MCP registration still requires the `claude mcp add` command printed by the installer. Partial installation exits non-zero.

### hook-guard
```bash
gograph hook-guard
```
Called by the Claude Code `PreToolUse` hook. Intercepts incoming tool-call JSON over `stdin`; resolves effective search paths against the payload's `cwd`; and blocks likely `grep`/`rg` Go-symbol searches with exit code 2 only when at least one target has a real `.gograph` ancestor. An omitted `cwd` falls back to the hook process working directory. Unindexed, non-Go, and comment-only searches are allowed. Identifier-only alternations are recognized according to grep/ripgrep regex mode; literal-pipe patterns in fixed-string mode and escaped pipes in extended grep/ripgrep remain allowed.

### version and help
```bash
gograph version
gograph help
```
Print the build version or the complete CLI help contract.

## Output Modes

Query/composed commands, `check`, and workspace build/status/query/path/impact
support `--json` using the envelope keys
`schema_version`, `command`, `status`, `query`, `count`, and `results`.
Successful envelopes always include numeric `count`; collection-shaped empty
results are `[]`, not `null`. Hard failures use `status: "error"` and exit 1.
`check` also exits 1 when its structured report has failed policy findings.
`session audit --json` is the deliberate raw-JSON exception.

`--files-only` is supported by `query`, `focus`, `node`, `public`, `fields`,
`embeds`, `imports`, `callers`, `callees`, `impact`, `implementers`, `envs`,
`interfaces`, `concurrency`, `tests`, `routes`, `sql`, `errors`, `flow`,
`orphans`, `mutate`, `constructors`, `literals`, `usages`, `returnusage`,
`schema`, `globals`, `mocks`, `fixtures`, `boundaries`, `httpcalls`, and
`dependents`. An empty files-only result writes zero lines. `--mermaid` is supported by
`callers`, `callees`, `impact`, `endpoint`, `dependents`, `deps`, `path`, and
`coupling`; bare `gograph --mermaid` renders `diagram`. Unsupported or
conflicting output flags fail. Other operational commands (repository `build`,
`wiki`, `gate`, `snapshot`, session create/end/cleanup, installation, help, and
version) remain text. Use global
`--intention` / `-i` to provide the rationale required by analytical CLI
commands during an active audit session.

Successful-result mode support is:

| Modes | Commands |
|---|---|
| JSON, files, Mermaid | `callers`, `callees`, `impact`, `dependents` |
| JSON, files | `query`, `focus`, `node`, `public`, `fields`, `embeds`, `imports`, `implementers`, `envs`, `interfaces`, `concurrency`, `tests`, `routes`, `sql`, `errors`, `flow`, `orphans`, `mutate`, `constructors`, `literals`, `usages`, `returnusage`, `schema`, `globals`, `mocks`, `fixtures`, `boundaries`, `httpcalls` |
| JSON, Mermaid | `path`, `coupling`, `deps`, `endpoint` |
| JSON | `source`, `errorflow`, `trace`, `stale`, `stats`, `summary`, `untested`, `doc`, `doctor`, `godobj`, `skeleton`, `arity`, `complexity`, `context`, `hotspot`, `changes`, `explain`, `plan`, `review`, `risk`, `api`, `check` |
| JSON | `workspace build`, `workspace status`, `workspace query`, `workspace path`, `workspace impact` |
| Raw JSON | `session audit` |
| Mermaid | `diagram`, or bare `gograph --mermaid` |

For commands with several supported presentations, request only one output mode
at a time.
