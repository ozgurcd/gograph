---
name: gograph
version: "0.1.0"
description: "Go repository intelligence for Claude Code. Use when reading, navigating, editing, reviewing, or refactoring a Go codebase. Adds AST-aware call graphs, blast-radius analysis, impact and security-flow candidates, and 63 query, analysis, and workflow capabilities through the local gograph MCP server."
argument-hint: "gograph stats | gograph plan UserService.Login | gograph review --uncommitted"
allowed-tools: Bash, Read
homepage: https://gograph.identuum.ai
repository: https://github.com/ozgurcd/gograph
author: ozgurcd
license: MIT
user-invocable: true
---

# gograph: Go Repository Intelligence

`gograph` is a local, AST-aware Go code intelligence engine that exposes 63 query, analysis, and workflow capabilities over the Model Context Protocol (67 endpoints including session lifecycle). It gives terminal LLMs (Claude Code, Cursor agents, OpenClaw) a structural view backed by a persisted or in-memory graph. `gograph_context` combines evidence that may otherwise require several navigation and source calls; actual savings depend on the task.

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

Verify the active installation with `gograph doctor --json`; it reports the
running binary, PATH resolution, and shadowed copies without executing them.
The marketplace plugin supplies this workflow
guidance; it does not install the binary or register an MCP server. Register
`gograph mcp <project-path>` for each project using the client's MCP setup.
Packaged and generated registrations keep refresh persistence off by default.

## Mandatory workflow (enforced)

1. **At the start of any Go coding session**, run CLI `gograph doctor --json`
   to detect installation shadowing, then invoke `gograph_capabilities` to
   confirm what the connected server exposes.
2. **Confirm graph health** before symbol queries. MCP creates an in-memory AST
   graph when the artifact is missing, unreadable, unsafe, or has an unsupported
   source-policy marker, and refreshes source analysis per call. Invoke
   `gograph_stats` with no parameters and require `build_status=complete`; a
   build with zero successful parses never replaces the previous graph, while
   partial failures are reported explicitly. When durable precise enrichment
   is needed, run CLI `gograph build . --precise`; if compilation prevents it,
   use CLI `gograph build .` and explain the fallback.
3. **For structural symbol / type / function discovery, use `gograph_query` instead of `grep`, `rg`, `find`, or glob.** Text search also matches comments and string literals; `gograph_query` returns AST-derived matches. Continue to use text search for literal strings, documentation, ordinary non-sensitive configuration, and non-Go files.
4. **Before editing any Go symbol**, invoke `gograph_plan` with
   `symbol=<symbol>`. The plan returns callers, tests connected to the symbol,
   and a blast-radius estimate. Edit decisions should reference the plan.
5. **To understand a function or method**, invoke `gograph_context` with
   `symbol=<symbol>`. This single call combines node + source + callers +
   callees + statically mapped tests; inspect source or `gopls` when the
   evidence is incomplete.
6. **After editing Go code**, invoke `gograph_review` with `uncommitted=true`;
   MCP refreshes source analysis before the review. If the task requires a
   durable precise artifact for later CLI or server processes, run CLI
   `gograph build . --precise` first. Run the repository's required tests and
   checks separately.

## High-value tools

| Tool | Use case |
|---|---|
| `gograph_capabilities` | Discover what the connected server exposes |
| `gograph_query` with `term=<term>` or `terms=[...]` | AST-derived structural symbol search |
| `gograph_context` with `symbol=<symbol>` | Node + source + callers + callees + tests in one call |
| `gograph_plan` with `symbol=<symbol>` | Pre-edit blast radius + callers + tests |
| `gograph_review` with `uncommitted=true` | Post-edit coverage check |
| `gograph_impact` with `symbol=<symbol>` | What breaks if this changes |
| `gograph_callers` / `gograph_callees` with `function=<symbol>` | Explicit call-graph traversal |
| `gograph_implementers` with `interface=<interface>` | All types implementing an interface |
| `gograph_routes` | HTTP route discovery across handler / service / repository layers |
| `gograph_sql` | SQL query inventory across the codebase |
| `gograph_complexity` | Cyclomatic complexity per function |
| `gograph_godobj` | God-object detection |
| `gograph_coupling` | Package coupling / instability scores |
| `gograph_diagram` | Mermaid architecture diagrams |
| `gograph_errors` with optional `term=<term>` | Error inventory |
| `gograph_errorflow` with `query=<term>` | Error propagation paths |
| `gograph_flow` | Potential HTTP/JSON/env paths to SQL, process, filesystem, and outbound HTTP sinks |
| `gograph_changes` | Diff source against the trusted persisted graph, or MCP startup fallback when no usable artifact exists |
| `gograph_tests` with `symbol=<symbol>, transitive=true` | Every test statically reaching a symbol, with exact/possible path and depth; omit `transitive` for direct edges |
| `gograph_coverage` with `test=<TestFunc>` | Transitive product symbols one unambiguous test statically reaches; exact/possible paths; optional `package` disambiguation |
| `gograph_identity` with `symbol=<symbol-or-stable-id>` | Print or re-resolve canonical symbol identity without silently choosing ambiguity; optional `package` disambiguation |
| `gograph_check` | Policy checks, including changed-route tests, coverage, orphans, API drift, arity, and complexity |

