---
title: PostgreSQL Static SQL Census v1
type: decision
status: current
updated: 2026-09-01
sources: []
---

# PostgreSQL Static SQL Census v1

CLI `sql` and MCP `gograph_sql` share `gograph.sql.v1`. Static PostgreSQL literals expose verb, read/write/ddl access, table access, and exact/partial/unknown status. CTEs report the terminal verb; data-modifying CTEs retain write evidence. Dynamic SQL is not invented. Filters AND-compose across categories and repeated values OR-compose. Tests stay included by default. Pages use deterministic cursors, 100/200 limits, and a 64 KiB bound measured over actual indented MCP JSON. CLI JSON mirrors pagination; `--files-only` follows all pages. Legacy graphs classify at query time.
