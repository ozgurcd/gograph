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
Run `gograph workspace --help` for the command family and a minimal valid
manifest; the complete schema and semantics are documented here.

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
    http_clients:
      - base: cfg.IDPURL
        authority_id: idp-api
        path_prefix: /v1
      - base: env:IDP_URL
        authority_id: idp-api

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

This manifest rule is intentionally separate from repository-local Go
workspace resolution. During one member's precise build, its `go.work` may
select sibling modules beneath that member's nearest real Git checkout (and is
otherwise confined beneath the Go workspace directory). That does not permit a
`.gograph-workspace.yml` member path to escape this manifest root.

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

# Select one tagged context for validation and every explicit member refresh.
gograph workspace build --refresh-members --tags=integration

# Apply the repository-build low-memory policy to each sequential refresh.
gograph workspace build --refresh-members --memory-mode=low --max-memory=1GiB
```

An ordinary workspace build refuses missing, stale, incompatible, or
insufficiently precise member graphs. `--refresh-members` is a sequential
multi-repository mutation, not a transaction. JSON reports `refresh_plan`,
`refresh_attempted`, `refresh_succeeded`, and `refresh_failed`, including
before/after member artifact fingerprints. If a later member fails, earlier
successful member publications remain, while the workspace overlay is not
replaced.
Low-memory options preserve member graph precision while applying aggressive
reclamation and an optional soft Go runtime memory target; the target is not a hard RSS
or cross-repository transaction limit.
Explicit comma-separated `--tags` replace any `GOFLAGS -tags` selection and
participate in each member's build-context fingerprint. Use the same tags on
workspace status/query/path/impact and `workspace mcp`; changing or omitting
them makes a differently selected member artifact stale rather than mixing
incompatible repository graphs.

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

Repository member graphs and the workspace overlay share the 512 MiB
whole-artifact JSON read bound. Oversized member graphs are unavailable until
rebuilt; `workspace build --refresh-members` may perform that explicit member
recovery, while ordinary workspace queries remain read-only.

## Queries

```bash
gograph workspace status --json
gograph workspace query --scope oss --tags=integration ApplyPolicy
gograph workspace path --scope oss idp-oss:ApplyPolicy ui:RenderPolicy
gograph workspace path --scope oss --include-possible idp-oss:ApplyPolicy ui:RenderPolicy
gograph workspace impact --scope oss idp-oss:ApplyPolicy
gograph workspace impact --scope oss --include-possible idp-oss:ApplyPolicy
gograph workspace mcp --tags=integration
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

### Dynamic HTTP URL bases

Repository facts record imported, unshadowed `net/http.Get`, `Post`, `PostForm`,
and `Head` calls, plus **possible request construction** through `NewRequest`
and `NewRequestWithContext`. Request construction alone does not prove dispatch;
its workspace relation requires `--include-possible` to traverse. Client receiver
methods (`client.Do`, `client.Get`) and arbitrary URL-builder functions are not
inferred by this extractor.

Static strings, local constants, and bounded lexical aliases/concatenations are
recognized. For dynamic URLs such as `cfg.IDPURL + "/items"` or
`os.Getenv("IDP_URL") + "/items"`, `http_clients` explicitly maps the lexical
`base` to a logical service `authority_id`. Each base is unique within its member;
it is an identifier/selector (`cfg.IDPURL`) or an `env:KEY` token. This is a
repository-wide configuration promise, not discovered runtime configuration.
Gograph never reads the environment value, executes configuration code, or guesses
ownership from variable or hostname conventions.

`path_prefix` describes the path portion of the complete base URL (empty by
default). It must be an absolute URL path without an authority, query, fragment,
or dot segments. Gograph concatenates prefix and static suffix; it does not
resolve a suffix as a replacement URL. For example, `/v1` plus `/items` yields
`/v1/items`. Query/fragment suffixes do not become contract identity. A dynamic
tail such as `base + "/items/" + id` is not promoted into an exact route.
Package variables and escaped local strings are not assumed constant.

Mappings participate in the workspace input fingerprint. Their service owner
must exist in the **selected scope**, so an OSS/CE or other out-of-scope match
cannot resolve a client. An absent owner leaves diagnostic evidence; it does not
silently select another scope. Exact configured resolution still needs an exact
caller identity and a known method; every uncertain dependency degrades traversal.

`workspace query` and `gograph_workspace_query` include a filtered
`http_unresolved` array alongside node `results`. Search by URL/base, method,
caller, file, or reason (for example `unconfigured_base`,
`authority_not_in_scope`, or `dynamic_url_not_bounded`). The persisted scope
overlay keeps the same evidence. These records are **not edges**, including
under `--include-possible`. Verified `workspace status` / MCP status reports
`overlay.http_unresolved_by_scope` counts; unresolved calls do not make a fresh
artifact stale. Counts are omitted until the overlay has passed verification.

Rebuild member graphs to populate `net_http_v2` extraction facts, then rebuild
the overlay. `workspace build --refresh-members` performs that explicitly.
Older overlays become stale because the HTTP resolver is `http-contract-v2`.

Resolution certainty and provenance are independent:

```text
resolution_status: exact | ambiguous | possible
evidence_origin: structural | configured | derived
```

Only exact relationships participate in default `path` and `impact`
traversal. `--include-possible` opts into ambiguous and possible relationships
for exploration. These weaker relationships cannot satisfy future machine
validation unless a predicate explicitly requests them.

When more than one traversable path exists, `workspace path` and
`gograph_workspace_path` select the same deterministic best route. Ranking is
lexicographic: exact before ambiguous before possible; then shorter paths;
production before tests; typed resolution before heuristics; and fewer
cross-repository transitions when otherwise equivalent. A canonical
relationship/provenance key breaks complete ties, so member or edge iteration
order cannot change the answer.

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
