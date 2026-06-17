# Release Notes

## v1.4.87 — 2026-06-17

### Improvements

#### Working Directory Independence for Cyclomatic Complexity and MCP Tools
Resolved relative path issues in the search and MCP layers. Commands like `complexity` and MCP tools (`gograph_source`, `gograph_changes`, `gograph_context`, `gograph_plan`, `gograph_stale`, and `gograph_impact`) now correctly resolve paths against `g.Root` instead of raw working directory relative paths, ensuring consistent execution when run from subdirectories or external runtimes.

#### MCP Import Cycle Resolution
Fixed a compiler import cycle between the `cli` and `mcp` packages by passing the version string directly as a parameter to the MCP server on startup.

### Fixes

#### ReachableOrphans Refinement
Refined the orphan detection logic to:
- Stop treating exported functions inside `internal/` packages as automatic entry points (roots), since Go prevents them from being imported by external modules.
- Treat `Test...`, `Benchmark...`, and `Fuzz...` functions in test files as roots.
- Exclude all helper symbols and definitions in `_test.go` files from being reported as orphans.

### Documentation

| Target | Changes |
|---|---|
| `RELEASE_NOTES.md` | Added entries for v1.4.85, v1.4.86, and v1.4.87 |
| `internal/cli/cli.go` | Added `httpcalls` documentation to help text and capabilities |

---

## v1.4.86 — 2026-06-17

### Improvements

#### Claude Code Integration Configuration
Integrated plugin metadata (`.claude-plugin/plugin.json`) and marketplace configuration (`.claude-plugin/marketplace.json`) to allow seamless installation of `gograph` as a marketplace tool in Claude Code.

### Documentation

| Target | Changes |
|---|---|
| `README.md` | Documented installation instructions via Claude Code marketplace |
| `RELEASE_NOTES.md` | Added entry for v1.4.86 |

---

## v1.4.85 — 2026-06-17

### New Commands

#### `gograph httpcalls` / MCP `gograph_httpcalls`
Added the `gograph httpcalls` command and corresponding `gograph_httpcalls` MCP tool. It statically extracts all outbound HTTP client calls (`net/http` Get, Post, PostForm, and Head) in the codebase. Allows developers to filter results by HTTP method or URL substring.

**Token-saving benefit:** Helps agents map outbound integrations and third-party dependencies in a single call instead of reading every file or parsing standard libraries.

### Documentation

| Target | Changes |
|---|---|
| `README.md` | Documented `httpcalls` in the infrastructure commands table |
| `docs/coding-agent-usage.md` | Documented `httpcalls` in the cheat sheet and tool registry |
| `RELEASE_NOTES.md` | Added entry for v1.4.85 |

---

## v1.4.84 — 2026-06-14

### Security

#### Path Traversal Prevention in `gograph_boundaries` / `Boundaries` / `CreateBoundaries`
Added explicit input validation to `Boundaries` and `CreateBoundaries` in `internal/search/boundaries.go` to reject config paths that are empty, contain `..` (path traversal), or contain backslashes. The same guard is applied at the MCP layer in `gograph_boundaries` before the path reaches the search layer, providing defence-in-depth.

#### Poisoned Graph File Guard in `Callers`, `Callees`, and `Source`
Added `isSafePathSegment` checks in `internal/search/search.go` before resolving any `File` field from the graph into an absolute path via `filepath.Join`. This prevents a crafted `graph.json` with malicious file paths (e.g. `../../etc/passwd`) from causing arbitrary file reads.

#### Git Reference Validation Refactored in `gograph_api`
Extracted the inline git-ref allowlist check into a dedicated `sanitizeGitRef` function in `internal/mcp/server.go`. The function now uses the pre-compiled package-level `safeGitRef` regex instead of recompiling it on every tool invocation. The validation logic is unchanged.

#### Session Log Argument Redaction
Added `redactArgs` in `internal/session/session.go` to sanitize CLI arguments before writing them to the session audit log. Arguments containing `--config=`, `--session=`, `session_`, `session/`, or `.gograph/` are replaced with `***REDACTED***` to prevent sensitive paths from persisting in telemetry.

### Fixes

#### Dead Code Removal
Removed `sanitizePath` (was defined but never called) and the duplicate `safePathSegment` package variable that had been added alongside the pre-existing one in `internal/mcp/server.go`.

#### Test Line Number Drift
Updated hardcoded line number references in `internal/search/advanced_features_test.go` (`TestComplexity_RealFile`, `TestComplexity_SortedDescending`) to reflect the new position of `func Query` in `search.go` after the `isSafePathSegment` helper was prepended to that file.

### Documentation

| Target | Changes |
|---|---|
| `RELEASE_NOTES.md` | This entry |

---

## v1.4.81 — 2026-06-12

### New Commands

#### `gograph risk` / MCP `gograph_risk`
Added a new `gograph risk` command and corresponding `gograph_risk` MCP tool that calculates a normalized 0–100 change risk score and verdict (`SAFE`, `REVIEW`, or `DANGER`). The risk score is determined by Blast Radius, Cyclomatic Complexity, Test Coverage, Exported API, and Downstream SQL/Env dependencies. This command acts as a primary token-saver, consolidating data from multiple tools into a single action-oriented response.

### Improvements

#### Non-Analytical Command Exemptions from Session Intention
Exempted `gograph capabilities`, `gograph wiki`, and `gograph doc` commands from session technical intention checks (`-i` / `--intention` flag). These commands are non-analytical and do not perform graph modifications or structural changes, so requiring a technical intention was a UX hindrance.

#### String Builder Writes Optimization in `search.Skeleton`
Avoided inefficient string builder concatenations within `sb.WriteString(...)` calls by using separate, simple writes, eliminating temporary allocations and satisfying compiler/IDE warnings.

#### Git Path Resolution in `search.UncommittedSymbols`
Updated the `git` invocation to use `-C g.Root` when `g.Root` is set, ensuring correct path resolution and preventing errors when executing the tool under the MCP server sandbox.

