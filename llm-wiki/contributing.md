---
title: Contributing Guidelines
type: workflow
status: current
updated: 2026-11-05
sources:
  - SRC-20260614-gograph-legacy-contributing
---

# Contributing to gograph

## Scope and source of truth

gograph intentionally analyzes Go repositories; other-language parsers are a non-goal. Derive behavior from current source, tests, live CLI help, and the MCP tool registry rather than copying older documentation. The module currently requires Go 1.27.2 as declared by `go.mod`.

## Adding or changing a capability

1. Implement graph data in `internal/graph` or extraction in `internal/parser` when needed.
2. Implement query/analysis logic in `internal/search` without importing CLI or MCP packages.
3. Add the CLI handler and dispatch in `internal/cli`.
4. Add the semantically equivalent MCP tool and typed parameters in `internal/mcp`.
5. Add CLI and MCP regression coverage, including schemas, error behavior, freshness, and transport-specific output. Keep `internal/cli/output_modes.go` synchronized with every supported CLI presentation.
6. Update live help/capabilities, README, maintained guides, public site, integrations, release notes, and affected wiki pages.

Query, analysis, and workflow features belong on both CLI and MCP, including workspace status/query/path/impact on the separate workspace server. The normal mapping is `<command>` to `gograph_<command>`; aliases, boundary creation, and session lifecycle retain their documented special mappings. The complete CLI-only boundary is `build`, `validate`, `doctor`, `gate`, `snapshot`, plugin/hook installation, project/workspace MCP startup, workspace build/member refresh, and help. The standalone `version` command has no dedicated MCP tool, while `gograph_capabilities.version` exposes the running server binary. Session CLI actions map to four `gograph_session_*` endpoints. Output presentation may differ: CLI `--json` and `--files-only` map to structured MCP content, while CLI `--mermaid` maps to `mermaid=true` for callers, callees, impact, endpoint, dependents, deps, path, and coupling. Registry-backed tests must prove every canonical CLI command and every project/workspace MCP tool remains documented.

## Package boundaries and style

- `internal/graph`: core data structures; standard library only.
- `internal/parser`: tolerant AST extraction; imports graph.
- `internal/search`: graph algorithms and narrowly scoped filesystem/Git reads; never imports CLI or MCP.
- `internal/wiki`: generated wiki rendering from graph/search data.
- `internal/cli` and `internal/mcp`: transport, process, persistence, and integration wiring.
- Keep deterministic ordering and explicit AST/precise/fallback semantics.
- Freshness changes require byte-digest tests that preserve mtimes, legacy-index fallback coverage, and changed-package versus unchanged-package reuse assertions. Precise incremental tests must prove repository-wide enrichment is reconstructed without duplicate or missing edges.
- Route extraction changes require grouped/nested and variadic-middleware parser fixtures plus explicit CLI/MCP parity coverage; constant prefixes may be composed, dynamic expressions must remain conservative, Gin/Fiber use the final variadic argument as handler, and Echo retains the handler before middleware. Route census changes must preserve the shared bounded `gograph.routes.v1` filtering and cursor contract.
- Surface errors directly, serialize successful empty collections as `[]` with count zero, preserve command-aware JSON error envelopes, and test ambiguous symbol names.
- For Go 1.27, use explicit `encoding/json/v2` semantics at strict, closed-schema trust boundaries. Keep established public JSON output on `encoding/json` unless a deliberate compatibility change is approved; Go 1.27 already backs that API with the v2 implementation while preserving v1 behavior. Add ML-DSA, ML-KEM, or other cryptography only for a concrete protocol requirement, and do not replace an upstream-owned transitive dependency with a local wrapper solely because a similarly named standard package exists.
- Document local I/O, Go-toolchain/network behavior, audit telemetry, and artifact mutation accurately.

## Build and verification

