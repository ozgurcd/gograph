# gograph

[![Go Report Card](https://goreportcard.com/badge/github.com/ozgurcd/gograph)](https://goreportcard.com/report/github.com/ozgurcd/gograph)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/github/go-mod/go-version/ozgurcd/gograph)](https://github.com/ozgurcd/gograph)
[![Homebrew](https://img.shields.io/badge/homebrew-available-orange)](https://github.com/ozgurcd/homebrew-tap)
[![Docs](https://img.shields.io/badge/docs-gograph.identuum.ai-blue)](https://gograph.identuum.ai)

**Stop burning tokens on `grep`. Give your AI agent a graph.**

`gograph` builds a local, AST-aware call graph of your Go repository and exposes **50+ query tools** via CLI and MCP so coding agents can navigate packages, symbols, call chains, routes, SQL, env vars, and tests — without reading raw files.

![Gograph Demo](gograph-demo.gif)

> **Zero network. Zero execution. Zero secrets read.** `gograph` is purely static analysis — it never runs your code, makes API calls, or opens non-`.go` files.

## Quick Start

```bash
# Install
brew install ozgurcd/tap/gograph

# Build the graph
gograph build . --precise

# Try it — who calls ValidateToken?
gograph callers "ValidateToken"

# Full context in ONE call (node + source + callers + callees + tests)
gograph context "ValidateToken"

# Change plan before editing (callers, tests, routes, SQL, env risk)
gograph plan "ValidateToken"
```

## Why gograph?

*Benchmarked on gograph's own codebase (70 files, 518 symbols, 16 packages):*
| Task | `grep -rn` | `gograph` | Savings |
|---|---|---|---|
| Find callers of `loadGraph` | 158 noisy lines (comments, docs, vars) | 56 exact structural call sites | ~65% noise eliminated |
| Locate symbol definitions | 842 lines matching "Symbol" | 83 true type/method declarations | ~90% noise eliminated |
| Read one function body | `cat` dumps 180+ lines of the whole file | `source` extracts the exact 12-line function | ~93% fewer tokens |
| Understand a symbol fully | 4–5 separate tool calls | 1 call: `context` bundles everything | 80% fewer tool calls |

## Key Features

**50+ Query Tools** — callers, callees, impact, context, plan, review, errorflow, orphans, hotspot, coupling, and more. Full [command reference →](https://gograph.identuum.ai/docs/command-reference/)

**Native MCP Server** — all tools available as MCP endpoints for Claude, Cursor, Copilot, and any MCP-compatible agent. One command setup: `gograph add-claude-plugin`

**Token-Saving Composites** — `context` replaces 5 calls. `plan` replaces 8. `explain` synthesizes architectural narratives. Built to minimize agent round-trips.

**Safe by Design** — no network, no code execution, no secrets, no `.env` files read. AI worktree directories (`.claude/`, `.cursor/`, `.agents/`) auto-excluded.

**Architecture Enforcement** — boundary rules, API drift detection, complexity gates, dead code sweeps, god-object detection, coupling analysis. Run in CI with `gograph gate`.

**Agent Compliance Auditing** — session telemetry tracks whether agents run `plan` before edits and `review` after. Grades agent behavior A–F with actionable recommendations.

## Command Reference

All commands support `--json` for machine-readable output and `--files-only` for flat file lists.

| Category | Commands | What it does |
|---|---|---|
| **Indexing** | `build . [--precise]`, `stale`, `stats` | Parse AST, write graph. Check freshness. Index health. |
| **Navigation** | `query`, `callers [--depth N]`, `callees [--depth N]`, `path`, `source`, `node` | Find symbols, trace call chains, extract source. |
| **Context** | `context`, `explain`, `focus`, `endpoint` | Bundled structural data in one call. Token savers. |
| **Change Analysis** | `plan`, `review`, `risk`, `impact [--uncommitted\|--since]`, `changes [--git]`, `api --since` | Pre-edit planning, post-edit review, risk analysis, blast radius, drift. |
| **Architecture** | `boundaries`, `coupling`, `complexity`, `godobj`, `orphans`, `arity` | Quality gates, dead code, coupling, god objects. |
| **Types & Structs** | `fields`, `implementers [--test-only]`, `interfaces`, `embeds`, `constructors`, `literals`, `usages`, `mutate`, `schema` | Struct fields, interface satisfaction, type usage. |
| **Infrastructure** | `routes`, `sql`, `envs`, `errors`, `concurrency`, `globals`, `deps [--transitive]`, `dependents` | HTTP routes, SQL, env vars, concurrency, imports. |
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

## AI Agent Integration

**One-command setup** (Claude Desktop + Claude Code):
```bash
gograph add-claude-plugin
```
This registers the MCP server, injects `CLAUDE.md` steering rules, and installs a `PreToolUse` hook that redirects `grep` on Go symbols to `gograph` tools.

**Alternative — install via Claude Code plugin marketplace:**
```bash
/plugin marketplace add ozgurcd/gograph
/plugin install gograph@gograph
```
Discovers gograph through Claude Code's plugin marketplace and ships a `SKILL.md` that auto-activates on Go work, teaching the agent the mandatory workflow (`capabilities` → `build` → `plan` → `context` → `review`) and that `grep`/`rg`/`find` must not be used for Go symbol search.

You still need the `gograph` binary installed (`brew install ozgurcd/tap/gograph` or `go install github.com/ozgurcd/gograph@latest`). Use `gograph add-claude-plugin` above for the full one-command bootstrap (MCP wiring + `CLAUDE.md` rules + `PreToolUse` hook). Use the plugin marketplace when you'd rather discover and install gograph from inside Claude Code's plugin UI.

**Other agents** (Cursor, Copilot, Antigravity, etc.):
```bash
gograph mcp .   # Run as MCP server over stdio
```
Add to your `.cursorrules` or AI system prompt:
> Before answering architecture or repository questions, inspect the available `gograph_*` MCP tools and use them instead of grep/find. Run `gograph capabilities` first.

All commands support `--json` for machine-readable output:
```bash
gograph callers "ValidateToken" --json
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

## Why not use a Language Server (`gopls`)?

`gopls` is optimized for human IDEs. `gograph` is optimized for terminal-based LLMs:

1. **Protocol Mismatch** — `gopls` returns `file:line:col` coordinates. Agents must then burn tokens running `cat`/`sed` to read the actual code. `gograph` extracts the exact structural slice and formats it as Markdown.
2. **Graph-Level Diagnostics** — `gopls` does hover and go-to-definition. `gograph` does reverse-BFS error tracing, full blast radius analysis, and PR-level change plans across the entire call graph.
3. **Composable Intelligence** — `gopls` answers one question at a time. `gograph context` bundles node + source + callers + callees + tests in a single call. `gograph plan` aggregates impact, routes, SQL, env, and test risk into one checklist.

<details>
<summary><strong>Correctness model</strong></summary>

- **Default mode** uses Go AST parsing and best-effort heuristics. Tolerates incomplete or non-compiling repositories.
- **Precise mode** uses type-checked enrichment and requires compilable packages.
- Heuristic extractors (routes, SQL, tests, error mapping) are navigation aids, not authoritative program analysis.
</details>

<details>
<summary><strong>Non-goals</strong></summary>

- No multi-language parsing
- No AI/model API calls
- No embeddings or SaaS backend
- No telemetry
- No replacement for compiler/type-checker correctness
</details>

## Contributing

Pull requests welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for build, test, and contribution guidelines.

> **Language Support:** `gograph` currently parses Go only. The architecture is extensible — if you want to add Python, TypeScript, Rust, etc., please open an issue first.

## License

MIT — see [LICENSE](LICENSE).

[![gograph MCP server](https://glama.ai/mcp/servers/ozgurcd/gograph/badges/score.svg)](https://glama.ai/mcp/servers/ozgurcd/gograph)