### Fixes

#### Snapshot Name Security Validation
Added character validation checking (`^[a-zA-Z0-9_\-]+$`) to snapshot names for the `gograph snapshot save`, `diff`, and `drop` commands to prevent path traversal attempts.

### Documentation

| Target | Changes |
|---|---|
+| `README.md` | Documented `risk` command in change analysis commands table. |
+| `docs/coding-agent-usage.md` | Documented `risk` command, `gograph_risk` MCP tool, and renumbered sections. |
| `docs/coding-agent-usage.md` | Documented `capabilities`, `wiki`, and `doc` as exempt from intention checks. |
| `RELEASE_NOTES.md` | This file |

---

## v1.4.80 — 2026-06-09

### New Commands

#### `gograph doc <pkg[.Symbol]>` / MCP `gograph_doc`

Thin wrapper around `go doc` that surfaces Go documentation (signature + doc comment) for **any stdlib or third-party symbol** — no graph build required.

**Problem it solves:** When gograph traces a call chain into an external dependency (e.g. `pgx`, `net/http`, `encoding/json`), agents hit a dead wall — the graph only indexes project-internal symbols. Previously the only options were to shell out or hallucinate the signature. `gograph doc` closes this gap in one call.

**Examples:**
```bash
gograph doc fmt.Errorf
gograph doc net/http.HandleFunc
gograph doc io.Reader
gograph doc github.com/jackc/pgx/v5.Conn.QueryRow
gograph doc encoding/json.Unmarshal
gograph doc --json fmt.Errorf      # machine-readable envelope
```

**Key properties:**
- **No graph required** — works even without `.gograph/graph.json`
- **Delegates to `go doc`** — same output as running `go doc` in your shell; supports all go doc query formats
- **Error passthrough** — surfaced directly from `go doc` stderr for clarity

**NOT for project-internal symbols** — use `gograph source` or `gograph context` for those; they provide callers/callees too.

---

### Documentation

| Target | Changes |
|---|---|
| `README.md` | Added Doc row to command table |
| `docs/coding-agent-usage.md` | Added `gograph doc` to cheat sheet |
| `gograph capabilities` | Added "External symbol signature" workflow entry |
| `gograph --help` | Added `doc` command description |
| `internal/mcp/server.go` | Registered `gograph_doc`; added to capabilities list |
| `RELEASE_NOTES.md` | This file |

---

## v1.4.79 — 2026-06-09

### New Commands

#### `gograph untested [--pkg name] [--top N]` / MCP `gograph_untested`

Sweeps the full graph in one pass and returns production functions and methods that have **at least one non-test caller but zero test edges** — the coverage gap invisible to both `orphans` (zero callers) and per-symbol `tests <sym>` queries.

**Key properties:**
- **Distinct from orphans:** Untested symbols *are* called in production code — they are actively exercised but unverified.
- **Risk-sorted:** Results ordered by `caller_count` descending. A function called 60× with no test is higher risk than one called 1×.
- **Test files excluded from both sides:** Only production callers count; only production symbols are checked.

**Flags:**
- `--pkg <name>` — filter by package name or path substring
- `--top N` — limit output to top N results
- `--json` — machine-readable envelope

**Token-saving benefit:** Replaces N sequential `gograph tests <sym>` calls across all 600+ symbols. A single sweep surfaces the entire coverage gap ranked by blast radius risk.

**Example output:**
```
Untested Functions (top 10, sorted by caller count):

FUNCTION                                  PACKAGE       CALLERS  FILE
------------------------------------------------------------------------------------------
PrintJSON                                 cli               60  internal/cli/output.go:37
loadGraph                                 cli               57  internal/cli/cli.go:970
printResults                              cli               34  internal/cli/cli.go:683
```

---

### Architecture

- **`search.Untested(g *graph.Graph) []UntestedResult`** — single graph sweep:
  1. Build `testedIDs` from `g.TestEdges` (both FQ-ID and short-name keys)
  2. Build `callerCount` from `g.Calls` (skipping test files)
  3. Emit any function/method symbol with `callerCount > 0` and not in `testedIDs`
- **`search.UntestedResult`** — typed struct with `name`, `kind`, `file`, `line`, `caller_count`, `package` fields (JSON-tagged)

---

### Documentation

| Target | Changes |
|---|---|
| `README.md` | Added Untested row to command table |
| `docs/coding-agent-usage.md` | Added `gograph untested` to cheat sheet |
| `gograph capabilities` | Added "Test coverage gaps" workflow entry |
| `gograph --help` | Added `untested` command description |
| `internal/mcp/server.go` | Registered `gograph_untested`; added to capabilities list |
| `RELEASE_NOTES.md` | This file |

---

## v1.4.78 — 2026-06-09

### New Commands

#### `gograph summary` / MCP `gograph_summary`

Single-call codebase briefing that replaces the five orientation queries agents run at the start of every session.

**Aggregates in one call:**
- Top 3 hotspots (most-called symbols with caller count)
- Worst instability package (highest Ce/(Ca+Ce) ratio)
- Highest cyclomatic complexity function (score + severity label)
- Total orphan count (unreachable symbols)
- God-object count (structs exceeding method/field/call thresholds)

**Token-saving benefit:** Eliminates `hotspot` + `coupling` + `orphans` + `complexity` + `godobj` (5 tool calls → 1). The session-start workflow in `gograph capabilities` and the MCP `session_start` recommended workflow both now lead with `summary`.

**Output formats:**
```
CODEBASE SUMMARY  (637 symbols, 13 packages)

Hotspots:           PrintJSON (116x), loadGraph (112x), printResults (68x)
Worst instability:  github.com/ozgurcd/gograph/scripts (1.00)
Highest complexity: NewServer (score=138, VERY HIGH)
Orphans:            187 unreachable symbols
God objects:        10
```

Supports `--json` for the standard machine-readable envelope.

---

### Documentation

