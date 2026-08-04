---
title: "Official MCP Registry"
weight: 5
description: "Discover and install gograph as a local MCP Bundle, select the correct platform asset, and understand the preview limitations."
---

gograph is published to the official Model Context Protocol Registry as:

```text
io.github.ozgurcd/gograph
```

The official Registry is currently in preview. Its API, stored data, and
client support may change before general availability.

## Registry installation is a separate path

Homebrew and `go install` place the normal `gograph` CLI on `PATH`. You then
configure an MCP client to run `gograph mcp <project-directory>`. The Claude
Code marketplace entry supplies workflow guidance but still needs that binary
and a project registration.

Registry installation instead downloads a self-contained MCPB into a client
that supports MCP Bundles. It does not install the Homebrew formula, run
`go install`, or configure the Claude Code marketplace plugin.

| Installation | Result |
|---|---|
| `brew install ozgurcd/tap/gograph` | CLI binary plus manual local MCP registration |
| `go install github.com/ozgurcd/gograph/cmd/gograph@latest` | CLI binary plus manual local MCP registration |
| Registry / MCPB | Platform-specific local server bundle managed by an MCPB-capable client |

Clients without MCPB support should use either normal binary installation
method.

## Select the Go project directory

The bundle asks for the root directory of the Go repository to analyze. The
Registry identifies the package as local stdio MCPB metadata; the bundle
manifest launches the executable with distinct arguments equivalent to:

```bash
gograph mcp /absolute/path/to/go-project
```

The manifest does not construct a shell command. It passes `mcp` and the
selected directory as separate argument values. Configure a separate project
directory for each repository-specific server instance.

The fixed bundle arguments omit `--persist-refresh`, so MCP refreshes stay in
memory and do not overwrite project artifacts. To opt into latest-state
publication, use a custom local registration with that flag. The opt-in mode
does not update `.gitignore` and is not a per-branch cache. It publishes
`graph.json` plus nine Markdown reports under `.gograph/`: graph/report
publishers coordinate through `.artifacts.lock`, rename reports first, and
rename `graph.json` last as the commit marker. Same-directory replacement is
atomic on Unix-like systems but is not guaranteed atomic by Go on non-Unix
platforms; the complete bundle is not one atomic transaction, and the lock
file remains as separate operational state.

## Supported targets

Every release supplies six genuine MCPB archives:

| Host | Target | Asset suffix |
|---|---|---|
| macOS Intel | `darwin/amd64` | `darwin_amd64.mcpb` |
| macOS Apple silicon | `darwin/arm64` | `darwin_arm64.mcpb` |
| Linux x86-64 | `linux/amd64` | `linux_amd64.mcpb` |
| Linux ARM64 | `linux/arm64` | `linux_arm64.mcpb` |
| Windows x86-64 | `windows/amd64` | `windows_amd64.mcpb` |
| Windows ARM64 | `windows/arm64` | `windows_arm64.mcpb` |

The complete asset pattern is
`gograph_<version>_<goos>_<goarch>.mcpb`.

### Preview limitation: clients cannot reliably select by CPU

The current Registry package schema has no standard OS or CPU selector. MCPB
manifests declare `darwin`, `linux`, or `win32`, but do not have a standard
architecture field. A Registry entry can therefore list all six bundles while
still leaving package selection to the client or user.

Choose the filename matching the host architecture. If the client does not
offer that choice, use Homebrew or `go install` and configure the local stdio
command manually. The filename is the architecture discriminator, not a
portable automatic-selection mechanism.

## Local operation and data handling

Registry installation changes packaging, not gograph's security model:

- The MCP server runs locally over stdio and opens no listening port.
- Source, graphs, query results, and session telemetry are not sent to a
  gograph service.
- Optional session metadata stays under the selected project's
  `.gograph/sessions/`; raw query results are not logged there.
- Default indexing parses source without executing the target repository's
  binaries or tests.
- Precise analysis and `doc` use the installed Go toolchain and therefore
  follow its configured module cache, proxy, and network policy.

## Publication integrity

Current releases pin Registry schema `2025-12-11`, MCPB manifest schema `0.4`,
`@anthropic-ai/mcpb` `2.1.2`, and `mcp-publisher` `1.7.9`. Release automation
builds and validates every bundle, records each SHA-256 in `server.json`,
publishes the immutable GitHub assets first, and then authenticates to the
Registry with GitHub Actions OIDC. No long-lived Registry token is stored.

Registry versions are immutable. Reruns succeed only when the existing GitHub
and Registry metadata match exactly; mismatches fail rather than overwriting a
publication. Maintainers can find the complete release checklist in the
repository's `docs/mcp-registry.md`.
