---
title: "Analyzing a Go Codebase with gograph — Starting with Itself"
date: 2026-06-24
description: "A deep-dive technical case study running gograph's static analysis engine on its own codebase. Discover how structural call-graph intelligence eliminates LLM token waste, tracks precise blast radii, and out-performs standard grep."
tags: ["golang", "go", "call-graph", "static-analysis", "developer-tools", "llm-tokens", "mcp-server"]
showToc: true
TocOpen: true
---

## 1. The Static Context Dilemma in AI-Assisted Engineering

Modern software engineering is increasingly co-authored by AI coding agents. However, developers face a systemic bottleneck: **LLM Context Window Bloat and Hallucinations**.

When an agent needs to understand how a function is used, it typically defaults to one of two inefficient strategies:
1. **Primitive Textual Grep**: Running broad text searches that flood the context window with comments, test mocks, markdown references, and unrelated matches.
2. **Whole-File Dumps**: Feeding entire raw source files to the LLM, burning thousands of tokens, increasing processing latency, and inducing model hallucinations.

**`gograph`** was built to solve this. By constructing a localized, persistent Abstract Syntax Tree (AST) call graph, it equips both developers and AI agents with precise structural awareness. Rather than guessing, `gograph` allows queries like *"find the exact callers of this method"* or *"extract only this function's source code block"* in milliseconds.

To demonstrate this structural intelligence in action, we ran `gograph` against its own Go codebase. The numerical results below are a snapshot from 2026-06-24; the repository and counts have continued to evolve.

---

## 2. The Test Subject: gograph Analyzing Itself

We ran the indexer from the root of the `gograph` repository:

```bash
gograph build . --precise
```

The indexing completed in **~400ms**, producing a compact `.gograph/graph.json` snapshot (schema v2):

```text
found 93 Go files to parse
schema_version: 2
packages: 13  files: 93  symbols: 702  calls: 17,748  imports: 457  test_edges: 1,257
```

Within half a second, `gograph` successfully parsed **13 Go packages** and mapped **702 distinct AST symbols** (functions, struct types, interfaces, and methods) interconnected by **17,748 individual call edges** with **1,257 test edges** and **457 import edges**.

The codebase has grown substantially since the initial release, adding new subsystems for session telemetry, architecture boundary enforcement, LLM-wiki generation, and more — all of which are visible in the graph. The graph schema itself has evolved to v2 with richer edge metadata.

Here is how the structural layers of `gograph` coordinate under the hood:

```text
 ┌─────────────────────────────────────────────────────────────┐
 │                      gograph COMPILER                       │
 └──────────────────────────────┬──────────────────────────────┘
                                │
                   1. Parse     ▼
 ┌─────────────────────────────────────────────────────────────┐
 │                      Go Source Code                         │
 └──────────────────────────────┬──────────────────────────────┘
                                │
                    2. Scan     ▼
 ┌─────────────────────────────────────────────────────────────┐
 │                    AST Scanner & Parser                     │
 └──────────────────────────────┬──────────────────────────────┘
                                │
                                ├──────────────────────────────┐
                   CHA Path (Precise)             Fast-Path    │
                                ▼                              ▼
 ┌─────────────────────────────────────────────────────────────┐
 │                  precise.TypeResolver                       │
 └──────────────────────────────┬──────────────────────────────┘
                                │
                                ▼
 ┌─────────────────────────────────────────────────────────────┐
 │                     graph.Graph                             │
 └──────────────────────────────┬──────────────────────────────┘
                                │
                                ▼
 ┌─────────────────────────────────────────────────────────────┐
 │                  .gograph/graph.json                        │
 └──────────────────────────────┬──────────────────────────────┘
                                │
         ┌──────────────────────┴──────────────────────┐
         ▼                                             ▼
 ┌───────────────┐                             ┌───────────────┐
 │ CLI Tools     │                             │ MCP Server    │
 └───────────────┘                             └───────┬───────┘
                                                       │
                                                       ▼ stdio
                                               ┌───────────────┐
                                               │ AI Agents     │
                                               └───────────────┘
```

