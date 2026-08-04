# LLM Wiki Index

## Operating Model

- `raw/` is the immutable source layer.
- `llm-wiki/` is the maintained knowledge layer.
- `AGENTS.md` plus wiki workflow, schema, and security pages are the agent schema.
- [project.md](project.md) — Project identity, correctness model, analysis modes, and MCP refresh/publication contract.
- [agent-contract.md](agent-contract.md) — Workflow lifecycle, verification, CLI/MCP parity, context fidelity, and auditing.
- [agent-rules.md](agent-rules.md) — Workflow guidelines and rules for development.
- [scrinium-guide.md](scrinium-guide.md) — Reference guide for using Scrinium.
- [mcp-registry.md](mcp-registry.md) — Live v1.5.3 Registry identity, MCPB representation, release invariants, and preview limits.

## Generated Reports (rebuilt dynamically)

- [overview.md](overview.md) — Codebase statistics, hotspots, and package instability.
- [architecture.md](architecture.md) — Package dependency diagram and coupling metrics.
- [hotspots.md](hotspots.md) — High-risk symbols, complex functions, and God Object candidates.
- [env.md](env.md) — Environment variables read dynamically by the application.
- [errors.md](errors.md) — Summary of errors defined or returned across the codebase.
- [concurrency.md](concurrency.md) — Location of goroutines, channels, and waitgroups.
- [api-surface.md](api-surface.md) — List of all exported package symbols and signatures.

## Package Notes

- [packages/README.md](packages/README.md) — Index of package-level implementation notes.
- Executables: [cmd/gograph](packages/cmd-gograph.md), [cmd/mcpb-release](packages/cmd-mcpb-release.md), and [scripts](packages/scripts.md).
- Core infrastructure: [baseline](packages/internal-baseline.md), [buildctx](packages/internal-buildctx.md), [cli](packages/internal-cli.md), [graph](packages/internal-graph.md), [mcp](packages/internal-mcp.md), and [mcpbundle](packages/internal-mcpbundle.md).
- Analysis pipeline: [parser](packages/internal-parser.md), [precise](packages/internal-precise.md), [report](packages/internal-report.md), [rootfind](packages/internal-rootfind.md), [scanner](packages/internal-scanner.md), [search](packages/internal-search.md), [session](packages/internal-session.md), and [wiki](packages/internal-wiki.md).

## Drafts

- `drafts/rules.md` — Proposed protected rules page awaiting review/promotion.

## Sources

- `source-registry.md` — Registry of ingested raw sources and derivative pages.
- `sources/README.md` — Directory guide for source summary pages.
- [sources/SRC-20260614-gograph-legacy-rules.md](sources/SRC-20260614-gograph-legacy-rules.md) — Source summary for legacy rules.
- [sources/SRC-20260614-gograph-legacy-project.md](sources/SRC-20260614-gograph-legacy-project.md) — Source summary for legacy project details.
- [sources/SRC-20260614-gograph-legacy-agent-contract.md](sources/SRC-20260614-gograph-legacy-agent-contract.md) — Source summary for legacy agent contract.
- [sources/SRC-20260614-gograph-legacy-overview.md](sources/SRC-20260614-gograph-legacy-overview.md) — Source summary for legacy codebase overview.
- [sources/SRC-20260614-gograph-legacy-architecture.md](sources/SRC-20260614-gograph-legacy-architecture.md) — Source summary for legacy architecture diagrams.
- [sources/SRC-20260614-gograph-legacy-contributing.md](sources/SRC-20260614-gograph-legacy-contributing.md) — Source summary for legacy contributing guidelines.
- [sources/SRC-20260614-gograph-legacy-errors.md](sources/SRC-20260614-gograph-legacy-errors.md) — Source summary for legacy error analysis report.
- [sources/SRC-20260614-gograph-legacy-env.md](sources/SRC-20260614-gograph-legacy-env.md) — Source summary for legacy environment variable report.
- [sources/SRC-20260614-gograph-legacy-concurrency.md](sources/SRC-20260614-gograph-legacy-concurrency.md) — Source summary for legacy concurrency primitives report.
- [sources/SRC-20260614-gograph-legacy-hotspots.md](sources/SRC-20260614-gograph-legacy-hotspots.md) — Source summary for legacy hotspots report.
- [sources/SRC-20260614-gograph-legacy-api-surface.md](sources/SRC-20260614-gograph-legacy-api-surface.md) — Source summary for legacy API surface report.
- [sources/SRC-20260712-mcp-registry-spec.md](sources/SRC-20260712-mcp-registry-spec.md) — Pinned official Registry publishing specification.
- [sources/SRC-20260712-mcpb-spec.md](sources/SRC-20260712-mcpb-spec.md) — Pinned official MCPB 2.1.2 specification.
- [sources/SRC-20260803-gograph-live-distribution.md](sources/SRC-20260803-gograph-live-distribution.md) — Live v1.5.3 GitHub release and official MCP Registry verification.

## Workflows

- [contributing.md](contributing.md) — Code-first capability parity, package boundaries, verification, and Scrinium workflow.
- `workflows/ingest.md` — How to process raw sources into the wiki.
- `workflows/query.md` — How to answer questions from the wiki and file durable answers.
- `workflows/lint.md` — How to health-check the wiki.

## Schemas and Security

- `schemas/page-schemas.md` — Page schemas for maintained wiki pages.
- `security/untrusted-sources.md` — Rules for treating raw sources as untrusted evidence.
- [security/path-traversal-prevention.md](security/path-traversal-prevention.md) — Path traversal prevention patterns: `isSafePathSegment`, `sanitizeGitRef`, config-path guards, and session log redaction.
- [security/flow-analysis.md](security/flow-analysis.md) — Security-flow sources, sinks, sanitizer policy, and analysis limits.
- [prompt-templates.md](prompt-templates.md) — Templates for structured prompt components.

## Logs

- `log.md` — Canonical chronological wiki log.
