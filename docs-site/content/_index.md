---
title: "gograph"
date: 2026-05-23
lastmod: 2026-11-25
description: "Local Go static analysis, call-graph, blast-radius, test-selection, and CI architecture-gate CLI, with a built-in stdio MCP server."
---

## Call-Graph and Architecture Analysis for Go

Before refactoring a Go codebase, engineers need structural answers that text search and single-hop references do not provide: which callers break if a signature changes, which tests transitively reach a modified function, and whether a pull request introduces a forbidden package import or a complexity regression.

`gograph` indexes a Go repository locally into an AST and optional type-checked (CHA/SSA) dependency graph. Query it from the terminal, enforce architectural constraints in CI, or expose the same graph to AI coding agents over stdio MCP—without hosted services, embeddings, or target-code execution.

**[Interactive browser demo →](/demo/)** · **[CI Architecture Gates →](/docs/ci-architecture-gates/)** · **[Compare with gopls, golangci-lint & ripgrep →](/docs/comparisons/)**

---

### Core Workflows

#### 1. Blast-Radius and Call-Graph Inspection
Trace transitive callers, interface implementations, and call paths before changing a shared function or interface. Render any call or package dependency slice as a Mermaid diagram.

```bash
gograph build . --precise
gograph impact Store.Save
gograph callers Store.Save --mermaid
gograph path HandleCheckout CommitLedger --mermaid
```

#### 2. Targeted Test Selection and Coverage Gaps
Map transitive static test reachability for uncommitted changes and combine it with `go test -coverprofile` to locate unexecuted lines.

```bash
# Print runnable `go test -run` commands for uncommitted changes
gograph tests --uncommitted --format go-test --max-depth 3

# List called production symbols with no static test path
gograph untested --exclude-interface-methods --top 20

# List changed lines that a coverage profile never executed
gograph uncovered --uncommitted --coverprofile cover.out
```

#### 3. CI Package Boundaries and Quality Gates
Commit layer import rules in `gograph/boundaries.json` and metric thresholds in `.gograph.yml` to enforce repository structure in CI.

```bash
gograph boundaries --create   # Snapshot current package layers into gograph/boundaries.json
gograph gate init             # Scaffold .gograph.yml complexity/instability/god-object limits
gograph build . --precise --strict && gograph boundaries && gograph gate
```

See the [CI Architecture Gates guide →](/docs/ci-architecture-gates/) for policy schemas and GitHub Actions configuration.

#### 4. Built-In Stdio MCP Server
For teams using Claude Code, Cursor, Codex, Copilot, or Antigravity, the same binary provides a 70-tool local stdio MCP server (`gograph mcp .`). It builds an in-memory graph on first use and refreshes changed packages automatically, with no manual build step required.

See [MCP & AI Agent Integration →](/docs/agent-integration/) or [Official MCP Registry →](/docs/mcp-registry/).

---

### Installation

```bash
brew install --cask ozgurcd/tap/gograph
```

```bash
go install github.com/ozgurcd/gograph/cmd/gograph@latest
```

Follow the [Getting Started guide →](/docs/getting-started/) to index a module and run initial queries.

---

### Indexed Signals

| Structural Signal | Queryable Facts |
|---|---|
| **Symbols & Types** | Functions, methods, structs, interfaces, fields, embeddings, constructors, and constants |
| **Call & Dispatch Graph** | Direct calls plus type-checked CHA/SSA interface dispatch and promoted-method forwarding |
| **HTTP & SQL Surface** | `net/http`, Gin, Echo, and Chi routes; static PostgreSQL query access, tables, and verbs |
| **Testing & Coverage** | Direct and transitive test-to-production paths, runnable `go test -run` filters, and coverprofile gaps |
| **Architecture & Quality** | Package fan-in/fan-out instability, McCabe complexity, god objects, arity, and layer boundaries |
| **Security & Concurrency** | Interprocedural source-to-sink taint candidates (`flow`), goroutines, channels, and `sync` primitives |

---

### Documentation

- **[Getting Started](/docs/getting-started/)** — Install `gograph`, choose AST vs. `--precise` mode, and inspect a repository.
- **[Command Reference](/docs/command-reference/)** — Complete CLI flag, output-mode (`--json`, `--files-only`, `--mermaid`), and MCP parity reference.
- **[CI Architecture Gates](/docs/ci-architecture-gates/)** — Enforce package boundaries, instability limits, and complexity ceilings in CI.
- **[Tool Comparisons](/docs/comparisons/)** — How `gograph` complements `gopls`, `golangci-lint`, `go-callvis`, and `ripgrep`.
- **[Engineering Workflows](/docs/workflows/)** — Onboarding, safe-edit refactoring, package splits, and security-flow review playbooks.
- **[Evidence & Methodology](/benchmarks/)** — Reproducible structural-evidence benchmark and raw JSON outputs.