---

## 3. Comparative Benchmark: Structural Intelligence vs. Primitive Grep

To prove the tangible token savings and precision, we benchmarked standard Unix `grep` against `gograph` queries on the repository itself.

| Objective | Primitive tool (`grep` / `ripgrep`) | gograph Engine | Token Reduction / Noise Reduction |
| :--- | :--- | :--- | :--- |
| **Track callers of `loadGraph`** | `grep -rn "loadGraph" .` <br><br> **158+ matching lines** across comments, markdown docs, imports, and variables. | `gograph callers loadGraph` <br><br> **Exactly 56 structural call sites** mapped with file names and line numbers. | **Massive Noise Reduction** <br> Mapped only true structural callers, removing documentation and comments. |
| **Locate structural definitions** | `grep -rn "Symbol" .` <br><br> **842+ matching lines** (highly noisy; catches every variable and type containing "Symbol"). | `gograph query Symbol` <br><br> **Exactly 83 structured symbols** matching type definitions and method declarations. | **Significant Noise Reduction** <br> Excluded variable assignments, string logs, and noise. |
| **Extract target code block** | `cat internal/search/advanced.go` <br><br> **350+ lines** of the entire file dumped into the LLM context. | `gograph source normalizeSymbolName` <br><br> **Exactly 10 lines of code** returning only the isolated helper function. | **Significant Token Savings** <br> Served only the 10-line helper function instead of the full 350+ line file. |

---

## 4. Deep-Dive: Core Commands & Real-World Codebase Insights

Running `gograph` on itself surfaced structural dependencies and architectural properties that are completely hidden to a simple file viewer.

### 4.1 Structural Blast Centers (`gograph hotspot`)
We queried the most highly-coupled nodes in the graph to find where our highest maintenance risk lay:

```bash
gograph hotspot --top 5
```

```text
Rank   Calls   Symbol Name      Source File
-----------------------------------------------------------------
1.     138     PrintJSON        internal/cli/output.go
2.     120     loadGraph        internal/cli/cli.go
3.      72     errEnvelope      internal/cli/output.go
4.      70     printResults     internal/cli/cli.go
5.      68     formatResults    internal/mcp/server.go
```

**Architectural Insight**: The top five are split between CLI output formatting (`PrintJSON`, `errEnvelope`, `printResults`) and the MCP server's result formatter (`formatResults`). In a CLI-first and MCP-first utility, the presentation layer acts as the primary "blast center." Notably, `errEnvelope` — a function that wraps errors into a consistent MCP response envelope — now ranks #3 with 72 callers, reflecting the growing surface area of the MCP server's tool handlers.

### 4.2 Tracking the Dependency Trail (`gograph callers`)
We traced who invokes our core graph deserializer `loadGraph` to verify state management:

```bash
gograph callers loadGraph
```

The call graph mapped that `loadGraph` is called from **56 locations** across the entire codebase:
- Every CLI command handler (from `runCallers` to `runWiki`, `runRisk`, `runCheck`, etc.).
- The MCP server's `runMCP` and `rebuild` closure.
- The snapshot save/diff engine (`internal/cli/snapshot.go`).
- The CI check and gate command handlers.

At the time of this snapshot, persisted CLI analysis commands — from `callers` to `plan`, `review`, `risk`, `wiki`, `summary`, and `explain` — depended on `loadGraph` to deserialize the graph. The MCP server has a distinct freshness model: source-analysis tools refresh an in-memory graph in the current requested mode, while persisted-index tools retain snapshot semantics.

### 4.3 Auditing Unused Code (`gograph orphans`)
We checked for dead, unreachable, or unexported methods that are safe to delete:

```bash
gograph orphans
```

```text
Found 224 unreachable symbols.
```

**Insight**: In this historical snapshot, reachability analysis reported 224 unreachable production functions or methods. Test-file declarations and non-callable fields are not orphan candidates. `godobj` separately reported 10 structs exceeding at least one enabled method, field, or outgoing-call threshold, including `ExplainResult` and `Graph`.

