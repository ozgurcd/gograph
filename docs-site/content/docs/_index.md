---
title: "Documentation Portal"
date: 2026-05-23
lastmod: 2026-11-25
description: "Browse gograph documentation: installation, Go call-graph CLI reference, CI architecture gates, tool comparisons, engineering workflows, and MCP integration."
---

Welcome to the `gograph` documentation portal. `gograph` is a local-only Go static analysis, call-graph, blast-radius, and CI architecture-gate CLI for Go engineers, with a built-in zero-step MCP server for AI coding agents.

---

## Documentation Map

### [Getting Started](/docs/getting-started/)
Install `gograph`, build your first AST or type-checked (`--precise`) call graph, and run blast-radius and targeted test queries.
- **Topics**: Homebrew and `go install`, AST vs. `--precise --strict`, `stats`, `stale`, `impact`, `tests --uncommitted`.

### [Command Reference](/docs/command-reference/)
Exhaustive manual covering every CLI command, flag, output mode (`--json`, `--files-only`, `--mermaid`), federated workspaces, and the CLI/MCP parity matrix.
- **Topics**: Indexing, call-graph traversal, interface resolution, HTTP/SQL extraction, security flow, test attribution, and CI gates.

### [CI Architecture Gates](/docs/ci-architecture-gates/)
Enforce Go package boundaries, fan-in/fan-out coupling limits, cyclomatic complexity ceilings, and exported API drift in CI pipelines.
- **Topics**: `gograph/boundaries.json`, `.gograph.yml`, `gograph gate`, `gograph api --since`, `gograph validate`, and GitHub Actions YAML.

### [Tool Comparisons](/docs/comparisons/)
An honest engineering breakdown of how `gograph` complements `gopls`, `golangci-lint`, `go-callvis`, and `ripgrep`.
- **Topics**: Structural graphs vs. LSP cursor navigation vs. file-level linters vs. visual call graphs vs. text search.

### [Engineering Workflows](/docs/workflows/)
Step-by-step operational playbooks for onboarding, safe refactoring, package splits, security-flow triage, and CI quality gates.
- **Topics**: Plan-to-review edit lifecycle, transitive test selection, coverage-gap audits, and CI baselines.

### [MCP & AI Agent Integration](/docs/agent-integration/)
Connect `gograph`'s built-in stdio MCP server to Claude Code, Cursor, Codex, Copilot, Antigravity, and OpenCode.
- **Topics**: Zero-step in-memory refreshes, `--persist-refresh`, Mermaid output, and `PreToolUse` hook guards.

### [Official MCP Registry](/docs/mcp-registry/)
Install a platform-specific local MCP Bundle (`io.github.ozgurcd/gograph`) in MCPB-capable clients.
- **Topics**: MCPB vs. Homebrew/`go install`, supported OS/arch targets, and local data handling.
