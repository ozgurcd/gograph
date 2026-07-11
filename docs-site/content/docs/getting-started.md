---
title: "Getting Started"
weight: 1
description: "Install gograph, build your first graph, and run your first queries."
---

## Prerequisites

- Go 1.26 or later
- A Go module repository (`go.mod` present)

## Install

**Homebrew (recommended)**
```bash
brew install ozgurcd/tap/gograph
gograph version
```

**Go install**
```bash
go install github.com/ozgurcd/gograph@latest
```

**From source**
```bash
git clone https://github.com/ozgurcd/gograph
cd gograph
make build        # produces bin/gograph
sudo make install # copies to /usr/local/bin
```

## Step 1 — Build the graph

Navigate to the root of any Go module and run:

```bash
gograph build .
```

This walks every `.go` file, extracts the AST, and writes:

- `.gograph/graph.json` — the machine-readable graph (all queries read from this)
- `.gograph/GRAPH_REPORT.md` — master index report
- `.gograph/graph-symbols.md` — all symbols
- `.gograph/graph-routes.md` — HTTP routes
- `.gograph/graph-sql.md` — SQL queries
- `.gograph/graph-errors.md` — error declarations
- `.gograph/graph-deps.md` — package dependencies
- `.gograph/graph-concurrency.md` — concurrency primitives
- `.gograph/graph-config.md` — environment reads
- `.gograph/graph-tests.md` — test edge mapping

`graph.json` also stores compact security-flow facts used by `gograph flow`; findings and sanitizer policy are evaluated when queried rather than written as a Markdown report.

`.gograph/` is automatically added to the Git repository root `.gitignore`.
Outside a Git worktree, `gograph` falls back to the build target `.gitignore`.
Files and directories ignored by Git are excluded consistently from builds,
freshness checks, and change detection. If no Go files are found, or every Go
file fails to parse, the build exits without replacing an existing graph.
Partial parse failures are retained in `graph.json` build metadata.

### Precise mode

```bash
gograph build . --precise
```

Attempts Go type loading plus CHA/SSA enrichment on top of the AST pass. Compilable packages are required for precise data; if enrichment fails, gograph warns and retains the AST graph. Use before major refactors or blast-radius analysis.

**When to use which:**

| Mode | Speed | Requires compilable? | Interface dispatch |
|---|---|---|---|
| `build .` | Fast | No | Heuristic (duck-typing) |
| `build . --precise` | Slower | Yes | Type-checked CHA |

## Step 2 — Check the index

```bash
gograph stats
```

Prints schema version, build timestamp, and counts:

```
schema_version: 2
generated_at:   2026-05-22T18:00:00Z
packages:       24
files:          187
symbols:        1843
calls:          6201
imports:        412
routes:         38
sqls:           29
env_reads:      14
test_edges:     522
build_status:   complete
parsed_files:   187/187
parse_failures: 0
```

```bash
gograph stale
```

Lists indexed source files that are newer than `graph.json`. It uses the same ignore policy as `build`, and all filesystem-backed commands resolve the repository root recorded in the graph, so it works identically from subdirectories. Rebuild if any files are listed.

## Step 3 — First queries

```bash
# Find a symbol
gograph query ValidateToken

# Read its source
gograph source ValidateToken

# Who calls it?
gograph callers ValidateToken

# What does it call?
gograph callees ValidateToken

# Full blast radius
gograph impact ValidateToken

# Before editing it
gograph plan ValidateToken

# Review potential production source-to-sink security paths
gograph flow --no-tests
```

### Example: Reading Symbol Source Code

Running `gograph source` directly against the codebase returns only the exact AST-extracted code block of the target symbol:

```text
$ gograph source normalizeSymbolName

// github.com/ozgurcd/gograph/internal/search::normalizeSymbolName (internal/search/advanced.go:12-21)
func normalizeSymbolName(name string) string {
	name = strings.TrimPrefix(name, "(")
	if idx := strings.Index(name, ")."); idx >= 0 {
		name = name[idx+2:]
	}
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		name = name[idx+1:]
	}
	return strings.ToLower(name)
}
```



## Output formats

Result-list queries support text, JSON, and files-only output:

```bash
gograph callers Foo              # text: [kind] Name — detail  (file:line)
gograph callers Foo --json       # {"schema_version":"1","command":"callers","status":"ok","query":"Foo","count":N,"results":[...]}
gograph callers Foo --files-only # flat list of unique file paths
```

Composed-analysis commands also support JSON where documented. Operational commands (`build`, `wiki`, `gate`, `snapshot`, sessions, installation, help, and version) remain text. Use `--files-only` only for result-list queries.

## Rebuilding

CLI analysis does **not** auto-update. Rebuild whenever source files change:

```bash
gograph build .           # fast rebuild, tolerates broken code
gograph build . --precise # type-checked rebuild (before big refactors)
```

You can check whether a rebuild is needed:

```bash
gograph stale
```

The MCP server checks source freshness per analysis call and rebuilds its in-memory AST graph only after edits. MCP `stale`, default `changes`, and `stats` still inspect the persisted graph. A precise graph is preserved until source changes.
