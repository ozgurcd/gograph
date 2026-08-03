---
title: "gograph"
description: "A local structural graph for safer Go refactors, repository-level analysis, and coding-agent workflows."
---

## Compiler-aware repository context for Go coding agents

gograph builds a local structural graph of a Go repository, with optional
type-checked CHA/SSA enrichment. Use it to trace callers and interface
implementations, plan change impact, and enforce architecture through CLI or
MCP without embeddings or a hosted code index.

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
  successful precise analysis, and visible precise fallback.
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

1. **`gograph build .`** — walks Go files selected by the shared scanner, extracts symbols, validated call edges, imports, HTTP routes, SQL queries, environment reads, struct fields, error declarations, and typed synchronization primitives. Writes everything under the target `.gograph/` directory, adds `.gograph/` to the enclosing Git repository root `.gitignore` when available, and falls back to the build target `.gitignore` outside Git. A zero-file or zero-successful-parse build does not replace existing artifacts; partial failures are recorded in atomically published graph metadata.
2. **CLI query commands** — read persisted `graph.json`; rebuild after source changes. MCP source-analysis tools check source freshness and newer persisted artifacts, adopt a newer precise graph, and rebuild after edits in the current requested mode. A failed precise refresh is returned visibly.
3. **`--precise` mode** — attempts type-checked CHA/SSA enrichment. It needs compilable, build-selected packages for precise data; if type/load analysis fails or omits an indexed non-test file, gograph warns, retains the AST graph, and records `precise_fallback` (`precise` and `ast` identify the other modes).

## What it captures

| Signal | How extracted |
|---|---|
| Functions, methods, structs, interfaces, types, consts | AST `FuncDecl`, `TypeSpec` |
| Call edges (caller → callee, with call-site file and line) | AST `CallExpr` |
| HTTP routes (method + path + handler) | `gin`, `echo`, `chi`, `http.Handle*` literal patterns |
| SQL queries | String literal heuristics on `db.Query`, `db.Exec`, etc. |
| Environment reads | `os.Getenv`, `viper.Get*` |
| Struct field mutations | AST `AssignStmt` on selector expressions |
| Error declarations and return sites | `errors.New`, `fmt.Errorf`, `panic` |
| Concurrency primitives | `go func`, `sync.Mutex`, channel ops, `WaitGroup` |
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
