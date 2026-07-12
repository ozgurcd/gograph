---
title: Agent Workflow Contract
type: workflow
status: current
updated: 2026-07-12
sources:
  - SRC-20260614-gograph-legacy-agent-contract
---

# Agent Workflow Contract

This contract defines the operational workflows, tool-selection guidance, and verification checklist that AI agents should follow when modifying the `gograph` codebase.

## Session Lifecycle

1. **Start**: Create a tracked audit session via `gograph session create [word]`.
2. **Pre-edit**: Call `gograph plan <symbol>` before editing a Go symbol.
3. **Rebuild**: Run `gograph build . --precise` when selected packages compile, or `gograph build .` when precise loading is unavailable.
4. **Post-edit**: Run `gograph review --uncommitted` to inspect the indexed change impact.
5. **Verify**: Run the repository's compiler, tests, linters, and other required checks; graph review is static evidence, not proof of runtime correctness.
6. **End**: Close tracking via `gograph session end` and view the compliance grade with `gograph session audit`.

Skipping planning or review degrades the session compliance grade, but a high grade does not replace repository verification.

## Pre-Edit Checklist

Before modifying Go code:

- Ensure the graph is fresh with `gograph stale`; rebuild if needed.
- Run `gograph stats` and inspect `build_status` and `precision`.
- Run `gograph plan <symbol>` to inspect indexed targets, risk factors, routes, environment reads, and statically mapped tests.
- Run `gograph context <symbol>` to view the declaration and retained caller/callee evidence.
- Do not treat zero callers, zero implementers, or an orphan result as proof. If precision is `ast` or `precise_fallback`, names are ambiguous, or a known source call is missing, cross-check with `gopls` or targeted source/text search and disclose the fallback.

## Post-Edit Checklist

After editing Go code:

- Run `gograph build . --precise` and inspect the reported precision/fallback status.
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
| Find struct fields | `gograph_fields` | `gopls` or declaration read |
| Search literals/comments/non-Go files | targeted `rg` / text search | not a graph query |
| Pre-edit analysis | `gograph_plan` / `gograph_context` | compiler and source inspection |
| Post-edit review | `gograph_review` | tests, vet, linters, and runtime checks |

Prefer composed calls such as `gograph_context` or `gograph_plan` when they match the question. Measure actual tool calls and tokens instead of assuming a fixed saving.