# Architectural Guidelines

This document outlines the core architectural philosophy, constraints, and development standards for `gograph`. Any new features, commands, or optimizations must adhere to these principles.

## 1. Core Philosophy & Technical Constraints

`gograph` is designed as a local, AST-aware repository navigation tool tailored specifically for AI coding agents.

- **Local Service Boundary:** Source and telemetry must never be sent to a gograph service or external analytics API. Default AST analysis is local but may invoke the installed Go toolchain to resolve effective build/module context; precise mode additionally type-loads repository packages, and `doc` runs `go doc`. Dependency/toolchain resolution follows the user's module/cache/network policy and is open-world. Indexing may read Go source, recognized non-Go build inputs, `go.mod`, `go.sum`, `go.work`, `go.work.sum`, `vendor/modules.txt`, `.gitignore`, Git state, persisted graph data, and explicitly selected gograph configuration; it must not intentionally scan `.env`, keys, certificates, kubeconfigs, tfstate, or credential files.
- **No Target-Code Execution:** The tool statically analyzes code and does not run target tests, binaries, or application entry points.
- **Repository Source Confinement:** Descendant links and special entries for every extension recognized by `go/build` must never be opened by repository build selection, and Go tool metadata reads must reject linked or non-regular `go.mod`, `go.sum`, `go.work`, `go.work.sum`, and `vendor/modules.txt` before toolchain use. Applicable `go.work use` paths must remain beneath the workspace directory, and every member directory, `go.mod`, and optional `go.sum` must be validated before `cmd/go`. Build parsing and every graph-directed source read must use the shared repository-rooted source reader, reject symlink path components, and parse caller-supplied bytes rather than reopening paths. Persisted `graph.json` must be read through the same regular-file boundary, and publication plus root discovery must reject a linked or non-directory `.gograph`. An explicitly symlinked repository root remains supported. Persisted graphs without the exact current source-policy marker are untrusted and rebuild-required. The serialized graph root is metadata and must be replaced with the trusted load location. Saved graph baselines must be regular, non-linked files inside that project with the exact marker, and their serialized roots must also be ignored. Precise repository package loading and `go doc` must reject source-tree links `cmd/go` may inspect across the selected root plus its effective module root, or the workspace root and every member tree; `.git` and `.gograph` are excluded from that preflight. `doc` must reject filesystem-shaped queries. Do not claim that dependency or toolchain resolution is closed-world.
- **Rooted Repository Mutation and Config:** Repository-relative session, snapshot, boundary, check/flow/gate config, gate-initialization, and generated-wiki operations must use the shared rooted filesystem layer. Reject descendant links and special entries before reads, writes, appends, or cleanup; validate path-derived IDs; use exclusive creation where overwrite is not the command contract. Relative wiki output is project-rooted, while an explicit absolute check config or wiki output selects a separate local location under its documented regular-file/directory contract.
- **Explicit Freshness:** CLI analysis uses a persisted `.gograph/graph.json` snapshot, except commands whose contract also reads source or Git state (for example `source`, `stale`, `changes`, and Git-baseline modes). MCP source-analysis tools refresh an in-memory graph using the current requested precision and adopt newer compatible persisted graphs; MCP persisted-index tools preserve CLI snapshot semantics. MCP refresh publication is off by default. The explicit `--persist-refresh` mode publishes `graph.json` plus nine reports, does not edit `.gitignore`, and retains only the latest state rather than a branch cache. Keep both paths bounded and deterministic.
- **Artifact Publication:** Require `.gograph` to be a real directory and an existing lock entry to be a regular file before opening the writer lock or staging files. Stage all artifacts under that local writer lock. Replace the nine reports first and atomically replace `graph.json` last so it acts as the publication commit marker. The automatic `.gitignore` update must reject links and special files. Do not describe the complete ten-file bundle as atomic or publication as resistant to a concurrent same-user filesystem swap: a process or filesystem failure can interrupt report replacement before `graph.json` is committed.
- **Token Efficiency:** The output of CLI commands must be concise and targeted to save LLM context window tokens.
- **CLI/MCP Parity:** Query, analysis, and workflow semantics belong on both surfaces, including workspace status/query/path/impact on the separate workspace MCP server. The complete CLI-only boundary is `build`, `validate`, `doctor`, `gate`, `snapshot`, plugin/hook installation, project/workspace MCP startup, workspace build/member refresh, help, and version. Transport presentation may differ, but supported options and documented evidence must remain aligned. The graph-oriented `callers`, `callees`, `impact`, `endpoint`, `dependents`, `deps`, `path`, and `coupling` commands map CLI `--mermaid` to MCP `mermaid=true`.

