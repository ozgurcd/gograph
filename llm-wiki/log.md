# LLM Wiki Log

This is the canonical chronological log for the project LLM Wiki. Keep entries append-only and parseable.

## Format

Use this heading pattern for every event:

```markdown
## [YYYY-MM-DD] <event-type> | <short title>
```

Event types include `session`, `ingest`, `query`, `lint`, `decision`, and `maintenance`.

## Entries

## [2026-06-13] ingest | Ingest legacy rules.md

- Ingested raw source `raw/inbox/legacy-llm-wiki/rules.md` under ID `SRC-20260614-gograph-legacy-rules`.
- Created source summary page `sources/SRC-20260614-gograph-legacy-rules.md`.
- Created draft `drafts/rules.md` to propose updating active project rules.
- Updated `index.md` and `source-registry.md` with the new source info.

## [2026-06-13] ingest | Ingest legacy project.md

- Ingested raw source `raw/inbox/legacy-llm-wiki/project.md` under ID `SRC-20260614-gograph-legacy-project`.
- Created source summary page `sources/SRC-20260614-gograph-legacy-project.md`.
- Created active project documentation at `project.md`.
- Updated `index.md` and `source-registry.md` with the new source details.

## [2026-06-13] ingest | Ingest legacy agent-contract.md

- Ingested raw source `raw/inbox/legacy-llm-wiki/agent-contract.md` under ID `SRC-20260614-gograph-legacy-agent-contract`.
- Created source summary page `sources/SRC-20260614-gograph-legacy-agent-contract.md`.
- Created active workflow document at `agent-contract.md`.
- Updated `index.md` and `source-registry.md` with the new source details.

## [2026-06-13] ingest | Ingest legacy overview, architecture, and contributing

- Ingested raw sources `overview.md`, `architecture.md`, and `contributing.md` under `raw/inbox/legacy-llm-wiki/`.
- Assigned IDs: `SRC-20260614-gograph-legacy-overview`, `SRC-20260614-gograph-legacy-architecture`, and `SRC-20260614-gograph-legacy-contributing`.
- Created source summaries under `sources/`.
- Created active workflow document at `contributing.md`.
- Regenerated active dynamic pages (`overview.md`, `architecture.md`, etc.) by building the precision graph index and calling `gograph wiki`.
- Updated `index.md` and `source-registry.md` with all details.

## [2026-06-13] maintenance | LLM Wiki lint corrections

- Added a Drafts section to `index.md` linking to `drafts/rules.md`.
- Created `packages/README.md` to serve as a consolidated index for auto-generated package-level reports.
- Linked `packages/README.md` under a new Package Notes section in `index.md`.
- Added guide type frontmatter to `sources/README.md` to resolve linter metadata flags.
- Verified and touched `source-registry.md` to ensure Scrinium session registry consistency.

## [2026-06-13] ingest | Ingest legacy errors.md

- Ingested raw source `errors.md` from `raw/inbox/legacy-llm-wiki/` under ID `SRC-20260614-gograph-legacy-errors`.
- Created source summary page at `sources/SRC-20260614-gograph-legacy-errors.md`.
- Updated `index.md` and `source-registry.md` with the new source references.

## [2026-06-13] ingest | Ingest legacy env.md

- Ingested raw source `env.md` from `raw/inbox/legacy-llm-wiki/` under ID `SRC-20260614-gograph-legacy-env`.
- Created source summary page at `sources/SRC-20260614-gograph-legacy-env.md`.
- Updated `index.md` and `source-registry.md` with the new source references.

## [2026-06-13] ingest | Ingest legacy concurrency.md

- Ingested raw source `concurrency.md` from `raw/inbox/legacy-llm-wiki/` under ID `SRC-20260614-gograph-legacy-concurrency`.
- Created source summary page at `sources/SRC-20260614-gograph-legacy-concurrency.md`.
- Updated `index.md` and `source-registry.md` with the new source references.

## [2026-06-13] ingest | Ingest legacy hotspots.md

