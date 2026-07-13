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
Replacing broad text searches with AST-derived, symbol-focused responses reduces irrelevant context and tool round trips. Results remain static-analysis evidence rather than proof: dynamic dispatch, grouped routes, test attribution, and error flow have documented limits.

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
gograph mcp [path]
```
If `.gograph/graph.json` does not exist, startup creates an in-memory AST graph; it does not publish CLI build artifacts. Source-analysis tools check source freshness and newer persisted artifacts per call, then rebuild after edits in the current requested mode. Persisted-index tools use the disk snapshot; precise and precise-fallback sessions re-run CHA/SSA, and a failed precise refresh is returned visibly.

The server exposes 65 endpoints: 61 CLI-equivalent query, analysis, and workflow tools plus four session lifecycle tools. `gograph_flow` matches the CLI `flow` filters (`term`, `source`, `sink`, `config`, and `no_tests`) and returns structured source, sink, severity, confidence, and path data.

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
   - **Command**: `gograph mcp` *(if not globally in PATH, specify the absolute path: `/opt/homebrew/bin/gograph mcp`)*
5. Click **Save**. 

Cursor will automatically start the background stdio session, parse the JSON schemas, and register every `gograph` capability as a native agent tool in Composer and Chat!

### 🌊 Windsurf MCP Configuration

To configure gograph inside the **Windsurf IDE**:

Add the following block to your global or workspace-local `mcp_config.json` file (typically located under `~/.codeium/windsurf/mcp_config.json`):

```json
{
  "mcpServers": {
    "gograph": {
      "command": "gograph",
      "args": ["mcp"],
      "env": {}
    }
  }
}
```

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

The hook is a steering aid, not a security boundary: it targets likely Go-identifier searches and intentionally allows comment, documentation, and non-Go searches.

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
