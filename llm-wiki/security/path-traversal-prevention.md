# Repository Filesystem Confinement

## Status

Implemented on 2026-08-04. Newly built graphs carry repository source-policy version 1. Graphs with a missing, older, or future marker are rebuild-required.

Credit for the original report: Dostxodjayev Abdullox (GitHub: `@squeeze440`).

## Threat model

The protected case is a static untrusted checkout containing symlinks, dangling links, special files, crafted persisted graphs, or path values intended to redirect gograph outside the selected project. An explicitly supplied symlink for the repository root remains supported; descendant entries are untrusted.

This boundary prevents the reported arbitrary Go-source disclosure through scanner indexing plus CLI `source` or MCP `gograph_source`. It also prevents repository-controlled redirects for persisted artifacts, default/relative configuration, and local mutations.

Dependency and toolchain resolution outside the applicable local module/workspace trees remains open-world under the operator's Go environment. Concurrent same-user mutation or mount replacement after validation is outside the static-checkout guarantee; do not describe the design as a general sandbox for `cmd/go`.

## Rooted filesystem layer

`internal/sourcefs` uses `os.OpenRoot` and relative rooted operations. Reads require a local path, reject absolute paths and traversal, Lstat every component, reject descendant symlinks and special final entries, open through the root handle, and compare the opened identity with the inspected regular file. Mutation helpers apply the same component checks to directory creation, regular writes/appends, directory reads, and cleanup.

The selected repository root may itself be a symlink. Root discovery only recognizes a real `.gograph` directory and artifact refresh tracking uses Lstat/type gates for the real artifact directory and regular `graph.json`.

## Scanner and parser

`internal/scanner` treats every extension recognized by `go/build` as a build input. Descendant linked or special recognized inputs are reported and excluded before build-constraint inspection or `go/build.ImportDir`. Directory enumeration supplies Lstat-derived information, and AST parsing receives bytes opened through the rooted reader instead of reopening a path.

The stronger preflight used before precise package loading and `go doc` rejects source-tree links `cmd/go` may inspect without following their targets, including otherwise-unrecognized file links. It scans the selected root plus the effective module root, or the workspace root and every applicable member tree; `.git` and `.gograph` are excluded from that walk.

## Go module and workspace metadata

Before the first `cmd/go` invocation, build-context validation checks the applicable regular, non-linked `go.mod`, optional `go.sum`, `go.work`, optional `go.work.sum`, and module/workspace `vendor/modules.txt`.

Every applicable `go.work use` path must remain beneath its workspace directory. Relative and absolute paths beneath the canonical workspace or a filesystem-identity-verified alternate spelling are normalized to a local rooted name. Each member directory, required `go.mod`, and optional `go.sum` is validated without following descendant links. The validator returns the effective module root, or the workspace root plus member roots, so precise loading and CLI/MCP `doc` can preflight each confined local source tree before `packages.Load` or `go doc`.

A lexical alias is never trusted merely because it was ascended in parallel with a canonical root; `os.SameFile` must prove it names the canonical workspace. Outside and linked members fail closed.

## Query-time source reads

CLI and MCP share the same search implementations. `source`, caller/callee snippets, complexity, changed-file parsing, coupling module reads, and precise loaded-source validation use rooted regular-file access. Graph file names and ranges are treated as untrusted metadata. A poisoned graph naming a repository symlink cannot cause an outside read.

## Persisted graph trust and publication

`.gograph/graph.json` must be a regular repository-confined file beneath a real `.gograph` directory. Its serialized root is metadata only; the trusted load location replaces it before filesystem access. Saved JSON baselines must also be regular non-linked files inside the selected project and carry the exact current source-policy marker.

Graph/report publication requires a real artifact directory and regular-or-absent lock entry. Reports are renamed first and `graph.json` last as the publication marker. Manual `.gitignore` updates reject linked or special targets. Older gograph binaries do not enforce source-policy version 1 and must not be used for untrusted repositories.

## Repository-controlled configuration and mutations

Default or relative check/flow/boundary/gate configuration uses project-rooted regular-file reads. Session pointers/logs, snapshots, boundary creation, gate initialization, relative wiki output, graph artifacts, locks, and cleanup use rooted operations and validated names. Documented absolute check or wiki paths are explicit operator-selected local locations.

## Verification

Regression coverage includes:

- the disclosed `*.go` symlink to an outside Go file, for scanner, CLI source, and MCP source;
- dangling links, linked directories, special recognized build inputs, and poisoned graph paths;
- linked `go.mod`, sums, workspace metadata, vendor metadata, workspace members, sibling-member source links, and workspace-vendor source links;
- absolute workspace members beneath the real workspace and false lexical-alias parents;
- precise loading and CLI/MCP `doc` preflights across effective module/workspace trees;
- linked graph/config/artifact/session/snapshot/wiki paths and explicitly symlinked project-root compatibility.

The full normal and race-enabled test suites, vet, staticcheck, golangci-lint, fuzz targets, `govulncheck`, documentation checks, and self-hosted precise build/review passed on the final tree.