- Ingested raw source `hotspots.md` from `raw/inbox/legacy-llm-wiki/` under ID `SRC-20260614-gograph-legacy-hotspots`.
- Created source summary page at `sources/SRC-20260614-gograph-legacy-hotspots.md`.
- Updated `index.md` and `source-registry.md` with the new source references.

## [2026-06-13] ingest | Ingest legacy api-surface.md

- Ingested raw source `api-surface.md` from `raw/inbox/legacy-llm-wiki/` under ID `SRC-20260614-gograph-legacy-api-surface`.
- Created source summary page at `sources/SRC-20260614-gograph-legacy-api-surface.md`.
- Updated `index.md` and `source-registry.md` with the new source references.
## 2026-06-14 — Security Disclosure AT-002 Analysis

Received a responsible-disclosure notice (scan date 2026-06-10) identifying three goroutines launched without `recover()` (rule AT-002):

- `internal/parser/testdata/concurrent/concurrent.go:28` — goroutine in `Worker.Run`
- `internal/parser/testdata/concurrent/concurrent.go:38` — goroutine in `Start`
- `internal/precise/mutations.go:263` — goroutine in `ssaAllFunctions`

**Finding status:** VALID. The committed code on GitHub HEAD lacked `defer recover()` wrappers in all three goroutines. Working-tree already contained the fixes (uncommitted changes). Verified via `git diff HEAD`.

**Fixes present in working tree (not yet committed):**
- `internal/parser/testdata/concurrent/concurrent.go` — added `defer func() { _ = recover() }()` to both goroutines
- `internal/precise/mutations.go` — added `defer func() { _ = recover() }()` to the `ssaAllFunctions` generator goroutine

**Verified:** `go build ./...` passes, `go test ./internal/precise/... ./internal/parser/...` passes.

**Action required:** Commit and push the working-tree fixes to remediate the public GitHub exposure.

## [2026-06-14] session | Security hardening: path traversal prevention

- Reviewed uncommitted changes from an external coding agent across 5 files.
- Confirmed correctness and security of all changes through 3 iterative review rounds.
- **`internal/search/search.go`** — Added `isSafePathSegment` helper and applied it in `Callers`, `Callees`, and `Source` to guard against poisoned `graph.json` path traversal.
- **`internal/search/boundaries.go`** — Added empty/`..`/backslash validation in `Boundaries` and `CreateBoundaries` before any file I/O on the config path.
- **`internal/mcp/server.go`** — Extracted `sanitizeGitRef` function; wired into `gograph_api` handler. Removed dead code: `sanitizePath` (unreachable) and duplicate `safePathSegment` var.
- **`internal/session/session.go`** — Added `redactArgs` to sanitize sensitive path arguments before writing to session audit JSONL log.
- **`internal/mcp/server_test.go`** — Cleaned up temp file handling; updated error string assertion for new message.
- **`internal/search/advanced_features_test.go`** — Fixed hardcoded line numbers that drifted when `isSafePathSegment` was prepended to `search.go`.
- All packages build clean. All tests pass.
- Committed as `1f62055`: `security: add path traversal prevention and fix dead code`.
- Created `security/path-traversal-prevention.md` with implementation invariants.
- Updated `index.md` to link the new security page.
- Updated `RELEASE_NOTES.md` with a v1.4.84 entry.

## [2026-06-17] session | Working directory independence for MCP tools and complexity calculations

- Identified and fixed path resolution issues where cyclomatic complexity calculations returned `-1` / `UNKNOWN` because of using relative paths from the graph without resolving against the project root.
- Modified `internal/search/complexity.go` to prepend `g.Root` to `sym.File` in `Complexity` calculations.
- Modified `internal/mcp/server.go` to use `g.Root` instead of relative/dynamic path resolution (`.` and `filepath.Abs(".")`) in tools `gograph_source`, `gograph_changes`, `gograph_context`, `gograph_plan`, `gograph_stale`, and `gograph_impact` to guarantee correct operation when the MCP server runs from a non-root directory.
- Regenerated the LLM wiki pages via `gograph wiki`.
- Verified that all unit, integration, lint, and security checks pass successfully.

