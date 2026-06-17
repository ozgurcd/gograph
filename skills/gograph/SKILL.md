---
name: gograph
version: "0.1.0"
description: "Go repository intelligence for Claude Code. Use whenever the user is reading, navigating, editing, reviewing, or refactoring a Go codebase. Replaces grep/rg/find for Go symbols with AST-accurate call graphs, blast radius analysis, impact analysis, and 57+ semantic queries via the local gograph MCP server. Mandatory for Go work whenever the gograph MCP server is connected."
argument-hint: "gograph status | gograph plan UserService.Login | gograph review"
allowed-tools: Bash, Read
homepage: https://gograph.identuum.ai
repository: https://github.com/ozgurcd/gograph
author: ozgurcd
license: MIT
user-invocable: true
---

# gograph: Go Repository Intelligence

`gograph` is a local, AST-aware Go code intelligence engine that exposes 57+ semantic query tools over the Model Context Protocol. It gives terminal LLMs (Claude Code, Cursor agents, OpenClaw) deterministic structural awareness of a Go codebase without grepping files. One `gograph_context` call replaces 4-5 separate Read / Grep tool calls.

`gopls` is optimized for human IDEs. `gograph` is optimized for AI coding agents that pay for every token of context.

## When to invoke this skill

Activate whenever the user is working in a Go repository:

- Reading code, asking what a function does, or tracing a behavior across files.
- Planning, editing, refactoring, or deleting any Go symbol (function, method, struct, interface, package).
- Reviewing a Go diff or unstaged changes.
- Hunting a bug, auditing for security issues, or measuring complexity / coupling.

If a `.go` file is in the CWD or the user mentions a Go symbol, type, package, or interface by name, the skill applies.

Do NOT invoke for non-Go work. The skill is Go-scoped.

## Prerequisite

The gograph binary must be installed and on `$PATH`:

```bash
go install github.com/ozgurcd/gograph@latest
```

Verify with `gograph --version`. The MCP server registration ships with the plugin and auto-connects once the binary is installed.

## Mandatory workflow (enforced)

1. **At the start of any Go coding session**, run `gograph_capabilities` to confirm what the connected server exposes.
2. **Build / refresh the graph** before symbol queries:
   - First choice: `gograph build . --precise` (requires the package to compile).
   - Fallback: `gograph build .` - and explain why precise mode was unavailable.
3. **For symbol / type / function discovery, NEVER use `grep`, `rg`, `find`, or glob.** Use `gograph_query` instead. Text search returns false positives across vendored code, comments, and string literals; `gograph_query` returns AST-accurate matches.
4. **Before editing any Go symbol**, run `gograph_plan <symbol>`. The plan returns callers, tests connected to the symbol, and a blast-radius estimate. Edit decisions should reference the plan.
5. **To understand a function or method**, use `gograph_context <symbol>`. This single call returns node + source + callers + callees + tests, replacing 4-5 separate tool calls and saving substantial context tokens.
6. **After editing Go code**, run `gograph_review --uncommitted` to verify test coverage and surface the blast radius of the change.

## High-value tools

| Tool | Use case |
|---|---|
| `gograph_capabilities` | Discover what the connected server exposes |
| `gograph_query` | AST-accurate symbol search (replaces grep) |
| `gograph_context <symbol>` | Node + source + callers + callees + tests in one call |
| `gograph_plan <symbol>` | Pre-edit blast radius + callers + tests |
| `gograph_review --uncommitted` | Post-edit coverage check |
| `gograph_impact <symbol>` | What breaks if this changes |
| `gograph_callers` / `gograph_callees` | Explicit call-graph traversal |
| `gograph_implementers <interface>` | All types implementing an interface |
| `gograph_routes` | HTTP route discovery across handler / service / repository layers |
| `gograph_sql` | SQL query inventory across the codebase |
| `gograph_complexity` | Cyclomatic complexity per function |
| `gograph_godobj` | God-object detection |
| `gograph_coupling` | Package coupling / instability scores |
| `gograph_diagram` | Mermaid architecture diagrams |
| `gograph_errors` / `gograph_errorflow` | Error propagation paths |
| `gograph_changes` | Diff what changed since the last build |
| `gograph_tests` | Tests connected to a symbol |

The full surface is 57+ tools; `gograph_capabilities` is the live source of truth.

## Privacy

Graph building is fully local. No source code leaves the machine. MCP transport is local stdio. Respects `.gitignore` and skips AI-agent worktree directories automatically. See `PRIVACY.md` in the gograph repo for details.

## Anti-patterns

- Using `grep` / `rg` / `find` on a Go codebase when gograph is connected. Text search is inaccurate and wastes tokens.
- Editing a Go function without `gograph_plan` first. You will miss callers and break tests downstream.
- Skipping `gograph_review` after a multi-file change. Blast radius regressions slip through.
- Reading 5-15K tokens of surrounding code with Read / Grep when `gograph_context <symbol>` returns the same answer in a single structured response.

## Why this exists

LLMs reading source through grep / Read / Glob burn 5-15K context tokens to understand a single function's surroundings. `gograph` returns the same answer in one structured response. Token cost drops by roughly 80% and the answer is AST-accurate, not text-pattern-fuzzy.
