---
title: Official MCP Registry Distribution
type: decision
status: current
updated: 2026-09-06
sources:
  - SRC-20260712-mcp-registry-spec
  - SRC-20260712-mcpb-spec
  - SRC-20260803-gograph-live-distribution
---

# Official MCP Registry Distribution

## Live publication

The official Registry entry `io.github.ozgurcd/gograph` includes active immutable version `1.7.0`. Its exact API record is `https://registry.modelcontextprotocol.io/v0.1/servers/io.github.ozgurcd%2Fgograph/versions/1.7.0`. GitHub release `v1.7.0`, published 2026-09-06, is at `https://github.com/ozgurcd/gograph/releases/tag/v1.7.0`. The checked-in `server.json`, local annotated tag, GitHub release, and Registry package hashes agree on 1.7.0; the tag dereferences to `9ba68b68fe1397d5b0fafae4ab6976736e9b3827`.

The immutable tag `v1.5.0` dereferences to implementation commit `e4f96315ec4edb805dddbdd584fffbc022f18c6d`. Workflow recovery commit `4299e2806a87c43343584f941159a413ade156d3` added the release-test binary prerequisite and an explicit existing-tag dispatch path without moving that tag. Successful release and Registry publication run `29242849952` used GitHub OIDC. The initial tag-triggered run failed before creating any release or Registry state because existing CLI contract tests expected `bin/gograph`; this was corrected on `main`, and the original tag was reverified and published through the safe dispatch path.

Post-publication verification for v1.7.0 checked all 14 release assets and exact MCPB hashes. The six ordinary archives, six MCPBs, `checksums.txt`, and `server.json` are present; the native MCPB exposes 68 tools. Release workflow `34064579154` passed all verification, GitHub/Homebrew publication, GitHub OIDC Registry publication, and active-record verification. Release-source CI `34063890164`, recovery-main CI `34064577374`, and documentation deployments `34063890177` / `34064577395` passed.

Homebrew tap commit `a6bad5d` publishes cask 1.7.0 with structured `postflight_steps`. The installed `/opt/homebrew/bin/gograph` and a newly launched MCP process both report 1.7.0; `brew doctor` is clean. Installed-binary acceptance on identuum-idp-oss returned the identical 259 production routes in CLI and MCP across three pages. CLI page bytes were 28161/27116/17094; full MCP JSON-RPC page bytes were 23609/22562/14501. All route cursors and a two-page common-query cursor sample round-tripped, the new capability contracts were present, and the persisted repository graph remained byte-identical. Existing MCP processes still require restart after an upgrade.

The initial v1.7.0 workflow `34063890449` correctly refused publication because preparation used Go 1.27.1 while the hosted compiler was 1.27.0. A local six-target rebuild with 1.27.0 reproduced every hosted mismatch exactly. Recovery commit `ec75c77` aligned the workflow compiler to 1.27.1, updated its regression assertion and maintainer guidance, then dispatched the original tag without changing it or bypassing hash checks.

## Identity and pinned formats

Metadata includes immutable GitHub repository ID `1233398203`, website `https://gograph.identuum.ai`, and stdio transport. Validation pins Registry schema `2025-12-11`, MCPB manifest `0.4` from `@anthropic-ai/mcpb@2.1.2`, `mcp-publisher v1.7.9`, the local ordinary-archive gate to GoReleaser `v2.17.0`, GitHub Actions Grype `v0.116.1`, and the release compiler to Go `1.27.1`. Vendored schemas and provenance are under `internal/mcpbundle/schemas/`.

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

Before preparation, the exact local Go compiler patch version must match `GO_VERSION` in the release workflow. Deterministic MCPB hashes are not promised across different compiler versions. For an owner-approved minor version, follow the explicit aligned-metadata/annotated-tag resume procedure in `docs/mcp-registry.md`; automatic version selection remains patch-only.

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