## [2026-06-17] session | Refine orphan detection logic to exclude internal package exports and tests

- Modified [internal/search/advanced.go](file:///Users/odemir/Development/2025-11/identuum/gograph/internal/search/advanced.go) to add `isInternal` helper and exclude exported symbols in `internal/` packages from being roots (unless called).
- Handled test files by treating `Test...`, `Benchmark...`, and `Fuzz...` functions as roots, and excluding symbols defined in `_test.go` files from reported orphans.
- Added [internal/search/orphans_test.go](file:///Users/odemir/Development/2025-11/identuum/gograph/internal/search/orphans_test.go) with unit tests for the refined logic.
- Removed temporary manual verification functions and ran full automated test/fuzz/linter verification.
- Regenerated the LLM wiki pages via `gograph wiki`.
- 2026-07-06 — Fixed GitHub issue #25: gograph build now errors with no Go files in <path> when the scanner finds zero Go files, before precise analysis or writing .gograph artifacts. Added TestBuildCommandRejectsDirectoryWithoutGoFiles and verified go test ./... -count=1.
- 2026-07-06 — Implemented GitHub issue #24 enhancement: gograph build now appends .gograph/ to the enclosing Git repository root .gitignore while keeping graph artifacts under the requested build target, with fallback to target .gitignore outside Git. Added TestBuildCommandWritesGitignoreAtRepositoryRoot and updated docs/coding-agent-usage.md.
- 2026-07-06 — Updated app help and user-facing documentation for GitHub issues #24 and #25. CLI help/capabilities, command-reference docs, getting-started docs, coding-agent usage docs, generated docs-site pages, and RELEASE_NOTES now describe repository-root .gitignore updates and empty-target build failures.

- 2026-07-06 — Verified documentation/help updates for GitHub issues #24 and #25. CLI help/capabilities, command-reference docs, getting-started docs, coding-agent usage docs, generated docs-site pages, and RELEASE_NOTES describe repository-root .gitignore updates and empty-target build failures.
- 2026-07-06 — Extended the documentation/help update for issues #24 and #25 to README.md and the docs-site home page source/public output, so the main project overview now matches CLI help and command-reference behavior.

## [2026-07-11] maintenance | Graph correctness and reliability remediation

- Removed variable-as-call edges, deterministic duplicates, and unconstrained CHA function-value expansion.
- Unified root/scanner behavior; added atomic, completeness-aware graph builds and qualified precise type identities.
- Shared Git baselines across CLI/MCP; made gates fail on stale graphs and repaired route/test policy checks.
- Serialized MCP graph refresh, replaced global stdout capture, corrected tool annotations, tightened sync/error extractors, and expanded CI verification.

- Verification: full unit and race suites, vet, staticcheck, golangci-lint, govulncheck, gofmt, and go.mod tidy checks passed.

- Follow-up: zero-parse and partial-build cases were confirmed as separately runnable tests; moved-repository graph roots are re-anchored to the loaded index.

## [2026-07-11] session | CLI, MCP, and documentation contract verification

- Exercised every CLI feature and all 60 non-session MCP tools; verified four session endpoints separately and exact CLI/MCP query-analysis command parity.
- Fixed orphan-summary semantics, MCP freshness/rooting/options/annotations, precise-graph preservation, boundary creation parity, mutation type disambiguation, and partial plugin-install failure reporting.
- Reconciled README, release notes, coding-agent guide, live help/capabilities, plugin metadata, and docs-site content; Hugo now builds without deprecation warnings.
- Raised the build floor to Go 1.26.5 after the SBOM scan identified vulnerable Go 1.26.4 stdlib artifacts.
- Verified 102/102 precise parses; unit/E2E/contract/race tests, vet, staticcheck, golangci-lint, govulncheck, grype, tidy diff, and docs build pass.
## [2026-07-11] maintenance | Normalize verification log formatting

- Removed the extra trailing blank line left by the previous governed append; no project facts changed.
