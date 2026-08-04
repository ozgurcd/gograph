---
title: "gograph"
description: "A local structural graph for safer Go refactors, repository-level analysis, and coding-agent workflows."
---

## Compiler-aware repository context for Go coding agents

gograph builds a local structural graph of a Go repository, with optional
type-checked CHA/SSA enrichment. Use it to trace callers and interface
implementations, plan change impact, and enforce architecture through CLI or
MCP without embeddings or a hosted code index.

**[Try verified gograph output without installing →](/demo/)**

Inspect the precise interface and change-context benchmark, its declared
ground truth, exact commands, and complete raw output. Read the
[methodology and limitations →](/benchmarks/)

```bash
gograph build .           # fast AST graph; tolerates incomplete packages
gograph stats             # verify build health and analysis precision
gograph summary           # repository overview; no symbol name required
gograph hotspot --top 5   # choose a real symbol for context or plan
gograph flow --no-tests   # potential production source-to-sink paths
```

For a compilable repository, run `gograph build . --precise` before a major
refactor. Then pass a real function or method reported by `hotspot`, `summary`,
or `complexity` to `gograph context` and `gograph plan`.

### Real Command Output Example

This captured example shows the output shape for callers of `loadGraph`; symbols and line numbers evolve with the repository:

```text
$ gograph callers loadGraph

[caller] BuildBaselineGraphFromGitRef — calls loadGraph  ->  `return buildGraph(tmpDir)`  (internal/cli/baseline.go) [call @ internal/cli/baseline.go:84]
[caller] NewServer$14 — calls loadGraph  ->  `baselineGraph, err := buildGraph(tmpDir)`  (internal/mcp/server.go) [call @ internal/mcp/server.go:531]
[caller] runAPI — calls loadGraph  ->  `currentGraph, err := loadGraph(".")`  (internal/cli/api.go:11) [call @ internal/cli/api.go:29]
[caller] runArity — calls loadGraph  ->  `g, err := loadGraph(".")`  (internal/cli/cli.go:1341) [call @ internal/cli/cli.go:1352]
[caller] runErrorFlow — calls loadGraph  ->  `g, err := loadGraph(".")`  (internal/cli/cli.go:2046) [call @ internal/cli/cli.go:2064]
[caller] runPlan — calls loadGraph  ->  `g, err := loadGraph(".")`  (internal/cli/cli.go:2433) [call @ internal/cli/cli.go:2450]
[caller] runStats — calls loadGraph  ->  `g, err := loadGraph(".")`  (internal/cli/cli.go:1241) [call @ internal/cli/cli.go:1242]
```



## Designed for focused agent workflows

Text search remains useful for strings, documentation, configuration, and
non-Go files. Language servers provide live compiler-backed navigation and
refactoring. gograph adds persisted repository structure and composed
change-analysis responses for coding agents such as Claude Code, Cursor,
Copilot, Antigravity, and OpenCode.

* **Focused structural evidence**: AST-derived results avoid comments and raw
  string matches when the question is about Go symbols or relationships.
* **Explicit correctness modes**: Build metadata distinguishes AST-only,
  successful precise analysis, and visible precise fallback. A failed precise
  retry does not replace an existing fresh precise artifact covering the same
  selected sources.
* **Composed workflows**: `context`, `plan`, `review`, and `summary` combine
  related graph evidence. Savings depend on the repository and task.




## Install

**Homebrew**
```bash
brew install ozgurcd/tap/gograph
```

**Go install**
```bash
go install github.com/ozgurcd/gograph/cmd/gograph@latest
```

**Official MCP Registry / MCPB (preview)**
```text
io.github.ozgurcd/gograph
```
This installs a platform-specific local MCP bundle in clients that support
MCPB; it does not place the CLI on `PATH`. Select the Go project directory and
the macOS/Linux/Windows amd64/arm64 asset matching the host. See the
[Registry guide](/docs/mcp-registry/) for the current CPU-selection limitation.

**From source**
```bash
git clone https://github.com/ozgurcd/gograph
cd gograph
make build
sudo make install
```

## How it works

