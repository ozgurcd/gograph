---
title: Contributing Guidelines
type: workflow
status: current
updated: 2026-08-05
sources:
  - SRC-20260614-gograph-legacy-contributing
---

# Contributing to gograph

## Scope and source of truth

gograph intentionally analyzes Go repositories; other-language parsers are a non-goal. Derive behavior from current source, tests, live CLI help, and the MCP tool registry rather than copying older documentation. The module currently requires Go 1.26.5 as declared by `go.mod`.

## Adding or changing a capability

1. Implement graph data in `internal/graph` or extraction in `internal/parser` when needed.
2. Implement query/analysis logic in `internal/search` without importing CLI or MCP packages.
3. Add the CLI handler and dispatch in `internal/cli`.
4. Add the semantically equivalent MCP tool and typed parameters in `internal/mcp`.
5. Add CLI and MCP regression coverage, including schemas, error behavior, freshness, and transport-specific output. Keep `internal/cli/output_modes.go` synchronized with every supported CLI presentation.
6. Update live help/capabilities, README, maintained guides, public site, integrations, release notes, and affected wiki pages.

Query, analysis, and workflow features belong on both CLI and MCP. Intentional CLI-only host/build operations include `build`, `gate`, `snapshot`, plugin/hook installation, MCP server startup, help, and version. Session CLI actions map to four `gograph_session_*` endpoints. Output presentation may differ: CLI `--json` and `--files-only` map to structured MCP content, while CLI `--mermaid` maps to `mermaid=true` for callers, callees, impact, endpoint, dependents, deps, path, and coupling.

## Package boundaries and style

- `internal/graph`: core data structures; standard library only.
- `internal/parser`: tolerant AST extraction; imports graph.
- `internal/search`: graph algorithms and narrowly scoped filesystem/Git reads; never imports CLI or MCP.
- `internal/wiki`: generated wiki rendering from graph/search data.
- `internal/cli` and `internal/mcp`: transport, process, persistence, and integration wiring.
- Keep deterministic ordering and explicit AST/precise/fallback semantics.
- Surface errors directly, serialize successful empty collections as `[]` with count zero, preserve command-aware JSON error envelopes, and test ambiguous symbol names.
- Document local I/O, Go-toolchain/network behavior, audit telemetry, and artifact mutation accurately.

## Build and verification

Use `make build` so version metadata is injected; do not substitute a raw repository binary build. `make test` is the canonical local suite: normal and race tests run with `-count=1`, and CLI subprocess tests compile the current checkout once into a cleaned OS temp directory with isolated fixtures. They never reuse `bin/gograph`, `bin/gograph-test`, or ambient `.gograph` state. `govulncheck` evaluates source reachability; Grype scans only explicit current inputs (`go.mod` and the freshly rebuilt native binary), so stale ignored output trees cannot contaminate the result. `make release-verify` additionally requires and scans the exact six fresh GoReleaser archives. Before finishing a change, run the repository-required formatting, unit, race, vet, staticcheck, golangci-lint, module verify/tidy, coverage, fuzz, vulnerability, documentation, and cross-platform build checks. Rebuild the repository graph precisely when compilable, inspect `stats`, and run a post-edit `review --uncommitted`. Static graph evidence does not replace compiler and test results.

## Scrinium governance

Scrinium is conditional durable-memory infrastructure, not a mandatory preflight for every task. Source, tests, generated contracts, release artifacts, and live services remain authoritative for behavior. Read-only work, trivial edits, and ordinary changes that create no reusable cross-session knowledge do not require a Scrinium session.

Use Scrinium when maintained wiki content must change or a material architecture, security, release, governance, or external-source decision should persist. Call `capabilities` once per server connection, begin a session, read `index.md`, `agent-rules.md`, and only directly relevant pages, then write durable knowledge through Scrinium. Avoid routine implementation and formatting-only log entries. Satisfy `session_status` and call `finish_session` only for a session that was started. Protected pages use the draft workflow.
