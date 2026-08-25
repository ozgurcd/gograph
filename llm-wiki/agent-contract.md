---
title: Agent Workflow Contract
type: workflow
status: current
updated: 2026-08-25
sources:
  - SRC-20260614-gograph-legacy-agent-contract
---

# Agent Workflow Contract

This contract defines the operational workflows, tool-selection guidance, and verification checklist that AI agents should follow when modifying the `gograph` codebase.

## Session Lifecycle

1. **Start**: Create a tracked audit session via `gograph session create [word]`.
2. **Pre-edit**: Call `gograph plan <symbol>` before editing a Go symbol.
3. **Rebuild**: Run `gograph build . --precise --strict` when precise enrichment is required; `--strict` preserves the diagnostic artifact but exits non-zero on fallback. Use ordinary `--precise` when a visible AST fallback is acceptable, or `gograph build .` when precise loading is unavailable.
4. **Post-edit**: Run `gograph review --uncommitted` to inspect the indexed change impact.
5. **Verify**: Run the repository's compiler, tests, linters, and other required checks; graph review is static evidence, not proof of runtime correctness.
6. **End**: Close tracking via `gograph session end` and view the compliance grade with `gograph session audit`.

Skipping planning or review degrades the session compliance grade, but a high grade does not replace repository verification.

## Pre-Edit Checklist

Before modifying Go code:

- Ensure the graph is fresh with `gograph stale`: exit `0` means current, `2` is the expected rebuild signal, and `1` is an operational or JSON serialization failure. In shell automation, especially under `set -e`, branch explicitly on `2` rather than treating every non-zero status as stale.
- Run `gograph stats` and inspect `build_status` and `precision`.
- Run `gograph plan <symbol>` to inspect indexed targets, risk factors, routes, environment reads, and statically mapped tests.
- Run `gograph context <symbol>` to view the declaration and retained caller/callee evidence.
- Do not treat zero callers, zero implementers, or an orphan result as proof. If precision is `ast` or `precise_fallback`, names are ambiguous, or a known source call is missing, cross-check with `gopls` or targeted source/text search and disclose the fallback.

## Post-Edit Checklist

After editing Go code:

- Run `gograph build . --precise --strict` when precise evidence is a release or CI requirement; otherwise run `--precise` and inspect the reported precision/fallback status.
- Run `gograph review --uncommitted` as a static change-impact review.
- Rebuild generated LLM-wiki pages with `gograph wiki` when structural context changed.
- Run `make test-coverage`, `make build`, and the repository's full required verification.
- Verify parity between CLI flags and MCP parameters when either public interface changes.

## Tool Selection

| Task | Preferred structural tool | Fallback or cross-check |
|---|---|---|
| Find Go symbol | `gograph_query` / `gograph_node` | `gopls` search or targeted source/text search |
| Read function body | `gograph_source` | `gopls` file context or scoped file read |
| Find callers | precise `gograph_callers` | `gopls` references and targeted source verification |
| Attribute one test | `gograph_coverage`/CLI `coverage` | run the test and runtime coverage tools for execution claims |
| Persist symbol reference | `gograph_identity`/CLI `identity` | compiler-backed rename/reference tools for refactors |
| Find struct fields | `gograph_fields` | `gopls` or declaration read |
| Search literals/comments/non-Go files | targeted `rg` / text search | not a graph query |
| Pre-edit analysis | `gograph_plan` / `gograph_context` | compiler and source inspection |
| Post-edit review | `gograph_review` | tests, vet, linters, and runtime checks |

Prefer composed calls such as `gograph_context` or `gograph_plan` when they match the question. Measure actual tool calls and tokens instead of assuming a fixed saving.

## CLI and MCP contract

All 63 repository query, analysis, and workflow capabilities must remain semantically equivalent across CLI and the project MCP server; four additional project-MCP endpoints implement session lifecycle. Workspace status, query, path, and impact use a separate four-tool read-only workspace MCP server and share native implementations with their CLI operations. The standard mapping is CLI `<command>` to MCP `gograph_<command>`; `contract`, `boundaries --create`, and session actions have explicit special mappings. The complete CLI-only boundary is `build`, `validate`, `doctor`, `gate`, `snapshot`, integration installation, project/workspace MCP startup, workspace build/member refresh, help, and version. `doctor --json` reports both installation/PATH findings and current repository graph availability, freshness, analysis mode, capabilities, and diagnostic so operational health remains machine-readable without adding a duplicate MCP method. Presentation is transport-specific: CLI `--json` and the supported `--files-only` commands correspond to structured MCP content, while the eight graph-oriented Mermaid commands use MCP `mermaid=true`. The CLI validates output-mode support and rejects conflicting modes. Successful JSON envelopes always carry `count`, collection-shaped empties use `[]`, and hard failures use an error envelope; `session audit --json` deliberately returns its native audit object.

CLI analytical commands require `--intention` while an audit session is active. MCP schemas do not expose or enforce an intention parameter. Instead, active sessions record observational MCP command, duration, success/failure, and empty intention; arguments and query results are omitted. Read-only annotations describe the functional analysis contract, with this local audit telemetry as an observational exception. When `--persist-refresh` is enabled, refresh-capable tools advertise filesystem mutation and may replace the latest graph/report artifacts.

Coverage responses must not merge ambiguous same-named tests, and any uncertain dispatch must propagate `possible` to downstream derived rows. Canonical symbol IDs are stable only while module/package, receiver, and name identity remain unchanged. Identity and coverage must preserve an orthogonal exact package qualifier for the in-package/external-test ID collision; file and line are current location, not identity. CLI repeatable `untested --exclude` and MCP `exclude[]` must use identical lexical repository-relative matching.

Context responses must not hide ambiguity or source-read failures. CLI JSON and MCP share a transport-safe payload with compatibility `node`, all matches in `nodes[]`, top-level `role`, test names plus structured `test_results[]`, and `source_error` when source extraction fails. Contextual plan modes on both transports must include their requested inspect contexts. CLI JSON, MCP `gograph_errorflow`, and the `trace` alias likewise share definition sites, return sites, paths, test evidence, and the static-analysis limitation.