1. **`gograph build .`** — first rejects linked/non-regular `go.mod`, `go.sum`, `go.work`, `go.work.sum`, and `vendor/modules.txt` metadata before toolchain use. Applicable `go.work use` members must remain beneath the workspace directory, and their directories, `go.mod`, and optional `go.sum` are validated before `cmd/go`. The shared scanner then excludes linked/special recognized Go build inputs before build selection and AST reads. It extracts symbols, validated call edges, imports, HTTP routes, SQL queries, environment reads, struct fields, error declarations, and typed synchronization primitives. Graph/report publishers require a real `.gograph` directory and regular-or-absent lock entry, stage `graph.json` plus nine reports, rename the reports first, and rename `graph.json` last as the commit marker. Same-directory replacement is atomic on Unix-like systems but is not guaranteed atomic by Go on non-Unix platforms; the ten-file bundle is not one atomic filesystem transaction, so a crash can leave reports ahead of the graph marker. The build adds `.gograph/` to an absent or regular enclosing Git-root `.gitignore` when available and falls back to the build target outside Git; it refuses a link or special file. A zero-file or zero-successful-parse build does not replace existing artifacts; parse failures and selection/security warnings are recorded in graph metadata and make status partial.
2. **Queries and refreshes** — graph-backed CLI analysis reads the last trusted, regular repository-confined `graph.json` with the current source-policy marker and replaces its serialized root with the selected project; commands whose contract reads source, Git, or other local state do so through their documented boundaries. MCP source-analysis tools check source freshness and newer usable persisted artifacts, adopt a newer compatible precise graph, and rebuild after edits in the current requested mode. When no usable artifact exists, persisted-index MCP tools use the startup fallback. Refreshes stay in memory by default. `gograph mcp [path] --persist-refresh` opts into publishing the latest successful refresh with the same graph-last protocol; a failed precise refresh is returned visibly.
3. **`--precise` mode** — attempts type-checked CHA/SSA enrichment. It needs compilable, build-selected packages for precise data; if type/load analysis fails or omits an indexed non-test file, gograph warns and normally records `precise_fallback` on the retained AST graph. A failed retry keeps an existing fresh successful precise artifact covering the same selected sources instead of downgrading it (`ast` identifies an explicitly requested AST build).

## What it captures

| Signal | How extracted |
|---|---|
| Functions, methods, structs, interfaces, types, variables, constants | AST `FuncDecl`, `TypeSpec`, `ValueSpec` |
| Call edges (caller → callee, with call-site file and line) | AST `CallExpr` |
| HTTP routes (method + path + handler) | `gin`, `echo`, `chi`, `http.Handle*` literal patterns |
| SQL queries | String literal heuristics on `db.Query`, `db.Exec`, etc. |
| Environment reads | `os.Getenv`, `os.LookupEnv`, supported Viper `Get*` |
| Struct-field and package-global mutations | Direct assignments plus precise alias, compound, atomic/sync/wrapper, and channel evidence |
| Error and panic sites | `errors.New`, `fmt.Errorf`, sentinel declarations, `panic` |
| Concurrency primitives | `go` statements, channel sends, typed `Mutex`/`RWMutex`/`WaitGroup`/`Once` calls |
| Test edges (test → tested symbol) | `_test.go` call analysis |
| Composite literal sites | `StructName{...}` |
| Security flow facts | Sources, assignments, calls, returns, and sensitive sinks for query-time analysis |

## Why use it?

Use `rg` for text and non-Go searches and
[`gopls`](https://go.dev/gopls/features/mcp) for live compiler-backed
navigation, diagnostics, implementations, and refactoring. gograph complements
them with persisted repository-level questions such as:

- What is the **blast radius** of changing this function?
- What **interfaces** does this struct satisfy?
- What **errors** can this HTTP handler return, and where do they originate?
- Which symbols changed **since my last commit** and what tests cover them?
- Is this function **reachable** from any entry point, or is it dead code?
- Can HTTP, decoded JSON, or environment data reach a **security-sensitive sink**?

These questions require a full in-memory call graph. gograph builds that graph and lets you query it directly from the terminal or from an AI agent via MCP.
