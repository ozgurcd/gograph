# gograph

[![Go Report Card](https://goreportcard.com/badge/github.com/ozgurcd/gograph)](https://goreportcard.com/report/github.com/ozgurcd/gograph)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/github/go-mod/go-version/ozgurcd/gograph)](https://github.com/ozgurcd/gograph)
[![Homebrew](https://img.shields.io/badge/homebrew-available-orange)](https://github.com/ozgurcd/homebrew-tap)
[![Docs](https://img.shields.io/badge/docs-gograph.identuum.ai-blue)](https://gograph.identuum.ai)

**Local Go static analysis, call-graph, blast-radius, and CI architecture-gate CLI—with a built-in stdio MCP server (`gograph mcp`).**

`gograph` indexes a Go repository into a local AST and optional type-checked (CHA/SSA) graph. Use it from the terminal or CI to trace transitive callers, select tests for uncommitted changes, render Mermaid dependency diagrams, and enforce package boundaries—without hosted services, embeddings, or target-code execution.

```bash
# Install
brew install --cask ozgurcd/tap/gograph
# or: go install github.com/ozgurcd/gograph/cmd/gograph@latest

# Index a Go repository (AST + type-checked CHA/SSA)
gograph build . --precise

# CLI workflows: blast radius, Mermaid graphs, targeted tests, CI gates
gograph impact Store.Save
gograph callers Store.Save --mermaid
gograph tests Store.Save --transitive
gograph tests --uncommitted --format go-test --max-depth 3
gograph boundaries && gograph gate

# Built-in stdio MCP server (no manual build step required)
gograph mcp .
```

**[Documentation](https://gograph.identuum.ai/)** ·
**[Interactive Demo](https://gograph.identuum.ai/demo/)** ·
**[CI Architecture Gates](https://gograph.identuum.ai/docs/ci-architecture-gates/)** ·
**[Tool Comparisons](https://gograph.identuum.ai/docs/comparisons/)** ·
**[Benchmark](docs/benchmarking.md)**

![Gograph Demo](gograph-demo.gif)

---

## Quick Start

### 1. Build and Inspect the Graph

```bash
gograph doctor --json              # verify binary path and graph health
gograph build . --precise          # AST + type-checked CHA/SSA graph
gograph build . --precise --strict # CI mode: exit non-zero if precise enrichment falls back
gograph stats                      # inspect precision and package/symbol/call counts
```

Common build flags:
- `--tags=integration` — include build-tagged files and tests in the fingerprinted graph context.
- `--exclude-dirs=legacy,examples/broken` — omit unrelated non-compiling subtrees ([details](docs/build-selection.md)).
- `--memory-mode=low --max-memory=1GiB` — reduce Go heap pressure on constrained CI hosts without reducing precision.
- `--rev origin/main` (global query flag) — query another commit, branch, or tag without a checkout (cached in `.gograph/revs/`).

### 2. Repository-Wide and Symbol Queries

```bash
# Repository-wide entry points (no symbol name required)
gograph summary
gograph hotspot --top 10
gograph coupling --internal-only
gograph flow --no-tests

# Symbol-level analysis (supports path/to/file.go:Name and file.go:Receiver.Method)
gograph explore YourSymbol --compact
gograph context YourSymbol
gograph plan YourSymbol
gograph path HandleOrder CommitLedger --mermaid
```

### 3. CLI and Built-In MCP Server

- **CLI:** Reads `.gograph/graph.json` produced by `gograph build .` (or `--precise`). Check freshness with `gograph stale` (exit `0` current, `2` stale, `1` error).
- **MCP (`gograph mcp .`):** Requires no manual build step. It builds an in-memory graph on first use, incrementally reparses changed packages before queries, and writes no disk artifacts unless started with `--persist-refresh`.

> **Local-only analysis:** `gograph` does not execute target code or transmit source or telemetry to external services. It invokes the installed `go` toolchain for module/build context and `--precise` package loading (set `GOPROXY=off GOTOOLCHAIN=local` for offline runs). See [docs/security-model.md](docs/security-model.md) for symlink confinement, `.artifacts.lock` publication, SHA-256 policy markers, and platform atomicity details.

---

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

---

## Key Capabilities

**Call-Graph and Blast-Radius Analysis** — trace direct and transitive callers, callees, paths, interface implementations (CHA/SSA), struct field mutations, HTTP routes, static PostgreSQL queries, and error origins. Export graph slices as text, `--json`, `--files-only`, or `--mermaid`.

**Change-Scoped Test Selection and Coverage Gaps** — run `gograph tests --uncommitted --format go-test --max-depth 3` to print runnable `go test -run` commands for local changes, `gograph untested --exclude-interface-methods` to list untested production paths, and `gograph uncovered --uncommitted --coverprofile cover.out` to report unexecuted changed lines.

**CI Architecture and Policy Gates** — enforce package import layers (`gograph boundaries`), complexity/instability/god-object limits (`gograph gate` via `.gograph.yml`), exported API drift (`gograph api --since origin/main`), and machine-verifiable structural predicates (`gograph validate`). See the [CI Architecture Gates guide](https://gograph.identuum.ai/docs/ci-architecture-gates/).

**Security Flow Triage** — `gograph flow --no-tests` traces interprocedural source-to-sink paths from HTTP requests, decoded JSON, and environment variables to SQL, process execution, filesystem, and outbound HTTP sinks, with configurable sanitizer rules in `gograph/flow.json`.

**Federated Workspaces** — model multiple checked-out repositories through independently fingerprinted repository graphs plus a small deterministic cross-repository overlay. Resolution scopes support alternative fleets such as OSS/CE without merging repository ownership. P0 resolves Go modules, ordinary cross-repository Go calls, and first-class HTTP contracts for workspace-wide status, query, path, and impact analysis. The four read-only workspace MCP tools return the same native result values as CLI `--json`; member refresh and overlay publication remain explicit CLI mutations. [Workspace guide →](docs/workspaces.md)

**Built-In Native MCP Server** — all 66 repository query, analysis, and workflow capabilities have project-MCP equivalents (`gograph_<command>`) plus 4 session lifecycle tools (70 project tools total), and a separate 4-tool workspace MCP server (`gograph workspace mcp`). CLI-only process/host/artifact operations are `build`, `validate`, `doctor`, `gate`, `snapshot`, plugin/hook installation, MCP startup, workspace build/member refresh, `version`, and `help`. [Complete CLI/MCP matrix →](https://gograph.identuum.ai/docs/command-reference/#cli--mcp-transport-matrix)

**Explicit Freshness and Query Contracts** — CLI `--json` envelopes include `gograph.graph-state.v1` (source, freshness, completeness, precision, refresh/persistence outcome); MCP attaches the same state in `_meta.gograph_graph_state`. File-qualified selectors (`path/to/file.go:Name`, `file.go:Receiver.Method`), bounded pagination (`--limit`/`--cursor`), and constant/field resolution are specified in [docs/query-contracts.md](docs/query-contracts.md).

**Agent Compliance Auditing** — optional local session tracking (`gograph session create|end|audit|cleanup`) records whether agents run `plan` before edits and `review` after, grading sessions A–F without logging raw query results. See [llm-wiki/session-scope.md](llm-wiki/session-scope.md).

**Companion projects:** [Scrinium](https://github.com/ozgurcd/scrinium) provides repository-owned, evidence-backed knowledge for coding agents, while [Rulefloor](https://github.com/ozgurcd/rulefloor) binds repository invariants to concrete tests and detects drift. They are independent, optional tools.

---

## Command Reference

Query and composed-analysis commands support `--json`; `--files-only` emits deduplicated paths; `--mermaid` renders `callers`, `callees`, `impact`, `endpoint`, `dependents`, `deps`, `path`, `coupling`, and `diagram` as fenced Mermaid (MCP accepts `mermaid=true`).

| Category | Commands | What it does |
|---|---|---|
| **Indexing** | `build . [--precise] [--strict] [--tags=...] [--exclude-dirs=...] [--memory-mode=low] [--max-memory=1GiB]`, `stale`, `stats` | Parse AST, optionally enrich with CHA/SSA, verify freshness and build health. |
| **Navigation** | `explore`, `query`, `strings`, `callers [--depth N]`, `callees [--depth N]`, `path`, `source`, `node` | Ranked discovery, symbol/string search, call chains, exact AST source extraction. |
| **Context** | `context`, `explain`, `focus`, `endpoint`, `summary` | Bundled structural evidence in a single call. |
| **Change Analysis** | `plan`, `review [--uncommitted]`, `risk`, `impact [--uncommitted\|--since]`, `changes [--git]`, `api --since` | Pre-edit impact planning, post-edit review, blast radius, and API drift. |
| **Testing & Coverage** | `tests [symbol] [--transitive]`, `tests --uncommitted\|--since <ref> --format go-test`, `coverage <TestFunc>`, `untested [--coverprofile]`, `uncovered --uncommitted --coverprofile`, `fixtures`, `mocks` | Direct/transitive test attribution, runnable `go test -run` selection, untested paths, and unexecuted changed lines. |
| **Architecture & CI** | `boundaries [--create]`, `gate [init]`, `check [--since\|--uncommitted]`, `coupling`, `complexity [--no-tests] [--top N]`, `godobj`, `orphans`, `arity`, `snapshot` | Layer import rules (`gograph/boundaries.json`), CI metric gates (`.gograph.yml`), complexity, coupling, and unreachable-symbol sweeps. |
| **Machine Validation** | `version --json`, `validate --repo PATH --binding-json JSON --json`, `whatsnew [--since vX.Y.Z]` | Closed structural and policy predicates (`imports_only_from`, `callers_only_from`, `call_arg_constant`) with tri-state exit codes (`0` pass, `1` fail, `2` cannot evaluate). |
| **Types & Infrastructure** | `implementers`, `interfaces`, `fields`, `embeds`, `constructors`, `literals`, `usages`, `mutate`, `schema`, `routes`, `sql`, `envs`, `errors`, `concurrency [--locks <function>]`, `globals`, `httpcalls`, `deps`, `dependents`, `imports` | Interface satisfaction, mutations, HTTP routes, static PostgreSQL queries, env reads, concurrency (with per-function lock scopes), and package dependencies. |
| **Security & Errors** | `flow [--no-tests] [--source] [--sink]`, `errorflow`, `trace` | Source-to-sink taint leads with `gograph/flow.json` sanitizers; reverse-BFS error tracing. |
| **Diagnostics & Docs** | `doctor [--json]`, `hotspot`, `returnusage`, `skeleton`, `diagram`, `public`, `identity`, `doc <pkg[.Symbol]>`, `wiki`, `session` | Install/graph diagnostics, hotspots, Mermaid architecture diagrams, `go doc` wrapper, `llm-wiki/` generator, and audit sessions. |

> Full command reference with flags and examples: [gograph.identuum.ai/docs/command-reference/](https://gograph.identuum.ai/docs/command-reference/)

<details>
<summary><strong>Architecture Boundary Enforcement (gograph/boundaries.json)</strong></summary>

Generate a baseline from current imports with `gograph boundaries --create`, or define layers in `gograph/boundaries.json`:

```json
{
  "layers": [
    { "name": "domain", "packages": ["internal/domain/**"], "may_import": [] },
    { "name": "handler", "packages": ["internal/handler/**"], "may_import": ["internal/service/**", "internal/domain/**"] }
  ]
}
```

Run `gograph build . --precise && gograph boundaries` in CI; violations exit non-zero with exact file and line numbers. See [docs/architecture-boundaries.md](docs/architecture-boundaries.md).
</details>

<details>
<summary><strong>Security Flow Sanitizer Policy (gograph/flow.json)</strong></summary>

`gograph flow --no-tests` reads `gograph/flow.json` (else `.gograph/flow.json`, or `--config <path>`):

```json
{
  "sanitizers": [
    { "function": "security.CleanPath", "for": ["filesystem"] },
    { "function": "security.ValidateURL", "for": ["outbound_http"] }
  ]
}
```

Omit `for` to trust the return value for every sink kind. A validator returning only `bool` or `error` does not sanitize the original input.
</details>

<details>
<summary><strong>Machine-Readable Structural Validation (gograph validate)</strong></summary>

Validate closed structural predicates without parsing text output:

```bash
gograph validate --repo /work/project --binding-json '{"schema_version":"gograph.binding.v1","predicate":"symbol_exists","subject":{"language":"go","kind":"symbol","id":"example.com/project/internal/auth::Authorize"},"required_precision":"ast"}' --json
```

Supports `symbol_exists`, `package_imports`, `call_edge_exists`, `type_implements`, `imports_only_from`, `callers_only_from`, and `call_arg_constant`. Read-only; exits `0` (pass), `1` (fail), or `2` (cannot evaluate / invalid). See [docs/machine-validation-contract.md](docs/machine-validation-contract.md).
</details>

---

## Built-In MCP Server and AI Agent Integration

Connect any MCP client over local stdio:

```bash
gograph mcp .                                     # in-memory auto-refresh (default)
gograph mcp . --persist-refresh                   # opt in to publishing refreshed .gograph/ artifacts
gograph mcp . --tags=integration                  # retain build tags on every refresh
gograph mcp . --memory-mode=low --max-memory=1GiB # low-memory refresh policy
```

- **Claude Desktop & Claude Code:** Run `gograph add-claude-plugin` to register Claude Desktop MCP, install shared rules, and set up the `PreToolUse` hook guard (`gograph hook-guard` also supports Codex and Antigravity). For Claude Code MCP, run `claude mcp add gograph -- gograph mcp .`, or install via the plugin marketplace (`/plugin marketplace add ozgurcd/gograph` then `/plugin install gograph@gograph`).
- **Official MCP Registry (preview):** MCPB-capable clients can discover `io.github.ozgurcd/gograph` in the [official MCP Registry](https://registry.modelcontextprotocol.io). See [docs/mcp-registry.md](docs/mcp-registry.md) for platform asset selection and limitations.
- **Codex & other agents:** See [docs/codex-integration.md](docs/codex-integration.md) and [docs/coding-agent-usage.md](docs/coding-agent-usage.md).
- **Generated `llm-wiki/`:** Run `gograph wiki` to emit machine-readable overview, architecture, hotspot, route, and per-package markdown pages.

---

## How gograph Complements `gopls`, `golangci-lint` and `ripgrep`

- **`gopls`** — live IDE diagnostics, autocomplete, cursor navigation, and compiler-backed renames.
- **`golangci-lint`** — file- and package-level lint rules, bug checks, and style enforcement.
- **`ripgrep`** — literal text, comment, config, and non-Go file search.
- **`gograph`** — persisted repository call graph for transitive blast radius, change-scoped test selection, CI layer/metric gates, and composed MCP queries.

Full comparison: [gograph.identuum.ai/docs/comparisons/](https://gograph.identuum.ai/docs/comparisons/).

<details>
<summary><strong>Correctness Model and Non-Goals</strong></summary>

- **Default mode (`build .`)** uses Go AST parsing and heuristics; tolerates incomplete or non-compiling code.
- **Precise mode (`build . --precise`)** adds `go/types` CHA/SSA enrichment for compilable repository packages (scoped to repository packages, not the full transitive dependency closure). Failed enrichment warns and records `precise_fallback` (or exits non-zero with `--strict`), while retaining an existing fresh precise artifact on a failed retry.
- **Open CHA and heuristics:** Open interface dispatch is conservative (possible static targets, not runtime proof). Reflection, `unsafe`, plugins, unnamed concrete types, and runtime-generated SQL remain incomplete. Static test attribution and security flow are review aids, not runtime coverage or exploitability proofs.
- **Non-goals:** No multi-language parsing, no AI/model API calls, no embeddings or SaaS backend, no remote telemetry, and no replacement for compiler/type-checker correctness.
- **Security and confinement details:** See [docs/security-model.md](docs/security-model.md).
</details>

---

## Contributing and License

Pull requests welcome—see [CONTRIBUTING.md](CONTRIBUTING.md). `gograph` is MIT-licensed ([LICENSE](LICENSE)).

[![gograph MCP server](https://glama.ai/mcp/servers/ozgurcd/gograph/badges/score.svg)](https://glama.ai/mcp/servers/ozgurcd/gograph)