Use `make build` so version metadata is injected; do not substitute a raw repository binary build. `make test` is the canonical local suite: normal and race tests run with `-count=1`, and CLI subprocess tests compile the current checkout once into a cleaned OS temp directory with isolated fixtures. They never reuse `bin/gograph`, `bin/gograph-test`, or ambient `.gograph` state. `govulncheck` evaluates source reachability; Grype scans only explicit current inputs (`go.mod` and the freshly rebuilt native binary), so stale ignored output trees cannot contaminate the result. `make release-verify` additionally requires and scans the exact four fresh GoReleaser archives. Before finishing a change, run the repository-required formatting, unit, race, vet, staticcheck, golangci-lint, module verify/tidy, coverage, fuzz, vulnerability, documentation, and cross-platform build checks. Rebuild the repository graph precisely when compilable, inspect `stats`, and run a post-edit `review --uncommitted`. Static graph evidence does not replace compiler and test results.

## Scrinium governance

Scrinium is conditional durable-memory infrastructure, not a mandatory preflight for every task. Source, tests, generated contracts, release artifacts, and live services remain authoritative for behavior. Read-only work, trivial edits, and ordinary changes that create no reusable cross-session knowledge do not require a Scrinium session.

Use Scrinium when maintained wiki content must change or a material architecture, security, release, governance, or external-source decision should persist. Call `capabilities` once per server connection, begin a session, read `index.md`, `agent-rules.md`, and only directly relevant pages, then write durable knowledge through Scrinium. Avoid routine implementation and formatting-only log entries. Satisfy `session_status` and call `finish_session` only for a session that was started. Protected pages use the draft workflow.

## Shared toolchain record (2026-10-09)

TOOLCHAIN-1 (f68c017 and 6b711c7) uses ozgurcd/lictor/.github/actions/go-toolchain@d35a8ad07dc59bd76153f0ed7d3b1ade22c6cf04 for CI and release setup. The caller go.mod selects Go; the action owns checksum-verified Staticcheck build inputs. Independent govulncheck and GoReleaser pins remain caller-owned.

## CI test reuse (GOGRAPH-CI-DEDUP-1105)

CI runs the full suite once with `go test -count=1 -race ./...`.
PR events always run this command.
The daily schedule at 02:37 UTC also runs it without reuse.
The local `make test` command remains unchanged.

Main can reuse a successful CI run for an identical tree.
The run is an earlier main push, or a same-repo pull_request run of a PR that GitHub associates with the target commit.
Every other CI step still runs in its original order.
Release retains builds, vet, vulnerability scans, bundle checks, documentation, and archive checks.
Release runs the full race suite when proof is missing.
Publication still requires successful release verification.

The lookup never runs on PR events.
Main uses inline workflow code.
Release calls `.github/workflows/test-proof.yml` from `ozgurcd/gograph@main`.
The lookup job checks out no source and runs no repository helper.
A separate job records `tested-commit-${{ github.sha }}` after successful full CI tests.
That job uses no checkout, artifact, or source-controlled output.
The test job explicitly checks out that same GitHub commit.

The consumer reads the producing run and its exact attempt through GitHub's API.
It requires successful CI, the successful race step, and the successful isolated marker job.
It rejects forks and requires the expected repository and workflow path.
It resolves the tested commit through the API and compares its tree with the target tree.
For PR runs, the marker names the executed merge commit.
That commit must have exactly two parents, one of them the run's head commit, and the target tree.
Both CI workflow files must match their trusted main versions at the run's head and, for PR runs, at the merge commit.
A producer commit, merge commit, or workflow file that does not exist (404) is skipped; any other API error causes full tests.
A producer with changed workflow logic cannot forge accepted commit metadata.
Release also compares the resolved target commit with the validated checkout commit.
Artifacts never supply test proof.

The search checks at most five pages of 100 successful main push runs, plus the PR runs of each associated PR head.
Incomplete job pages, API failures, changed attempts, and unsuitable evidence cause full tests.
The lookup has a three-minute job limit and ten-second API request limits.
No lookup waits for another run.
A missed or failed lookup cannot disable PR, nightly, main fallback, or release fallback tests.
The first PR works before the new reusable workflow reaches main.
GitHub may delay scheduled jobs.

Validation and timing estimates belong to the workspace report `GOGRAPH-CI-DEDUP-1105`.
Local validation uses offline API fixtures and a focused workflow contract test.
It does not replace the future GitHub run.
GitHub documents PR merge commits in its [event reference](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows).