This is a healthy signal for a growing codebase — the `orphans` and `godobj` commands exist precisely to surface these refactoring targets.

### 4.4 Calculating Refactor Impact (`gograph plan`)
Before making a modification to `internal/parser/parser.go`, we evaluated the exact downstream impact:

```bash
gograph plan ParseFile
```

```text
Change plan for ParseFile (internal/parser/parser.go:41)
===================================================

[DIRECT IMPACT]
  - BuildGraph (internal/cli/cli.go:662)
  - Changes (internal/search/changes.go:137)
  - Complexity (internal/search/complexity.go:106)

[TEST FILES]
  - internal/parser/parser_test.go
  - internal/parser/parser_features_test.go
  - internal/parser/parser_concurrency_test.go
  - internal/parser/parser_httpclient_test.go
  - internal/parser/parser_wrapped_routes_test.go
  - internal/cli/cli_test.go

[RISK ASSESSMENT]
  - Public API: YES
  - Touches SQL: NO
  - Touches Routes: NO
  - Env Vars: NONE
```

Within milliseconds, the engine calculates the direct impact set, the test files that cover the change, and a risk assessment. If we change the signature of `ParseFile`, we immediately know we must audit the CLI build pipeline, the structural changes engine, and the complexity analyzer — plus every parser test file.

### 4.5 Tracing Error Flow Boundaries (`gograph errorflow`)
One of the most complex tasks for an engineer (or AI agent) is tracking how an error propagates or where a specific error is wrapped and returned.

For instance, if we want to trace the origins and handling sites of an `"invalid arguments"` error inside `gograph`:

```bash
gograph errorflow "invalid arguments"
```

The output instantly isolates every single return, wrap, or check site for that error boundary:

```text
ErrorFlow Report for "invalid arguments"
==================================================
34 Return / Wrap / Check Sites:
   - NewServer (internal/mcp/server.go:221) -> error message: invalid arguments
   - NewServer (internal/mcp/server.go:242) -> error message: invalid arguments
   - NewServer (internal/mcp/server.go:263) -> error message: invalid arguments
   ...
   - initNewTools (internal/mcp/server.go:2088) -> error message: invalid arguments
```

**Why this is a major Token Saver**: Reconstructing this error boundary map using textual searches (`grep -rn "invalid arguments" .`) returns a noisy deluge of logs, imports, test mock validations, and markdown strings. `gograph` uses structural AST matching to map only true functional error returns in less than **10ms**, cutting out context window clutter entirely. Notably this codebase now has 34 error sites (up from the initial release) — the MCP server gained `initNewTools` which generates tool handlers dynamically, each with argument validation.

### 4.6 Generating Architectural Narratives (`gograph explain`)
Before editing or refactoring a symbol, we can ask `gograph` to synthesize its entire structural role and relationship to the rest of the codebase in a single line-optimized block:

```bash
gograph explain normalizeSymbolName
```

The output instantly prints:

```text
=== EXPLAIN: github.com/ozgurcd/gograph/internal/search::normalizeSymbolName ===

normalizeSymbolName is a function in package search (internal/search/advanced.go:12).
It is called by 6 production caller(s). It delegates to 4 callee(s).
Cyclomatic complexity: 3 (LOW). No direct test coverage.

ARCHITECTURAL ROLE: Internal Utility.
```

**Token & Cognitive Savings**: Re-compiling this narrative manually requires opening `advanced.go`, reading the function's scope, scanning the package structure, counting external caller references with `grep`, and calculating McCabe Cyclomatic complexity. That process consumes hundreds of lines of file text. `gograph` serves the exact synthesized structural role in **6 lines of clean text**.

### 4.7 Architecture Boundary Enforcement (`gograph boundaries`)
Beyond individual commands, `gograph` now supports architectural layering rules via a `boundaries.json` config:

```bash
gograph boundaries
```

