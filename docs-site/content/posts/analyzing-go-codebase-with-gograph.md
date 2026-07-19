---
title: "Analyzing a Go Codebase with gograph — Starting with Itself"
date: 2026-06-24
description: "A historical, reproducible snapshot of gograph analyzing its own Go codebase, with structural-query examples and documented limitations."
tags: ["golang", "go", "call-graph", "static-analysis", "developer-tools", "llm-tokens", "mcp-server"]
showToc: true
TocOpen: true
---

## 1. The Static Context Dilemma in AI-Assisted Engineering

Modern software engineering is increasingly co-authored by AI coding agents.
Those agents need both textual search and structural evidence to navigate a
repository efficiently.

When an agent needs to understand how a function is used, it typically defaults to one of two inefficient strategies:
1. **Broad textual search**: Useful for literals and non-Go content, but it also
   returns comments, test mocks, Markdown references, and unrelated same-name
   matches when the question is structural.
2. **Whole-file reads**: Sometimes necessary, but often larger than the
   function or relationship needed for the immediate task.

**`gograph`** adds a local, persisted Abstract Syntax Tree (AST) graph, with
optional type-checked enrichment. It supports questions such as *"show the
indexed callers of this method"* or *"extract this function's source block"*.
Its output remains static-analysis evidence with documented limits.

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

## 3. Historical Output Comparison: Structural Queries and Grep

The following point-in-time comparison records output sizes from standard Unix
`grep` and gograph queries on the repository itself. The commands answer
different questions, so the counts illustrate filtering and payload shape—not
relative correctness or end-to-end agent token use.

| Objective | Text search (`grep` / `ripgrep`) | gograph query | Observed output difference |
| :--- | :--- | :--- | :--- |
| **Track callers of `loadGraph`** | `grep -rn "loadGraph" .` <br><br> **158+ matching lines** across comments, Markdown docs, imports, and variables. | `gograph callers loadGraph` <br><br> **56 AST-derived call-site rows** with file names and line numbers. | Fewer rows for the structural question; text matches remain useful for other questions. |
| **Locate structural definitions** | `grep -rn "Symbol" .` <br><br> **842+ matching lines** across identifiers, comments, and documentation. | `gograph query Symbol` <br><br> **83 indexed symbol results** in this snapshot. | About 90% fewer rows in this run; result semantics differ. |
| **Extract target code block** | `cat internal/search/advanced.go` <br><br> **350+ lines** of the whole file. | `gograph source normalizeSymbolName` <br><br> The isolated **10-line function block** in this snapshot. | A smaller payload when only that function is needed. |

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
We checked for indexed functions and methods that were not reachable from the
configured static entry points. Every result still requires review before
deletion:

```bash
gograph orphans
```

```text
Found 224 unreachable symbols.
```

**Insight**: In this historical snapshot, reachability analysis reported 224 unreachable production functions or methods. Test-file declarations and non-callable fields are not orphan candidates. `godobj` separately reported 10 structs exceeding at least one enabled method, field, or outgoing-call threshold, including `ExplainResult` and `Graph`.

This is an inventory of review candidates, not proof that the symbols are dead
at runtime. The `orphans` and `godobj` commands help prioritize that review.

### 4.4 Calculating Refactor Impact (`gograph plan`)
Before modifying `internal/parser/parser.go`, we evaluated indexed downstream
impact candidates:

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

In this captured run, the engine reported the direct impact set, statically
mapped test files, and a risk assessment. A signature change to `ParseFile`
would make the CLI build pipeline, structural changes engine, complexity
analyzer, and listed tests review priorities; the report does not replace
compilation and test execution.

### 4.5 Tracing Error Flow Boundaries (`gograph errorflow`)
One of the most complex tasks for an engineer (or AI agent) is tracking how an error propagates or where a specific error is wrapped and returned.

For instance, if we want to trace the origins and handling sites of an `"invalid arguments"` error inside `gograph`:

```bash
gograph errorflow "invalid arguments"
```

In this snapshot, the heuristic reported 34 return, wrap, or check candidates:

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

**Why the focused output can help**: A textual search for `"invalid
arguments"` also returns documentation, tests, and unrelated strings. The
error-flow query returns AST-derived candidates connected through the indexed
call graph. It is a navigation heuristic, not proof of every runtime error
path.

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

**Compact context**: Building the same summary manually can require several
navigation and source reads. `gograph explain` combines the indexed role,
callers, callees, and complexity in one response; whether that saves calls or
tokens depends on the task and client.

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

## 5. Composed Context for AI Agents

The biggest leap in developer experience occurs when connecting `gograph` directly to an AI agent (such as **Claude Code**, **OpenCode**, **Cursor**, **Windsurf**, or **Google Antigravity**) via the Model Context Protocol (MCP).

When an agent needs to locate a bug or draft a feature:
1. It queries the `gograph mcp` server over standard I/O.
2. The agent receives a focused structural response containing indexed call
   relationships, hotspots, or dependencies for the selected query.
3. The agent can then inspect source where the static evidence is incomplete or
   ambiguous.

### Composite Agent Commands

The schema v2 graph format supports commands that combine several related
evidence categories in one response:

- **`gograph context <symbol>`** — bundles source code, retained callers and
  callees, statically mapped tests, and risk evidence.
- **`gograph plan <symbol>`** — combines direct and transitive impact with test,
  route, SQL, environment, and risk candidates.
- **`gograph review --uncommitted`** — combines changed symbols with mapped
  tests, routes, environment reads, and a risk profile.
- **`gograph explain <symbol>`** — synthesized architectural narrative with role classification and complexity analysis.
- **`gograph summary`** — repository briefing with hotspots, instability,
  complexity, orphan candidates, and god-object candidates.

### Evaluation Guidance
The defensible benefit is measurable at the tool boundary: fewer search calls and fewer irrelevant source lines returned for structural questions. Accuracy still depends on the analysis mode and feature limitations; teams should evaluate task success and token use on their own repositories rather than treating static-analysis output as runtime proof.

---

## 6. Try it Today

### 6.1 Installation
Install the static analysis utility using Homebrew:

```bash
brew install ozgurcd/tap/gograph
```

Alternatively, install directly from source (Go 1.26.5+):

```bash
go install github.com/ozgurcd/gograph/cmd/gograph@latest
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

Use text search, `gopls`, and gograph together: text search for literals and
non-Go content, `gopls` for live compiler-backed workspace operations, and
gograph for persisted repository-level analysis and composed agent workflows.