| Target | Changes |
|---|---|
| `README.md` | Added `summary` row to command table |
| `docs/coding-agent-usage.md` | Added `gograph summary` to cheat sheet |
| `gograph capabilities` | Updated session-start workflow to lead with `summary` |
| `internal/mcp/server.go` | Registered `gograph_summary`; updated `session_start` workflow; added to capabilities list |
| `RELEASE_NOTES.md` | This file |

---

## v1.4.72 — 2026-06-03

### Bug Fixes

#### Root-Aware Graph Loading — Plan/Review Now Work from Subdirectories
`gograph plan` and `gograph review` (and all other query commands) failed when invoked from a subdirectory of the project root:

```
cannot read .../internal/session/.gograph/graph.json — run `gograph build` first
```

**Root cause:** `loadGraph(".")` resolved `"."` to the current working directory via `filepath.Abs()`. When the working directory was a subdirectory, graph.json was sought under `<cwd>/.gograph/graph.json` instead of `<project-root>/.gograph/graph.json`.

**Fix:** `loadGraph` now calls `rootfind.FindRoot()` when invoked with `"."` (the default for all query commands). `FindRoot()` walks up from the current working directory until it finds a `.gograph/` directory, returning the project root. Falls back to `"."` when no `.gograph/` is found (fresh directories, test temp dirs).

This single-point fix in `loadGraph` makes **all ~50 query commands** (plan, review, callers, callees, context, explain, etc.) work from any subdirectory. Explicit path calls (e.g., `gograph build <path>`, `gograph mcp <path>`) are unaffected.

---

### Architecture Improvements

#### New `internal/rootfind` Package
Extracted the root-discovery logic into a dedicated shared package (`internal/rootfind`) to avoid coupling graph loading (a core concern) with session telemetry. Both `internal/cli` and `internal/session` now import `rootfind.FindRoot()` instead of duplicating the walk-up logic.

| Consumer | Before | After |
|---|---|---|
| `internal/session` | Inline `FindGographRoot()` with manual walk-up loop | Thin wrapper delegating to `rootfind.FindRoot()` |
| `internal/cli` | `loadGraph(".")` → `filepath.Abs(".")` → cwd | `loadGraph(".")` → `rootfind.FindRoot()` → project root |

`session.FindGographRoot()` is preserved as a backward-compatible wrapper.

#### `runPlan --with-context` Root Fix
The `--with-context` code path in `runPlan` used `filepath.Abs(".")` to resolve the root for source lookups. Updated to use `rootfind.FindRoot()` so that source file extraction also works from subdirectories.

---

### Test Coverage

#### New `internal/rootfind` Tests (3 tests)
- `TestFindRoot_NoGographDir_FallsBackToCwd` — no `.gograph/` anywhere → returns `"."`
- `TestFindRoot_FromRoot` — cwd = root with `.gograph/` → returns root
- `TestFindRoot_FromSubdirectory` — cwd = nested subdir → walks up, returns root

#### New Subdirectory Graph Loading Regression Tests (4 tests)
- `TestPlanFromRoot` — plan succeeds from repo root
- `TestPlanFromSubdirectory` — plan succeeds from a subdirectory (the key regression)
- `TestReviewFromSubdirectory` — review succeeds from a subdirectory
- `TestSessionAndGraphLoading_SubdirectoryE2E` — full lifecycle: session create at root → chdir into subdirectory → plan with `-i` → review with `-i` → end session → audit → verify `total_commands >= 2`, `success_count >= 2`, `failure_count = 0`, `plan_run = true`, `review_run = true`, grade ≠ F

---

### Documentation

| Target | Changes |
|---|---|
| `README.md` | Added **Subdirectory Aware** feature bullet |
| `docs/coding-agent-usage.md` | Added subdirectory-awareness guarantee to the "Why this is safe" section |
| `gograph capabilities` | Added `Subdirectory safe` entry to the LIMITATIONS block |
| `RELEASE_NOTES.md` | This file |

---

## v1.4.71 — 2026-06-03

### New MCP Tools (CLI↔MCP Parity Completion)

Three CLI commands that previously had no MCP equivalent are now fully accessible via the MCP server, closing the only remaining CLI↔MCP parity gap. The four intentionally CLI-only commands (`gate`, `snapshot`, `add-claude-plugin`, `hook-guard`) remain CLI-only by design.

#### `gograph_trace`
Alias for `gograph_errorflow`, identical behaviour and output schema. Added to the MCP tool registry for backward compatibility with agents that learned the `trace` name from earlier CLI documentation.

- **Parameters:** `term` (required), `no_tests` (bool)
- **Returns:** Same structured JSON as `gograph_errorflow` (definition sites, return sites, likely call paths to entry points)
- **When to use:** Prefer `gograph_errorflow` directly; this tool exists purely so agents trained on older documentation continue to function without modification.

#### `gograph_diagram`
Generates a Mermaid architecture diagram of the repository’s package dependency graph. Equivalent to CLI `gograph diagram`.

- **Parameters:** `group_by` (package/module/service/file, default: package), `max_depth` (int, 0 = unlimited), `include_stdlib` (bool)
- **Returns:** Mermaid diagram text, ready to paste into any Markdown renderer or Mermaid live editor.
- **When to use:** Onboarding to an unfamiliar repository, architecture review, or communicating package structure. Use `group_by=module` for monorepos; `group_by=file` for deep drill-downs. Diagrams with >30 nodes may be hard to read — use `max_depth=2` or a coarser grouping level.

#### `gograph_check`
Runs static policy checks against the repository graph. Equivalent to CLI `gograph check`.

- **Parameters:** `since` (git ref for api\_drift baseline), `uncommitted` (bool), `config` (path to checks.json — defaults to `.gograph/checks.json` if present)
- **Checks performed:** `boundaries` (package layer violations), `api_drift` (breaking changes vs baseline ref), `max_arity`, `max_complexity`, `test_coverage`, `no_orphans`
- **Returns:** Structured JSON with `status` (pass/warn/fail), `findings` array (level, check, message, location), and `summary` counts.
- **When to use:** During PR review or pre-commit analysis to surface policy violations. For CI/CD enforcement requiring a non-zero exit code, use CLI `gograph gate` instead.

