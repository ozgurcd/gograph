---
title: "AI Agent Integration"
weight: 3
description: "Integrate gograph's local structural graph with Claude Code, Cursor, Copilot, Antigravity, OpenCode, and other MCP clients."
---

gograph is an **agent-oriented static-analysis tool**. Humans can use the CLI,
while MCP clients such as Claude Code, Cursor, Copilot, Antigravity, and
OpenCode can request the same structured repository evidence. Results follow
the documented AST, precise-analysis, and heuristic limits; they are not
runtime proof.

---

## Text search, gopls, and gograph have different jobs

`rg`, `grep`, and `find` are appropriate for literal text, documentation,
configuration, generated content, and non-Go files. Text matching alone cannot
resolve Go interface satisfaction or distinguish a call expression from a
comment or string.

[`gopls`](https://go.dev/gopls/features/mcp) provides live compiler-backed
workspace diagnostics, navigation, references, implementations, refactoring,
and experimental MCP support. gograph complements it with persisted snapshots,
repository-level graph analyses, composed change workflows, and policy gates.

Choose the tool that matches the question:

1. **Text and file search**: use `rg`, `grep`, and `find` for literal or
   non-structural questions.
2. **Live Go semantics**: use `gopls` for compiler-backed editor and workspace
   operations.
3. **Persisted repository analysis**: use gograph for impact, reachability,
   routes, SQL, environment reads, security-flow candidates, and architecture
   policies.

---

## ⚡ Comparative Analysis: Unix vs. gograph

### Practical Impact
Replacing broad text searches with AST-derived, symbol-focused responses reduces irrelevant context and tool round trips. Results remain static-analysis evidence rather than proof: dynamic dispatch, dynamically computed route prefixes, test attribution, and error flow have documented limits. Constant nested Gin/Echo/Fiber groups and Chi Route closures are composed into their final indexed paths.

---

| Objective | Text-search route (`grep`, `find`) | The `gograph` route | Typical output shape | Analysis caveat |
|---|---|---|---|---|
| **Find callers of a method** | `grep -rn "Update" .` <br>*(Scans mocks, comments, other types)* | `gograph callers UserStore.Update` or `gograph callers Repository.Update` <br>*(AST-derived; interface-qualified queries use precise CHA targets)* | Broad source context vs. compact caller rows | Default AST evidence; conservative multi-target CHA when precise |
| **Find interface implementers** | Multi-step searches of method receivers and method sets | `gograph implementers Connection` | Many file reads vs. concrete type rows | Heuristic AST mode; package-qualified precise mode when available |
| **Trace wrapped errors** | String searches inside formatting blocks | `gograph errorflow "invalid token"` | Broad scans vs. structured candidate paths | Navigation heuristic, not SSA data-flow proof |
| **Review untrusted-data paths** | Repeated searches for request reads and sensitive APIs | `gograph flow --no-tests` | One structured source-to-sink report | Interprocedural heuristic with explicit confidence, not exploitability proof |

---


### Concrete Workflow Example: Modifying a Struct Field

Suppose an agent needs to add a required field to a struct named `Config`. 

* **The text-search route**:
  1. Agent runs `grep -rn "type Config struct" .` to locate the type definition.
  2. Agent tries to locate every composite literal declaration to see where it is initialized: `grep -rn "Config{" .`.
  3. Because `Config` is a common word, it matches test utilities, local files, config parser variables, and external package docs.
  4. The agent inspects the matching files to determine which results are Go
     composite literals for the intended type.

* **The gograph Way (Structural Call)**:
  1. Agent runs a single tool call:
     ```bash
     gograph literals Config
     ```
  2. gograph queries the AST graph and returns file/line rows for matching composite literals (`Config{...}`).
  3. This is one focused syntax-based query. Generated or dynamically
     constructed values remain outside its scope.

---

## 🔬 Empirical Case Study: gograph Codebase Benchmark

The following is a historical point-in-time benchmark from an earlier gograph revision (16 packages, 70 Go files, 518 AST symbols, and 5,443 call edges). Current repository counts differ.

Here are the concrete, measured results:

### Case 1: Broad Symbol Queries
An agent needs to locate symbols matching the word `"Symbol"`.
* **The Unix Way (`grep -rn "Symbol" .`)**: Returns **842 matching lines** from comments, markdown guides, local variables, and unrelated documentation blocks, completely flooding the context window.
* **The `gograph` Way (`gograph query Symbol`)**: Returned **83 structured results** in that snapshot, about **90% fewer rows** than the text search. The two commands return different kinds of evidence, so this is a noise comparison rather than a correctness benchmark.

### Case 2: Tracking Callers of a Helper Function
An agent needs to track callers of the function `loadGraph`.
* **The Unix Way (`grep -rn "loadGraph" .`)**: Returns **158 matching lines** across comments, markdown docs, function declarations, and call expressions.
* **The `gograph` Way (`gograph callers loadGraph`)**: Returned **56 AST-derived call-site rows** in that snapshot.

### Case 3: Viewing Function Source Code
An agent needs to read the definition of `normalizeSymbolName`.
* **The Unix Way**: Agent greps for the declaration and then must call `view_file` to read the entire `internal/search/advanced.go` file (or guess line ranges).
* **The `gograph` Way (`gograph source normalizeSymbolName`)**: Returned the 12-line function block in that snapshot.

---




## Model Context Protocol (MCP): Open & Client-Agnostic

gograph implements a stdio [Model Context Protocol](https://modelcontextprotocol.io)
server. MCP clients that support local stdio servers can register the `gograph`
binary and its tool schemas.

Client configuration and support vary; use the integration instructions for
your host rather than assuming identical behavior across every MCP client.

### Starting the Server (Standard I/O)

To start the MCP JSON-RPC server over standard I/O:
```bash
gograph mcp [path] [--persist-refresh] [--tags=integration[,tag...]] [--memory-mode=low] [--max-memory=1GiB]
```
By default, if `.gograph/graph.json` is missing, unreadable, unsafe, or has a
missing/unsupported source-policy marker, startup creates an in-memory AST
graph without publishing CLI build artifacts. A loaded graph's serialized root
is ignored in favor of the selected project. Source-analysis tools compare
source-content digests and the build/module fingerprint, check newer trusted
persisted artifacts per call, then reparse changed packages after edits.
`gograph_stale`, default
`gograph_changes`, and `gograph_stats` use a trusted persisted graph when one
exists and otherwise inspect the startup in-memory fallback. Precise and
precise-fallback sessions still re-run repository-wide CHA/SSA, and a failed precise refresh is
returned visibly. A failed precise publication retry cannot replace an
existing fresh successful precise artifact covering the same selected sources.
`--tags` selects the same validated, fingerprinted Go build context as CLI
`build`; an explicit value replaces `GOFLAGS -tags`, and startup plus every
later MCP refresh retains it. `gograph_capabilities` reports requested and
effective tags under `analysis_build_context`.

On constrained hosts, low-memory mode applies aggressive GC and phase
reclamation to startup analysis and every later refresh without changing graph
precision. `--max-memory` is a soft Go runtime memory target, not a hard RSS cap;
`gograph_capabilities` reports both requested and effective byte targets.

Refresh publication is opt-in. `--persist-refresh` writes or overwrites
`.gograph/graph.json` and the nine reports after a successful refresh, without
changing `.gitignore`. It keeps only the latest state and is not a branch
cache. A failed initial auto-build publication prevents startup. A later
tool-triggered failure is returned as a tool error and retried using the
already-fresh in-memory graph. Graph/report publishers wait up to 30 seconds on
`.gograph/.artifacts.lock`, stage all ten artifacts, rename reports first, and
rename `graph.json` last as the commit marker. Publication requires a real
`.gograph` directory and a regular-or-absent lock entry; persisted graph reads
require a regular repository-confined file. Same-directory replacement is
atomic on Unix-like systems but is not guaranteed atomic by Go on non-Unix
platforms, and the bundle is not one atomic transaction; a crash can leave
reports ahead of the previous graph marker. The lock file remains as separate
operational state. Because default `gograph_changes` compares
with persisted `graph.json`, successful publication advances that comparison
baseline. Fixed Registry/plugin configurations omit the flag. When persistence
is enabled, refresh-capable MCP tools advertise non-read-only, destructive
annotations because a stale refresh may replace these generated artifacts.
In the default mode, read-only annotations describe the analysis contract. If
an audit session is active, every non-session MCP call also appends local
observational command/status telemetry; tool arguments and query results are
not logged. MCP tools do not expose or enforce the CLI's `--intention` field,
so MCP audit records have an empty intention.

Eight graph-oriented MCP tools match the CLI's Mermaid surface. Set
`mermaid: true` on `gograph_callers`, `gograph_callees`, `gograph_impact`,
`gograph_endpoint`, `gograph_dependents`, `gograph_deps`, `gograph_path`, or
`gograph_coupling`; a successful match returns Mermaid flowchart text instead
of the tool's normal response. `gograph_diagram` always returns Mermaid
architecture text.

The server exposes 67 endpoints: 63 tools corresponding to CLI query,
analysis, and workflow commands plus four session lifecycle tools. Tool
arguments and transport-level status presentation can differ from CLI flags
and process exit codes. `gograph_flow` matches the CLI `flow` filters (`term`,
`source`, `sink`, `config`, and `no_tests`) and returns structured source,
sink, severity, confidence, and path data.
`gograph_coverage` matches CLI `coverage` for reverse transitive test
attribution, and `gograph_identity` matches CLI `identity` for durable symbol
references. Both accept an optional exact `package` disambiguator for an
in-package/external-test ID collision. `gograph_tests` preserves direct results
by default; `transitive=true` maps to CLI `tests --transitive` and returns the
same `gograph.tests.v1` exact/possible paths. `gograph_untested.exclude[]` is the
typed equivalent of repeatable CLI `--exclude` globs; its rows include full
stable IDs, corresponding to CLI `untested --wide` presentation.

The normal mapping is CLI `<command>` to project-MCP
`gograph_<command>`; aliases, boundary creation, and session actions are listed
in the [complete CLI/MCP transport matrix](/docs/command-reference/#cli--mcp-transport-matrix).
A separate server started with `gograph workspace mcp` exposes
`gograph_workspace_status`, `gograph_workspace_query`,
`gograph_workspace_path`, and `gograph_workspace_impact`, backed by the same
native operations as their CLI counterparts. The matrix also identifies every
process-, host-, CI-, and artifact-lifecycle command that remains CLI-only.
At session start, run CLI `gograph doctor --json` before relying on the MCP
connection; it detects an older PATH-resolved or shadowed installation without
executing alternate binaries.

### Official Registry / MCPB installation

Clients that support MCP Bundles can discover
`io.github.ozgurcd/gograph` in the official MCP Registry. The Registry is in
preview. Installation prompts for the Go project directory and launches the
bundled executable with `mcp` and that directory as separate arguments.

Registry/MCPB distribution is distinct from Homebrew, `go install`, and the
Claude Code marketplace plugin. Six bundles cover macOS, Linux, and Windows on
amd64 and arm64. Because the current Registry package format has no portable
CPU selector, users must choose the matching asset filename when the client
offers multiple packages. See [Official MCP Registry](/docs/mcp-registry/) for
the target matrix, local-data behavior, and client fallback instructions.

---

## 🛠️ Client Integration Examples

Since the MCP server communicates over standard stdio streams, it plugs seamlessly into any compliant client environment. Here are configurations for several common developer setups:


### 🧭 Cursor MCP Configuration

To add gograph as an MCP server in **Cursor**:

1. Open Cursor **Settings** (`Cmd + ,` or `Ctrl + ,`).
2. Navigate to **Features** -> **MCP**.
3. Click **+ Add New MCP Server**.
4. Configure the fields:
   - **Name**: `gograph`
   - **Type**: `command`
   - **Command**: `gograph mcp /absolute/path/to/go-project` *(if not globally in PATH, also use the absolute binary path)*
5. Click **Save**. 

Use an explicit project path for global client configuration; bare `gograph
mcp` analyzes the process working directory selected by the client. Add
`--persist-refresh` only if Cursor should publish refreshed artifacts; the
default command keeps source refreshes in memory.

Cursor will automatically start the background stdio session, parse the JSON schemas, and register every `gograph` capability as a native agent tool in Composer and Chat!

### 🌊 Windsurf MCP Configuration

To configure gograph inside the **Windsurf IDE**:

Add the following block to your global or workspace-local `mcp_config.json` file (typically located under `~/.codeium/windsurf/mcp_config.json`):

```json
{
  "mcpServers": {
    "gograph": {
      "command": "gograph",
      "args": ["mcp", "/absolute/path/to/go-project"],
      "env": {}
    }
  }
}
```

Append `"--persist-refresh"` to `args` only when this server should publish
refreshed artifacts; leave it out for the default in-memory behavior.

Save the file. Windsurf will instantly hot-reload the configuration and expose all `gograph` analytical features to the AI critic and coder loop!

---


### 🤖 Claude Code Integration

gograph includes native automation to integrate directly with Claude Code.

### Automatic Plugin Installation

Run the following command inside your Go repository:
```bash
gograph add-claude-plugin
```

This single command performs three critical setup steps:

1. **Claude Desktop Configuration**: Registers `gograph mcp <absolute-project-path>` in Claude Desktop's config.
2. **Shared Steering**: Adds rules to `~/.claude/CLAUDE.md`.
3. **Claude Code Hook**: Installs `~/.claude/hooks/gograph-guard.sh` and registers it in `~/.claude/settings.json`.

Claude Code MCP registration is separate; run the `claude mcp add gograph -- gograph mcp .` command printed by the installer. The installer exits non-zero if any of its three file/configuration steps fail.

---

## The Hook Guard: Steering structural Go searches

AI agents often execute `grep` or `rg` for both literal searches and Go symbol
questions. The former is appropriate; the latter mixes declarations, comments,
strings, mocks, and unrelated same-name symbols.

gograph solves this structurally with the **Hook Guard**:

```
[Agent wants to run grep]
           │
           ▼
   ~/.claude/hooks/gograph-guard.sh
           │
           ▼
   gograph hook-guard  ◄── Intercepts symbol query
           │
 ┌─────────┴─────────┐
 │                   │
 ▼ (Generic grep)    ▼ (Gograph equivalent)
[BLOCKED]           [ALLOWED]
"Use gograph instead"
```

If the agent tries to run `grep -rn "MyStruct" .`, the hook guard blocks the command with exit code `2` and returns a helpful message:
> *Blocked: This looks like a Go symbol search. Start with `gograph query
> MyStruct` or `gograph callers MyStruct`. If precision fell back or a known
> call is missing, verify with `gopls` or targeted source/text search.*

The hook is a steering aid, not a security boundary: it targets likely Go-identifier searches and intentionally allows comment, documentation, and non-Go searches. It also allows searches whose effective targets have no real `.gograph` ancestor, so unindexed folders in multi-root workspaces are unaffected.

Identifier-only alternations are also steered: basic `grep` recognizes `\|`,
while `grep -E` and `rg` recognize bare `|`. Literal-pipe patterns in
fixed-string mode, escaped pipes in `grep -E`/`rg`, mixed regex/literal patterns,
and unsupported dynamic shell syntax remain allowed. Quotes, pattern flags,
glob/context option values, and the first shell pipeline stage are parsed before
classification. Mixed targets are blocked when any selected path can contain Go
source and belongs to an indexed repository; comment markers do not hide
another symbol branch.

Index detection uses `cwd` from the hook payload and the parser's effective
search paths. Relative targets are resolved against `cwd`; an omitted `cwd`
falls back to the hook process working directory. A search from an unindexed
folder into an indexed sibling is still steered, while a search from an indexed
folder into only unindexed targets is allowed.

---

## Workspace Steering Rules

When `add-claude-plugin` runs, it adds these explicit directives to `CLAUDE.md` to keep the model on track:

```markdown
# Go Codebase Navigation Steering

- Use native gograph MCP tools first for supported structural Go queries.
- Use text search for literals, comments, generated/non-indexed files, and non-Go content.
- If precision is `ast`/`precise_fallback`, results are ambiguous, or a known call is missing, verify with `gopls` or targeted source/text search and disclose the fallback.
- Run `gograph_plan` before editing a symbol to inspect indexed downstream risks.
- For security reviews, use `gograph_flow` before broad source searches; inspect every reported path because it is static, path-insensitive evidence rather than proof.
- After editing, run CLI `gograph build . --precise`, `gograph_review` with `uncommitted=true`, and the repository's required tests and checks.
```
