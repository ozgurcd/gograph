---
title: Official MCP Registry Distribution
type: decision
status: current
updated: 2026-09-01
sources:
  - SRC-20260712-mcp-registry-spec
  - SRC-20260712-mcpb-spec
  - SRC-20260803-gograph-live-distribution
---

# Official MCP Registry Distribution

## Live publication

The official Registry entry `io.github.ozgurcd/gograph` has active immutable versions 1.5.0 through 1.6.9. Registry discovery marks version `1.6.9` latest; its exact API record is `https://registry.modelcontextprotocol.io/v0.1/servers/io.github.ozgurcd%2Fgograph/versions/1.6.9`. GitHub release `v1.6.9`, published 2026-09-01, is at `https://github.com/ozgurcd/gograph/releases/tag/v1.6.9`. The checked-in `server.json`, local annotated tag, GitHub release, and Registry package hashes agree on 1.6.9; the tag dereferences to `ef414a4e3cf33d350bddbce76bc262bf793f7392`.

The immutable tag `v1.5.0` dereferences to implementation commit `e4f96315ec4edb805dddbdd584fffbc022f18c6d`. Workflow recovery commit `4299e2806a87c43343584f941159a413ade156d3` added the release-test binary prerequisite and an explicit existing-tag dispatch path without moving that tag. Successful release and Registry publication run `29242849952` used GitHub OIDC. The initial tag-triggered run failed before creating any release or Registry state because existing CLI contract tests expected `bin/gograph`; this was corrected on `main`, and the original tag was reverified and published through the safe dispatch path.

Post-publication verification for v1.6.9 checked all 14 release assets. All six ordinary archives and all six MCPBs match `checksums.txt`; `server.json` and `checksums.txt` match GitHub asset digests, and the native MCPB exposes 68 tools. The Homebrew tap cask is 1.6.9, its four platform archive URLs and hashes match the immutable published release, and `tap_migrations.json` retains the same-name formula-to-cask migration. Release workflow run `33540733522` reverified the tagged source, reconciled Homebrew from published checksums, published through GitHub OIDC, and confirmed the active Registry record; CI run `33540733508` and documentation deployment `33540733419` also completed successfully.

## Identity and pinned formats

Metadata includes immutable GitHub repository ID `1233398203`, website `https://gograph.identuum.ai`, and stdio transport. Validation pins Registry schema `2025-12-11`, MCPB manifest `0.4` from `@anthropic-ai/mcpb@2.1.2`, `mcp-publisher v1.7.9`, the local ordinary-archive gate to GoReleaser `v2.17.0`, and GitHub Actions Grype `v0.116.1`. Vendored schemas and provenance are under `internal/mcpbundle/schemas/`.

## Representation

Each deterministic MCPB ZIP contains only `manifest.json`, `LICENSE`, and `server/gograph` (or `server/gograph.exe`). The binary is built with CGO disabled, trimpath, no VCS embedding, and an exact linked release marker. Portable build metadata validation checks OS, architecture, baseline architecture level, module, and CGO-disabled link settings. Linux must have no dynamic interpreter or libraries; Darwin and Windows may use only platform system libraries.

The manifest requires a `project_directory` directory input and launches without a shell:

```json
"command": "${__dirname}/server/gograph",
"args": ["mcp", "${user_config.project_directory}"]
```

Registry packages omit `packageArguments`; the embedded MCPB launch configuration is authoritative, avoiding duplicate arguments.

The fixed MCPB launch intentionally omits `--persist-refresh`, so refreshes remain in memory and do not replace CLI artifacts. Durable refresh publication requires a custom local registration using `gograph mcp <project-directory> --persist-refresh`; that mode publishes only the latest graph plus nine reports, retains `.gograph/.artifacts.lock` as operational coordination state, and does not edit `.gitignore`. Reports are renamed first and `graph.json` last as the commit marker. Same-directory replacement is atomic on Unix-like systems but is not guaranteed atomic by Go on non-Unix platforms; the ten-file bundle is not one atomic transaction.

## Targets and limitation

