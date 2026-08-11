# Gograph + Claude Code Integration

[Claude Code](https://docs.anthropic.com/en/docs/agents-and-tools/claude-code) is Anthropic's official CLI-based agent. Its text tools are useful for literals, documentation, configuration, and non-Go files, but text matching alone does not resolve Go interface satisfaction or call relationships.

Adding `gograph` gives Claude Code a local AST-derived repository graph and
composed change-analysis workflows. The benefit depends on the repository and
task; treat results as static-analysis evidence rather than runtime proof.
Indexing reads Go source, recognized non-Go build inputs, and ordinary project
metadata such as `go.mod`, `go.sum`, `go.work`, `go.work.sum`,
`vendor/modules.txt`, `.gitignore`, Git state, persisted graph data, and explicitly selected gograph
configuration. It does not intentionally scan secret-bearing `.env`, key,
certificate, kubeconfig, tfstate, or credential files. Descendant links and
special files for recognized Go build inputs are excluded, linked
module/workspace files, sums, and `vendor/modules.txt` are rejected before
toolchain use, and applicable workspace members must stay beneath the workspace
directory with their directories and module metadata validated first. Precise
package loading and `doc` reject source-tree links `cmd/go` may inspect across
the selected root plus its effective module root, or the workspace root and
member trees, before toolchain use (`.git` and `.gograph` are excluded from that walk),
and graph-directed source reads are
confined to regular files beneath the analyzed repository. Persisted
`graph.json` is subject to the same descendant-link boundary, `.gograph` must
be a real directory for publication, and graphs with a missing or unsupported
source-policy marker require rebuilding. Use the current binary for untrusted
repositories; older binaries do not enforce this confinement.

## 1. How Gograph Complements Gopls

[`gopls`](https://go.dev/gopls/features/mcp) is the Go project's
compiler-backed language server and should remain the primary tool for live
workspace diagnostics, navigation, implementations, and refactoring. Its
experimental MCP server also exposes a subset of that functionality to coding
assistants. **`gograph` adds compact, repository-level agent workflows**:

- **Tool-Call Overhead**: `gograph context` bundles node metadata, callers, callees, tests, role, and requested source in one response instead of requiring several position-follow-up reads.
- **Repository-Level Signals**: gograph adds routes, SQL, env reads, coupling, reachability, risk, policy, and change-oriented composites that are not ordinary LSP queries.
- **Interface Navigation**: default mode uses structural method-set heuristics; precise mode adds package-qualified type information when packages compile.

Benchmark results depend on repository size, cache state, query, and follow-up behavior. The controlled suite in `benchmarks/` publishes manually reviewable ground truth, exact commands, and raw output; use `scripts/benchmark.go` to reproduce it or adapt the methodology to your own codebase.

## 2. Installation

Ensure `gograph` is built and accessible in your system `$PATH` so Claude Code can invoke it directly from the terminal.

```bash
# Using Homebrew
brew tap ozgurcd/tap
brew install gograph

# Or manually from source
cd /path/to/gograph
make build
# symlink bin/gograph to /usr/local/bin/gograph
```

MCPB-capable desktop clients can alternatively discover the local server as
`io.github.ozgurcd/gograph` in the official MCP Registry. The Registry is in
preview, and this path installs a platform-specific bundle rather than placing
the CLI on `PATH`; it does not replace Claude Code's marketplace plugin or its
per-project MCP registration. The bundle prompts for the Go project directory
and launches arguments equivalent to `gograph mcp <project-directory>`.
Releases cover macOS, Linux, and Windows on amd64 and arm64, but current
Registry metadata cannot select CPU architecture portably. Choose the matching
asset filename, or use Homebrew/`go install` plus manual registration. See
[Official MCP Registry and MCPB Distribution](mcp-registry.md).

The Registry bundle and installer-generated MCP registrations leave refresh
persistence off. A custom local registration can add `--persist-refresh` when
you explicitly want successful refreshes to overwrite `.gograph` artifacts.

## 3. Project Instructions Setup

Claude Code looks for a `CLAUDE.md` file in the root of your repository to understand project-specific rules and tool preferences. 

Add the following block to your repository's `CLAUDE.md`:

```markdown
## Repository Navigation (CRITICAL)
This project is indexed using `gograph`. **DO NOT use `grep` or `cat` for structural Go code analysis.**

1. Before answering architecture or repository questions, inspect the available `gograph_*` MCP tools for the current project and use the server registered for that repository.
2. The MCP server builds and refreshes an in-memory AST graph automatically. If MCP tools are unavailable, run `gograph build .` in the terminal, then use CLI commands such as `gograph implementers <InterfaceName>`.
3. For compilable, build-selected packages, `gograph build . --precise` adds type-checked CHA/SSA enrichment. CHA is conservative, and a failed retry retains an already-fresh successful precise artifact for the same sources instead of replacing it with a fallback.
4. To extract a function body or mock stub without reading the whole file, use the source tool.
5. Use targeted text search for string literals, ordinary non-sensitive configuration, generated or non-indexed files, and documentation. Do not inspect `.env`, key, certificate, kubeconfig, tfstate, or credential files unless the user explicitly places them in scope.
```

## 4. Example Workflows

Here is how Claude Code behaves before and after `gograph`:

### Scenario: Finding how an interface is implemented

**❌ Without Gograph (The `grep` loop)**
1. Claude: `grep -rn "AuthService" .`
2. Claude: *Receives declarations, comments, mocks, and unrelated same-name text.*
3. Claude opens likely source files and compares method sets manually.
4. Claude uses compiler or source evidence to verify the candidate implementation.

**✅ With Gograph (The structural loop)**
1. Claude: `gograph implementers "AuthService" --json`
2. Claude receives indexed implementation candidates and their file paths.
3. Claude: `gograph source "authServiceImpl" --json`
4. Claude inspects the matching declaration, then verifies ambiguous or fallback results with `gopls` or targeted source search.

### Scenario: Modifying a function safely

**✅ With Gograph (Blast Radius check)**
1. Claude: `gograph impact "ValidateToken"`
2. Claude: *Sees the transitive upstream callers, including candidate HTTP handlers affected by the signature change.*
3. Claude: `gograph source "ValidateToken"`
4. Claude: *Reads the function, plans the edit, and safely applies it.*
5. Claude: `gograph check --uncommitted`
6. Claude reviews static policy findings, then runs the repository's compiler, tests, and required checks for behavioral verification.

### Scenario: Reviewing untrusted-data paths

1. Claude runs `gograph flow --no-tests` or invokes MCP `gograph_flow` with `no_tests=true`.
2. Claude receives structured HTTP/JSON/environment source paths to SQL query text, process execution, filesystem, and outbound HTTP sinks.
3. Claude uses `gograph source <symbol>` to inspect each finding. Flow results are path-insensitive review leads with bounded call-site matching, not proof of exploitability.

## 5. MCP Integration

Registering `gograph` as a [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) server exposes its query, analysis, and workflow capabilities as tools such as `gograph_query` and `gograph_impact`. Host/build operations (`build`, `gate`, `snapshot`, plugin/hook installation, MCP startup, help, and version) intentionally remain CLI-only. Presentation is transport-specific: MCP uses typed parameters and content payloads instead of CLI output flags. For `callers`, `callees`, `impact`, `endpoint`, `dependents`, `deps`, `path`, and `coupling`, set `mermaid=true` to receive the same Markdown-fenced Mermaid diagram produced by CLI `--mermaid`.

### Claude Desktop Config + Shared Claude Rules/Hook

```bash
gograph add-claude-plugin
```

This single command performs **three installation steps**:

| Step | What is installed | Location |
|---|---|---|
| **MCP server** | Registers gograph in `claude_desktop_config.json` | macOS: `~/Library/Application Support/Claude/` |
| **CLAUDE.md rules** | Injects steering rules Claude reads at every session start | `~/.claude/CLAUDE.md` |
| **PreToolUse hook** | Smart hook that intercepts `grep`/`rg` on Go symbols in indexed repositories | `~/.claude/hooks/gograph-guard.sh` + `~/.claude/settings.json` |

The installer exits non-zero if any step fails. Restart Claude Desktop / Claude Code after successful installation. Claude Code MCP registration is still per project, as shown below.

#### What the PreToolUse hook does

The hook (`gograph hook-guard`) runs automatically before every `Bash` tool call Claude makes. When Claude tries to `grep` for a Go symbol, the hook:

1. Resolves parsed search targets against the tool call's `cwd` and checks for a real `.gograph` ancestor
2. Detects it is a Go symbol search (every non-exempt pattern branch is a valid identifier or an identifier-only alternation, with each branch matching `[A-Za-z_][a-zA-Z0-9_]{2,}`)
3. Blocks the call (exit code `2`) and outputs which `gograph` tool to use instead
4. Claude immediately retries using the correct `gograph_query` / `gograph_context` / etc. call

**The hook is smart — it only intercepts symbol searches.** These pass through unchanged:
- Searches whose effective targets have no `.gograph` ancestor, including non-Go repositories in multi-root workspaces
- Searches targeting only non-Go files (`*.yaml`, `*.md`, `*.sql`, `*.sh`)
- Comment/doc-only searches (TODO, FIXME, HACK, DEPRECATED)
- Searches targeting only `docs/`, `.github/`, `testdata/`, or `migrations/`
- Short patterns and patterns containing non-identifier regex/literal text
- Literal-pipe patterns in fixed-string mode and escaped pipes in `grep -E`/`rg`

Alternation is mode-aware: basic `grep` uses `\|`; `grep -E` and `rg` use bare
`|`. The hook understands direct-command quoting, pattern flags, glob/context
option values, and the first shell pipeline stage. Unsupported or dynamic shell
syntax is allowed because the hook is steering rather than a security boundary.
Explicit paths take precedence over the launch directory for index detection:
searches from an unindexed folder into an indexed sibling are steered, while
searches from an indexed folder into only unindexed targets are allowed. When
the hook payload omits `cwd`, the hook process working directory is used.

### Claude Code (CLI) — Per-Project Registration

Because Claude Code isolates tools per-project, you must explicitly add `gograph` to each repository:

```bash
claude mcp add gograph -- gograph mcp .
```

The registration above uses the safe default: refreshed graphs remain in
memory. To make successful refreshes available to later CLI/server processes,
register `gograph mcp . --persist-refresh` instead. This overwrites the latest
`.gograph/graph.json` and nine reports, does not update `.gitignore`, and is not
a per-branch cache. Reports are replaced first and `graph.json` is replaced
last as the publication marker. Same-directory replacement is atomic on
Unix-like systems but is not guaranteed atomic by Go on non-Unix platforms;
the whole ten-file bundle is not one atomic transaction, and `.artifacts.lock`
remains as separate operational state. Publication failures during tool-triggered refreshes are
returned to the MCP tool and retried on a later refresh-capable call; failure
to publish a required startup auto-build prevents the server from starting.

**How it works:**
- Claude Code records the MCP server entry in your home configuration (`~/.claude.json`) and scopes it to the current project directory.
- The `.` in `gograph mcp .` tells the server to index whatever specific folder Claude Code is currently operating in.
- **You must run this command once for each Go project repository** you wish to use it in. This prevents your agent from accidentally querying index databases from other projects!
