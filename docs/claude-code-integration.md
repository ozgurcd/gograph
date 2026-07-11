# Gograph + Claude Code Integration

[Claude Code](https://docs.anthropic.com/en/docs/agents-and-tools/claude-code) is Anthropic's official CLI-based agent. By default, Claude Code uses basic text tools (`grep`, `ls`, `cat`) to explore repositories. In large Go codebases, this often leads to:
1. **Context Window Exhaustion:** Reading full files (e.g., `cat handler.go`) consumes tens of thousands of tokens.
2. **Hallucinations:** `grep` fails to resolve interfaces, duck-typing, or call chains reliably.

By providing Claude Code with `gograph`, you give it a **semantic, AST-aware understanding** of your Go repository, drastically reducing token usage and improving coding accuracy.

## 1. Why Gograph instead of Gopls (LSP)?

While some agents use `gopls` directly via MCP, **`gograph` is designed for compact, composed agent workflows**:

- **Tool-Call Overhead**: `gograph context` bundles node metadata, callers, callees, tests, role, and requested source in one response instead of requiring several position-follow-up reads.
- **Repository-Level Signals**: gograph adds routes, SQL, env reads, coupling, reachability, risk, policy, and change-oriented composites that are not ordinary LSP queries.
- **Interface Navigation**: default mode uses structural method-set heuristics; precise mode adds package-qualified type information when packages compile.

Benchmark results depend on repository size, cache state, query, and follow-up behavior. Use `scripts/benchmark.go` to measure the workflow on your own codebase.

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

## 2. Project Instructions Setup

Claude Code looks for a `CLAUDE.md` file in the root of your repository to understand project-specific rules and tool preferences. 

Add the following block to your repository's `CLAUDE.md`:

```markdown
## Repository Navigation (CRITICAL)
This project is indexed using `gograph`. **DO NOT use `grep` or `cat` for structural Go code analysis.**

1. Before answering architecture or repository questions, inspect the available `gograph_*` MCP tools for the current project and use them. Each project ships its own gograph MCP server; pick the matching one.
2. If MCP tools are not available, run `gograph build .` in the terminal to ensure the index is fresh, then use the CLI commands (e.g., `gograph implementers <InterfaceName>`).
3. If the codebase is in a compilable state, building with `gograph build . --precise` enables strict type-checked interface analysis and highly precise call edges.
4. To extract a function body or mock stub without reading the whole file, use the source tool.
5. Use `grep` ONLY for string literals, configuration files (.env), or markdown documentation.
```

## 3. Example Workflows

Here is how Claude Code behaves before and after `gograph`:

### Scenario: Finding how an interface is implemented

**❌ Without Gograph (The `grep` loop)**
1. Claude: `grep -rn "AuthService" .`
2. Claude: *Gets 400 lines of noise from tests, mocks, and dependency injection.*
3. Claude: `cat internal/auth/service.go` *(burns 5,000 tokens reading the file)*
4. Claude: *Guesses which struct actually implements it.*

**✅ With Gograph (The Precision loop)**
1. Claude: `gograph implementers "AuthService" --json`
2. Claude: *Instantly receives the exact struct name `authServiceImpl` and file path.*
3. Claude: `gograph source "authServiceImpl" --json`
4. Claude: *Extracts exactly the 20 lines of the struct definition and nothing else. Total cost: ~100 tokens.*

### Scenario: Modifying a function safely

**✅ With Gograph (Blast Radius check)**
1. Claude: `gograph impact "ValidateToken"`
2. Claude: *Sees the transitive upstream callers, including candidate HTTP handlers affected by the signature change.*
3. Claude: `gograph source "ValidateToken"`
4. Claude: *Reads the function, plans the edit, and safely applies it.*
5. Claude: `gograph check --uncommitted`
6. Claude: *Verifies that the changes didn't break architectural boundaries, test requirements, or introduce too much complexity.*

## 4. MCP Integration (Native Plugins)

Instead of passing CLI instructions via `CLAUDE.md`, you can give Claude native superpowers by installing `gograph` as a [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) plugin. This exposes all of `gograph`'s capabilities as native LLM tools (e.g., `mcp_gograph_query`, `mcp_gograph_impact`), allowing the agent to invoke them automatically.

### Claude Desktop Config + Shared Claude Rules/Hook

```bash
gograph add-claude-plugin
```

This single command performs **three installation steps**:

| Step | What is installed | Location |
|---|---|---|
| **MCP server** | Registers gograph in `claude_desktop_config.json` | macOS: `~/Library/Application Support/Claude/` |
| **CLAUDE.md rules** | Injects steering rules Claude reads at every session start | `~/.claude/CLAUDE.md` |
| **PreToolUse hook** | Smart hook that intercepts `grep`/`rg` on Go symbols | `~/.claude/hooks/gograph-guard.sh` + `~/.claude/settings.json` |

The installer exits non-zero if any step fails. Restart Claude Desktop / Claude Code after successful installation. Claude Code MCP registration is still per project, as shown below.

#### What the PreToolUse hook does

The hook (`gograph hook-guard`) runs automatically before every `Bash` tool call Claude makes. When Claude tries to `grep` for a Go symbol, the hook:

1. Detects it is a Go symbol search (pattern matches a valid Go identifier: `[A-Za-z_][a-zA-Z0-9_]{2,}`)
2. Blocks the call (exit code `2`) and outputs which `gograph` tool to use instead
3. Claude immediately retries using the correct `gograph_query` / `gograph_context` / etc. call

**The hook is smart — it only intercepts symbol searches.** These pass through unchanged:
- Searches in non-Go files (`*.yaml`, `*.md`, `*.sql`, `*.sh`)
- Comment/doc searches (TODO, FIXME, HACK, DEPRECATED)
- Searches in `docs/`, `.github/`, `testdata/`, `migrations/`
- Short patterns or patterns with regex special characters

### Claude Code (CLI) — Per-Project Registration

Because Claude Code isolates tools per-project, you must explicitly add `gograph` to each repository:

```bash
claude mcp add gograph -- gograph mcp .
```

**How it works:**
- Claude Code registers the plugin centrally in your home directory (`~/.claude.json`), but **maps it directly to your current project directory**.
- The `.` in `gograph mcp .` tells the server to index whatever specific folder Claude Code is currently operating in.
- **You must run this command once for each Go project repository** you wish to use it in. This prevents your agent from accidentally querying index databases from other projects!
