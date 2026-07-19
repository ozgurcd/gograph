---
title: Project Identity and Architecture
type: project
status: current
updated: 2026-07-18
sources:
  - SRC-20260614-gograph-legacy-project
---

# Project: gograph

`gograph` is a local Go repository indexer for coding agents. CLI and MCP share 61 query, analysis, and workflow capabilities; MCP has 65 endpoints including four session tools. CLI reads persisted `.gograph/graph.json`; MCP keeps source-analysis requests fresh in memory.

## Positioning and onboarding

gograph is a persisted repository-analysis layer, not a replacement for every Go or search tool. Use `gopls` for live compiler-backed workspace diagnostics, navigation, implementations, and refactoring; use `rg` or other text search for literals, comments, generated/non-indexed files, and non-Go content. Use gograph for repository-level impact, reachability, application inventories, composed agent workflows, and policy gates. When graph precision is AST/fallback, results are ambiguous, or a known call is missing, cross-check with `gopls` or targeted source/text search and disclose the fallback.

The executable Go package is `github.com/ozgurcd/gograph/cmd/gograph`. First-run documentation must use repository-wide commands such as `stats`, `summary`, and `hotspot` before examples that require a project-specific symbol. Product claims must distinguish reproducible snapshot counts from inference and must not promise absolute accuracy, fixed token savings, or hallucination-rate improvements without published methodology and data.

## Analysis modes and health

Default builds produce an AST graph and tolerate partial parsing when at least one Go file succeeds. `BuildMetadata.Precision` records `ast`, `precise`, or `precise_fallback`; this is independent of complete/partial AST build health. Precise builds attempt go/packages type loading plus CHA/SSA. Missing indexed production files or type/load errors retain the AST graph and record a visible fallback instead of claiming precise success. `gograph stats` and MCP stats expose both precision and build health. `gate` fails closed for stale source, but it does not currently reject a fresh graph whose build metadata is incomplete; CI workflows that require complete coverage must check build health explicitly.

Scanner file selection and `go/packages` share one cmd/go-resolved build configuration: GOOS/GOARCH, cgo, compiler, user and GOFLAGS tags, tool tags, and release tags. Active constrained tests remain indexed; inactive constraints, generated files, cmd/go wildcard-excluded directories, go.mod ignore paths, and Git-ignored paths do not enter any graph records. The persisted selection fingerprint includes the selected source inventory plus build and module-selection state, so freshness checks detect tag/platform changes, module-boundary changes, active/inactive transitions, additions, and deletions.

## Precise call-graph representation

A dynamic interface invocation is represented by one deterministic `CallEdge` per valid named in-repository CHA target. Parallel target edges retain the same source expression provenance: caller identity, raw selector, file, line, column, and return-use metadata. User-facing caller/callee results deduplicate that source expression, while depth traversal, impact, diagrams, reachability, and orphan analysis follow every target.

Promoted methods may appear in SSA as wrappers with no source declaration. Precise graphs retain the wrapper as the concrete interface target and add a `Synthetic: true` wrapper-to-declared-method forwarding edge. Synthetic edges have no source call site, remain hidden from presentation and source metrics, and consume no visible traversal depth.

Interface-qualified caller queries such as `OIDCStateRepository.Delete` resolve through precise `Implements` edges, including embedded interface method sets and promoted concrete methods. Bare names, concrete receiver dot notation, fully-qualified symbol IDs, and exact matching use the same resolver in CLI and MCP callers.

## MCP refresh policy

Source-analysis MCP tools check the effective selected-file inventory, build/module fingerprint, source modification times, and replacement of the persisted graph artifact. A later successfully decoded precise publication is adopted even when overlapping builds give it an earlier build-start `GeneratedAt`. A precise or precise-fallback session never adopts an AST-only downgrade, and source-selection or source-content changes rebuild in the current requested mode. Precise refresh failure is returned visibly while retaining fallback metadata for diagnosis and retry.

## Compatibility

The graph schema remains version 2. Precision, call-column, synthetic-forwarding, and build-context fingerprint fields are additive JSON fields. Current readers normalize missing legacy precision to AST-only and missing call metadata to ordinary line-only edges. Older v2 binaries can decode new files, but may count or display synthetic forwarding records as ordinary calls; current binaries are required for the new traversal, presentation, and build-selection freshness semantics.

## Static-analysis limits

Precise dispatch uses conservative CHA, so it can include named implementations that are not instantiated in one runtime configuration. Reflection, `unsafe`, plugins, unresolved function values, test-only packages, unnamed concrete types, module-external implementations, and incomplete package loading can still cause omissions. AST callback recovery filters known ordinary identifiers, but selector-valued arguments remain name-based and can create false call edges when a data field shares a name with a callable. Precise implementer edges use qualified IDs, while legacy ID-less graphs and the AST implementer fallback remain package-insensitive. Changed-route policy checks also normalize handler names and can conflate same-named handlers across packages or receiver types. Treat zero-result and heuristic/fallback results as evidence to cross-check, not proof. Flow analysis remains path-insensitive with up to 16 call-site frames and query-time sanitizer policy; findings are review leads, not proof.

Non-goals include other languages, model APIs, SaaS, remote telemetry, and target binary/test execution.