**Token-saving benefit:** Agents can run a complete policy surface scan in a single MCP call instead of sequentially invoking `gograph_boundaries`, `gograph_complexity`, `gograph_arity`, and `gograph_orphans` individually.

---

### Scanner Improvement

#### `.agents` Directory Excluded from Walk
`.agents` added to the hardcoded scanner blocklist alongside `.claude` and `.cursor`. Covers generic agent framework scratch and worktree directories that may contain copies of the project source.

**Two-layer defence recap (full picture after v1.4.70 + v1.4.71):**

| Layer | Mechanism | Coverage |
|---|---|---|
| Hardcoded blocklist | `ignoredDirs` map (zero I/O, always active) | `.git`, `.gograph`, `vendor`, `testdata`, `node_modules`, `dist`, `build`, `.terraform`, `.claude`, `.cursor`, `.agents` |
| `.gitignore`-aware pruning | `git check-ignore --quiet <dir>` per directory | Any directory already listed in the repo’s `.gitignore`, including future tool directories not yet in the blocklist |

**Regression test added:** `TestWalk_SkipsAgentsDirs` in `internal/scanner/ignore_test.go`.

---

### Documentation

All mandatory documentation targets updated to reflect the changes introduced in v1.4.70 and v1.4.71:

| Target | Changes |
|---|---|
| `README.md` | Added `gograph diagram` to usage block; added **AI Worktree Safe** feature bullet |
| `docs/coding-agent-usage.md` | Added `diagram`, `check`, `check --uncommitted`, `check --since` to cheat sheet; added `gograph_trace`, `gograph_diagram`, `gograph_check` to MCP tools registry; added AI worktree exclusion note to the safety section |
| `gograph capabilities` | Added `diagram` to QUERY COMMANDS; updated `build` to list excluded dirs; updated `session` entry with `cleanup` subcommand and MCP audit fix note |
| `gograph --help` | Updated `build` with AI worktree exclusion note; updated `session` with `cleanup` and MCP audit note |
| `RELEASE_NOTES.md` | This file |
| `plugin.json` | Description updated to accurately reflect 57 tools; keywords expanded with `architecture-diagram`, `mermaid`, `workflow-telemetry`, `blast-radius`, `code-quality` |

---

## v1.4.70 — 2026-06-03

### Bug Fixes

#### MCP Session Telemetry — Plan/Review Counters Were Always Zero
`gograph_session_audit` reported `total_commands: 0`, `plan_run: false`, and `review_run: false` even when the coding agent had invoked `gograph_plan` and `gograph_review` via MCP.

Root cause: MCP tool handlers called `search.Plan` / `search.Review` directly and completely bypassed `session.LogCommand`. The CLI path already recorded every command at the end of `Run()`, but the MCP path had no equivalent.

**Fix:** The `addTool` closure in `NewServer` now wraps every handler registration with a timing + telemetry shim. It strips the `gograph_` prefix so `"gograph_plan"` records as `"plan"` — matching the CLI convention the audit engine reads. The four session management tools are excluded from recording to avoid noise. One change site covers all 50+ tools.

**Regression test:** `TestMCPSessionTelemetry_PlanAndReviewIncrementCounters` — creates a session, invokes `gograph_plan` and `gograph_review` via their MCP handlers, ends the session, audits, and asserts `total_commands >= 2`, `plan_run = true`, `review_run = true`, `grade != F`.

---

#### Duplicate Symbols from AI Agent Worktrees ([Issue #17](https://github.com/ozgurcd/gograph/issues/17))
When Claude Code (or other agents) create working trees inside the project directory (e.g. `.claude/worktrees/agent-<id>/`), `gograph build` was picking up those files, duplicating every symbol and call edge in the graph and polluting all outputs.

**Fix — two-layer defence:**
1. **Hardcoded blocklist** (always active, zero I/O): `.claude` and `.cursor` added to `ignoredDirs`, joining `.git`, `vendor`, `testdata`, etc.
2. **`.gitignore`-aware directory pruning**: `Walk` now calls `git check-ignore --quiet <dir>` before descending into any directory. If git reports it as gitignored the entire subtree is pruned with `filepath.SkipDir`. This is the general solution — any worktree or scratch directory already listed in `.gitignore` is automatically excluded, including future tool directories. Silently no-ops when git is unavailable.

**Regression tests:** `TestWalk_SkipsClaudeWorktrees` and `TestWalk_SkipsCursorDirs` — both work without git (blocklist layer).

---

### Architecture Improvements

#### Boundary Violations Resolved
Three violations in `.gograph/boundaries.json` caused by the session layer additions:
- `gopkg.in/yaml.v3` → `internal_cli.may_import`
- `internal/session/**` → `internal_cli.may_import`
- `internal/session/**` → `internal_mcp.may_import`

`gograph boundaries` now reports: **"No boundary violations found. Architecture is clean!"**

#### Hollow Pass-Through Wrapper Removed (`internal/cli/session.go`)
Six re-export stubs that added zero logic removed. `cli.go` now imports `internal/session` directly, consistent with `mcp/server.go`.

---

### Test Coverage
- **`internal/session`**: 18 new tests covering the full session lifecycle, `LogCommand` hook-guard filter, `FindMostRecentSessionID`, `CleanupSessions`, and `RunAudit`.
- **`internal/precise`**: 8 new tests covering `Enrich` integration, determinism, invalid-dir handling, and helper functions.

---

## v1.4.69 — 2026-05-30

### New Features

