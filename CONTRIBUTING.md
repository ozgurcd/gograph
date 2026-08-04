# Contributing to gograph

First off, thank you for considering contributing to `gograph`! It's people like you that make open source such a great community.

## Language Scope

`gograph` intentionally analyzes Go repositories. Multi-language parsing is a
current non-goal; open an issue before proposing any change to that product and
architecture boundary.

## Development Setup

Install the Go version declared by [`go.mod`](go.mod) (currently Go 1.26.5 or
newer) and GNU Make. The Makefile injects version metadata, so repository builds
must use `make build` rather than a raw `go build` command.

1. Fork the repository on GitHub.
2. Clone your fork locally:
   ```bash
   git clone https://github.com/YOUR_USERNAME/gograph.git
   cd gograph
   ```
3. Build the project:
   ```bash
   make build
   ```
4. Run tests:
   ```bash
   make test
   ```

## Pull Request Process

1. **Create a branch:** Create a new branch for your feature or bugfix (`git checkout -b feature/my-new-feature`).
2. **Write code:** Implement your changes. Ensure you add tests if you are adding new functionality.
3. **Format and verify:** Run `make test` before committing. It disables the Go
   test cache for both the normal and race suites, runs formatting, vet, lint,
   static analysis, and `govulncheck`, and applies Grype's high-severity gate
   to `go.mod` plus the freshly rebuilt native binary. CLI executable tests
   compile the current checkout into an ephemeral directory and use isolated
   fixtures; they never reuse `bin/gograph`, `bin/gograph-test`, or a
   repository-resident graph. Install the tools used by the Makefile first.
4. **Commit:** Write clear, concise commit messages.
5. **Push:** Push to your fork and submit a Pull Request against the `main` branch.
6. **Review:** Maintainers will review your PR, suggest changes if needed, and merge it.

## CLI, MCP, and Documentation Contracts

Query, analysis, and workflow features must have semantically equivalent CLI
and MCP entry points with tests for both. Host/build operations such as
`build`, `gate`, `snapshot`, plugin/hook installation, MCP server startup,
help, and version may remain CLI-only. Output presentation may differ by
transport: CLI commands use flags such as `--json`, `--files-only`, and
`--mermaid`, while MCP tools expose typed parameters and content payloads.

Document user-visible behavior in `README.md`,
`docs/coding-agent-usage.md`, CLI help/capabilities, the public docs site,
`RELEASE_NOTES.md`, and any affected integration metadata. For visual parity,
the eight graph-oriented CLI commands that accept `--mermaid` have MCP tools
with an optional `mermaid=true` parameter; both surfaces return
Markdown-fenced Mermaid for that presentation.

Use Scrinium for durable wiki maintenance. Read `llm-wiki/index.md`,
`llm-wiki/project.md`, `llm-wiki/agent-rules.md`, and
`llm-wiki/agent-contract.md` before project changes. Do not edit the protected
`agent-rules.md` directly; propose changes through Scrinium's draft workflow.

## Publishing an MCP Registry Release

The official Registry entry is `io.github.ozgurcd/gograph`. Registry/MCPB
publication is part of the normal tagged release; it must not replace or remove
the ordinary archives, checksums, or Homebrew update. The official Registry is
currently in preview, and published versions and metadata are immutable.

Current release pins are Registry schema `2025-12-11`, MCPB manifest schema
`0.4`, `@anthropic-ai/mcpb` `2.1.2`, `mcp-publisher` `1.7.9`, and GoReleaser
`v2.17.0`; GitHub Actions vulnerability scans use Grype `v0.116.1`. Review the
official upstream schema and release notes before changing a pin. CI must
download the publisher by exact version, verify its pinned SHA-256 digest, and
must not use a moving `latest` URL.

The normal maintainer flow remains one command. Commit the feature or fix on
any attached branch whose HEAD includes the latest official `main`, leave the
worktree clean, and run:

```bash
make release
```

No version argument is required. The target computes the next patch version,
builds all six MCPBs, renders their immutable URLs and SHA-256 hashes into
`server.json`, runs the complete release verification and native MCP smoke
test, verifies modules and `go mod tidy`, runs `go vet`, and builds a pinned,
non-publishing GoReleaser snapshot in the temporary release directory. The
gate scans only explicit current inputs: declared modules in `go.mod`, the
fresh native binary, and each of the exact six newly generated ordinary
GoReleaser archives. A missing or extra archive fails closed. Ambient ignored
outputs under `bin/`, `dist/`, `.release-mcpb/`, and `.release-work/` are not
release evidence and are never included by a repository-wide Grype scan. It
then commits only the generated version/release metadata and creates an
annotated version tag. It atomically pushes that exact commit to the official
remote's `main` together with the tag. The tag starts the immutable GitHub
release, Homebrew reconciliation, and official MCP Registry publication
workflow.

The command fails closed before publishing unless HEAD is attached to a branch,
the worktree is clean, the selected remote's `main` is an ancestor of HEAD, the
current declared version has a remote baseline tag in that history, the next
version is unused, and every build and validation gate passes. The selected
remote's push URL must be the official `ozgurcd/gograph` repository, and the
remote update is therefore a fast-forward. The coordinator never checks out,
merges, rebases, force-pushes, changes local `main`, or pushes the working
branch ref. A clone whose official remote is named `upstream` can use
`make release RELEASE_REMOTE=upstream`. This preserves the old `git commit`
followed by `make release` workflow while including the new MCPB and Registry
gates. If the command is rerun at the same already-tagged release commit, it
recognizes that release and does not increment the patch version again.

Use `make release-dry-run` to exercise the same automatic patch preparation,
full verification, and immutable-state checks while restoring the metadata and
creating no commit, tag, or push. If an atomic push fails, the coordinator
retains the already-verified local release commit and tag. Rerun `make release`
to retry that same version when remote `main` still permits a fast-forward; if
the unchanged release commit later reaches remote `main`, a rerun can publish
only the missing tag without moving `main`. An incompatible remote advance
fails closed and requires manual reconciliation of the unpublished local
release state. Branch protection may likewise require repository-specific
approval before the atomic update can succeed.

For CI or diagnosis of an already prepared release state, the non-publishing
gate remains available:

```bash
make release-verify
```

This advanced target runs the cache-disabled local suites; explicit
source/native/archive vulnerability scans; and the full MCPB, schema, hash,
documentation, and smoke-test checks against the currently declared version
without creating a commit, tag, or push.

After the atomic push, GoReleaser publishes the ordinary assets, six MCPBs,
checksums, and `server.json`. The workflow reconciles GoReleaser's generated
Homebrew formula idempotently, waits for every referenced GitHub asset to be
publicly downloadable, publishes with GitHub Actions OIDC, and verifies the
Registry record and downloadable hashes. Do not add a long-lived Registry
token.

Reruns may no-op only when the existing GitHub release and Registry record
match exactly. Any mismatch must fail closed, and no existing version, tag, or
release asset may be rewritten. Use the repository's independent validation as
the release gate rather than relying on publisher 1.7.9's standalone
`mcp-publisher validate` command. The complete package-selection, security, and
maintenance rationale is in
[docs/mcp-registry.md](docs/mcp-registry.md).

When a tag-triggered run fails before publication, repair the workflow on
`main` and use `gh workflow run release.yml --ref main -f tag=vX.Y.Z`. This
re-verifies the existing tag commit; never delete, recreate, or move the tag.

## Code of Conduct
By participating in this project, you agree to abide by our [Code of Conduct](CODE_OF_CONDUCT.md).
