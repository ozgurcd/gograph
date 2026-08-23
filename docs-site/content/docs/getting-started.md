---
title: "Getting Started"
weight: 1
description: "Install gograph, build your first graph, and run your first queries."
---

## Prerequisites

- Go 1.27.0 or later
- A Go module repository (`go.mod` present)

## Install

**Homebrew (recommended)**
```bash
brew install --cask ozgurcd/tap/gograph
gograph version
```

**Go install**
```bash
go install github.com/ozgurcd/gograph/cmd/gograph@latest
gograph version
```

**Official MCP Registry / MCPB (preview)**

MCPB-capable clients can discover `io.github.ozgurcd/gograph` in the official
Registry. This installs a self-contained local MCP server bundle rather than a
CLI on `PATH`. Select the bundle matching macOS, Linux, or Windows and the
host's amd64/arm64 architecture, then choose the root directory of the Go
project to analyze. Current Registry metadata cannot select CPU architecture
portably, so verify the asset filename instead of assuming automatic client
selection. See [Official MCP Registry](/docs/mcp-registry/) for details.

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

This walks the `.go` files selected by the documented scanner/build-context
policy, extracts their ASTs, and writes:

- `.gograph/graph.json` — the machine-readable graph used by graph-backed CLI queries
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

Graph/report publishers coordinate through `.gograph/.artifacts.lock`. They
stage the graph and all nine reports, rename reports first, and rename
`graph.json` last as the publication commit marker. Publication refuses a
linked or non-directory `.gograph`, and readers require a regular
repository-confined `graph.json`. Same-directory replacement
is atomic on Unix-like systems but is not guaranteed atomic by Go on non-Unix
platforms. The complete ten-file bundle is not a single transaction; a crash
can leave newer reports beside the previous `graph.json`. The lock file remains
as separate operational state. Consumers should treat `graph.json` as the
authoritative marker.

`.gograph/` is automatically added to the Git repository root `.gitignore`.
Outside a Git worktree, `gograph` falls back to the build target `.gitignore`.
The update accepts only an absent or regular `.gitignore`; it refuses a link or
special file without modifying its target.
Files and directories ignored by Git are excluded consistently from builds,
freshness checks, and change detection. If no Go files are found, or every Go
file fails to parse, the build exits without replacing an existing graph.
Partial status retains parse failures and selection/security warnings in
`graph.json` build metadata. Each successfully parsed source stores a SHA-256
digest. Subsequent builds reparse changed packages and reuse parser records for
unchanged packages; precise builds still recompute repository-wide CHA/SSA.
Descendant symlinks and special files for
extensions recognized by `go/build` are reported and excluded;
linked/non-regular `go.mod`, `go.sum`, `go.work`, `go.work.sum`, and
`vendor/modules.txt` metadata is rejected before toolchain use. Applicable
`go.work use` members must remain beneath the workspace directory; their
directories, `go.mod`, and optional `go.sum` are validated before `cmd/go`. An explicitly
symlinked repository root remains supported. AST and
query-time source reads are confined to regular files beneath that root.

Graphs with a missing or unsupported repository source-policy marker are not
trusted. The current CLI and MCP startup reject those artifacts and rebuild (or
ask you to run `gograph build`) rather than trusting source-derived or
unknown-policy data already stored in them. Older binaries do not enforce this
boundary and should not be used for untrusted repositories.

### Precise mode

```bash
gograph build . --precise
```

Attempts Go type loading plus CHA/SSA enrichment on top of the AST pass. Before its first `cmd/go` invocation, normal indexing validates Go tool metadata (`go.mod`, `go.sum`, `go.work`, `go.work.sum`, and `vendor/modules.txt`) and confines applicable workspace members to their workspace directory. The scanner then excludes linked or special recognized Go build inputs before build selection and AST reads. Before repository package type loading, the stronger preflight rejects source-tree links `cmd/go` may inspect across the selected root plus its effective module root, or the workspace root and member trees; `.git` and `.gograph` are excluded from that walk. Unsafe repository input makes enrichment fail closed while the safe AST graph remains available as `precise_fallback`. Compilable, build-selected production packages are required for precise data; if enrichment fails or omits an indexed non-test source file, gograph warns and retains the AST graph. Successful, fallback, and AST-only status is persisted as `precise`, `precise_fallback`, or `ast`, except that a failed retry keeps an existing fresh successful precise artifact covering the same sources. A separate non-fatal typed pass resolves direct and conservative interface test-call targets. Broken test packages yield `typed_partial` test attribution without downgrading successful production precision. A precise interface invocation retains every valid named in-repository CHA target, so `callers Repository.Delete` can resolve direct, embedded-interface, and promoted concrete methods without dropping alternative implementations. Promoted wrappers forward through traversal-only synthetic edges that do not appear as source call sites. CHA can still over-approximate runtime targets, while reflection, plugins, `unsafe`, test-only packages, unnamed concrete types, and module-external implementations remain incomplete. Go dependency/toolchain resolution follows the user's environment and remains open-world. Use before major refactors or blast-radius analysis.

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
schema_version : 2
generated_at   : 2026-05-22 18:00:00 UTC
precision      : precise
test_resolution: typed_complete
packages       : 24
files          : 187
symbols        : 1843
calls          : 6201
imports        : 412
routes         : 38
sqls           : 29
env_reads      : 14
test_edges     : 522
flow_functions : 311
build_status   : complete
parsed_files   : 187/187
reused_files   : 0
rebuilt_pkgs   : 42
parse_failures : 0
```

```bash
gograph stale
```

Compares the current selected-file inventory, effective Go build context, and SHA-256 source-content digests with `graph.json`; modification times are diagnostic only. It uses the same build-constraint and ignore policy as `build`; `stale` and graph-backed query commands use the trusted directory from which `graph.json` was loaded, not its serialized `root` metadata, so they work identically from subdirectories. Exit `0` means current, `2` means stale, and `1` means an operational or JSON serialization error; text and `--json` modes use the same contract. Normally rebuild for status `2`. A status-`1` message that explicitly reports a missing or unsupported source-policy marker is a one-time rebuild migration; resolve other status-`1` errors instead of hiding them with an automatic rebuild.

## Step 3 — Run repository-wide queries

```bash
# No project-specific symbol names are required for these commands
gograph summary
gograph hotspot --top 5
gograph flow --no-tests
```

Choose a real function or method reported by `summary`, `hotspot`, or
`gograph complexity`, then replace `YourSymbol` below with that name:

```bash
# Read its source and surrounding graph context
gograph source YourSymbol
gograph context YourSymbol

