---
title: Query snapshot, identity, and confidence contract
type: decision
status: current
updated: 2026-10-10
sources: []
---

# Query snapshot, identity, and confidence contract

Owner-approved decision for the eight-improvement release. This page records
rationale and boundaries; current source, tests, and live release metadata remain
the authority for what is installed or published.

## Stable query evidence

Repository graphs remain immutable once a request adopts them. MCP serializes
refresh and publication, not independent readers. A request retains one graph
and its provenance; its cached indexes cannot switch under it. Cancellation
reaches context-aware Go work and phase boundaries. Publication can be canceled
before commit, but an already-started artifact-set commit finishes without a
rollback promise.

Continuation tokens bind graph content and selection, not just a row offset.
Replacing the graph must invalidate outstanding cursors rather than combine
pages from two observations. Common result pages reserve a 16 KiB native budget
because MCP carries both text and structured content and escapes them again.
An oversized individual fact is refused with guidance, never silently clipped.
Specialized route/SQL schemas retain their documented contracts.

Cache scope follows evidence scope: repository indexes belong to the current
fingerprint (old readers may temporarily retain an earlier snapshot). Workspace
verification keeps at most 16 positive receipts. A receipt never bypasses source
freshness, path confinement, ownership, or exact artifact-byte checks.

## Identity and incomplete knowledge

Resolved call identities outrank display spellings. Repository impact includes
labeled possible paths by default; exact-only excludes any uncertain dependency.
An independent exact path can establish exact reachability. Workspace traversal
keeps its separate exact-by-default policy. Explanations require unambiguous
symbol selection and location/identity-scoped supporting facts; they cannot
borrow evidence from another same-named function or package.

Changes compare declarations, including bodies, using the saved platform/tag
selection and current module ownership. File selection failures and unsafe
inputs do not prove deletion. Repeated initializers and package qualifiers must
survive comparison. A mixed source observation is invalid, not a clean census.

A declaration baseline is not a historical call graph. Current-graph consumers
must refuse a deletion requiring historical caller evidence, an incomplete
comparison, or a missing/ambiguous target. Review is a scoped exception: it reports
deleted declarations using the changes --git HEAD census and explicitly says
historical callers, tests and risk are not evaluated. Surviving declarations keep
current-graph analysis. Do not turn unsupported historical traversal into empty
successful impact evidence or silently reconstruct old callers by name.

## Bounded dynamic HTTP resolution

Retain first-class contract nodes and explicit per-scope authority ownership.
Recognized lexical URL bases with static suffixes may resolve through explicit
member http_clients mappings. env:KEY identifies configuration evidence; never
read the runtime environment to infer a service. Request construction alone is
possible evidence, not proof of dispatch. Unsupported/unmapped URLs remain
queryable diagnostics, never traversal edges. Resolver/cache versions must
invalidate old extraction facts when these semantics change.

## Evidence and maintenance

- Public contract: ../docs/query-contracts.md and ../docs/workspaces.md.
- Search implementations: internal/search/{impact,changes,result_page,snapshot,explain_evidence,git}.go.
- Transport contract: internal/mcp/{request_snapshot,list_pagination,stdio_cancellation_test}.go and CLI parity tests.
- HTTP/security fixtures: internal/parser/http_urls_test.go and internal/workspace/http_clients_test.go.
- Cache security: internal/workspace/verification_cache_test.go.

These are repository-local design decisions, not externally ingested claims.
Future agents should verify source/tests and actual publication state before
reusing a version or asserting a release is available. Restart running MCP
processes after upgrading; replacing the executable does not update live schemas.


## Read answers are independent of their presentation

2026-09-17: Direct v1.7.2 stdio measurements showed that source/context retained
the answer in MCP text but supplied only graph provenance in structured content.
The new gograph.read.v1 structured response includes the answer or named refusal.
This separates a presentation defect from source extraction: the latter already
worked for the measured symbol. A missing context now refuses on CLI and MCP;
source ambiguity names candidates, and a 65536-byte source budget refuses with
the measured size. Indexed-but-unreadable source is named in the read and in
graph_state.read_diagnostic without changing graph precision or freshness.
See docs/query-contracts.md and internal/mcp/read_result_test.go for the contract
and fixture proof. The consumer gograph-first hook belongs to its harness and
was not changed.

## Declaration answers and build-context diagnostics

The GOGRAPH-1.7.11 owner ruling accepts a freshness diagnostic instead of silently
reusing saved build context. The graph-state freshness_context and capabilities
name the querying process context, including GOFLAGS and explicit MCP startup
tags. They disclose no environment values; a context mismatch can still be stale.

Constant route paths and visible fields are query evidence. Unresolved route paths
stay visible, and typed mutation receiver evidence separates same-named fields.
Known fields without indexed assignments carry a named mutation-resolution limit:
arbitrary pointer-argument, alias and reflective effects are not resolved.
A file-qualified selector must bind a declaration or name a limit: lexical-only
relationship records cannot pretend to provide file-qualified identity. The public
query contract lists supported tools and remaining limits. CLI and MCP regression
fixtures cover the same answers and refusals.

## Constant references and blank declarations

GOGRAPH-SESSIONS-2 (2026-10-10) re-measured v1.7.12 in isolated repositories: unrelated CLI readers were already independent; precise usages already included typed/untyped package constants in comparison, case, composite-value and argument positions. Broad query omitted that indexed evidence, and repeated blank assertions still refused plan/review.

Broad query now exposes the resolved package references as usage rows. No lexical guess substitutes for precise identity, and local shadows remain excluded. Blank declarations remain in the complete changes census but are skipped during symbol traversal; review retains that evidence and states the limitation. Named ambiguity, incomplete comparison and deletion refusals remain binding. See internal/cli/answers1713_test.go and internal/search/uncommitted_declarations_test.go for CLI/MCP and traversal controls. This goal does not alter the caller-session contract or audit scoring.
