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

## Code of Conduct
By participating in this project, you agree to abide by our [Code of Conduct](CODE_OF_CONDUCT.md).
