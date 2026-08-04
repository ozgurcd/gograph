---
title: Project Identity and Architecture
type: project
status: current
updated: 2026-08-04
sources:
  - SRC-20260614-gograph-legacy-project
---

# Project: gograph

`gograph` is a local Go repository indexer for coding agents. CLI and MCP share 61 query, analysis, and workflow capabilities; MCP has 65 endpoints including four session tools. CLI graph-backed commands read persisted `.gograph/graph.json`. MCP keeps source-analysis requests fresh in memory by default and can publish refreshed artifacts only when started with `--persist-refresh`.

## Positioning and onboarding

gograph is a local repository-analysis layer backed by persisted or in-memory graph state, not a replacement for every Go or search tool. Use `gopls` for live compiler-backed workspace diagnostics, navigation, implementations, and refactoring; use `rg` or other text search for literals, comments, generated/non-indexed files, and non-Go content. Use gograph for repository-level impact, reachability, application inventories, composed agent workflows, and policy gates. When graph precision is AST/fallback, results are ambiguous, or a known call is missing, cross-check with `gopls` or targeted source/text search and disclose the fallback.

The executable Go package is `github.com/ozgurcd/gograph/cmd/gograph`. First-run documentation must use repository-wide commands such as `stats`, `summary`, and `hotspot` before examples that require a project-specific symbol. Product claims must distinguish reproducible snapshot counts from inference and must not promise absolute accuracy, fixed token savings, or hallucination-rate improvements without published methodology and data.

## Analysis modes and health

Default builds produce an AST graph and tolerate partial parsing when at least one Go file succeeds. `BuildMetadata.Precision` records `ast`, `precise`, or `precise_fallback`; this is independent of complete/partial AST build health. Precise builds attempt go/packages type loading plus CHA/SSA. Missing indexed production files or type/load errors normally retain the AST graph and record a visible fallback instead of claiming precise success. A failed precise retry preserves an existing fresh successful precise artifact covering the same selected sources. `gograph stats` and MCP stats expose both precision and build health. `gate` fails closed for stale source, but it does not currently reject a fresh graph whose build metadata is incomplete; CI workflows that require complete coverage must check build health explicitly.

Scanner file selection and `go/packages` share one cmd/go-resolved build configuration: GOOS/GOARCH, cgo, compiler, user and GOFLAGS tags, tool tags, and release tags. Active constrained tests remain indexed; inactive constraints, generated files, cmd/go wildcard-excluded directories, go.mod ignore paths, and Git-ignored paths do not enter any graph records. The persisted selection fingerprint includes the selected source inventory plus build and module-selection state, so freshness checks detect tag/platform changes, module-boundary changes, active/inactive transitions, additions, and deletions.

`gograph stale` is a tri-state freshness predicate in both text and JSON modes: exit `0` means current, `2` means stale, and `1` means an operational or JSON serialization error. Exit `2` is an expected freshness result and is recorded as successful session telemetry. Shell automation must distinguish `2` from `1`, especially under `set -e`, so genuine failures are not mistaken for rebuild requests.

## Precise call-graph representation

A dynamic interface invocation is represented by one deterministic `CallEdge` per valid named in-repository CHA target. Parallel target edges retain the same source expression provenance: caller identity, raw selector, file, line, column, and return-use metadata. User-facing caller/callee results deduplicate that source expression, while depth traversal, impact, diagrams, reachability, and orphan analysis follow every target.

Promoted methods may appear in SSA as wrappers with no source declaration. Precise graphs retain the wrapper as the concrete interface target and add a `Synthetic: true` wrapper-to-declared-method forwarding edge. Synthetic edges have no source call site, remain hidden from presentation and source metrics, and consume no visible traversal depth.

Interface-qualified caller queries such as `OIDCStateRepository.Delete` resolve through precise `Implements` edges, including embedded interface method sets and promoted concrete methods. Bare names, concrete receiver dot notation, fully-qualified symbol IDs, and exact matching use the same resolver in CLI and MCP callers.

## MCP refresh policy

Source-analysis MCP tools check the effective selected-file inventory, build/module fingerprint, source modification times, and replacement of the persisted graph artifact. A later successfully decoded precise publication is adopted even when overlapping builds give it an earlier build-start `GeneratedAt`. A precise or precise-fallback session never adopts an AST-only downgrade, and source-selection or source-content changes rebuild in the current requested mode. Precise refresh failure is returned visibly while retaining fallback metadata for diagnosis and retry.

MCP refresh persistence is opt-in with `gograph mcp [path] --persist-refresh`; default and fixed plugin/MCPB launches remain in-memory-only. The option publishes the latest successful startup build or stale refresh to the fixed `.gograph` output bundle (graph plus nine reports), not a multi-branch or historical cache, and it does not edit `.gitignore`. Graph/report artifact publishers coordinate through the persistent operational file `.gograph/.artifacts.lock`, derive the new baseline from the immediately preceding persisted graph under that lock, stage and sync the complete bundle, then rename reports before `graph.json` as the commit marker. Same-directory replacements are atomic on Unix-like systems but are not guaranteed atomic by Go on non-Unix platforms; the ten-file bundle is not one atomic transaction, so a crash can leave reports ahead of the previous graph marker. Refresh publication rejects stale candidates and never replaces an equal or richer fresh graph with AST or precise-fallback output; manual builds remain authoritative except that a failed precise retry preserves an existing fresh precise artifact.

An initial MCP publication failure prevents server startup. A later refresh publication failure is returned by the triggering tool while the valid in-memory graph remains pending for a publication retry without a forced rebuild. Persistence-enabled tool annotations disclose filesystem mutation; tools that cannot refresh retain their original annotations.

Query, analysis, and workflow features have semantically equivalent CLI and MCP entry points. Host/build operations such as build, gate, snapshots, installation, server startup, help, and version remain CLI-only. CLI presentation flags map to typed MCP parameters or structured content; callers, callees, impact, endpoint, dependents, deps, path, and coupling map CLI `--mermaid` to MCP `mermaid=true`. Output modes are command-validated and mutually exclusive. API/check baselines accept either a validated local Git ref or a saved graph path ending in `.json`. CLI audit sessions require `--intention` for analytical commands. MCP has no intention parameter; while a session is active it records observational command/duration/status telemetry without arguments or query results.

## Compatibility

The graph schema remains version 2. Precision, call-column, synthetic-forwarding, and build-context fingerprint fields are additive JSON fields. Current readers normalize missing legacy precision to AST-only and missing call metadata to ordinary line-only edges. Older v2 binaries can decode new files, but may count or display synthetic forwarding records as ordinary calls; current binaries are required for the new traversal, presentation, and build-selection freshness semantics.

## Static-analysis limits

Precise dispatch uses conservative CHA, so it can include named implementations that are not instantiated in one runtime configuration. Reflection, `unsafe`, plugins, unresolved function values, test-only packages, unnamed concrete types, module-external implementations, and incomplete package loading can still cause omissions. AST callback recovery filters known ordinary identifiers, but selector-valued arguments remain name-based and can create false call edges when a data field shares a name with a callable. Precise implementer edges use qualified IDs, while legacy ID-less graphs and the AST implementer fallback remain package-insensitive. Changed-route policy checks also normalize handler names and can conflate same-named handlers across packages or receiver types. Treat zero-result and heuristic/fallback results as evidence to cross-check, not proof. Flow analysis remains path-insensitive with up to 16 call-site frames and query-time sanitizer policy; findings are review leads, not proof.

Non-goals include other languages, model APIs, SaaS, remote telemetry, and target binary/test execution.