Six assets named `gograph_<version>_<goos>_<goarch>.mcpb` cover darwin, linux, and windows on amd64 and arm64. The manifest declares the truthful OS and namespaced architecture metadata. Registry packages currently have no OS/CPU selector, and MCPB has no standard CPU field, so preview clients may require manual asset choice. Homebrew or `go install` plus `gograph mcp <project-directory>` remains the fallback.

## Maintainer release command

The normal patch-release flow is: commit the feature or fix on any clean attached branch whose HEAD descends from the fetched official remote's `main`, then run `make release`. No version argument is supplied, and local `main` may be stale. The coordinator validates that the selected remote has exactly one fetch URL and one effective push URL and that both identify the official `ozgurcd/gograph` repository. It fetches remote `main`, requires it to be an ancestor of the captured source HEAD, requires the current version's remote baseline tag in that history, computes the next stable patch, and rejects reuse of local, remote, GitHub, or Registry release state.

Preparation uses a unique ignored `.release-work/` transaction. The coordinator updates only `.bumpversion.cfg`, `plugin.json`, and the deterministically rendered `server.json`; builds and verifies all six MCPBs; and runs `make release-verify`. That gate includes module verification and tidiness, `go vet`, cache-disabled unit and race tests, lint and static analysis, MCPB schema/layout/hash checks, native initialization plus `tools/list`, documentation, and a pinned non-publishing GoReleaser snapshot for ordinary archives and the Homebrew cask. CLI subprocess tests compile the current checkout once into a cleaned OS temp directory and use isolated fixtures instead of trusting `bin/gograph`, `bin/gograph-test`, or ambient `.gograph` state. Vulnerability evidence is restricted to explicit current inputs: `govulncheck` evaluates reachable source, while Grype scans `go.mod`, the freshly rebuilt native binary, and each of the exact six newly generated GoReleaser `.tar.gz`/`.zip` archives. Missing, substituted, or extra matching archives fail closed. Repository-wide `grype dir:.` output and ignored historical artifacts under `bin/`, `dist/`, `.release-mcpb/`, or `.release-work/` are not release evidence. Exact owned-file bytes and modes are rechecked before and after the release commit.

After verification, the coordinator creates the release metadata commit and annotated `v<version>` tag on the still-checked-out source branch. It never checks out, merges, rebases, force-pushes, moves local `main`, or pushes the source branch ref. It atomically pushes the captured verified commit to the official remote's `main` together with only the new tag, so the tag workflow can require that exact commit on `main`.

`make release-dry-run` performs the same preparation, verification, and immutable-state checks, then restores owned metadata without creating a commit, tag, or remote update. Restoration is compare-and-swap so a concurrent editor change is not overwritten. If tagging or a transient atomic push fails, the verified commit and any local tag remain for a same-version retry. If that unchanged commit later reaches remote `main` without its tag, a rerun atomically compares the captured newer `main` tip and publishes only the missing tag. A genuinely divergent remote fails closed and requires manual reconciliation of the unpublished local state. A rerun from an already-tagged and published release commit is a no-op, including when remote `main` subsequently advances.

## Publication invariants

The tag workflow is `verify -> release -> registry`.

- Verify requires the tag commit on `main`, aligned versions, all repository checks, six deterministic bundles, schema/layout/hash checks, native initialize plus `tools/list`, docs, and a GoReleaser dry run.
- Release alone receives `contents: write`. It preserves ordinary archives and checksums while adding MCPBs and `server.json`; it never replaces assets. The generated Homebrew cask is reconciled idempotently afterward so a tap failure is safely rerunnable. The same atomic tap commit adds the same-name formula-to-cask migration record and removes the obsolete formula.
- Registry receives only `contents: read` and `id-token: write`, verifies public assets and hashes, verifies the pinned publisher checksum, authenticates with GitHub OIDC, publishes, and waits for exact-version `active` status.
- Matching immutable state is a no-op. Missing state proceeds. Partial or divergent state fails closed. Never reuse or rewrite a tag, release, asset, or Registry version.
- If a tag-triggered run fails before publication, fix the workflow on `main` and dispatch `release.yml` with the existing tag input. The recovery path checks out, dereferences, and fully re-verifies that tag; it does not move or recreate it.

MCPB changes distribution only: gograph remains local over stdio with no hosted telemetry. Registry and client support remain preview limitations; CLI, graph, Homebrew, Go installation, and ordinary archives remain compatible.