# Who calls it?
gograph callers YourSymbol

# What does it call?
gograph callees YourSymbol

# Full blast radius
gograph impact YourSymbol

# Before editing it
gograph plan YourSymbol
```

### Example: Reading Symbol Source Code

The following is captured, repository-specific output from running
`gograph source normalizeSymbolName` in the gograph repository. Your project
will have different symbol names and locations:

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

Many result-list queries support text, JSON, and files-only output:

```bash
gograph callers Foo              # text: [kind] Name — detail  (file:line)
gograph callers Foo --json       # {"schema_version":"1","command":"callers","status":"ok","query":"Foo","count":N,"results":[...]}
gograph callers Foo --files-only # flat list of unique file paths
```

Successful JSON envelopes always include `count`; empty collection results use
`[]`. Operational failures use `status: "error"` and exit 1. Policy findings
can make `check --json` exit 1 while still returning its structured report.

`--files-only` is implemented by `query`, `focus`, `node`, `public`, `fields`,
`embeds`, `imports`, `callers`, `callees`, `impact`, `implementers`, `envs`,
`interfaces`, `concurrency`, `tests`, `routes`, `sql`, `errors`, `flow`,
`orphans`, `mutate`, `constructors`, `literals`, `usages`, `returnusage`,
`schema`, `globals`, `mocks`, `fixtures`, `boundaries`, `httpcalls`, and
`dependents`. Empty files-only results write zero lines.

Composed-analysis commands also support JSON where documented. Operational
commands (`build`, `wiki`, `gate`, `snapshot`, installation, help, and version)
remain text. Session create/end/cleanup are text; `session audit` additionally
supports raw JSON.

`--mermaid` is a global CLI output flag for `callers`, `callees`, `impact`,
`endpoint`, `dependents`, `deps`, `path`, and `coupling`. Bare
`gograph --mermaid` is shorthand for the package architecture `diagram`.
Request only one of `--json`, `--files-only`, or `--mermaid`; unsupported or
conflicting output flags fail instead of being silently ignored.

`-i <message>` / `--intention <message>` is also global and may appear before
or after the command. When a CLI audit session is active, it is mandatory for
analytical commands and is stored with command telemetry; operational and
index-status commands do not require it.

## Rebuilding

Graph-backed CLI analysis does **not** auto-update. Rebuild whenever source
files change:

```bash
gograph build .           # fast rebuild, tolerates broken code
gograph build . --precise # type-checked rebuild (before big refactors)
```

You can check whether a rebuild is needed:

```bash
gograph stale
```

The MCP server checks source-content digests, the selected inventory/build fingerprint, and newer persisted graphs per analysis call. After an edit it reparses changed packages while reusing unchanged package AST records. A precise session still re-runs repository-wide CHA/SSA rather than silently becoming AST-only; if that refresh cannot complete precisely, the analysis call returns an error. MCP `stale`, default `changes`, and `stats` inspect a trusted persisted graph when present, or the startup in-memory fallback when the artifact is missing, unreadable, unsafe, or uses an unsupported source policy.

MCP refreshes are in-memory by default. Start with
`gograph mcp [path] --persist-refresh` to publish successful refreshes to the
latest `.gograph/graph.json` and nine reports. Publication uses the same
`.artifacts.lock` and graph-last commit marker described above; it is not a
bundle-atomic transaction, and the lock file remains as separate operational
state. The option does not update `.gitignore` and is not a branch cache.
Publication failure is returned as a tool error and retried
later without rebuilding the fresh graph; failure to publish a required
startup auto-build prevents the server from starting. A successful publication
advances the persisted baseline used by default `changes`.

For visual output through MCP, set `mermaid: true` on `gograph_callers`,
`gograph_callees`, `gograph_impact`, `gograph_endpoint`,
`gograph_dependents`, `gograph_deps`, `gograph_path`, or `gograph_coupling`.
The selected tool returns Mermaid flowchart text instead of its normal response.
