# Official MCP Registry and MCPB Distribution

gograph is published to the official Model Context Protocol Registry as
`io.github.ozgurcd/gograph`. The Registry is currently in preview: its API,
stored data, and client support may change before general availability.

## Choose the installation path that matches your client

The available installation paths solve different problems:

| Path | What it installs | How MCP starts |
|---|---|---|
| Homebrew | The normal `gograph` CLI on `PATH` | Configure the client to run `gograph mcp <project-directory>` |
| `go install` | The normal `gograph` CLI in `GOBIN` or `GOPATH/bin` | Configure the client to run `gograph mcp <project-directory>` |
| Official Registry / MCPB | A self-contained, platform-specific bundle for an MCPB-capable client | The bundle prompts for the project directory and starts the bundled binary |
| Claude Code marketplace | gograph workflow guidance and plugin metadata | The `gograph` binary must still be installed and registered for the project |

Registry installation is therefore an alternative binary distribution path,
not another spelling of `brew install`, `go install`, or the Claude Code plugin
command. Clients that do not support MCPB should use the normal binary plus a
local stdio configuration.

## Registry discovery and configuration

Search for the exact server name:

```text
io.github.ozgurcd/gograph
```

During installation, the bundle asks for one required directory: the root of
the Go repository to analyze. Registry metadata identifies the package as a
local stdio MCPB; the bundle manifest represents the launch as an executable
plus two separate arguments:

```json
{
  "command": "${__dirname}/server/gograph",
  "args": ["mcp", "${user_config.project_directory}"]
}
```

This is equivalent to the following manual setup, without shell interpolation:

```bash
gograph mcp /absolute/path/to/go-project
```

Select a different project directory for each repository-specific server
configuration. gograph anchors its graph, source refresh, configuration, Git
operations, and local session metadata to that analyzed project.

## Supported release targets

Each release provides a genuine MCPB ZIP containing `manifest.json`, `LICENSE`,
and one CGO-disabled, self-contained gograph executable. The six
OS/architecture targets are listed below. Unix bundles use `server/gograph`;
Windows bundles use `server/gograph.exe`. As with other native applications,
Darwin and Windows executables can use operating-system libraries.

| Host | Go target | MCPB manifest platform | Asset suffix |
|---|---|---|---|
| macOS on Intel | `darwin/amd64` | `darwin` | `darwin_amd64.mcpb` |
| macOS on Apple silicon | `darwin/arm64` | `darwin` | `darwin_arm64.mcpb` |
| Linux x86-64 | `linux/amd64` | `linux` | `linux_amd64.mcpb` |
| Linux ARM64 | `linux/arm64` | `linux` | `linux_arm64.mcpb` |
| Windows x86-64 | `windows/amd64` | `win32` | `windows_amd64.mcpb` |
| Windows ARM64 | `windows/arm64` | `win32` | `windows_arm64.mcpb` |

Release assets use the complete pattern
`gograph_<version>_<goos>_<goarch>.mcpb`.

### Registry preview limitation: CPU selection

The current official `server.json` package format has no standardized OS or
CPU selector. The MCPB manifest can declare `darwin`, `linux`, or `win32`, but
has no standard CPU-architecture field, and clients see that manifest only
after downloading the bundle. Consequently, a Registry entry can list all six
valid packages but cannot guarantee that every client will automatically pick
the native one.

Choose the package whose filename matches the host architecture. If a client
does not expose that choice, install gograph with Homebrew or `go install` and
register `gograph mcp <project-directory>` manually. The asset filename is the
architecture discriminator; it is not a portable automatic-selection
mechanism.

## Local data and network behavior

An MCPB installation does not turn gograph into a hosted service:

- The server runs locally over stdio and opens no listening port.
- gograph sends no source, graph, query result, or session telemetry to a
  gograph service.
- Optional audit sessions write metadata under the selected project's
  `.gograph/sessions/` directory; raw query results are not logged there.
- Default indexing parses source locally and does not execute the target
  repository's binaries or tests.
- Precise analysis and `gograph doc` invoke the installed Go toolchain, which
  follows the user's configured module cache, proxy, and network policy.

The selected project directory is intentionally visible to the local MCP
client because gograph must read its Go source, `go.mod`, Git metadata, ignore
rules, and gograph configuration to provide the requested analysis.

## Maintainer release process

The release implementation is pinned to these official formats and tools:

| Component | Pinned version |
|---|---|
| Registry `server.json` schema | `2025-12-11` |
| MCPB manifest schema | `0.4` |
| MCPB schema provenance/tooling reference | `@anthropic-ai/mcpb` `2.1.2` |
| Registry publisher | `mcp-publisher` `1.7.9` |

Treat changes to any pin as a compatibility change: review the official schema
and release notes, update the vendored validation input and tests, and verify
all six targets before changing it. Do not download an unverified moving
`latest` publisher in CI. The release workflow checks the pinned publisher
archive's SHA-256 digest and embedded version before execution.

For a new release:

1. Choose a new semantic version. Registry versions and their metadata are
   immutable, so never reuse a published version or alter an existing release.
2. Run the repository's complete verification suite. A Registry publication
   must depend on the same verified tag commit that produces the GitHub
   release.
3. Set `VERSION` to the new, unused semantic version (`1.5.0` is the initial
   publication), then build the deterministic bundles:

   ```bash
   VERSION=1.5.0 # replace for each later release
   go run ./cmd/mcpb-release build --version "$VERSION" --output .release-mcpb
   ```

4. Render `server.json` from the six verified bundle hashes and immutable
   versioned URLs, then verify the manifest schemas, archive layout, executable
   paths, version agreement, asset URLs, and every hash:

   ```bash
   go run ./cmd/mcpb-release render-server \
     --version "$VERSION" \
     --input .release-mcpb \
     --output server.json
   go run ./cmd/mcpb-release verify \
     --version "$VERSION" \
     --input .release-mcpb \
     --server server.json
   ```

5. Smoke-test MCP initialization and `tools/list` from the native bundle:

   ```bash
   go run ./cmd/mcpb-release smoke \
     --version "$VERSION" \
     --input .release-mcpb
   ```

6. Create the tag only from the verified commit. GoReleaser publishes the
   ordinary archives, six MCPBs, `server.json`, and checksums. The workflow
   idempotently reconciles the generated Homebrew formula afterward, allowing
   a tap failure to be retried without replacing release assets.
7. Publish to the Registry only after every referenced GitHub release asset is
   publicly available. The GitHub Actions job uses `id-token: write`,
   authenticates with `mcp-publisher login github-oidc`, and publishes without
   a long-lived Registry token.
8. Query the Registry for the exact name and version, download every referenced
   asset, compare its hash, and repeat the native MCP smoke test. Also confirm
   the ordinary release archives, checksums, and Homebrew formula remain
   intact.

Release jobs are safe to rerun only when the already-published GitHub and
Registry metadata match exactly. A mismatch must fail closed; publication must
never overwrite an existing version. The repository's independent schema and
artifact verification is the release gate—do not substitute the standalone
`mcp-publisher validate` command from publisher 1.7.9, whose installed command
surface is not a dependable validation contract.

If the tag-triggered workflow fails before publication, fix the workflow on
`main` and dispatch the existing tag without moving it:

```bash
gh workflow run release.yml --ref main -f tag=vX.Y.Z
```

The recovery path checks out the named tag, dereferences it, requires that
exact commit to be contained in `origin/main`, and repeats every release gate.
It is not permission to delete, recreate, or retarget the tag.