The live surface is 67 MCP endpoints; `gograph_capabilities` is the tested source of truth. `gograph_flow` is path-insensitive with bounded call-site matching; use it for security review leads, not exploitability proof.
For `gograph_callers`, `gograph_callees`, `gograph_impact`,
`gograph_endpoint`, `gograph_dependents`, `gograph_deps`, `gograph_path`, and
`gograph_coupling`, set `mermaid=true` to request Markdown-fenced Mermaid
instead of the tool's normal response.

## Privacy

Graph artifacts and MCP transport are local. Indexing asks the installed Go
toolchain for effective build/module context; precise mode additionally
type-loads packages, and `doc` runs `go doc`. These operations follow the
configured module-cache and network policy and remain open-world. Indexing reads Go
source and ordinary project/gograph metadata; it does not intentionally scan
`.env`, key, certificate, kubeconfig, tfstate, or credential files. It respects
`.gitignore` and skips AI-agent worktree directories automatically. See
`PRIVACY.md` in the gograph repo for details.

Descendant symlinks and special files for recognized Go build inputs are
excluded. AST and graph-directed source reads are confined to regular files beneath the analyzed repository;
an explicitly symlinked repository root remains supported. Persisted
`graph.json` must also be a regular repository-confined file, and publication
refuses a linked or non-directory `.gograph` and linked/non-regular lock files.
The automatic `.gitignore` update rejects links rather than modifying their
targets. Linked/non-regular `go.mod`, `go.sum`, `go.work`, `go.work.sum`, and
`vendor/modules.txt` metadata is rejected before gograph or the Go toolchain
reads it. Applicable `go.work use` members must remain beneath the workspace
directory, and each member directory, `go.mod`, and optional `go.sum` is
validated before `cmd/go`. A persisted graph with a missing
or unsupported source-policy marker must be rebuilt before graph-backed tools
use it, and its serialized root is ignored. Saved `.json` baselines for
`gograph_api` and `gograph_check` must be regular, non-linked files inside the
selected project with the exact current marker; their serialized roots are
also ignored. Use the current binary for untrusted repositories. Precise
repository package loading and `go doc` are refused when source/metadata-link
validation fails; their preflight rejects source-tree links without following
targets that `cmd/go` may inspect across the selected root plus its effective
module root, or the workspace root and member trees; `.git` and
`.gograph` are excluded from that walk. `doc` also rejects filesystem-shaped queries.

Most MCP tools are read-only. Boundary creation writes configuration, session
create/end mutate telemetry, session cleanup deletes stale logs, and
`gograph_wiki` writes documentation; their MCP annotations declare those
effects. Repository-controlled session, snapshot, boundary, gate-init, and
relative wiki paths use rooted regular-file operations and reject linked path
components. Absolute wiki output is an explicit local destination whose
generated descendants remain confined beneath its real directory.

An operator can opt into durable MCP refreshes with
`gograph mcp [path] --persist-refresh`. After a successful refresh this writes
or overwrites `.gograph/graph.json` and the nine reports, without modifying
`.gitignore`. Refresh-capable tools then advertise that they may write. A
publication failure during a tool-triggered refresh is a tool error and is
retried on a later refresh-capable call without rebuilding the fresh in-memory
graph. If startup must auto-build, failure to publish prevents the server from
starting. A failed precise retry retains an already-fresh successful precise
artifact for the same sources. The artifact is the latest state only, not a
branch cache. Because default `gograph_changes` compares the working tree with
the persisted graph,
a successful publication also advances that comparison baseline. Reports are
replaced first and `graph.json` is replaced last as the publication marker.
Same-directory replacement is atomic on Unix-like systems but is not guaranteed
atomic by Go on non-Unix platforms; the complete ten-file bundle is not one
atomic transaction, and `.artifacts.lock` remains as separate operational state.

## Anti-patterns

- Treating text search or gograph as universally authoritative. Use gograph
  first for supported structural queries; use `gopls` or targeted source/text
  search when precision is AST/fallback, results are ambiguous, or a known call
  is missing. Use text search directly for literals, comments, generated or
  non-indexed files, and non-Go content.
- Editing a Go function without `gograph_plan` first. This can miss relevant callers and downstream tests.
- Skipping `gograph_review` with `uncommitted=true` after a multi-file change
  and therefore missing a useful static review signal.
- Repeating broad source reads when `gograph_context` with `symbol=<symbol>` can provide a
  focused structural starting point.
- Assuming MCP refreshes are durable, or that `--persist-refresh` caches branch
  history. Default registrations refresh only in memory, and the opt-in mode
  keeps one latest artifact set.

## Why this exists

Coding agents often need a symbol's source, callers, callees, tests, and role at
the same time. `gograph_context` combines that indexed evidence in one response.
Measure tool calls, actual model tokens, false positives, false negatives, and
task success on your own repository; gograph output remains static-analysis
evidence rather than runtime proof.
