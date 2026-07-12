# gograph

[![Go Report Card](https://goreportcard.com/badge/github.com/ozgurcd/gograph)](https://goreportcard.com/report/github.com/ozgurcd/gograph)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/github/go-mod/go-version/ozgurcd/gograph)](https://github.com/ozgurcd/gograph)
[![Homebrew](https://img.shields.io/badge/homebrew-available-orange)](https://github.com/ozgurcd/homebrew-tap)
[![Docs](https://img.shields.io/badge/docs-gograph.identuum.ai-blue)](https://gograph.identuum.ai)

**Give Go coding agents a compiler-aware map for safer refactors.**

`gograph` builds a local structural graph of your Go repository, with optional
type-checked CHA/SSA enrichment. Its CLI and MCP workflows help coding agents
trace callers and interface implementations, plan change impact, and enforce
architecture without embeddings or a hosted code index.

![Gograph Demo](gograph-demo.gif)

> **Static analysis; no target-code execution.** Default indexing parses Go source locally and does not call application services. It also reads project metadata such as `go.mod`, `.gitignore`, graph/config JSON, and Git state. Precise mode and `doc` invoke the local Go toolchain, which follows your configured module/cache/network policy. Session telemetry is local under `.gograph/sessions/`; nothing is sent to gograph services.

## Quick Start

```bash
# Install
brew install ozgurcd/tap/gograph
# or: go install github.com/ozgurcd/gograph/cmd/gograph@latest

# Build a fast AST graph, then verify it
gograph build .
gograph stats

# Start with repository-wide results that require no guessed symbol
gograph summary
gograph hotspot --top 5
gograph flow --no-tests
```

Choose a real function or method shown by `summary`, `hotspot`, or
`gograph complexity`, then substitute its name below:

```bash
gograph context "YourSymbol" # source + callers + callees + tests

# For compilable repositories, enrich the graph before a major refactor
gograph build . --precise
gograph plan "YourSymbol"
```

Build artifacts are written under the target `.gograph/` directory. `gograph`
adds `.gograph/` to the enclosing Git repository root `.gitignore` when
available, falls back to the build target `.gitignore` outside Git, and exits
without replacing artifacts if no Go files are found or no source file parses
successfully. Individual ignored files and ignored directories use the same
scanner policy for building, freshness checks, and change detection.

## Why gograph?

*Illustrative point-in-time output comparison from an earlier gograph revision
(counts vary as the repository evolves; these commands return different kinds
of evidence):*
| Task | `grep -rn` | `gograph` | Observed output difference |
|---|---|---|---|
| Find callers of `loadGraph` | 158 matching lines (comments, docs, vars) | 56 AST-derived call-site rows | ~65% fewer rows in that run |
| Locate symbol definitions | 842 lines matching "Symbol" | 83 true type/method declarations | ~90% noise eliminated |
| Read one function body | `cat` displays 180+ lines of the whole file | `source` extracts the 12-line function | ~93% fewer source lines in that run |
| Gather common symbol context | Separate node, source, caller, callee, and test queries | `context` bundles those fields | Five evidence types in one response |

## Key Features

**61 Query and Analysis Capabilities** — callers, callees, impact, context, plan, review, flow, errorflow, orphans, hotspot, coupling, and more. The MCP server registers 65 endpoints including four session lifecycle tools. Full [command reference →](https://gograph.identuum.ai/docs/command-reference/)

**Native MCP Server** — every query and analysis capability has an MCP endpoint for Claude, Cursor, Copilot, and other MCP clients. Host integration and CI artifact commands (`build`, `gate`, `snapshot`, plugin/hook installation, server startup, help, and version) remain CLI-only.

**Explicit Freshness Model** — CLI analysis reads the last persisted graph. MCP source-analysis tools check freshness per call, adopt a newer persisted precise graph, and rebuild in memory after edits using the latest requested analysis mode. MCP `stale`, default `changes`, and `stats` inspect the persisted snapshot.

**Compact Composite Workflows** — `context`, `plan`, and `explain` combine source and graph evidence that would otherwise require several separate queries. Actual tool-call and token savings depend on the repository and task.

**Narrow by Design** — never runs target repository binaries or tests and does not read `.env`, key, certificate, or credential files. AI worktree directories (`.claude/`, `.cursor/`, `.agents/`) are excluded. Precise analysis and external documentation use the installed Go toolchain.

**Architecture Enforcement** — boundary rules, API drift detection, complexity gates, dead code sweeps, god-object detection, coupling analysis. Run in CI with `gograph gate`.

**Security Flow Analysis** — `flow` follows potential HTTP request, decoded JSON, and environment data across assignments and function calls to SQL query text, process execution, filesystem paths, and outbound HTTP targets. Findings include severity, confidence, and source-to-sink path steps; MCP exposes the same analysis as `gograph_flow`.

**Integrity-Aware Indexing** — `graph.json` is atomically replaced only after a successful parse, records complete/partial build health and `ast`/`precise`/`precise_fallback` analysis status, and exposes both through `gograph stats`. `gate` refuses to evaluate a stale graph.

**Agent Compliance Auditing** — session telemetry tracks whether agents run `plan` before edits and `review` after. Grades agent behavior A–F with actionable recommendations.

## Command Reference

Query and composed-analysis commands support `--json`; result-list queries also support `--files-only`. Operational commands such as `build`, `wiki`, `gate`, `snapshot`, sessions, installation, help, and version use text output.

| Category | Commands | What it does |
|---|---|---|
| **Indexing** | `build . [--precise]`, `stale`, `stats` | Parse AST, write graph. Check freshness. Index health. |
| **Navigation** | `query`, `callers [--depth N]`, `callees [--depth N]`, `path`, `source`, `node` | Find symbols, trace call chains, extract source. |
| **Context** | `context`, `explain`, `focus`, `endpoint` | Bundled structural data in one call. Token savers. |
| **Change Analysis** | `plan`, `review`, `risk`, `impact [--uncommitted\|--since]`, `changes [--git]`, `api --since` | Pre-edit planning, post-edit review, risk analysis, blast radius, drift. |
| **Architecture** | `boundaries`, `coupling`, `complexity`, `godobj`, `orphans`, `arity` | Quality gates, dead code, coupling, god objects. |
| **Types & Structs** | `fields`, `implementers [--test-only]`, `interfaces`, `embeds`, `constructors`, `literals`, `usages`, `mutate`, `schema` | Struct fields, interface satisfaction, type usage. |
| **Infrastructure** | `routes`, `sql`, `envs`, `errors`, `concurrency`, `globals`, `httpcalls`, `deps [--transitive]`, `dependents`, `imports` | HTTP routes, SQL, env vars, concurrency, outbound HTTP calls, imports. |
| **Security** | `flow [term] [--source kind] [--sink kind] [--config path] [--no-tests]` | Potential untrusted-data paths to SQL, process, filesystem, and outbound HTTP sinks. |
| **Testing** | `tests`, `fixtures`, `mocks` | Test coverage map, helpers, mock implementations. |
| **Error Tracing** | `errorflow [--no-tests]`, `trace` | Reverse-BFS from error strings to HTTP entry points. |
| **Diagnostics** | `hotspot`, `returnusage`, `skeleton`, `diagram`, `changes`, `public` | Hotspots, return usage, API signatures, Mermaid diagrams. |
| **CI/CD** | `check [--since\|--uncommitted]`, `gate`, `snapshot save\|diff\|list\|drop` | Policy checks, threshold enforcement, metric snapshots. |
| **Telemetry** | `session create\|end\|audit\|cleanup` | Agent compliance tracking and grading (A–F). |
| **LLM-Wiki** | `wiki [--output dir]` | Generate `llm-wiki/` — machine-first markdown pages for zero-cost agent orientation (overview, architecture, hotspots, routes, env, errors, concurrency, per-package, API surface). |
| **Summary** | `summary [--json]` | Single-call codebase briefing: top 3 hotspots, worst instability package, highest complexity function, orphan count, god-object count. Replaces 5 separate calls. |
| **Untested** | `untested [--pkg name] [--top N] [--json]` | Functions with callers but zero test edges — coverage gaps invisible to orphans or per-symbol test queries. One sweep replaces N `tests <sym>` calls. |
| **Doc** | `doc <pkg[.Symbol]> [--json]` | `go doc` wrapper — signature + doc comment for any stdlib or third-party symbol. No graph required. Closes the gap when call chains leave the project. |

> Full command reference with examples: [gograph.identuum.ai/docs/command-reference](https://gograph.identuum.ai/docs/command-reference/)

<details>
<summary><strong>Architecture Boundary Enforcement</strong></summary>

Define boundaries in `.gograph/boundaries.json`:
```json
{
  "layers": [
    { "name": "domain", "packages": ["internal/domain/**"], "may_import": [] },
    { "name": "handler", "packages": ["internal/handler/**"], "may_import": ["internal/service/**", "internal/domain/**"] }
  ]
}
```
Run `gograph boundaries` — exits with code 1 on violation. Works in CI/CD.
</details>

<details>
<summary><strong>Security flow sanitizer policy</strong></summary>

`gograph flow` includes test files by default; add `--no-tests` for production-only results. It automatically reads `.gograph/flow.json` when present, or accepts `--config <path>` for another JSON file inside the graph root. Sanitizers apply to a function's return value and can be scoped to selected sink kinds:

```json
{
  "sanitizers": [
    { "function": "security.CleanPath", "for": ["filesystem"] },
    { "function": "security.ValidateURL", "for": ["outbound_http"] }
  ]
}
```

Omit `for` to trust the return value for every sink kind. `function` accepts the call spelling or a fully-qualified symbol ID; use the fully-qualified form when names collide. A validator that returns only `bool` or `error` does not sanitize the original input; wrap validation in a function that returns the trusted value if that is the intended policy.
</details>

## AI Agent Integration

**Desktop config, shared rules, and Claude Code hook setup:**
```bash
gograph add-claude-plugin
```
This registers the Claude Desktop MCP server, injects shared `CLAUDE.md` steering rules, and installs a Claude Code `PreToolUse` hook. For Claude Code MCP registration, also run the command printed by the installer: `claude mcp add gograph -- gograph mcp .`. The installer exits non-zero when any installation step fails.

**Alternative — install via Claude Code plugin marketplace:**
```bash
/plugin marketplace add ozgurcd/gograph
/plugin install gograph@gograph
```
Discovers gograph through Claude Code's plugin marketplace and ships a `SKILL.md` that auto-activates on Go work, teaching the agent the workflow (`capabilities` → `build` → `plan` → `context` → edit → `build --precise` → `review`), when to use structural queries, and when to verify with `gopls` or targeted text/source search.

You still need the `gograph` binary installed (`brew install ozgurcd/tap/gograph` or `go install github.com/ozgurcd/gograph/cmd/gograph@latest`). Use `gograph add-claude-plugin` for Claude Desktop MCP wiring plus shared rules and the Claude Code hook; register the Claude Code MCP server with the printed `claude mcp add` command. Use the plugin marketplace when you prefer discovery from Claude Code's plugin UI.

**Other agents** (Cursor, Copilot, Antigravity, etc.):
```bash
gograph mcp .   # Run as MCP server over stdio
```
Add to your `.cursorrules` or AI system prompt:
> Before answering architecture or repository questions, inspect the available
> `gograph_*` MCP tools and run `gograph capabilities`. Prefer gograph for
> supported structural queries; use `gopls` or targeted source/text search when
> results are ambiguous, precision fell back, or a known source call is missing.

Query and composed-analysis commands support `--json` for machine-readable output:
```bash
gograph callers "YourSymbol" --json
# → {"schema_version": "1", "command": "callers", "status": "ok", "count": 2, "results": [...]}
```

For full integration guides, see [docs/coding-agent-usage.md](docs/coding-agent-usage.md).

**Zero-cost orientation with `llm-wiki/`:** Run `gograph wiki` once per session to generate a directory of machine-first markdown pages — overview, architecture diagram, hotspots, routes, env vars, error sites, concurrency, per-package docs, and the full API surface. Agents read these pages instead of issuing dozens of individual tool calls:
```bash
gograph build . --precise
gograph wiki                 # writes to ./llm-wiki/
# then read: llm-wiki/README.md → project.md → rules.md → agent-contract.md → overview.md
```
Add `llm-wiki/` to `.gitignore` — these files are regenerated each session.

## Example Output

When you run `gograph build .`, the generated `GRAPH_REPORT.md` gives your AI a condensed context map:

**External Dependencies (Tech Stack)**
| Module | Version |
|--------|---------|
| `github.com/gin-gonic/gin` | `v1.9.1` |
| `github.com/jackc/pgx/v5` | `v5.5.5` |

**Important Symbols (Top by outgoing calls)**
| Symbol | Kind | File | Line | Calls out |
|--------|------|------|------|-----------|
| `(Server).Start` | method | `server.go` | 42 | 18 |
| `ValidateAuth` | function | `auth.go` | 12 | 14 |

---

## How does gograph complement `gopls`?

[`gopls`](https://go.dev/gopls/features/mcp) is the Go project's
compiler-backed language server. It provides live workspace diagnostics,
navigation, references, implementations, refactoring support, and an
experimental MCP server. It should remain the first choice for editor and
compiler-aware workspace operations.

`gograph` adds a different layer for repository and agent workflows:

1. **Persisted snapshots** — CLI analysis can inspect a stable graph artifact,
   while MCP refreshes source-analysis state and preserves the requested
   precision mode.
2. **Repository-level analyses** — change impact, reachability, routes, SQL,
   environment reads, security-flow candidates, coupling, and policy gates are
   represented together.
3. **Composed responses** — `context`, `plan`, `review`, and `summary` package
   related evidence for agent workflows rather than exposing only one language
   operation at a time.

Use `gopls` for live compiler-backed navigation and refactoring, `rg` for text
and non-Go searches, and gograph when a persisted repository graph or composed
change-analysis workflow is useful. See [the benchmark guidance](docs/benchmarking.md)
for how to measure these different workflows without assuming one tool is a
drop-in replacement for another.

<details>
<summary><strong>Correctness model</strong></summary>

- **Default mode** uses Go AST parsing and best-effort heuristics. Tolerates incomplete or non-compiling repositories.
- **Precise mode** attempts type-checked enrichment and needs compilable, build-selected packages for CHA/SSA results. If enrichment fails or omits an indexed non-test source file, the command warns, publishes the AST graph, and records `precise_fallback`; successful and AST-only builds record `precise` and `ast` respectively.
- Each precise interface invocation is represented by one call edge per valid named in-repository CHA target. `callers Interface.Method` (including methods inherited from embedded interfaces and concrete methods promoted from embedded fields) expands through the interface's implementers and reports a shared source expression once; concrete receiver notation and fully-qualified method IDs remain available for disambiguation. Compiler-generated promoted-method forwarding is stored as a traversal-only synthetic edge and is hidden from call-site output.
- CHA is conservative rather than points-to precise: it may retain implementations that cannot occur in one runtime configuration. Reflection, `unsafe`, plugins, unresolved function values, test-only packages, unnamed concrete types, and module-external implementations can still be incomplete.
- Callback references are retained only when they resolve to repository callables, and exact call edges are deduplicated before serialization.
- Mutation queries ignore ordinary local assignments and retain owning type information when statically known, so `Type.Field` disambiguates same-named fields.
- Synchronization extraction requires a receiver tied to a known `sync` type. Error messages come from `panic`, `errors.New`, and `fmt.Errorf`, including import aliases.
- Heuristic extractors (routes, SQL, tests, and error mapping) are navigation aids, not authoritative program analysis.
- Security flow analysis is interprocedural and path-insensitive, with call/return matching across up to 16 nested repository calls. Default graphs resolve direct local/imported functions; `build . --precise` supplies stronger method/interface targets. It does not model reflection, globals, arbitrary heap aliases, or every dynamic call. Unresolved external transformations are retained with low confidence; every finding requires source review.
</details>

<details>
<summary><strong>Non-goals</strong></summary>

- No multi-language parsing
- No AI/model API calls
- No embeddings or SaaS backend
- No remote telemetry or hosted analytics (optional audit sessions write local metadata only)
- No replacement for compiler/type-checker correctness
</details>

## Contributing

Pull requests welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for build, test, and contribution guidelines.

> **Language Support:** `gograph` currently parses Go only. The architecture is extensible — if you want to add Python, TypeScript, Rust, etc., please open an issue first.

## License

MIT — see [LICENSE](LICENSE).

[![gograph MCP server](https://glama.ai/mcp/servers/ozgurcd/gograph/badges/score.svg)](https://glama.ai/mcp/servers/ozgurcd/gograph)