#### Agent Intention Audit & Session Telemetry
Introduced a workflow logging and session tracking engine designed to audit agent behaviors, track compliance with core workflow guidelines, and log tool execution telemetry.
- **CLI Commands:**
  - `gograph session create [word]`: Initiates a telemetry session. Generates IDs in the format `<word>_<timestamp>` (or `<session_slug>_<timestamp>` if the word is omitted).
  - `gograph session end`: Ends the active telemetry session.
  - `gograph session audit [session_id]`: Reads the session log stream, computes agent compliance score (Plan rule 35%, Review rule 35%, Composability 30%), calculates success rates, assigns a compliance grade (A, B, C, F), and renders highly actionable recommendations. Supports `--json` machine parsing.
  - `gograph session cleanup`: Deletes all inactive `.jsonl` files in `.gograph/sessions/` (safely skipping the active session file if one is active) to keep the repository clean.
- **MCP Server Tools:** Added matching `gograph_session_create`, `gograph_session_end`, `gograph_session_audit`, and `gograph_session_cleanup` native tools.
- **Intention Enforcement:** All analytical commands executed while a session is active are blocked unless an intention states their technical rationale via `--intention` / `-i`. Non-analytical commands (like `build`, `session`, `mcp`, `version`, `help`) are exempt from intention enforcement.
- **Append-Only Telemetry Logs:** Log metadata (latency, exit status, intention, command args) is stored in `.gograph/sessions/session_<session_id>.jsonl` with zero execution output bloat to guarantee low I/O overhead.
- **Agent Rules:** Agents are strictly forbidden from reading, listing, or parsing files in the `.gograph/sessions/` directory.

---

### Improvements

#### Asymmetric MCP Route Warning Resolution (Phase A)
- **MCP Descriptions:** Updated tool descriptions for `gograph_endpoint` and `gograph_routes` inside `internal/mcp/server.go` to explicitly outline AST static limitations regarding route-group variable drop behaviors (e.g., Gin, Echo, Chi, and Fiber `Group()` receiver paths).
- **Limitations Telemetry:** Surfaced warning context inside `gograph_capabilities` limitations array to prevent coding agents from suffering route lookup failures.

#### Telemetry Log Noise Reduction
- **Hook Guard Filter:** Skip recording successful `hook-guard` pre-tool use commands in the telemetry session logs. Only failed or blocked hook validations are written, ensuring full security audit capabilities while completely eliminating `.jsonl` file pollution.

---

## v1.4.60 – v1.4.68 — 2026-05-28
Integrated several major analytical and infrastructure hardening sweeps:
- **Symbol Resolution:** Added standard package-qualified dot-notation for all symbol queries (e.g. `service.GenerateRequest`, `graph.Graph`), resolving overload and package disambiguation limits.
- **MCP Server Hardening:** Refactored and hardened all 50 MCP tool schemas with strict usage rules, safety boundaries, and concrete symbol examples to maximize LLM client discovery and prevent hallucinated arguments.
- **Architecture Diagrams:** Implemented the `gograph diagram` command with Mermaid output formats supporting `package`, `module`, `service`, and `file` grouping boundaries.
- **Precision Reachability:** Upgraded graph traversal to track function-value references inside initializers, struct/variable assignments, and nested call expressions.
- **Orphan Sweeps:** Resolved various reachability edge cases inside the AST analysis logic to guarantee highly accurate dead-code identification.

---

## v1.4.59 — 2026-05-22

### Improvements

#### `gograph plan --with-context` / MCP `with_context=true`
When set, `plan` bundles full context (source, callers, callees, role, tests) for every symbol in its `inspect_first` list. Eliminates the N sequential `context` calls that normally follow `plan`.

- CLI: `gograph plan <sym> --with-context` prints the plan then each inspect_first symbol's full context block.
- MCP: `gograph_plan` with `with_context=true` adds `inspect_contexts` array to the response — each entry has `symbol`, `role`, `node`, `source`, `callers`, `callees`, `tests`.
- Works with `--uncommitted` too: `gograph plan --uncommitted --with-context`.

**Token-saving benefit:** Reduces `plan + N×context` (N+1 calls) to a single call. In a typical editing session with 3–5 inspect_first symbols, this saves 3–5 tool calls.

---

#### `gograph context` now includes architectural role
Every `context` response now includes a `role` field — a lightweight architectural classification computed from callers, callees, routes, and SQL already fetched during the call. No extra round trips.

Values: `"HTTP handler"`, `"data access"`, `"entry point"`, `"orchestrator"`, `"coordinator"`, `"utility"`, `"internal"`.

- CLI: displayed on the NODE line as `role: <value>`.
- MCP: included in the `risk` map as `risk.role`.
- `context --uncommitted` also includes `role` per symbol.

**Token-saving benefit:** Eliminates the follow-up `explain` call agents make just to get the architectural role. `context` now answers both "what data do I need?" and "what does this symbol do?" in one call.

---

#### `gograph returnusage <function>` / MCP `gograph_returnusage`
Shows how each caller consumes the return value of a named function. Recorded at parse time by classifying the AST statement wrapping each call site.

Labels: `discarded` (`foo()` standalone), `assigned` (`x := foo()`), `partially_ignored` (`_, err := foo()`), `returned` (`return foo()`), `goroutine` (`go foo()`), `deferred` (`defer foo()`), `passed` (nested inside another call).

- New field `ReturnUsage string` on `graph.CallEdge` (schema-compatible, `omitempty`).
- Parser change: `buildReturnUsageMap` walks the function body at the statement level before the existing call-extraction pass, mapping each `CallExpr.Pos()` to a label.

**Gap this fills:** before changing a return signature (adding an error return, changing a type), an agent needs to know which callers silently discard the return value — those will compile but behave incorrectly. `returnusage` shows this in one call; `callers` alone cannot.

---

#### MCP CLI parity — 17 new MCP tools
Added MCP equivalents for CLI commands that had no MCP counterpart:
`gograph_node`, `gograph_envs`, `gograph_interfaces`, `gograph_tests`, `gograph_hotspot`, `gograph_deps`, `gograph_changes`, `gograph_path`, `gograph_stale`, `gograph_complexity`, `gograph_coupling`, `gograph_mutate`, `gograph_arity`, `gograph_concurrency`, `gograph_fixtures`, `gograph_godobj`, `gograph_skeleton`.

