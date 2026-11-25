---
title: "gograph vs gopls, golangci-lint, go-callvis & ripgrep"
weight: 4
date: 2026-11-25
lastmod: 2026-11-25
description: "An engineering comparison of gograph vs gopls, golangci-lint, go-callvis, and ripgrep: structural call graphs vs LSP navigation, lint rules, and text search."
---

`gograph` does not replace your language server, linter, or text search tool. Each tool answers a different class of question; `gograph` covers repository-wide structural queries, transitive impact, and CI architecture gates.

---

## Comparison Matrix

| Capability | `gograph` | `gopls` | `golangci-lint` | `go-callvis` | `ripgrep` (`rg`) |
|---|---|---|---|---|---|
| **Primary Role** | Persisted call graph, blast radius & CI architecture gates | Live editor LSP (autocomplete, rename, diagnostics) | File/package lint rules & style checks | Graphviz call-graph rendering | Literal & regex text search |
| **Non-Compiling Code** | Yes (AST mode); `--precise` adds CHA/SSA | Partial (requires valid workspace) | No (most linters require type-checking) | No (requires full SSA build) | Yes |
| **Transitive Blast Radius (`impact`)** | Built-in CLI & MCP | Single-hop references | No | Visual only | No |
| **Change-Scoped Test Selection** | Built-in (`tests --uncommitted --format go-test`) | No | No | No | No |
| **CI Package Layer Boundaries** | Built-in (`boundaries`, `gate`, `validate`) | No | Via plugins (`depguard`) | No | No |
| **HTTP Route / SQL / Env / Flow Census** | Built-in | No | Limited (per-linter) | No | Regex heuristic only |
| **Machine-Readable JSON & Mermaid** | Native (`--json`, `--mermaid`, `--files-only`) | LSP protocol / experimental MCP | Lint reports (JSON/SARIF) | DOT / SVG / PNG | Line matches |
| **Built-In MCP Server** | 70 project + 4 workspace tools | Experimental MCP | No | No | No |

---

## `gograph` vs `gopls`

[`gopls`](https://go.dev/gopls/) is the official Go language server. It handles **cursor-level, live compiler operations** inside an editor: go-to-definition, rename refactoring, hover types, live diagnostics, and exact single-hop references.

`gograph` operates at the **repository graph level**:

- **Transitive impact:** `gopls` reports direct references to `Save`. `gograph impact Save` traces the upstream call tree, affected HTTP routes, and mapped tests.
- **Composed responses:** `gograph plan`, `gograph context`, and `gograph review --uncommitted` bundle source, callers, callees, tests, SQL, routes, and risk signals into a single bounded output.
- **Headless CI scripting:** `gograph` persists a fingerprinted graph (`.gograph/graph.json`) that CI jobs evaluate (`gograph boundaries`, `gograph gate`, `gograph api --since main`) without running an editor daemon.

**How to combine them:** Keep `gopls` for interactive IDE editing and compiler-exact refactoring; use `gograph` for blast-radius analysis, targeted test selection, CI architecture enforcement, and MCP queries.

---

## `gograph` vs `golangci-lint`

[`golangci-lint`](https://golangci-lint.run/) runs linters that detect bugs, error-handling mistakes, formatting issues, and performance pitfalls (`errcheck`, `staticcheck`, `govet`, `revive`, and others).

`gograph` complements it in CI with **structural and topological gates**:

- **Layer architecture:** `gograph boundaries --create` snapshots current module layers into `gograph/boundaries.json` so teams can remove unwanted cross-layer imports incrementally.
- **Repository metrics:** Track package instability (`FanOut / (FanIn + FanOut)`), god objects, unreachable orphan candidates, and exported API drift against `origin/main`.
- **Structural predicates:** `gograph validate` enforces closed invariants—such as restricting calls to `os/exec::Command` to a single package—with exact file:line failure output.

**How to combine them:** Run `golangci-lint` for local code correctness and style; run `gograph boundaries` and `gograph gate` for architectural integrity.

---

## `gograph` vs `go-callvis`

[`go-callvis`](https://github.com/ondrajz/go-callvis) renders full-program SSA call graphs through Graphviz, which is useful for visually inspecting a small service.

On larger repositories, full-program graphs become dense, and `go-callvis` requires a compiling SSA build and cannot be queried programmatically.

`gograph` makes the call graph **queryable and sliceable**:

- Render only the subgraph relevant to a change (`gograph callers Foo --depth 2 --mermaid` or `gograph path HandleOrder ChargeCard --mermaid`).
- Emit structured JSON (`--json`) or deduplicated file lists (`--files-only`) for scripts and CI.
- Fall back to fast AST parsing when working on incomplete code.

---

## `gograph` vs `ripgrep` (`rg`)

`ripgrep` is the right tool for string literals, comments, YAML/JSON configs, Markdown docs, and non-Go files.

For **Go structural questions**—such as callers of a specific method or types implementing an interface—text search mixes comments, unrelated same-name methods, and test mocks, and cannot resolve implicit Go interface satisfaction.

`gograph` resolves declarations, call edges, struct fields, and CHA interface implementations directly from Go's AST and type checker.

---

## Recommended Go Tooling Stack

1. **`gopls`** in the editor for live navigation, diagnostics, and symbol renames.
2. **`ripgrep`** for literal strings, configuration files, and non-Go assets.
3. **`golangci-lint`** in CI for file- and package-level lint rules.
4. **`gograph`** in the terminal, CI, and MCP clients for call-graph blast radius, targeted test selection, security-flow triage, and architecture gates.

Next: **[Getting Started](/docs/getting-started/)** · **[CI Architecture Gates](/docs/ci-architecture-gates/)**.
