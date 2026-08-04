# Privacy Policy

**Effective date:** 2026-08-04

## Overview

Gograph is a local static-analysis tool. Gograph itself has no hosted service and does not collect or transmit repository code or personal data. It does write project artifacts and optional workflow metadata locally.

## Data Collection

Gograph collects **no data**. Specifically:

- **No code is uploaded.** All AST parsing and graph analysis runs locally against files on your filesystem.
- **No remote telemetry.** Optional audit sessions write command metadata to `.gograph/sessions/`; raw query results are not logged or transmitted.
- **Local MCP transport.** The MCP server communicates over stdio and opens no listening port. Default AST indexing makes no application-service calls, but asks the installed Go toolchain for effective build/module context. Precise mode additionally type-loads packages, and `gograph doc` runs `go doc`; these operations follow the user's configured module cache/proxy/network policy. MCP refreshes remain in memory unless the server is explicitly started with `--persist-refresh`.
- **No accounts or authentication.** Gograph requires no login, API key, or user account.

## Repository Data

When you run `gograph build .`, it reads selected regular Go source plus project metadata such as `go.mod`, `go.sum`, `go.work`, `go.work.sum`, `vendor/modules.txt`, `.gitignore`, and Git ignore state. Linked/non-regular Go module/workspace metadata, sums, workspace members, and vendor metadata are rejected before build-context resolution invokes `cmd/go`. The scanner then reports and excludes descendant links or special files for every extension recognized by `go/build` before build selection or AST reads; an explicitly symlinked repository root is supported. AST parsing and graph-directed source reads use repository-rooted file access, reject symlink path components, and cannot follow a source path outside the analyzed root. Applicable `go.work use` members must remain beneath the workspace directory; their directories, `go.mod`, and optional `go.sum` are validated before `cmd/go`. Before precise package loading or `doc`, source trees are preflighted for links `cmd/go` may inspect across the selected root plus its effective module root, or the workspace root and member trees; `.git` and `.gograph` subtrees are excluded from that walk. `doc` also rejects filesystem-shaped queries, while dependency/toolchain resolution remains open-world under the user's Go environment.

Persisted `graph.json` must be a regular repository-confined file with the exact current source-policy marker. Its serialized `root` is metadata; the trusted load location replaces it before filesystem access. A saved `.json` baseline must likewise be a regular, non-linked file—including no linked ancestor—inside the selected project with the exact marker, and its serialized root is ignored. Other commands may read check/flow/boundary JSON or YAML and Git state. Default and relative check configs, flow configs, boundaries, and `.gograph.yml` are read as regular non-linked project files; an explicitly absolute check config selects another regular local file.

Gograph writes `.gograph/graph.json`, Markdown reports, snapshots, boundary configuration, the `gate init` configuration, wiki pages, and optional session logs locally. Repository-relative session, snapshot, boundary, gate-init, and wiki paths use rooted operations that reject descendant links and special files; an explicit absolute wiki output selects another local root. Publication refuses a linked or non-directory `.gograph` or a linked/non-regular artifact lock, and manual build refuses a linked `.gitignore` rather than modifying its target. `source`/`context` return requested Go source, inline route-handler bodies may be stored in the graph, and compact source/transfer/sink facts are stored for query-time security-flow analysis. Persisted graphs with a missing or unsupported marker are not trusted and must be rebuilt. Older binaries do not enforce this confinement and should not be used for untrusted repositories.

`gograph mcp [path] --persist-refresh` is an explicit local-write mode. After
a successful source refresh it writes or overwrites `.gograph/graph.json` and
the generated Markdown reports under the analyzed project, using
`.gograph/.artifacts.lock` for local writer coordination. It does not change
`.gitignore`; fixed plugin and MCP bundle configurations leave the option off.
Only the latest state is retained, not a history or per-branch cache. An
initial auto-build publication failure prevents server startup. A later
tool-triggered failure is returned to that tool, while the server keeps the
fresh in-memory graph for a publication retry.

## Third-Party Services

Gograph has no analytics or hosted backend integration. Local integrations include Git, the Go toolchain, MCP clients, and optional Claude configuration files; those tools retain their own policies and permissions.

## Contact

For questions, open an issue at: https://github.com/ozgurcd/gograph/issues
