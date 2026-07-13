---
title: Official MCP Registry Distribution
type: decision
status: current
updated: 2026-07-12
sources:
  - SRC-20260712-mcp-registry-spec
  - SRC-20260712-mcpb-spec
---

# Official MCP Registry Distribution

## Identity and pinned formats

The official name is `io.github.ozgurcd/gograph`; initial publication is `1.5.0` from GitHub release `v1.5.0`. Metadata includes repository ID `1233398203`, the project website, and stdio transport. Validation pins Registry schema `2025-12-11`, MCPB manifest `0.4` from `@anthropic-ai/mcpb@2.1.2`, and `mcp-publisher v1.7.9`. Vendored schemas and hashes are under `internal/mcpbundle/schemas/`.

## Representation

Each deterministic MCPB ZIP contains only `manifest.json`, `LICENSE`, and `server/gograph` (or `server/gograph.exe`). The binary is built with CGO disabled, trimpath, no VCS embedding, and linked release version. Portable build metadata validation checks OS, architecture, baseline architecture level, module, and CGO-disabled link settings. Linux must have no dynamic interpreter or libraries; Darwin and Windows may use only platform system libraries.

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

MCPB changes distribution only: gograph remains local over stdio with no hosted telemetry. Registry and client support remain preview limitations; CLI, graph, Homebrew, Go installation, and ordinary archives remain compatible.
