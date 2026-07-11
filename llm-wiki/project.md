---
title: Project Identity and Architecture
type: project
status: current
updated: 2026-07-11
sources:
  - SRC-20260614-gograph-legacy-project
---

# Project: gograph

`gograph` is a local Go AST indexer for coding agents. CLI and MCP share 61 query, analysis, and workflow capabilities; MCP has 65 endpoints including four session tools. CLI reads persisted `.gograph/graph.json`; MCP refreshes source analysis after edits.

Default builds tolerate broken code. Precise builds attempt type checking plus CHA/SSA and retain the AST graph on failure. `flow` is path-insensitive security analysis with up to 16 call-site frames and query-time sanitizer policy; findings are review leads, not proof.

Non-goals include other languages, model APIs, SaaS, remote telemetry, and target binary/test execution.