This checks whether any package imports violate declared constraints — for example, ensuring that CLI handler packages never import repository packages directly. When violations are found, the output pinpoints the exact forbidden import edge with file and line number. This can be run in CI as `gograph gate` with configurable thresholds for complexity, coupling, and dead code.

### 4.8 Agent Session Telemetry (`gograph session`)
One of the most unique capabilities is agent compliance tracking. When AI agents work in the repository, `gograph session create` starts a telemetry session that records:

- Whether the agent called `gograph plan` before editing
- Whether it called `gograph review` after editing
- Overall tool success rates
- Session duration and command frequency

At the end of a session, `gograph session audit` grades the agent's behavior from A–F with actionable recommendations:

```text
=== SESSION AUDIT ===
Grade: B
Plan compliance:  100% (always planned before editing)
Review compliance: 75% (missed review on 1 of 4 edits)
Tool success rate: 96%
Recommendation: Add a post-edit shell hook to auto-run gograph review
```

This turns AI-assisted development from a black box into an auditable, measurable process.

---

## 5. AI Agent Context Gating: Drastic Reductions in Token Usage

The biggest leap in developer experience occurs when connecting `gograph` directly to an AI agent (such as **Claude Code**, **OpenCode**, **Cursor**, **Windsurf**, or **Google Antigravity**) via the Model Context Protocol (MCP).

When an agent needs to locate a bug or draft a feature:
1. It queries the `gograph mcp` server over standard I/O.
2. Rather than downloading or scanning the whole repository, the agent receives a highly pruned structural layout containing only the exact call chain, hotspots, and dependencies.
3. The context window remains clean, pristine, and target-focused.

### New in v2: Composite Token-Saving Commands

The schema v2 graph format enables powerful composite commands that collapse 5–8 separate calls into one:

- **`gograph context <symbol>`** — bundles source code, callers, callees, tests, and risk assessment in a single response. Replaces 5 separate tool calls.
- **`gograph plan <symbol>`** — pre-edit planning: direct impact, transitive impact, test coverage, routes, SQL, env vars. Replaces 8 calls.
- **`gograph review --uncommitted`** — post-edit verification: changed symbols, tests, routes, env, risk profile. Replaces 6 calls.
- **`gograph explain <symbol>`** — synthesized architectural narrative with role classification and complexity analysis.
- **`gograph summary`** — single-call codebase briefing: top 3 hotspots, worst instability package, highest complexity function, orphan count, god-object count. Replaces 5 separate calls.

### Evaluation Guidance
The defensible benefit is measurable at the tool boundary: fewer search calls and fewer irrelevant source lines returned for structural questions. Accuracy still depends on the analysis mode and feature limitations; teams should evaluate task success and token use on their own repositories rather than treating static-analysis output as runtime proof.

---

## 6. Try it Today

### 6.1 Installation
Install the static analysis utility using Homebrew:

```bash
brew install ozgurcd/tap/gograph
```

Alternatively, install directly from source (Go 1.26+):

```bash
go install github.com/ozgurcd/gograph@latest
```

### 6.2 Running an Analysis
Initialize the graph repository and query your structures:

```bash
# 1. Build the call graph (runs concurrent AST parser)
gograph build .

# 2. Get a single-call codebase briefing
gograph summary

# 3. Find structural bottlenecks
gograph hotspot --top 10

# 4. Pre-edit change planning
gograph plan YourFunction

# 5. Post-edit verification
gograph review --uncommitted

# 6. Generate LLM-ready wiki
gograph wiki --output llm-wiki
```

### 6.3 Continuous Integration
To enforce structural integrity and prevent dead code, integrate `gograph` directly in your pre-commit hooks or GitHub Actions:

```yaml
- name: Run gograph structural gate
  run: |
    gograph build . --precise
    gograph gate --max-complexity 30 --max-coupling 15
```

By transitioning from primitive text matching to compile-grade AST call-graph awareness, `gograph` brings deterministic codebase navigation back to developers and AI agents alike.
