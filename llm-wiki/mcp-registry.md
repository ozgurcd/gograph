---
title: Official MCP Registry Distribution
type: decision
status: current
updated: 2026-07-13
sources:
  - SRC-20260712-mcp-registry-spec
  - SRC-20260712-mcpb-spec
---

# Official MCP Registry Distribution

## Live publication

The official Registry entry `io.github.ozgurcd/gograph` version `1.5.0` is active and marked latest. The exact API record is `https://registry.modelcontextprotocol.io/v0.1/servers/io.github.ozgurcd%2Fgograph/versions/1.5.0`; discovery search returns exactly one matching server. GitHub release `v1.5.0` is at `https://github.com/ozgurcd/gograph/releases/tag/v1.5.0`.

The immutable tag `v1.5.0` dereferences to implementation commit `e4f96315ec4edb805dddbdd584fffbc022f18c6d`. Workflow recovery commit `4299e2806a87c43343584f941159a413ade156d3` added the release-test binary prerequisite and an explicit existing-tag dispatch path without moving that tag. Successful release and Registry publication run `29242849952` used GitHub OIDC. The initial tag-triggered run failed before creating any release or Registry state because existing CLI contract tests expected `bin/gograph`; this was corrected on `main`, and the original tag was reverified and published through the safe dispatch path.

Post-publication verification downloaded all 14 release assets. All six ordinary archives and all six MCPBs matched `checksums.txt`; `server.json` and `checksums.txt` matched GitHub's asset digests. The downloaded native MCPB initialized as gograph 1.5.0 and returned 65 tools. The Homebrew tap formula is 1.5.0 and its four platform archive URLs and hashes match the published release.

## Identity and pinned formats

Metadata includes immutable GitHub repository ID `1233398203`, website `https://gograph.identuum.ai`, and stdio transport. Validation pins Registry schema `2025-12-11`, MCPB manifest `0.4` from `@anthropic-ai/mcpb@2.1.2`, and `mcp-publisher v1.7.9`. Vendored schemas and provenance are under `internal/mcpbundle/schemas/`.

## Representation

Each deterministic MCPB ZIP contains only `manifest.json`, `LICENSE`, and `server/gograph` (or `server/gograph.exe`). The binary is built with CGO disabled, trimpath, no VCS embedding, and an exact linked release marker. Portable build metadata validation checks OS, architecture, baseline architecture level, module, and CGO-disabled link settings. Linux must have no dynamic interpreter or libraries; Darwin and Windows may use only platform system libraries.

The manifest requires a `project_directory` directory input and launches without a shell:

```json
"command": "${__dirname}/server/gograph",
"args": ["mcp", "${user_config.project_directory}"]
```

Registry packages omit `packageArguments`; the embedded MCPB launch configuration is authoritative, avoiding duplicate arguments.

## Targets and limitation

Six assets named `gograph_<version>_<goos>_<goarch>.mcpb` cover darwin, linux, and windows on amd64 and arm64. The manifest declares the truthful OS and namespaced architecture metadata. Registry packages currently have no OS/CPU selector, and MCPB has no standard CPU field, so preview clients may require manual asset choice. Homebrew or `go install` plus `gograph mcp <project-directory>` remains the fallback.

## Publication invariants

The tag workflow is `verify -> release -> registry`.

- Verify requires the tag commit on `main`, aligned versions, all repository checks, six deterministic bundles, schema/layout/hash checks, native initialize plus `tools/list`, docs, and a GoReleaser dry run.
- Release alone receives `contents: write`. It preserves ordinary archives and checksums while adding MCPBs and `server.json`; it never replaces assets. The generated Homebrew formula is reconciled idempotently afterward so a tap failure is safely rerunnable.
- Registry receives only `contents: read` and `id-token: write`, verifies public assets and hashes, verifies the pinned publisher checksum, authenticates with GitHub OIDC, publishes, and waits for exact-version `active` status.
- Matching immutable state is a no-op. Missing state proceeds. Partial or divergent state fails closed. Never reuse or rewrite a tag, release, asset, or Registry version.
- If a tag-triggered run fails before publication, fix the workflow on `main` and dispatch `release.yml` with the existing tag input. The recovery path checks out, dereferences, and fully re-verifies that tag; it does not move or recreate it.

MCPB changes distribution only: gograph remains local over stdio with no hosted telemetry. Registry and client support remain preview limitations; CLI, graph, Homebrew, Go installation, and ordinary archives remain compatible.
