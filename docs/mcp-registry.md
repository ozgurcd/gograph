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

The fixed bundle arguments intentionally keep `--persist-refresh` off. The
server therefore refreshes its working graph in memory and does not publish
refresh artifacts. Users who want successful refreshes to overwrite the
latest `.gograph` graph and reports must use a custom local MCP registration
that adds `--persist-refresh`; that mode does not update `.gitignore` and is
not a branch cache.

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
| GoReleaser local/CI gate | `v2.17.0` |

Treat changes to any pin as a compatibility change: review the official schema
and release notes, update the vendored validation input and tests, and verify
all six targets before changing it. Do not download an unverified moving
`latest` publisher in CI. The release workflow checks the pinned publisher
archive's SHA-256 digest and embedded version before execution.

For a new patch release, first commit the feature or fix on an attached branch
whose HEAD includes the latest official `main`, and leave the worktree clean.
Then run the same command used before Registry support:

```bash
make release
```

The target requires no manually supplied version. It computes the next patch
version and fails closed unless HEAD is attached to a branch, the selected
remote's `main` is an ancestor of HEAD, the worktree is clean, the current
version has a remote baseline tag in that history, and the next version, local
and remote tag, GitHub release, and Registry record are all unused. The
selected remote must push to the official `ozgurcd/gograph` repository; use
`make release RELEASE_REMOTE=upstream` when that remote is not named `origin`.
It then:

1. Updates the version metadata and builds the six deterministic MCPBs.
2. Renders `server.json` from their immutable versioned URLs and SHA-256
   hashes.
3. Runs the complete repository verification suite, validates the schemas,
   manifests, archive layouts, executable paths, version agreement, URLs, and
   hashes, and smoke-tests MCP initialization and `tools/list` from the native
   bundle. It also verifies/tidies modules, runs `go vet`, and builds all
   ordinary release archives with a pinned, non-publishing GoReleaser snapshot
   whose MCPB and output paths remain inside the temporary release transaction.
4. Commits only the generated version/release metadata and creates an
   annotated `v<version>` tag at that exact verified commit.
5. Atomically pushes the exact verified commit to the official remote's `main`
   together with the tag, so neither remote reference advances alone. The tag
   starts the GitHub Actions release workflow.

The checked-out branch remains the release source and stays checked out. The
coordinator does not check out, merge, rebase, force-push, move local `main`,
or push the source branch ref. This lets `git commit` followed by `make release`
work directly from a fast-forward topic or agent branch while keeping remote
`main` as the only publication branch.

A rerun at the same already-tagged release commit recognizes the release and
does not increment the patch version again. For CI or advanced diagnosis of a
prepared release state, run `make release-verify`; it performs the complete
local release gate against the currently declared version without committing,
tagging, or pushing. `make release-dry-run` instead exercises automatic patch
preparation plus the same gates and immutable-state checks, then restores the
metadata without creating any ref or remote change. If an atomic push fails,
the verified local release commit and tag are retained. Rerunning
`make release` retries that same version if remote `main` still allows the
fast-forward. If the unchanged release commit later lands on remote `main`, a
rerun verifies it and atomically publishes only the missing tag while leaving
the newer `main` tip unchanged. An incompatible remote advance fails closed
and requires manual reconciliation of the unpublished local commit and tag.
Protected-branch policy may also require repository-specific approval before
the atomic push can succeed.

GoReleaser publishes the ordinary archives, six MCPBs, `server.json`, and
checksums from the verified tag. The workflow then idempotently reconciles the
generated Homebrew formula, waits until every Registry-referenced asset is
publicly available, authenticates with `mcp-publisher login github-oidc`, and
publishes without a long-lived Registry token. Post-publication checks query
the exact Registry name and version, download and hash every package, repeat
the native MCP smoke test, and confirm the ordinary archives, checksums, and
Homebrew formula remain intact.

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
