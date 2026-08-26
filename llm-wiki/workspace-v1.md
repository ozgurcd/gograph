---
title: Federated workspace v1
type: decision
status: current
updated: 2026-08-26
sources: []
---

# Federated workspace v1

## Decision

Gograph models a Go system as independently generated repository graphs plus a deterministic workspace overlay. Repository graphs own local facts; the overlay stores cross-repository ownership, resolution, and contract facts. Queries form a virtual graph from selected repository graphs and one scope overlay. No merged fleet graph is persisted.

## Trust and scopes

The checked-in `.gograph-workspace.yml` establishes the workspace root; derived `.gograph/workspace.json` does not. Manifest member paths and symlink traversal must remain below that root. Configured paths are authoritative: serialized graph roots are ignored, confinement is rechecked immediately before member load or refresh, source/build freshness is recomputed at that path, serialized module ownership is verified against actual `go.mod` directives, and input identity binds exact member artifact bytes. A member repository's own `go.work` may reference sibling modules outside that member path only when they remain inside the nearest real Git checkout boundary; this repository-analysis allowance does not widen manifest membership. Workspace root discovery and repository root discovery are separate; workspace-only derived state cannot establish repository authority.

The confinement and Go-tool preflight model assumes a static checkout during a command. It rejects static descendant links and path escapes but is not a sandbox against a same-user process concurrently replacing directories or mount points.

Repository IDs and canonical paths are unique. Module ownership, configured HTTP aliases, and logical HTTP authority IDs are unique within a scope unless shared HTTP ownership is explicit; duplicates across mutually exclusive scopes are valid. V1 permits one HTTP service with authority aliases per repository because route facts do not identify which of several services owns a handler. Scope membership and `default_scope` are fingerprinted. Multiple scopes without a default require `--scope`.

## Identities and resolution

Cross-repository references persist as structured `NodeRef` values; `repository:symbol` is query/display syntax only. A cross-repository Go call is a call-site resolution record materialized as ordinary `calls`, never `calls_go`. Module import resolutions materialize as `imports_module` virtual edges.

HTTP clients and handlers resolve through first-class contracts identified by logical authority, method, and normalized path. Scheme, host, and port are qualifiers, not identity.

Certainty (`exact`, `ambiguous`, `possible`) and evidence origin (`structural`, `configured`, `derived`) are independent. Only exact facts traverse by default. Ambiguous/possible facts require opt-in and cannot silently satisfy machine validation. A derived fact is exact only if every dependency is exact.

When several traversable routes exist, workspace path selects one deterministic best route rather than the first breadth-first hit. Ranking is lexicographic by worst certainty (`exact`, then `ambiguous`, then `possible`), visible path length, production before tests, typed resolution before heuristics, and cross-repository transition count. Canonical relationship/provenance identity breaks complete ties. Categorical path states remain distinct during traversal so a later possible, test, or heuristic edge cannot discard a prefix that becomes globally preferable after degradation. Synthetic forwarding consumes no visible length. This ranking is query-derived state and does not add facts to the persisted overlay or change the singular path response schema.

Parser-only `pkg.Func` matching is possible because a local value may shadow an import. Type-resolved static targets are exact; CHA interface targets remain possible. Synthetic precise wrapper edges remain exact traversal facts. Dynamic or unresolved HTTP handlers degrade their serving relations instead of being labeled exact.

## Fingerprints and mutation

The input fingerprint covers canonical manifest data, ordered exact member artifact fingerprints, and resolver versions. Exact artifact bytes already bind member analysis capabilities and scope membership. The workspace artifact fingerprint is external over exact canonical bytes.

`workspace build` reads members and writes only the overlay. `--refresh-members` explicitly permits sequential member graph mutations and reports planned, attempted, succeeded, and failed refreshes with before/after hashes. Every completed refresh is revalidated before it is reported successful. Member refresh is not transactional. Overlay publication uses rooted, deterministic atomic replacement and rejects linked state paths; member failure preserves the existing overlay.

## CLI and MCP parity

Workspace status, query, path, and impact share one native implementation and result contract with `gograph_workspace_status`, `gograph_workspace_query`, `gograph_workspace_path`, and `gograph_workspace_impact` on the separate read-only workspace server. CLI `--json` places the native value in its generic `results` envelope; the matching MCP tool returns that exact value as JSON text. Scope selection, deterministic ranked path selection, exact-only default traversal, and explicit possible-edge traversal therefore cannot diverge by transport. Workspace build, member refresh, overlay publication, and workspace-server startup remain CLI-only lifecycle operations; workspace MCP exposes no mutation tool.

## P0 boundary

V1 includes confined manifests, scopes, member validation/capabilities, collision checks, module ownership, ordinary Go-call resolution, HTTP contracts, status/query/path/impact, stable JSON, and four read-only MCP tools. Changes, snapshots, RPC, topics, schemas, and API-dependency contracts are deferred; changes must not freeze incomplete add/delete semantics.
