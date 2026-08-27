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

**[Explore the interactive no-install demo](https://gograph.identuum.ai/demo/)** ·
**[Review the reproducible benchmark](docs/benchmarking.md)**

**Companion projects:** [Scrinium](https://github.com/ozgurcd/scrinium)
provides repository-owned, evidence-backed knowledge for coding agents, while
[Rulefloor](https://github.com/ozgurcd/rulefloor) protects repository-local
invariants by binding them to concrete tests and detecting drift. They are
independent, optional tools: Scrinium can keep Gograph structural observations
and Rulefloor validation results as separate evidence without treating either
as proof of unrelated behavior or global project correctness.

![Gograph Demo](gograph-demo.gif)

> **Static analysis; no target-code execution.** Default indexing parses Go source locally and does not call application services. Linked directories and linked/special files for extensions recognized by `go/build` are excluded; unrelated regular-file or dangling links with non-Go extensions are not Go tool inputs and do not block precise analysis. Graph-directed source reads remain confined to regular files beneath the analyzed repository, and linked/non-regular Go tool metadata (`go.mod`, `go.sum`, `go.work`, `go.work.sum`, and `vendor/modules.txt`) is rejected before toolchain invocation; an explicitly symlinked repository root remains supported. Applicable `go.work use` members may be sibling modules beneath the nearest real Git checkout; without that boundary they remain confined beneath the workspace directory. Each member directory, `go.mod`, and optional `go.sum` is validated before `cmd/go` starts. Gograph also reads project metadata such as `.gitignore`, graph/config JSON, and Git state. Indexing asks the installed Go toolchain for the effective build/module context; precise mode additionally performs package type loading, and `doc` runs `go doc`. Those operations follow your configured module/cache/network policy. Before repository package loading or `go doc`, applicable local module/workspace source trees are preflighted for links that `cmd/go` may inspect; `.git` and `.gograph` subtrees are excluded. Session telemetry is local under `.gograph/sessions/`; nothing is sent to gograph services.

## Quick Start

```bash
# Install
brew install --cask ozgurcd/tap/gograph
# or: go install github.com/ozgurcd/gograph/cmd/gograph@latest

# Confirm which installation will run and detect PATH shadowing
gograph doctor --json

# Build a type-enriched precise graph, then verify it
gograph build . --precise
gograph stats

# Optional CI contract: fail when precise enrichment falls back
gograph build . --precise --strict

# Optional: prioritize lower heap use on constrained hosts
gograph build . --precise --memory-mode=low --max-memory=1GiB

# Optional: include integration-tagged files and tests in this graph
gograph build . --precise --tags=integration

# Start with repository-wide results that require no guessed symbol
gograph summary
gograph hotspot --top 5
gograph flow --no-tests
```

Homebrew and `go install` install the normal `gograph` CLI. MCP clients that
support MCP Bundles can instead discover the local stdio server in the
[official MCP Registry](https://registry.modelcontextprotocol.io) as
`io.github.ozgurcd/gograph`. Registry/MCPB installation is a separate
distribution path; it does not install the Homebrew cask or configure the
Claude Code marketplace plugin. The Registry is currently in preview. See
[Official MCP Registry and MCPB installation](docs/mcp-registry.md) for client
support, target selection, and current limitations.

Choose a real function or method shown by `summary`, `hotspot`, or
`gograph complexity`, then substitute its name below:

```bash
gograph explore "YourSymbol" --compact # low-token discovery, identity/role, and complete evidence counts
gograph explore "YourSymbol"           # standard source + callers/callees + tests + exact identity impact
gograph explore "YourSymbol" --deep    # standard response + depth-3 exact evidence, package context, explanation
gograph context "YourSymbol" # source + callers + callees + tests

# For compilable repositories, enrich the graph before a major refactor
gograph build . --precise
gograph plan "YourSymbol"
```

Build artifacts are written under the target `.gograph/` directory. `gograph`
adds `.gograph/` to the enclosing Git repository root `.gitignore` when
available, falls back to the build target `.gitignore` outside Git, and exits
without replacing artifacts if no Go files are found or no source file parses
successfully. The update accepts only an absent or regular `.gitignore`; a
repository-provided link is refused and its target is not modified. Go build
constraints, explicit comma-separated `--tags` (or inherited `GOFLAGS` when
the flag is absent), cmd/go package-directory rules, generated
sources, module-mode ignore directives, and Git ignores use the same scanner
policy for building, freshness checks, and change detection. Linked `.go`
files, linked directories, and other non-regular recognized Go inputs are
reported and excluded. Unrelated regular-file and dangling links with non-Go
extensions (for example YAML configuration or TSV fixtures) are ignored by
Go-tool preflight;
linked/non-regular `go.mod`, `go.sum`, `go.work`, `go.work.sum`, and
`vendor/modules.txt` entries are rejected before gograph or the Go toolchain
reads them. Applicable `go.work use` members may be sibling modules beneath the
nearest real Git checkout. Non-Git layouts retain workspace-directory
confinement, nested Git boundaries are not crossed, and every member directory,
`go.mod`, and optional `go.sum` is validated before `cmd/go` starts.
`.gograph` itself must be a real directory, and `graph.json` must be a regular
repository-confined file. Graphs with a missing or unsupported confinement
policy marker must be rebuilt with the current binary before graph-backed
commands use them. Older binaries do not enforce this boundary and should not
be used to analyze untrusted repositories.

Each indexed source file stores a SHA-256 content digest. Rebuilds reparse all
selected files in a changed package together and reuse parser records for
unchanged packages; `stats` reports `reused_files` and `rebuilt_packages`.
Precise builds reuse that AST work but still recompute repository-wide
type/CHA/SSA enrichment so cross-package dispatch remains correct.

Low-memory mode preserves those graph semantics while using more aggressive
garbage collection, reclaiming memory between production and test analysis,
and avoiding a full JSON copy of the AST graph. `--max-memory` accepts integer
byte sizes such as `1GB` or `1GiB` and requires `--memory-mode=low`. It is a
soft Go-runtime memory target—not a hard RSS cap—so memory-mapped files,
the executable, and Go toolchain subprocesses can make process memory exceed
the requested value. Aggressive GC can increase CPU time, and a target that is
too low may make the build much slower or fail; Gograph never silently reduces
precision to meet it.

Precise fallback continues to exit zero by default for compatibility and is
recorded in graph metadata. Add `--strict` with `--precise` when fallback must
fail CI; Gograph still publishes or retains the diagnostic artifact before
returning non-zero.

## Machine-readable structural validation

External consumers can validate one closed structural predicate without
parsing human CLI output:

```bash
gograph version --json
gograph validate --repo /work/project --binding-json '{"schema_version":"gograph.binding.v1","predicate":"symbol_exists","subject":{"language":"go","kind":"symbol","id":"example.com/project/internal/auth::Authorize"},"required_precision":"ast"}' --json
```

The version and result schemas are `gograph.version.v1` and
`gograph.validation.v1`; bindings use `gograph.binding.v1`. V1 supports only
`symbol_exists`, `package_imports`, `call_edge_exists`, and `type_implements`.
Validation is read-only and never builds or refreshes the graph. Exit `0` means
`pass`, exit `1` means a conclusively evaluated `fail`, and exit `2` means
`cannot_evaluate` or an invalid request.

Negative results require predicate-specific completeness: symbol and direct
import absence need a current complete AST graph; implementation absence needs
a current precise-complete graph; call absence additionally requires complete
resolution of the subject's relevant call edges. Missing, stale, partial,
ambiguous, or unresolved evidence degrades to `cannot_evaluate`; a
`precise_fallback` graph may support AST presence but never evaluated absence.
The result binds the exact graph bytes, selected source/build-context manifest,
and canonical binding with SHA-256 fingerprints.

Gograph validates selected-build-context Go structure. It does not prove
runtime behavior or business correctness. CHA edges are possible static targets,
not runtime dispatch certainty. V1 excludes reachability, unstable or external
symbol identities, unnamed types, and non-Go languages. See the exact
[machine-validation contract](docs/machine-validation-contract.md).
Applicable local module/workspace source roots must remain beneath the explicit
`--repo` root; v1 returns `cannot_evaluate` instead of widening that authority.

MCP refreshes stay in memory by default. To publish each successful refresh
for CLI consumers and later server processes, start the server explicitly with:

```bash
gograph mcp . --persist-refresh
# Keep MCP startup and every later refresh on the integration-tagged selection:
gograph mcp . --tags=integration
# Optional low-memory policy for startup analysis and later refreshes:
gograph mcp . --memory-mode=low --max-memory=1GiB
```

This opt-in mode writes or overwrites `.gograph/graph.json` and the nine
Markdown reports after a confirmed-fresh refresh. It does not modify
`.gitignore`, so ignore `.gograph/` yourself before enabling it when needed.
The directory holds only the latest published state; it is not a per-branch
cache. If no usable graph exists (including an unsafe or unsupported artifact),
the startup auto-build is published before serving;
a failure there prevents startup. A later tool-triggered publication failure
makes that tool return an error, and the server retries the pending publication
on another refresh-capable call without rebuilding the already-fresh in-memory
graph. Writers coordinate through a local `.gograph/.artifacts.lock` file; an
existing lock entry must be regular rather than a link or special file.
Reports are replaced first and `graph.json` is replaced last as the publication
commit marker; the complete ten-file bundle is not a single atomic filesystem
transaction. Same-directory replacement is atomic on Unix-like systems; Go
does not guarantee atomic rename semantics on non-Unix platforms. The lock
file remains as operational coordination state in addition to the ten outputs.

Persisted graphs are bound to their effective Go environment and build
selection. Start MCP with the same `GOWORK`, `GOFLAGS`, and `--tags` context
used to build the graph; a mismatch is stale and must refresh successfully or
return a diagnostic rather than silently serving incompatible facts.
`gograph doctor --json` reports that repository diagnostic.

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

**Machine and Agent Workflows** — `explore` provides bounded first-call discovery with ranked lexical matches, explicit symbol selection, source, callers, callees, tests, and exact identity-resolved impact; focused callers, callees, broader impact, reverse test coverage, stable identity, plan, review, flow, validation, and policy commands remain available. The MCP server registers 68 endpoints including four session lifecycle tools. Full [command reference →](https://gograph.identuum.ai/docs/command-reference/)

**Federated Workspaces** — model multiple checked-out repositories through independently fingerprinted repository graphs plus a small deterministic cross-repository overlay. Resolution scopes support alternative fleets such as OSS/CE without merging repository ownership. P0 resolves Go modules, ordinary cross-repository Go calls, and first-class HTTP contracts for workspace-wide status, query, path, and impact analysis. The four read-only workspace MCP tools return the same native result values as CLI `--json`; member refresh and overlay publication remain explicit CLI mutations. [Workspace guide →](docs/workspaces.md)

**Native MCP Server** — all 64 repository query, analysis, and workflow capabilities have project-MCP equivalents for Claude, Cursor, Copilot, and other MCP clients; four additional endpoints cover session lifecycle (68 project tools total). A separate workspace server provides status, query, path, and impact with the same native results as the corresponding CLI operations. The normal mapping is CLI `<command>` to MCP `gograph_<command>`; `contract`, `boundaries --create`, and session actions use the documented special mappings. CLI-only process/host/artifact operations are `build`, `validate`, `doctor`, `gate`, `snapshot`, plugin/hook installation, project/workspace MCP startup, workspace build/member refresh, and help. The standalone `version` command has no MCP tool, but `gograph_capabilities` reports the running server version. Transport presentation differs where appropriate, but paired operations share functional semantics. [Complete CLI/MCP matrix →](https://gograph.identuum.ai/docs/command-reference/#cli--mcp-transport-matrix)

**Explicit Freshness Model** — CLI graph-backed analysis reads the last trusted persisted graph. Its JSON envelope includes `gograph.graph-state.v1`, separating source (`persisted`/`in_memory`), freshness (`current`/`stale`), completeness (`complete`/`partial`), precision (`ast`/`precise`/`fallback`), refresh outcome, and persistence outcome; bounded diagnostics remain on the operation that produced them. Text `stats` and `stale` report the same persisted state. `gograph stale` compares selected source content digests plus the effective build/module fingerprint; mtimes are diagnostic only for current indexes. It is a tri-state predicate: exit `0` means current, `2` means stale, and `1` means an operational or JSON serialization error; a missing or unsupported source-policy marker is an explicit status-1 rebuild requirement. MCP source-analysis tools check the same freshness per call, adopt a newer persisted precise graph, and incrementally rebuild changed package ASTs in memory using the latest requested analysis mode. Refresh-backed tools preserve their compatibility text and add `gograph.mcp-result.v1` structured content plus `_meta.gograph_graph_state`. Failed precise enrichment can serve a clearly marked current in-memory fallback, while an ordinary refresh failure can serve the last trusted stale graph; neither degraded result is silently published, and a mismatched effective Go environment still fails closed. MCP `stale`, default `changes`, and `stats` inspect the trusted persisted snapshot, or the startup auto-build fallback when no usable artifact exists. With `--persist-refresh`, that snapshot advances after a successful refresh; publication failures leave the fresh in-memory graph usable and explicitly report `persistence.outcome=failed` with a persistence diagnostic for retry.

**Compact Composite Workflows** — `explore`, `context`, `plan`, and `explain` combine source and graph evidence that would otherwise require several separate queries. `explore` is additive: specialized commands remain the complete, stable interfaces for focused analysis. Actual tool-call and token savings depend on the repository and task.

**Narrow by Design** — never runs target repository binaries or tests and does not intentionally scan `.env`, key, certificate, or credential files. Linked directories and linked/special recognized Go build inputs are excluded; unrelated non-Go regular-file links are outside Go-tool preflight. On-demand source and snippet reads use a repository-rooted filesystem handle and accept only regular `.go` files without symlink components. Linked/non-regular Go module/workspace metadata, sums, and `vendor/modules.txt` are rejected before toolchain use. Applicable `go.work` members may be siblings inside the nearest real Git checkout and otherwise stay beneath the workspace directory; their directories plus module metadata are preflighted before `cmd/go`. Default/relative policy configs are project-confined; documented absolute config/output arguments are explicit operator-selected local locations. AI worktree directories (`.claude/`, `.cursor/`, `.agents/`) are excluded. The installed Go toolchain resolves effective build context during indexing; precise repository package loading and external `go doc` run only after a preflight that rejects source-tree links `cmd/go` may inspect across the selected root plus its effective module root, or the workspace root and member trees, excluding `.git` and `.gograph`. Dependency and toolchain resolution remain open-world under the user's Go environment.

**Architecture Enforcement** — boundary rules, API drift detection, complexity gates, dead code sweeps, god-object detection, coupling analysis. Run in CI with `gograph gate`.

**Security Flow Analysis** — `flow` follows potential HTTP request, decoded JSON, and environment data across assignments and function calls to SQL query text, process execution, filesystem paths, and outbound HTTP targets. Findings include severity, confidence, and source-to-sink path steps; MCP exposes the same analysis as `gograph_flow`.

**Integrity-Aware Indexing** — publication refuses a linked or non-directory `.gograph`; `graph.json` is staged and replaced last only after a successful parse (the same-directory rename is atomic on Unix-like systems), records complete/partial build health and `ast`/`precise`/`precise_fallback` analysis status, and exposes both through `gograph stats`. `gate` refuses to evaluate a stale graph.

**Agent Compliance Auditing** — session telemetry tracks whether agents run `plan` before edits and `review` after. Grades agent behavior A–F with actionable recommendations.

## Command Reference

Query and composed-analysis commands support `--json`; `version --json` and
`validate ... --json` use their dedicated machine schemas. The exact `--files-only`
surface is listed in the command reference. Operational commands such as
`build`, `wiki`, `gate`, `snapshot`, installation, and help use text
output; `doctor` and workspace build/status/query/path/impact also accept `--json`, and
`session audit` additionally supports raw JSON. CLI `--mermaid` renders
`callers`, `callees`, `impact`, `endpoint`, `dependents`, `deps`, `path`, and
`coupling` as fenced Mermaid. Their MCP equivalents accept `mermaid=true` and
return the same Markdown-fenced Mermaid text; without it, each tool retains its
normal response format.

| Category | Commands | What it does |
|---|---|---|
| **Indexing** | `build . [--precise] [--strict] [--memory-mode=low] [--max-memory=1GiB]`, `stale`, `stats` | Parse AST, optionally require precise success or prioritize lower heap use, write graph, check freshness and health. |
| **Machine Validation** | `version --json`, `validate --repo PATH --binding-json JSON --json` | Versioned exact structural predicates with tri-state outcomes. |
| **Navigation** | `query`, `callers [--depth N]`, `callees [--depth N]`, `path`, `source`, `node` | Find symbols, trace call chains, extract source. |
| **Context** | `context`, `explain`, `focus`, `endpoint` | Bundled structural data in one call. Token savers. |
| **Change Analysis** | `plan`, `review`, `risk`, `impact [--uncommitted\|--since]`, `changes [--git]`, `api --since` | Pre-edit planning, post-edit review, risk analysis, blast radius, drift. |
| **Architecture** | `boundaries`, `coupling`, `complexity`, `godobj`, `orphans`, `arity` | Quality gates, dead code, coupling, god objects. |
| **Types & Structs** | `fields`, `implementers [--test-only]`, `interfaces`, `embeds`, `constructors`, `literals`, `usages`, `mutate`, `schema` | Struct fields, interface satisfaction, type usage. |
| **Infrastructure** | `routes [term] [--module MODULE] [--include-tests] [--limit N] [--cursor CURSOR]`, `sql`, `envs`, `errors`, `concurrency`, `globals`, `httpcalls`, `deps [--transitive]`, `dependents`, `imports` | Bounded, filterable HTTP route pages (production-only by default; including constant nested Gin/Echo/Fiber groups and Chi Route closures), SQL, env vars, concurrency, outbound HTTP calls, imports. |
| **Security** | `flow [term] [--source kind] [--sink kind] [--config path] [--no-tests]` | Potential untrusted-data paths to SQL, process, filesystem, and outbound HTTP sinks. |
| **Testing** | `tests [symbol] [--transitive] [--exact-only] [--package name]`, `coverage <TestFunc> [--exact-only] [--package name]`, `untested [--pkg name] [--top N] [--exclude glob] [--wide]`, `fixtures`, `mocks` | Direct and transitive reverse exact/possible static test attribution, one-sweep gap census, full stable-ID output, helpers, mock implementations. |
| **Error Tracing** | `errorflow [--no-tests]`, `trace` | Reverse-BFS from error strings to HTTP entry points. |
| **Diagnostics** | `doctor [--json]`, `hotspot`, `returnusage`, `skeleton`, `diagram`, `changes`, `public` | Install/PATH plus current graph freshness/capability diagnostics, hotspots, return usage, API signatures, Mermaid diagrams. |
| **CI/CD** | `check [--since\|--uncommitted]`, `gate`, `snapshot save\|diff\|list\|drop` | Policy checks, threshold enforcement, metric snapshots. |
| **Telemetry** | `session create\|end\|audit\|cleanup` | Agent compliance tracking and grading (A–F). |
| **LLM-Wiki** | `wiki [--output dir]` | Generate `llm-wiki/` and prune obsolete generator-owned package pages while preserving custom pages and `packages/README.md`. |
| **Summary** | `summary [--json]` | Single-call codebase briefing: top 3 hotspots, worst instability package, highest complexity function, orphan count, god-object count. Replaces 5 separate calls. |
| **Stable IDs** | `identity <symbol-or-stable-id> [--package name] [--json]` | Print and re-resolve module/package/receiver/name identity that survives line shifts and file moves inside a package; package disambiguates external-test collisions. |
| **Reverse Attribution** | `coverage <TestFunc> [--exact-only] [--package name] [--json]` | Transitive product-symbol set for one unambiguous test, with stable-ID paths and exact/possible propagation. Static evidence only—not runtime or branch coverage. |
| **Tests reaching a symbol** | `tests <symbol> --transitive [--exact-only] [--package name] [--json]` | Versioned reverse attribution listing every test with a representative stable-ID path to one product symbol. Default `tests` remains direct for compatibility. |
| **Untested** | `untested [--pkg name] [--top N] [--exclude glob] [--wide] [--json]` | Called production symbols without an exact transitive test path. Precise builds devirtualize only proven concrete receivers; open interface paths remain `test_resolution=possible`. JSON includes `stable_id`; `--wide` prints it without truncation. |
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

**Official MCP Registry (preview):** MCPB-capable clients can discover
`io.github.ozgurcd/gograph`. The bundle asks for the root directory of the Go
project and launches the bundled executable with separate arguments equivalent
to `gograph mcp <project-directory>`. Releases provide macOS, Linux, and
Windows bundles for both amd64 and arm64. The current Registry package schema
cannot select by CPU architecture, so choose the asset whose filename matches
the host; do not assume a client will select it automatically. All analysis
still runs locally over stdio, with no hosted gograph service or remote
telemetry.

The Registry bundle and installer-generated MCP registrations intentionally omit
`--persist-refresh`, keeping disk publication off by default. Use a custom
local MCP command if you explicitly want that behavior.

**Desktop config, shared rules, and Claude Code hook setup:**
```bash
gograph add-claude-plugin
```
This registers the Claude Desktop MCP server, injects shared `CLAUDE.md` steering rules, and installs a Claude Code `PreToolUse` hook. The hook redirects Go-symbol searches only when an effective search target belongs to a repository with a `.gograph` index, so unindexed folders in multi-root workspaces remain unaffected. For Claude Code MCP registration, also run the command printed by the installer: `claude mcp add gograph -- gograph mcp .`. The installer exits non-zero when any installation step fails.

**Alternative — install via Claude Code plugin marketplace:**
```bash
/plugin marketplace add ozgurcd/gograph
/plugin install gograph@gograph
```
Discovers gograph through Claude Code's plugin marketplace and ships a `SKILL.md` that auto-activates on Go work, teaching the agent the workflow (`doctor --json` → `capabilities` → `stats` → `plan` → `context` → edit → `review`), when a durable precise CLI build is useful, when to use structural queries, and when to verify with `gopls` or targeted text/source search.

You still need the `gograph` binary installed (`brew install --cask ozgurcd/tap/gograph` or `go install github.com/ozgurcd/gograph/cmd/gograph@latest`). Use `gograph add-claude-plugin` for Claude Desktop MCP wiring plus shared rules and the Claude Code hook; register the Claude Code MCP server with the printed `claude mcp add` command. Use the plugin marketplace when you prefer discovery from Claude Code's plugin UI.

**Other agents** (Cursor, Copilot, Antigravity, etc.):
```bash
gograph mcp .                     # stdio server; refreshes stay in memory
gograph mcp . --persist-refresh   # opt in to publishing refreshed artifacts
gograph mcp . --tags=integration  # retain the same tagged context on every refresh
gograph mcp . --memory-mode=low --max-memory=1GiB  # same low-memory refresh policy as CLI builds
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
# generated orientation starts at: llm-wiki/overview.md
# if maintained governance pages exist, read:
# llm-wiki/index.md → project.md → agent-rules.md → agent-contract.md
```
Add generated wiki output to `.gitignore` when it is disposable. Do not
overwrite a repository's maintained or Scrinium-protected `agent-rules.md`;
propose governed changes through that repository's documented workflow.
Regeneration removes only obsolete package pages that match Gograph's generated
signature; custom package pages and `packages/README.md` are preserved.

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
- **Repository source boundary** excludes linked directories plus linked/special recognized Go build inputs before build selection, while unrelated regular-file or dangling non-Go links do not block precision. It supplies confined bytes to the AST parser and confines later `source`, caller/callee snippet, complexity, and changed-file reads to regular repository files. Linked/non-regular `go.mod`, `go.sum`, `go.work`, `go.work.sum`, and `vendor/modules.txt` metadata is rejected before gograph or the Go toolchain reads it. Applicable `go.work use` paths may select sibling modules beneath the nearest real Git checkout; without one they remain beneath their workspace directory. Nested Git boundaries are not crossed, and member directories, `go.mod`, and optional `go.sum` are validated before `cmd/go`. Precise loading and `doc` preflight the selected root plus its effective module root, or the workspace root and every member tree; `.git` and `.gograph` are excluded from that source-tree walk. Persisted `graph.json` is also read through this boundary, `.gograph` must be a real directory, and an explicitly symlinked repository root is allowed. Missing or unsupported source-policy markers and artifacts larger than 512 MiB are rebuild-required, and serialized graph roots are never trusted. Saved baseline graphs must be regular non-linked files inside the selected project with the exact marker and size bound. Default/relative check and flow configs, boundaries, gate config, and repository-controlled session/snapshot/wiki mutations reject linked path components; documented absolute config/wiki locations are explicit operator selections. Use the current binary for untrusted repositories.
- **Precise mode** attempts type-checked production enrichment and needs compilable, build-selected packages for CHA/SSA results. SSA bodies are built for selected repository packages, not the full transitive dependency closure; imported types and local external-call references remain available without dependency-body call graphs or their source-less wrapper noise. If enrichment fails or omits an indexed non-test source file, the command warns, publishes the AST graph, and records `precise_fallback`; if a fresh successful precise artifact already covers the same sources, a failed retry keeps that artifact instead. Default fallback remains exit zero for compatibility; `--strict` requires `--precise` and returns non-zero after publication or retention. Successful and AST-only builds record `precise` and `ast` respectively. Test packages are loaded in a separate non-fatal typed pass: broken tests yield `typed_partial` test-call attribution without downgrading successful production precision. Typed-only test targets are recomputed rather than reused as parser facts, preventing edge multiplication across unchanged precise builds.
- **Low-memory mode** changes execution policy, not analysis meaning. It uses aggressive GC, releases completed production type/SSA state before typed-test loading, and honors an optional soft Go runtime memory target. The target is neither an RSS ceiling nor a guarantee that all repositories can complete within that amount; Gograph reports fallback/failure normally rather than silently omitting precise facts.
- A precise interface invocation whose SSA receiver is proven to contain one concrete dynamic type is devirtualized to one exact ordinary call. Otherwise it is represented by one call edge per valid named in-repository CHA target. A single visible implementation is never treated as proof. `callers Interface.Method` (including inherited and promoted methods) expands through recorded implementers and deduplicates shared source expressions. Compiler-generated promoted-method forwarding remains traversal-only.
- Open CHA dispatch is conservative rather than points-to precise: it may retain implementations that cannot occur in one runtime configuration. Reflection, `unsafe`, plugins, unresolved function values, test-only implementations, unnamed concrete types, and module-external implementations can still be incomplete. Precise test attribution also proves single-assignment, non-escaping concrete interface locals; other interface targets remain explicitly possible, and `typed_partial` means some tests stayed on parser heuristics.
- Callback references are retained only when they resolve to repository callables, and exact call edges are deduplicated before serialization.
- Mutation queries ignore ordinary local assignments and retain owning type information when statically known, so `Type.Field` disambiguates same-named fields.
- Synchronization extraction requires a receiver tied to a known `sync` type. Error messages come from `panic`, `errors.New`, and `fmt.Errorf`, including import aliases.
- Heuristic extractors (routes, SQL, parser-only tests, and error mapping) are navigation aids, not authoritative program analysis. Typed test attribution is still static evidence rather than runtime coverage proof.
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
