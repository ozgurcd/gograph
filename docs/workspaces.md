# Cross-Repository Workspaces

Gograph workspaces model a fleet of checked-out Go repositories without
combining their repository graphs into one mutable database. Each member's
`.gograph/graph.json` remains authoritative for that repository. The derived
`.gograph/workspace.json` stores only module ownership, cross-repository call
resolutions, HTTP contract nodes, and their evidence.

The virtual query graph is the selected scope's repository graphs plus its
workspace overlay.

## Manifest

A workspace root is established only by a regular, non-linked
`.gograph-workspace.yml` manifest. Derived `workspace.json` state cannot
establish a trusted workspace root by itself.

```yaml
schema_version: gograph.workspace-manifest.v1
name: idp-system
default_scope: oss

defaults:
  precision: precise

repositories:
  - id: idp-oss
    path: idp-oss
    services:
      - id: idp-api
        http:
          authorities: [idp, idp.internal]
  - id: idp-ce
    path: idp-ce
  - id: ui
    path: ui

scopes:
  - id: oss
    repositories: [idp-oss, ui]
  - id: ce
    repositories: [idp-ce, ui]
```

Every member path must be a unique relative descendant of the workspace root.
Every path component must be a real directory; descendant symlinks are
rejected even when they point back inside the root. Arbitrary absolute member
paths are not supported by workspace manifest v1. Member confinement is
rechecked when a graph is loaded or refreshed. As with the rest of Gograph's
Go-tool preflight, workspace v1 assumes a static checkout while a command is
running; it is not a sandbox against a same-user process concurrently replacing
directories or mount points.

Repository IDs, scope IDs, and service IDs use letters, digits, `.`, `_`, and
`-`. Multiple scopes may be configured. `default_scope` is explicit and
stable; when it is absent, queries require `--scope` unless exactly one scope
exists.

Exact duplicate Go module ownership is an error inside one scope. It is valid
across mutually exclusive scopes, which supports alternative OSS/CE editions.
Nested module paths use longest segment-prefix ownership. HTTP authority
aliases are unique within a scope unless every duplicate owner explicitly sets
`shared_authority: true`. Logical service IDs are also unique per scope unless
all owners explicitly declare shared ownership; the same logical ID may be
owned independently in mutually exclusive scopes. Workspace v1 supports one
HTTP service with authority aliases per repository because route facts do not
yet identify which of several configured services owns a handler.

## Build and mutation boundary

```bash
# Reads member graphs and writes only .gograph/workspace.json.
gograph workspace build

# Explicitly permits writes to stale or missing member .gograph artifacts.
gograph workspace build --refresh-members
```

An ordinary workspace build refuses missing, stale, incompatible, or
insufficiently precise member graphs. `--refresh-members` is a sequential
multi-repository mutation, not a transaction. JSON reports `refresh_plan`,
`refresh_attempted`, `refresh_succeeded`, and `refresh_failed`, including
before/after member artifact fingerprints. If a later member fails, earlier
successful member publications remain, while the workspace overlay is not
replaced.

Overlay publication is deterministic for identical inputs and atomic. The
persisted `input_fingerprint` binds the canonical manifest, ordered exact
member graph artifact fingerprints, scope membership, and resolver versions.
`workspace status` computes the workspace artifact fingerprint externally over
the exact bytes. Member capabilities independently report production analysis
and `test_call_resolution` (`ast_heuristic`, `typed_complete`, or
`typed_partial`) for the exact graph artifact. Repository revision and dirty
state are advisory only; their
read-only Git probes disable repository-configured filesystem monitors and
optional index locking.

## Queries

```bash
gograph workspace status --json
gograph workspace query --scope oss ApplyPolicy
gograph workspace path --scope oss idp-oss:ApplyPolicy ui:RenderPolicy
gograph workspace path --scope oss --include-possible idp-oss:ApplyPolicy ui:RenderPolicy
gograph workspace impact --scope oss idp-oss:ApplyPolicy
gograph workspace impact --scope oss --include-possible idp-oss:ApplyPolicy
gograph workspace mcp
```

`repo:symbol` is query/display syntax only. Persisted node identities are
structured into repository, module, node, kind, and language fields.

Workspace Go-call records resolve a concrete unresolved local call site to a
structured external target. The virtual graph exposes the result as the same
ordinary `calls` relationship used inside a repository. HTTP relationships use
a first-class contract node:

```text
client function --calls_http--> HTTP contract --serves_http--> handler
```

The HTTP contract identity is logical `authority_id + method + normalized
path`. Scheme, hostname, and port are evidence qualifiers, so local HTTP and
deployed HTTPS can represent the same logical API.

Resolution certainty and provenance are independent:

```text
resolution_status: exact | ambiguous | possible
evidence_origin: structural | configured | derived
```

Only exact relationships participate in default `path` and `impact`
traversal. `--include-possible` opts into ambiguous and possible relationships
for exploration. These weaker relationships cannot satisfy future machine
validation unless a predicate explicitly requests them.

Parser-only `pkg.Func` call matching is possible evidence because a local value
can shadow an import name. Type-resolved static call targets are exact; CHA
interface targets remain possible. Dynamic HTTP handler factories and
unresolved/ambiguous handler names likewise degrade their derived relationships
instead of being reported as exact.

The workspace MCP server exposes four read-only tools:

- `gograph_workspace_status`
- `gograph_workspace_query`
- `gograph_workspace_path`
- `gograph_workspace_impact`

CLI and MCP use the same native result contracts for these four operations.
In `--json` mode, the CLI places that value in its generic `results` envelope;
the corresponding MCP tool returns the exact same value as JSON text. Scope
selection, default exact-only traversal, `include_possible`, ordering, empty
collections, and errors therefore have one shared implementation rather than
independent CLI and MCP semantics. `workspace build` and member refresh remain
CLI-only mutation operations; the workspace MCP server cannot build or refresh
member graphs or publish an overlay.

Workspace changes, named workspace snapshots, topics, RPC, and shared-schema
resolution are intentionally outside workspace v1's first delivery slice.
