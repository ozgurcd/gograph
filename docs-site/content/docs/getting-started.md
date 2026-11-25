---
title: "Getting Started with gograph"
weight: 1
date: 2026-05-23
lastmod: 2026-11-25
description: "Install the gograph Go static analysis CLI, build an AST or type-checked call graph, and run blast-radius, test-selection, and architecture queries."
---

Install `gograph`, index a Go module, and run your first call-graph, test-selection, and architecture queries.

## Requirements

- **Go 1.27.0 or newer** on `PATH`
- A local Go module (`go.mod`) or Go workspace (`go.work`)

## 1. Install the CLI

Install with Homebrew or `go install`:

```bash
# Homebrew
brew install --cask ozgurcd/tap/gograph

# Or via Go toolchain
go install github.com/ozgurcd/gograph/cmd/gograph@latest
```

Verify the installed binary and check for `PATH` shadowing:

```bash
gograph version
gograph doctor --json
```

To build from source:

```bash
git clone https://github.com/ozgurcd/gograph
cd gograph
make build
sudo make install
```

> **MCPB clients:** Clients supporting MCP Bundles can alternatively discover `io.github.ozgurcd/gograph` in the [Official MCP Registry](/docs/mcp-registry/). That installs a client-managed stdio server bundle rather than placing `gograph` on `PATH`.

## 2. Build the Repository Graph

From the root of a Go repository, choose the indexing mode suited to the task:

| Command | Speed | Requires Compiling Code? | Best For |
|---|---|---|---|
| `gograph build .` | Fastest | No (tolerates broken/WIP code) | Rapid exploration, symbol search, package layout |
| `gograph build . --precise` | Moderate | Yes | Interface dispatch (CHA/SSA), refactors, blast radius |
| `gograph build . --precise --strict` | Moderate | Yes (exits non-zero on fallback) | CI jobs requiring guaranteed type-checked precision |

```bash
gograph build . --precise
```

### Build Options

- **Build tags:** Include tagged files and tests with `--tags=integration`. The selected tag context is fingerprinted into the index so later queries and MCP sessions remain aligned.
- **Exclude broken subtrees:** Omit unrelated non-compiling directories with `--exclude-dirs=legacy,examples/broken`.
- **Constrained CI runners:** Lower heap pressure without sacrificing precision using `--memory-mode=low --max-memory=1GiB`.
- **Offline execution:** `gograph` makes no network requests of its own, though `--precise` invokes the local `go` toolchain to resolve module metadata and load packages. Set `GOPROXY=off GOTOOLCHAIN=local` to keep toolchain execution offline.

Indexing writes `.gograph/graph.json` and nine Markdown reports under `.gograph/` and appends `.gograph/` to `.gitignore`. Each parsed file records a SHA-256 content digest so subsequent builds reparse only changed packages. For symlink confinement, publication locking, and source-policy markers, see the [Security & Source Confinement Model](https://github.com/ozgurcd/gograph/blob/main/docs/security-model.md).

## 3. Verify Index Health and Freshness

Confirm the graph built cleanly and achieved `precise` resolution:

```bash
gograph stats
```

Check whether local edits or Go build-context changes have invalidated the saved graph:

```bash
gograph stale
```

`gograph stale` exits `0` when current, `2` when a rebuild is needed, and `1` on an operational error. Under `set -e`, inspect the exit code explicitly rather than chaining `gograph stale || gograph build .`.

## 4. Run Structural Queries

Begin with repository-wide commands that require no symbol names:

```bash
gograph summary                # Codebase overview: hotspots, instability, complexity, orphans
gograph hotspot --top 10       # Highest fan-in functions
gograph coupling               # Package fan-in, fan-out, and instability metrics
gograph flow --no-tests        # Production source-to-sink security flow leads
```

Select a symbol from `summary` or `hotspot` and inspect its neighborhood:

```bash
# Source, callers, callees, and mapped tests in one response
gograph context YourSymbol

# Transitive callers and blast radius before editing
gograph impact YourSymbol
gograph callers YourSymbol --mermaid

# Pre-edit plan with risk signals (public API, SQL, routes, env vars)
gograph plan YourSymbol
```

When short symbol names collide across packages, qualify them with a file path: `internal/store/repo.go:Save` for functions or types, and `internal/store/repo.go:Repo.Save` for methods.

## 5. Select Tests and Audit Coverage Gaps

After editing, rebuild the index and run only the tests that reach your changes:

```bash
gograph build . --precise
gograph review --uncommitted
gograph tests --uncommitted --format go-test --max-depth 3
```

To inspect runtime coverage against uncommitted changes:

```bash
go test -coverprofile=cover.out ./...
gograph uncovered --uncommitted --coverprofile cover.out
```

## 6. Output Formats

Most query commands support mutually exclusive output modes:

- **Default text** — terminal-friendly rows (`[kind] Name — detail (file:line)`).
- **`--json`** — structured JSON envelope with `count`, `results`, pagination (`--limit`/`--cursor`), and `graph_state` freshness provenance.
- **`--files-only`** — deduplicated file paths for shell pipelines.
- **`--mermaid`** — fenced Mermaid flowcharts on `callers`, `callees`, `impact`, `endpoint`, `dependents`, `deps`, `path`, `coupling`, and `diagram`.

## Next Steps

- Enforce package boundaries and complexity ceilings in CI: **[CI Architecture Gates](/docs/ci-architecture-gates/)**
- Browse all 66+ commands and flags: **[Command Reference](/docs/command-reference/)**
- Connect Claude Code, Cursor, or Copilot over stdio MCP: **[MCP & AI Integration](/docs/agent-integration/)**
- Compare roles alongside `gopls` and `golangci-lint`: **[Tool Comparisons](/docs/comparisons/)**