CLI and MCP are now at full functional parity for all query and analysis commands. Remaining CLI-only commands (`check`, `gate`, `snapshot`) are CI/automation tools not appropriate for the MCP surface.

---

### Fix

#### `gograph add-claude-plugin` — unused parameter and stale CLAUDE.md rules
- `installMCPServer` had an unused `home string` parameter; removed.
- `claudeMDBlock` (the rules injected into `~/.claude/CLAUDE.md`) updated to reflect current workflow: `plan with_context=true`, `context uncommitted=true`, and the role field on context responses.

---

### Documentation

- `README.md`: added `plan --with-context`, `returnusage`, updated context description to mention role.
- `docs/coding-agent-usage.md`: added `plan --with-context`, `returnusage` to cheat sheet; added all 17 new MCP tools to MCP tools list; updated `gograph_plan` and `gograph_context` MCP entries.
- `gograph capabilities` and `gograph --help`: updated context, plan, and all 17 new MCP tool entries.

---

## v1.4.58 — 2026-05-22

### New Commands

#### `gograph dependents <package>`
Returns all packages in the repository that import the named package — the inverse of `gograph deps`. Accepts short name, path suffix, or full import path. Case-insensitive. New MCP tool: `gograph_dependents`.

---

#### `gograph literals <struct>`
Finds every composite-literal initialization site for a named struct (`Foo{Field: val}`). Collected at parse time via `ast.CompositeLit` walk. New `LiteralEdge` in `graph.json`. New MCP tool: `gograph_literals`.

**Gap this fills:** `constructors` finds `NewFoo()` but misses struct literals. Adding a required field breaks every literal site at compile time — `literals` finds them all before the change.

---

#### `gograph usages <type>`
Finds every place a named type is referenced in function signatures (param/return), struct fields, and interface method signatures. Word-boundary matching prevents false positives (`AuthService` does not match `AuthServiceImpl`). New MCP tool: `gograph_usages`.

**Gap this fills:** `implementers` shows who satisfies an interface; `usages` shows who *consumes* it — the true blast radius of an interface change.

---

### New Flags

#### `gograph context --uncommitted` / MCP `uncommitted=true`
Bundles context for all uncommitted modified symbols in one call. Replaces 5–8 sequential `context <sym>` calls after `plan --uncommitted`. MCP `gograph_context` `symbol` parameter is now optional — provide either `symbol` or `uncommitted=true`.

---

#### `gograph impact --since <ref>` / MCP `since=<ref>`
Blast radius of all symbols changed since a git ref — the PR-level equivalent of `impact --uncommitted`. Composes `ChangesByGitRef` + `ImpactMultiple` internally. MCP `gograph_impact` also gained `uncommitted` boolean (was CLI-only).

---

### Improvements

#### `make test` extended with security and quality gates
Added to the `test` target: `staticcheck ./...`, `golangci-lint run ./...`, `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`, `grype dir:. --fail-on high`. All four gates run on every `make test`.

---

#### `gograph capabilities` restructured for agent onboarding
Capabilities output reorganised into five labelled sections: PREREQUISITE (build step requirement), COMMON WORKFLOWS (task → command), WHEN TO USE WHAT (disambiguation for overlapping commands), OUTPUT FORMAT (--json/--files-only), and STATIC ANALYSIS LIMITATIONS. Previously a flat command reference; now structured for a new agent reading it cold.

---

#### `gograph implementers <iface> --test-only`
Adds a `--test-only` flag to `implementers`. When set, results are filtered to structs defined in test or mock files — equivalent to the former `mocks` command.

- `gograph mocks <iface>` is now a one-line alias for `gograph implementers <iface> --test-only`. Kept for compatibility.
- MCP: `gograph_implementers` gains an optional `test_only` boolean parameter.
- `gograph_mocks` MCP tool retained for compatibility; description updated.

#### `gograph errorflow <term> --no-tests`
Adds a `--no-tests` flag to `errorflow`. When set, skips collecting `RelatedTests` from test files.

- `gograph trace <term> [--no-tests]` is now a one-line alias delegating to `errorflow`. Kept for compatibility.
- MCP: `gograph_errorflow` gains an optional `no_tests` boolean parameter. CLI and MCP behaviour are now identical.

---

### Fix

#### `gograph_orphans` MCP tool now uses reachability analysis
The MCP tool `gograph_orphans` was calling `search.Orphans` (simple 0-incoming-calls check) while the CLI `gograph orphans` was calling `search.ReachableOrphans` (full BFS from `main`, HTTP routes, and exported symbols). The MCP tool now calls `search.ReachableOrphans`, matching CLI behaviour. The tool description was updated to reflect this.

---

### Documentation

- `README.md`: added `dependents`, `literals`, `usages`, `context --uncommitted`, `impact --since`, `plan --with-context`; updated `mocks`/`trace` as aliases; fixed unclosed code block.
- `docs/coding-agent-usage.md`: updated cheat sheet and MCP tools list for all new commands.
- `gograph capabilities` and `gograph --help`: updated all affected command entries.

---

## v1.4.57 — 2026-05-22

### New Flags

#### `gograph callers <sym> --depth N` and `gograph callees <sym> --depth N`
Extends `callers` and `callees` with bounded BFS traversal up or down the call graph.

- **Default** (`--depth 1`, unchanged): direct callers/callees only.
- **`--depth 2`**: callers of callers (or callees of callees), one extra hop.
- **`--depth N`** (max 10): expands N hops, deduplicating by symbol ID across levels.
- Each result carries `depth N` in the Detail field so output is level-labelled.
- Combines with `--no-tests` as before.
- `--json` returns the standard machine-readable envelope.

**Gap this fills:** `callers` was depth 1, `impact` was unlimited. Agents doing PR review or tracing a narrow change radius now have a middle option — "2–3 hops up" without the full blast radius noise.

