---
title: "gograph"
description: "Local AST-based Go codebase analysis tool for token reduction in AI agents. Build a semantic graph of your Go repository to dramatically cut context window costs."
---

## What is gograph?


gograph walks a Go repository, parses selected `.go` files, and stores a structured graph in `.gograph/graph.json`. CLI analysis reads that persisted snapshot. The MCP server loads or auto-builds an in-memory graph, checks source freshness and newer artifacts per source-analysis call, and rebuilds after edits in the current requested mode; persisted-index tools (`stale`, default `changes`, and `stats`) retain CLI semantics. Default AST analysis does not execute target code; precise mode and `doc` invoke the local Go toolchain.

```bash
gograph build .                        # index the repo — fast, tolerates broken code
gograph build . --precise              # type-checked CHA — use before refactors
gograph callers ValidateToken          # who calls this?
gograph plan HandleLogin               # safe-edit plan before changing a function
gograph errorflow "invalid token"      # trace an error to the HTTP layer
gograph flow --no-tests                # potential untrusted-data security paths
gograph changes --git main             # what changed since main?
```

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



## 🧠 Designed for AI Agents (Massive Token Reduction & Context Savings)

AI coding assistants and agent systems (like Claude Code, Cursor, Copilot, Google Antigravity, and OpenCode) are highly habituated to using standard Unix tools (`grep`, `find`) to search and navigate Go repositories. In large Go codebases, this is highly inaccurate, slow, and expensive.

gograph completely transforms this dynamic:

* **Less Search Noise**: Generic `grep` mixes declarations with mocks, logs, comments, and strings. gograph returns AST-derived relationships, with documented heuristic limits for dynamic dispatch, routes, tests, and error flow.
* **Dramatic Token Reduction**: By replacing broad text scans with precise, symbol-focused graph queries, gograph drastically reduces the context size sent to the model, saving significant token overhead.
* **Agent-Oriented Workflows**: Composed tools such as `context`, `plan`, `review`, and `summary` reduce repeated exploration calls and keep structural evidence compact.




## Install

**Homebrew**
```bash
brew install ozgurcd/tap/gograph
```

**Go install**
```bash
go install github.com/ozgurcd/gograph@latest
```

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

Standard tooling — `grep`, `find`, language servers — answer file-level questions. gograph answers structural questions:

- What is the **blast radius** of changing this function?
- What **interfaces** does this struct satisfy?
- What **errors** can this HTTP handler return, and where do they originate?
- Which symbols changed **since my last commit** and what tests cover them?
- Is this function **reachable** from any entry point, or is it dead code?
- Can HTTP, decoded JSON, or environment data reach a **security-sensitive sink**?

These questions require a full in-memory call graph. gograph builds that graph and lets you query it directly from the terminal or from an AI agent via MCP.
