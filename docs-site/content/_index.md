---
title: "gograph"
description: "Local AST-based Go codebase analysis tool for token reduction in AI agents. Build a semantic graph of your Go repository to dramatically cut context window costs."
---

## What is gograph?


gograph is a CLI tool that walks a Go repository, parses every `.go` file with the standard AST, and stores the result as a structured graph in `.gograph/graph.json`. Every query reads from that graph — no re-parsing, no network calls, no external services.

```bash
gograph build .                        # index the repo — fast, tolerates broken code
gograph build . --precise              # type-checked CHA — use before refactors
gograph callers ValidateToken          # who calls this?
gograph plan HandleLogin               # safe-edit plan before changing a function
gograph errorflow "invalid token"      # trace an error to the HTTP layer
gograph changes --git main             # what changed since main?
```

### Real Command Output Example

Here is the actual, exact output when querying `gograph` for callers of its index-loading function, `loadGraph`:

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

* **No Search Noise**: Generic `grep` returns hundreds of lines of mocks, test logs, and comments. gograph queries the compiled AST database directly, returning 100% accurate, ground-truth relationships.
* **Dramatic Token Reduction**: By replacing broad text scans with precise, symbol-focused graph queries, gograph drastically reduces the context size sent to the model, saving significant token overhead.
* **Production-Proven Performance**: In real-world production evaluations running swarms of **35 specialized Claude Code agents** on large Go codebases, replacing raw `grep` across 80+ endpoints with gograph's pruned, AST-accurate context **dropped the measured hallucination rate from ~12% down to 2%**.




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

1. **`gograph build .`** — walks all `.go` files concurrently, extracts symbols, call edges, imports, HTTP routes, SQL queries, environment reads, struct fields, error declarations, and concurrency primitives. Writes everything under the target `.gograph/` directory, adds `.gograph/` to the enclosing Git repository root `.gitignore` when available, falls back to the build target `.gitignore` outside Git, and exits without artifacts if no Go files remain after ignore filtering.
2. **Query commands** — read from `graph.json` in memory. All queries are millisecond-fast regardless of repository size.
3. **`--precise` mode** — runs the full Go type checker (CHA) on top of the AST pass for accurate interface dispatch resolution. Slower, requires compilable code.

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

## Why use it?

Standard tooling — `grep`, `find`, language servers — answer file-level questions. gograph answers structural questions:

- What is the **blast radius** of changing this function?
- What **interfaces** does this struct satisfy?
- What **errors** can this HTTP handler return, and where do they originate?
- Which symbols changed **since my last commit** and what tests cover them?
- Is this function **reachable** from any entry point, or is it dead code?

These questions require a full in-memory call graph. gograph builds that graph and lets you query it directly from the terminal or from an AI agent via MCP.