**New search functions:** `search.CallersDepth` and `search.CalleesDepth` in `internal/search/search.go`. Depth 1 delegates to the original functions (no behaviour change).

---

### Documentation

- `README.md`: added `--depth` examples to the callers/callees usage block.
- `docs/coding-agent-usage.md`: updated cheat sheet callers/callees entries with `--depth N`.
- `gograph capabilities`: updated callers/callees one-liners with `--depth N`.
- `gograph --help`: updated CALL GRAPH section entries with `--depth N`.


---


## v1.4.56 — 2026-05-22

### New Commands

#### `gograph stats`
Returns a compact index health summary in a single zero-parse call. Reads `graph.json` and emits:
- `schema_version` — graph schema version (currently `"2"`)
- `generated_at` — UTC timestamp of the last `gograph build` run
- `packages`, `files`, `symbols`, `calls`, `imports` — core graph counts
- `routes`, `sqls`, `env_reads`, `test_edges` — domain-specific signal counts

No flags required. Supports `--json` for machine-readable output (standard JSON envelope).

**Token-saving benefit:** Agents can confirm the graph is populated and check its version/timestamp in one call, without reading `GRAPH_REPORT.md` or running `gograph stale`. Typical use: run at the start of any analysis session as a sanity check.

**MCP tool registered:** `gograph_stats` — no arguments, returns the same payload.

---

### New Flags

#### `gograph changes --git <ref>`
Extends the existing `gograph changes` command with a git-ref mode. Instead of comparing file modification times against `graph.json`, it runs `git diff --name-only <ref>` and returns symbols in the changed files.

- **Default mode** (`gograph changes`) is unchanged: mtime vs `graph.json` generated_at.
- **Git-ref mode** (`gograph changes --git <ref>`) returns `[MODIFIED]` symbols from files git reports as changed since that ref.
- Accepts any valid git ref: branch name, tag, commit SHA (e.g. `--git main`, `--git HEAD~5`, `--git v1.4.50`).
- Ref is validated against a positive allowlist `[A-Za-z0-9._/\-~^]+` to prevent injection.
- `NEW` and `DELETED` classification is not available in git-ref mode (requires a full baseline graph build from that ref). A note is printed in text mode.
- Supports `--json` for the standard machine-readable envelope (`query` field is set to the ref).

**Token-saving benefit:** Agents can scope symbol changes to a PR branch (`--git main`) or a release (`--git v1.4.50`) without reading files or rebuilding the graph.

---

### Documentation

- `README.md`: added `stats` to the features list and usage block; updated Change Detection bullet with `--git` flag.
- `docs/coding-agent-usage.md`: added `gograph stats`, `gograph changes --git <ref>` to the cheat sheet; `gograph_stats` to MCP tool registry; expanded change detection section.
- `gograph capabilities`: added `stats` and `changes --git <ref>` entries.
- `gograph --help`: added `stats` to INDEXING section; `changes --git <ref>` to CODE QUALITY section.

---

## v1.4.55 — 2026-05-22

### Other

- fix scripts/gen-release-notes.sh
- style: refactor code and tests with consistent indentation and add CLAUDE.md to .gitignore
- +RELEASE_NOTES.md file


---


## v1.4.55 — 2026-05-22

### Other

- fix scripts/gen-release-notes.sh
- style: refactor code and tests with consistent indentation and add CLAUDE.md to .gitignore
- +RELEASE_NOTES.md file


---


## v1.4.54 — 2026-05-18

### New Commands

#### `gograph explain <symbol>`
LLM-ready architectural narrative for any function, struct, or interface. Synthesizes callers (prod vs test split), callees (cross-package ratio), cyclomatic complexity, SQL exposure, env reads, HTTP routes, concurrency primitives, test coverage, interface satisfaction, and struct metadata into a single prompt-ready prose block with an opinionated role classification (e.g. high-traffic leaf utility, service orchestrator, HTTP handler, data transfer object). Designed to collapse 6-8 separate tool calls into one. Supports `--json`.

#### `gograph gate`
First enforcement command in gograph. Reads thresholds from `.gograph.yml` at the repository root and exits with a non-zero code if any configured metric is violated, making it suitable as a CI/CD pipeline step. Does not trigger a rebuild — operates on the already-built `graph.json`. Warns if the graph is stale.

Supported thresholds:

| Field | Type | Description |
|---|---|---|
| `max_complexity` | integer | Maximum cyclomatic complexity for any single function |
| `max_instability` | float | Maximum instability score (0.0–1.0) for any package |
| `max_god_object_methods` | integer | Maximum methods on any single struct |
| `allow_new_orphans` | bool | If false, any increase in unreachable symbol count fails the gate |
| `max_new_coupling_edges` | integer | Maximum new import edges versus the last build |

Each check prints a pass/fail status line with the configured threshold, actual worst value, and location. Baseline orphan and coupling edge counts are captured automatically on each `gograph build` run.

#### `gograph snapshot`
Captures the current architectural metric state under a named label. Snapshots are stored in `.gograph/snapshots/` as JSON files.

Subcommands:

| Subcommand | Description |
|---|---|
| `snapshot save <name>` | Capture metrics (symbols, orphans, god objects, complexity, instability, coupling edges) |
| `snapshot diff <name>` | Compare current graph against a snapshot — marks each metric as improved or WORSE |
| `snapshot list` | Tabular list of all saved snapshots |
| `snapshot drop <name>` | Delete a named snapshot |

Useful for tracking architectural health trends across a sprint, measuring refactor impact, or generating PR-level regression data.

---

### Improvements

- **Graph baseline persistence**: `gograph build` now captures the previous orphan count and coupling edge count before overwriting `graph.json`. This baseline is embedded in the new graph and consumed by `gograph gate` for delta comparisons — no separate state file required.
- **MCP server**: `gograph_explain` registered as a first-class MCP tool alongside all existing tools. Capabilities registry updated for agent auto-discovery.

