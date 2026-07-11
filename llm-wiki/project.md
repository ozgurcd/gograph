---
title: Project Identity and Architecture
type: project
status: current
updated: 2026-07-11
sources:
  - SRC-20260614-gograph-legacy-project
---

# Project: gograph

`gograph` is a local Go AST indexer for coding agents. CLI and MCP share 60 query/analysis/workflow capabilities; MCP has 64 endpoints including four session tools. CLI reads persisted `.gograph/graph.json`; MCP refreshes source analysis after edits.

Default builds tolerate broken code. Precise builds attempt type checking plus CHA/SSA and retain the AST graph on failure. Precise mode and `doc` invoke the configured Go toolchain and may use its module/cache/network policy.

Non-goals: other languages, model APIs, embeddings, SaaS, remote telemetry, or executing target binaries/tests. Audit telemetry is local. Heuristic results aid navigation; they are not compiler proofs.
