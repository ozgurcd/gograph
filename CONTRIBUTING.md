# Contributing to gograph

First off, thank you for considering contributing to `gograph`! It's people like you that make open source such a great community.

## Language Extensibility
As mentioned in the README, `gograph` was initially built exclusively for Go. If you are looking to add parsers or support for other languages, we highly encourage this! Please open an issue titled `Feature Request: Support for [Language]` to discuss the implementation approach before writing extensive code.

## Development Setup

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
   *(If you don't have make, simply run `go build -o bin/gograph ./cmd/gograph`)*
4. Run tests:
   ```bash
   make test
   ```

## Pull Request Process

1. **Create a branch:** Create a new branch for your feature or bugfix (`git checkout -b feature/my-new-feature`).
2. **Write code:** Implement your changes. Ensure you add tests if you are adding new functionality.
3. **Format and verify:** Run `make format-check`, `go test -race ./...`, `go vet ./...`, `staticcheck ./...`, `golangci-lint run ./...`, and `govulncheck ./...` before committing. `make test` runs the full local verification suite when those tools and Grype are installed.
4. **Commit:** Write clear, concise commit messages.
5. **Push:** Push to your fork and submit a Pull Request against the `main` branch.
6. **Review:** Maintainers will review your PR, suggest changes if needed, and merge it.

## Publishing an MCP Registry Release

The official Registry entry is `io.github.ozgurcd/gograph`. Registry/MCPB
publication is part of the normal tagged release; it must not replace or remove
the ordinary archives, checksums, or Homebrew update. The official Registry is
currently in preview, and published versions and metadata are immutable.

Current release pins are Registry schema `2025-12-11`, MCPB manifest schema
`0.4`, `@anthropic-ai/mcpb` `2.1.2`, and `mcp-publisher` `1.7.9`. Review the
official upstream schema and release notes before changing a pin. CI must
download the publisher by exact version, verify its pinned SHA-256 digest, and
must not use a moving `latest` URL.

For each new semantic version:

1. Run the full verification documented above and confirm the tag will point
   to that exact verified commit.
2. Set `VERSION` to the new, unused semantic version (`1.5.0` is the initial
   publication), then build all six MCPBs. The command prints each SHA-256:

   ```bash
   VERSION=1.5.0 # replace for each later release
   go run ./cmd/mcpb-release build --version "$VERSION" --output .release-mcpb
   ```

3. Render all six `server.json` package versions, immutable `v<version>` asset
   URLs, and `fileSha256` values from the verified bundles. Then validate the
   pinned schemas, manifest and archive layout, executable paths, version
   agreement, URLs, and hashes, and smoke-test MCP initialization and
   `tools/list` from the native bundle:

   ```bash
   go run ./cmd/mcpb-release render-server \
     --version "$VERSION" \
     --input .release-mcpb \
     --output server.json
   go run ./cmd/mcpb-release verify \
     --version "$VERSION" \
     --input .release-mcpb \
     --server server.json
   go run ./cmd/mcpb-release smoke \
     --version "$VERSION" \
     --input .release-mcpb
   ```

4. Let GoReleaser publish the ordinary assets, six MCPBs, checksums, and
   `server.json`. The workflow then reconciles GoReleaser's generated Homebrew
   formula idempotently, so a tap failure can be retried without replacing
   release assets. Registry publication must wait until every referenced
   GitHub release asset is publicly downloadable.
5. Publish with GitHub Actions OIDC (`id-token: write`, `contents: read`) using
   `mcp-publisher login github-oidc`; do not add a long-lived Registry token.
6. Query the official Registry for the exact name and version, re-download and
   hash every package, repeat the native MCP smoke test, and confirm Homebrew
   and the ordinary release archives remain intact.

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
