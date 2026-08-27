---
title: Agent Workflow Contract
type: workflow
status: current
updated: 2026-08-27
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
| Explore unfamiliar structural term | `gograph_explore` / CLI `explore` | focused `query`, `context`, or `impact` for a complete section |
| Read function body | `gograph_source` | `gopls` file context or scoped file read |
| Find callers | precise `gograph_callers` | `gopls` references and targeted source verification |
| Confirm relationship path | `gograph_path` / CLI `path`; workspace equivalent for fleet paths | inspect exact source edges when the selected route is possible or heuristic |
| Inventory HTTP routes | `gograph_routes` / CLI `routes` with term/module filters and cursors | `gograph_endpoint` and source inspection for one handler |
| Attribute one test | `gograph_coverage`/CLI `coverage` | run the test and runtime coverage tools for execution claims |
| Persist symbol reference | `gograph_identity`/CLI `identity` | compiler-backed rename/reference tools for refactors |
| Find struct fields | `gograph_fields` | `gopls` or declaration read |
| Search literals/comments/non-Go files | targeted `rg` / text search | not a graph query |
| Pre-edit analysis | `gograph_plan` / `gograph_context` | compiler and source inspection |
| Post-edit review | `gograph_review` | tests, vet, linters, and runtime checks |

Prefer composed calls such as `gograph_explore`, `gograph_context`, or `gograph_plan` when they match the question. Explore is a bounded discovery layer, not a replacement for focused commands. Measure actual tool calls and tokens instead of assuming a fixed saving.

## CLI and MCP contract

All 64 repository query, analysis, and workflow capabilities must remain semantically equivalent across CLI and the project MCP server; four additional project-MCP endpoints implement session lifecycle. Workspace status, query, path, and impact use a separate four-tool read-only workspace MCP server and share native implementations with their CLI operations. The standard mapping is CLI `<command>` to MCP `gograph_<command>`; `contract`, `boundaries --create`, and session actions have explicit special mappings. The complete CLI-only boundary is `build`, `validate`, `doctor`, `gate`, `snapshot`, integration installation, project/workspace MCP startup, workspace build/member refresh, and help. The standalone `version` command has no dedicated MCP tool; MCP-only consumers record the running binary from the top-level `gograph_capabilities.version` field. `doctor --json` reports both installation/PATH findings and current repository graph availability, freshness, analysis mode, capabilities, and diagnostic so operational health remains machine-readable without adding a duplicate MCP method. Presentation is transport-specific: CLI `--json` and the supported `--files-only` commands correspond to structured MCP content, while the eight graph-oriented Mermaid commands use MCP `mermaid=true`. The CLI validates output-mode support and rejects conflicting modes. Successful JSON envelopes always carry `count`, collection-shaped empties use `[]`, and hard failures use an error envelope; `session audit --json` deliberately returns its native audit object.

Successful repository graph-backed CLI JSON envelopes include `gograph.graph-state.v1` for the exact persisted graph used by the command; text `stats` and `stale` expose the same core state. Refresh-backed MCP results retain their compatibility text and add a graph-state-only `gograph.mcp-result.v1` structured companion plus `_meta.gograph_graph_state`. Agents must inspect source, freshness, completeness, precision, refresh, and persistence before treating an absence, impact result, or validation input as authoritative. A current in-memory fallback and a trusted stale persisted graph are deliberately usable but degraded; different effective Go build contexts remain fail-closed. Snapshot MCP `stale`, default `changes`, and `stats` report the state they inspect rather than the live refresh state. Refresh and persistence diagnostics are bounded and remain on their respective axes.

CLI analytical commands require `--intention` while an audit session is active. MCP schemas do not expose or enforce an intention parameter. Instead, active sessions record observational MCP command, duration, success/failure, and empty intention; arguments and query results are omitted. Read-only annotations describe the functional analysis contract, with this local audit telemetry as an observational exception. When `--persist-refresh` is enabled, refresh-capable tools advertise filesystem mutation and may replace the latest graph/report artifacts.

CLI `path` and MCP `gograph_path`, plus workspace `workspace path` and `gograph_workspace_path`, must share deterministic best-path selection. Competing routes are ordered lexicographically by worst certainty (`exact`, `ambiguous`, `possible`), visible length, production before tests, typed resolution before heuristics, and fewer cross-repository transitions; complete ties use canonical relationship/provenance identity. Repository calls without resolved target identity are possible heuristic edges, CHA targets remain possible typed edges, and synthetic forwarders consume no visible length. Existing singular response shapes, workspace exact-only defaults, explicit possible-edge opt-in, and Mermaid behavior remain compatible.

CLI `explore` and MCP `gograph_explore` must use the same native `gograph.explore.v1` value for equivalent query, exact, limit, and response-mode inputs. CLI `--compact`/`--deep` and MCP typed `compact`/`deep` booleans are mutually exclusive. Compact defaults to five rows and preserves discovery, selected node/role, complete totals, and explicit `omitted_sections` without token-heavy evidence bodies. Standard defaults to ten rows and preserves the original source/direct evidence/exact-impact response. Deep defaults to 25 rows and adds bounded depth-3 exact identity callers/callees, selected package context, explanation, totals, and truncation metadata; ambiguous selections must omit deep expansion until an exact fully-qualified identity is supplied. An explicit 1-100 limit overrides the selected mode default. Every response must identify direct versus ranked lexical selection, preserve ambiguity, and state that question-like input is lexical rather than model-interpreted. Bundled impact and deep traversal follow exact identity-resolved call edges, traverse synthetic forwarding without reporting it, and exclude possible dispatch. Deep caller/callee rows retain stable identity, call-site provenance, and declaration location when indexed; specialized commands remain available for complete or broader focused output.

CLI `routes` and MCP `gograph_routes` must use the same native `gograph.routes.v1` page for equivalent term, module, test-inclusion, limit, and cursor inputs. Production-only is the default on both transports, with tests available only through `--include-tests`/`include_tests=true`. Term matching covers method/path, handler, and file; module selection uses exact module identity/directory or a unique directory basename with longest-directory ownership. Pages default to 100 rows, reject limits outside 1-200, and enforce a 64 KiB compact-result budget. A deterministic opaque `next_cursor` continues the same filters; callers must restart after graph changes. Whole-result refusal and out-of-band spill files are not normal pagination. Variadic Gin/Fiber registration identifies the final argument as handler, while Echo retains the handler immediately after the path; unresolved handler factories remain dynamic.

Coverage responses must not merge ambiguous same-named tests, and any uncertain dispatch must propagate `possible` to downstream derived rows. Canonical symbol IDs are stable only while module/package, receiver, and name identity remain unchanged. Identity and coverage must preserve an orthogonal exact package qualifier for the in-package/external-test ID collision; file and line are current location, not identity. CLI repeatable `untested --exclude` and MCP `exclude[]` must use identical lexical repository-relative matching.

Context responses must not hide ambiguity or source-read failures. CLI JSON and MCP share a transport-safe payload with compatibility `node`, all matches in `nodes[]`, top-level `role`, test names plus structured `test_results[]`, and `source_error` when source extraction fails. Contextual plan modes on both transports must include their requested inspect contexts. CLI JSON, MCP `gograph_errorflow`, and the `trace` alias likewise share definition sites, return sites, paths, test evidence, and the static-analysis limitation.