## 2. Correctness Model

- **Default Mode (Heuristic):** The default `gograph build .` uses raw Go AST parsing (`go/ast`, `go/parser`). It uses duck-typing and structural heuristics. It **must** tolerate incomplete, uncompilable, or messy codebases.
- **Precise Mode (Type-Checked):** The `gograph build . --precise` command uses `go/types` for Class Hierarchy Analysis (CHA) and exact interface satisfaction. Each interface invocation retains all valid named in-repository CHA targets as parallel edges. Promoted-method wrappers use explicitly marked, traversal-only forwarding edges with no source provenance. Precise analysis is allowed to fail and publish the AST graph if the target codebase does not compile, but that fallback must be visible in metadata and stats. If the same selected sources already have a fresh successful precise artifact, a failed precise retry retains that artifact rather than replacing it with a fallback.
- **Navigation Aids, Not Proofs:** Heuristic extractors (such as REST route mappers, SQL query extractors, or test edge mappers) are strictly navigation aids for AI agents. They are not guaranteed to find every dynamic invocation. Do not use hyperbolic language (e.g., "cryptographic proof") to describe AST analysis.
- **Security Flow Contract:** Flow analysis may be interprocedural but must remain bounded, deterministic, tolerant of broken code, and explicit about path insensitivity and call-context limits. Persist reusable AST facts in the graph and apply sanitizer policy at query time. Report confidence and never describe a finding as proof of exploitability.

## 3. Package Architecture

The codebase is organized into strict domains:
- **`internal/graph`**: Defines the core data models (`SymbolNode`, `MutationEdge`, `Dependency`, etc.). Keep this lightweight and easily serializable to JSON.
- **`internal/parser`**: Handles AST inspection, scope resolution, and metadata extraction. All logic for extracting structural data (functions, globals, concurrency primitives, and security-flow facts) belongs here.
- **`internal/search`**: Contains query processing, graph traversal, duck-typing, filtering, and query-time security-flow propagation/policy. Most functions are graph-pure; filesystem/Git-backed functions must accept an explicit graph root and use shared scanner/baseline rules. Graph-derived Go source must be read through `internal/sourcefs`.
- **`internal/cli`**: Orchestrates the user-facing commands, argument parsing, and CLI formatting.
- **`internal/mcp`**: Handles the Model Context Protocol stdio server wrapper around the search functions.

## 4. Development Standards

- **Go Version:** The project requires **Go 1.27.0 or newer**. Never default to or generate code for older versions; 1.27.0 is the minimum version used by CI and release builds.
- **Build Pipeline:** Always compile the binary using `make build`. Never use raw `go build`, as the Makefile handles version injection (`ldflags`) via `bump2version`.
- **Documentation Discipline:** Every new feature, command, or flag must be immediately documented across all relevant targets:
  1. `README.md`
  2. `docs/coding-agent-usage.md`
  3. `gograph capabilities` (`internal/cli/cli.go`)
  4. `gograph --help` (`internal/cli/cli.go`)
  5. `llm-wiki/index.md` — update the page index if a new maintained page type is added
  6. `llm-wiki/agent-contract.md` — update tool selection rules if a new command changes workflow
  7. `RELEASE_NOTES.md` and the public docs-site command reference
- **Governed Agent Rules:** `llm-wiki/agent-rules.md` is protected project policy. Never overwrite it directly; use Scrinium's draft workflow for proposed changes.
