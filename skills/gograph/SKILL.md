---
name: gograph
version: "0.1.0"
description: "Go repository intelligence for Claude Code. Use when reading, navigating, editing, reviewing, or refactoring a Go codebase. Adds AST-aware call graphs, blast-radius analysis, impact and security-flow candidates, and 61 query and analysis capabilities through the local gograph MCP server."
argument-hint: "gograph status | gograph plan UserService.Login | gograph review"
allowed-tools: Bash, Read
homepage: https://gograph.identuum.ai
repository: https://github.com/ozgurcd/gograph
author: ozgurcd
license: MIT
user-invocable: true
---

# gograph: Go Repository Intelligence

`gograph` is a local, AST-aware Go code intelligence engine that exposes 61 query, analysis, and workflow capabilities over the Model Context Protocol (65 endpoints including session lifecycle). It gives terminal LLMs (Claude Code, Cursor agents, OpenClaw) a persisted structural view of a Go codebase. `gograph_context` combines evidence that may otherwise require several navigation and source calls; actual savings depend on the task.

`gopls` provides live compiler-backed navigation, diagnostics, implementations,
refactoring, and experimental MCP support. `gograph` complements it with a
persisted repository graph, composed change-analysis workflows, and policy
gates for coding agents.

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
go install github.com/ozgurcd/gograph/cmd/gograph@latest
```

Verify with `gograph --version`. The MCP server registration ships with the plugin and auto-connects once the binary is installed.

## Mandatory workflow (enforced)

1. **At the start of any Go coding session**, run `gograph_capabilities` to confirm what the connected server exposes.
2. **Build / refresh the graph** before symbol queries:
   - First choice: `gograph build . --precise` (requires the package to compile).
   - Fallback: `gograph build .` - and explain why precise mode was unavailable.
   - Run `gograph_stats` and require `build_status=complete`; a build with zero successful parses never replaces the previous graph, while partial failures are reported explicitly.
3. **For structural symbol / type / function discovery, use `gograph_query` instead of `grep`, `rg`, `find`, or glob.** Text search also matches comments and string literals; `gograph_query` returns AST-derived matches. Continue to use text search for literal strings, documentation, configuration, and non-Go files.
4. **Before editing any Go symbol**, run `gograph_plan <symbol>`. The plan returns callers, tests connected to the symbol, and a blast-radius estimate. Edit decisions should reference the plan.
5. **To understand a function or method**, use `gograph_context <symbol>`. This single call combines node + source + callers + callees + statically mapped tests; inspect source or `gopls` when the evidence is incomplete.
6. **After editing Go code**, run `gograph build . --precise`, then `gograph_review --uncommitted` to inspect test mappings and the indexed blast radius. Run the repository's required tests and checks separately.

## High-value tools

| Tool | Use case |
|---|---|
| `gograph_capabilities` | Discover what the connected server exposes |
| `gograph_query` | AST-derived structural symbol search |
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
| `gograph_flow` | Potential HTTP/JSON/env paths to SQL, process, filesystem, and outbound HTTP sinks |
| `gograph_changes` | Diff what changed since the last build |
| `gograph_tests` | Tests connected to a symbol |
| `gograph_check` | Policy checks, including changed-route tests, coverage, orphans, API drift, arity, and complexity |

The live surface is 65 MCP endpoints; `gograph_capabilities` is the tested source of truth. `gograph_flow` is path-insensitive with bounded call-site matching; use it for security review leads, not exploitability proof.

## Privacy

Graph artifacts and MCP transport are local. Default parsing makes no network
calls; precise mode and `doc` invoke the installed Go toolchain and therefore
follow its configured module-cache and network policy. Respects `.gitignore`
and skips AI-agent worktree directories automatically. See `PRIVACY.md` in the
gograph repo for details.

Most MCP tools are read-only. Boundary creation writes configuration, session create/end mutate telemetry, session cleanup deletes stale logs, and `gograph_wiki` writes documentation; their MCP annotations declare those effects.

## Anti-patterns

- Treating text search or gograph as universally authoritative. Use gograph
  first for supported structural queries; use `gopls` or targeted source/text
  search when precision is AST/fallback, results are ambiguous, or a known call
  is missing. Use text search directly for literals, comments, generated or
  non-indexed files, and non-Go content.
- Editing a Go function without `gograph_plan` first. This can miss relevant callers and downstream tests.
- Skipping `gograph_review` after a multi-file change and therefore missing a useful static review signal.
- Repeating broad source reads when `gograph_context <symbol>` can provide a
  focused structural starting point.

## Why this exists

Coding agents often need a symbol's source, callers, callees, tests, and role at
the same time. `gograph_context` combines that indexed evidence in one response.
Measure tool calls, actual model tokens, false positives, false negatives, and
task success on your own repository; gograph output remains static-analysis
evidence rather than runtime proof.
