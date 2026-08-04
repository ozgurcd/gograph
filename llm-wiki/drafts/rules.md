---
title: Rules and Constraints
type: schema
status: current
updated: 2026-08-04
sources:
  - SRC-20260614-gograph-legacy-rules
---

# Rules & Constraints

This document details the binding rules and architectural constraints for development in this repository.

## 1. Git Commit & Push Discipline
- **NEVER** commit or push changes without explicit instructions from the user.
- Explicit instructions include exact commands like "push", "commit", "deploy", etc.
- Implicit confirmations such as "go ahead" or "do it" are NOT authorizations to push or commit.

## 2. Go Code Analysis
- Always use the AST-aware `gograph` tool suite (e.g., `gograph build`, `gograph plan`, `gograph review`, or equivalent MCP tools) for analyzing Go code, symbol structure, and call paths.
- Do not use plain text tools like `grep`, `find`, or `sed` to search for Go symbols.
- Use `grep_search` only for non-Go or markdown files.

## 3. Build & Versioning
- Always build the binary using `make build`. Direct compilation via `go build` is forbidden because it bypasses version injection.
- The minimum Go version is 1.26.5, as declared by `go.mod`; CI and release builds must not use an older patch.

## 4. Verification Requirements
- All tests must pass before completing any implementation.
- Execute unit and integration tests with `make test-coverage` and check coverage metrics.
- Execute fuzz tests via `make test-fuzz`.

## 5. Documentation
- Document any new commands, options, or behavior across:
  1. `README.md`
  2. `docs/coding-agent-usage.md`
  3. `gograph capabilities` (in `internal/cli/cli.go`)
  4. `gograph --help` (in `internal/cli/cli.go`)
  5. `RELEASE_NOTES.md` (mandatory on every release)
- Run `gograph wiki` to regenerate generated documentation pages in `llm-wiki/`.
- Ensure parity between CLI flags and MCP tool parameters (matching names, types, logic, and output schemas).

## 6. Architectural Boundaries
- Product analysis and MCP transport remain local and open no application-service connection. Precise analysis and `doc` invoke the installed Go toolchain, which may use its configured module cache/proxy/network policy.
- Static analysis must not execute user code or run target binaries/tests.
- Maintain package layout boundaries:
  - `internal/graph`: Core data models (stdlib only).
  - `internal/parser`: AST parsing and scope resolution.
  - `internal/search`: Search/query algorithms (must not import `internal/cli`).
  - `internal/wiki`: Code stats and markdown documentation generator.
  - `internal/cli`: CLI runner.
  - `internal/mcp`: MCP stdio server.
