---
title: PostgreSQL Static SQL Census v1
type: decision
status: current
updated: 2026-09-02
sources: []
---

# PostgreSQL Static SQL Census v1

CLI `sql` and MCP `gograph_sql` share `gograph.sql.v1`. Static PostgreSQL strings expose verb, read/write/ddl access, table access, and exact/partial/unknown status. Extraction accepts direct literals plus syntax-provable local or same-file package `const`/`var` declarations, straight-line assignments, and bounded string concatenations. Reassignment, conditional ambiguity, runtime builders, and unsupported dynamic SQL are not invented. SQL method argument selection distinguishes standard `*Context` methods and context-first APIs from ordinary query arguments, so SQL-looking bind values are not promoted to statements.

CTEs report the terminal verb; data-modifying CTEs retain write evidence. Filters AND-compose across categories and repeated values OR-compose. Tests stay included by default. Pages use deterministic cursors, 100/200 limits, and a 64 KiB bound measured over actual indented MCP JSON. CLI JSON mirrors pagination; CLI `--files-only` follows all pages for a complete file census, not a statement-row dump.

Parser analysis-cache version 5 is required for these declaration-resolution facts. Rebuild repository graphs after upgrading; older artifacts are rebuild-required rather than silently reusing incomplete per-file SQL records.