---

### Documentation

- `README.md`: added `gate` and `snapshot` command examples to the command reference block.
- `docs/coding-agent-usage.md`: added `explain`, `gate`, and all four `snapshot` subcommands to the AI agent cheat sheet and MCP tool registry.
- `gograph capabilities`: updated with `gate` and `snapshot` entries.
- `gograph --help`: updated CODE QUALITY section with `gate` and `snapshot` descriptions.

---

## v1.4.53 — 2026-05-17

### New Commands

#### `gograph explain <symbol>`
*(Initial implementation shipped in this tag — see v1.4.54 for full description.)*

---

## v1.4.49 — 2026-05-16

### Fix

- **MCP auto-build on startup**: `gograph mcp` now automatically runs a graph build when started if no `graph.json` is found. Prevents agents from receiving empty results on a fresh clone without a manual build step.
- **Plugin installer path**: `gograph add-claude-plugin` now uses the absolute project path when writing the MCP server config, preventing path resolution failures when Claude Desktop launches from a different working directory.

---

## v1.4.47 — 2026-05-15

### New Commands

#### `gograph add-claude-plugin`
Single command that performs three installation steps:
1. Registers the MCP server in `claude_desktop_config.json` (Claude Desktop).
2. Injects steering rules into `~/.claude/CLAUDE.md` so Claude knows to use `gograph_*` tools instead of `grep` for Go symbol searches.
3. Installs a smart `PreToolUse` hook at `~/.claude/hooks/gograph-guard.sh` that intercepts `grep`/`rg` calls targeting Go symbols and redirects Claude to the appropriate `gograph` MCP tool.

The hook only intercepts patterns that look like Go identifiers (PascalCase/camelCase, 3+ characters). Legitimate searches in YAML, Markdown, SQL, or comment files are passed through unchanged.

---

## v1.4.45 — 2026-05-14

### New Commands

#### `gograph check`
Static policy checks using `.gograph/checks.json`. Supports `--uncommitted` to include staged changes and `--since <ref>` to include API drift against a baseline git reference.

#### `gograph boundaries`
Enforce package architecture layering constraints using `.gograph/boundaries.json`. Exits non-zero and prints the violating file if any package imports a layer it is not permitted to depend on. `--create` auto-generates a baseline `boundaries.json` from the current import graph.

---

## v1.4.44 — 2026-05-13

### Improvements

- **MCP tool parity**: expanded MCP server to full parity with CLI. All major query commands now registered as MCP tools. Capabilities registry made machine-readable for agent auto-discovery.

---

## v1.4.42 — 2026-05-12

### New Commands

#### `gograph endpoint <route>`
Full vertical slice for a single HTTP endpoint. Composes route resolution, handler symbol lookup, full BFS callee chain, SQL emitted, and env vars read into one response. Supports `--depth N`, `--json`, and `--include-tests`. Accepts a route pattern (e.g. `POST /api/users`) or a handler symbol name. Handler name is preferred — route pattern lookup only resolves flat string literals and fails with grouped routers (Gin Group, Echo Group, Chi).

---

## v1.4.41 — 2026-05-11

### New Commands

#### `gograph api --since <ref>`
Detects breaking API and contract changes between the current graph and a git reference. Identifies removed exported functions, changed signatures, and deleted types.

---

## v1.4.40 — 2026-05-10

### Fix

- **Multiline SQL in markdown report**: SQL queries containing newlines or carriage returns (raw string literals with embedded line breaks) now have whitespace collapsed before insertion into the markdown table. Fixes malformed report output.

---

## v1.4.39 — 2026-05-09

### New Commands

#### `gograph errorflow <term>`
Traces an error string heuristically from its definition up through the call chain to HTTP handlers. Complements `gograph trace` (which traverses backwards from entry points). Uses AST heuristics — no SSA required. Accepts a search term matching error message text.

#### `gograph review <symbol>` / `gograph review --uncommitted`
Post-edit review report. Aggregates the current AST state of modified files and answers: are all callers tested, did complexity increase, were new SQL or env reads introduced, were any interfaces broken. Run after editing, before committing.

---

## v1.4.38 — 2026-05-08

### New Commands

#### `gograph plan <symbol>` / `gograph plan --uncommitted`
Pre-edit change plan. Aggregates callers, tests, blast radius, SQL/env/route exposure into a single checklist before modifying a symbol. Designed to be run before any edit as the primary safety check.

#### `gograph boundaries` *(initial)*
*(See v1.4.45 for full release notes.)*

---

## v1.4.37 — 2026-05-08

### Fix

- **`gograph trace` performance**: rewrote the trace engine to use a precomputed reverse adjacency map and a single reverse BFS per matched error. Previous implementation performed a full forward BFS from every entry point to every error instance, causing combinatorial explosion on large codebases. Now resolves instantly regardless of codebase size.

---

## v1.4.36 — 2026-05-08

### Fix

- **Precise call graph enrichment**: `gograph build --precise` no longer overwrites the heuristic call edges collected in the base build. Enrichment is now additive — type-checked edges are merged in without discarding AST-inferred edges.

---

## v1.4.35 — 2026-05-08

### New Commands

#### `gograph fixtures <pkg>`
Find test helper structs and functions in test files for a given package. Distinct from `gograph tests` (which maps coverage to a symbol) — `fixtures` surfaces the test infrastructure itself.

#### `gograph globals <pkg>`
Find package-level variables, constants, and the functions that mutate them. Extended to include constants in this release.

---

## v1.4.32 — 2026-05-08

### New Commands

#### `gograph source <symbol>` — polymorphic method support
`gograph source` now returns all concrete implementations of a method when the named symbol is defined on an interface. Previously returned only the interface definition.

---

## v1.4.31 — 2026-05-08

### Improvements

- **`--files-only` flag**: all search and query commands now accept `--files-only`, which strips all structural output and returns a flat deduplicated list of file paths. Token-efficient for building file checklists without full context.