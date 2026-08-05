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
## [2026-07-11] maintenance | Fix Hugo CI and action deprecations

- Diagnosed Actions run 29145461614: Hugo 0.147.6 could not evaluate the newer Language Direction/Locale template APIs.
- Pinned Hugo 0.164.0 and upgraded checkout, setup-go, Pages upload/deploy, and GoReleaser actions to Node 24 major releases.
- Verified a warning-free 32-page Hugo build, full Go tests, YAML parsing, and clean diff checks.
## [2026-07-11] maintenance | Fix cross-platform plugin installer E2E

- Diagnosed CI run 29145724492: the plugin installer test hardcoded the macOS Claude Desktop path on Ubuntu.
- Isolated HOME, USERPROFILE, and APPDATA and selected the expected Desktop config path by GOOS.
- Focused, full, race, vet, staticcheck, and golangci-lint verification passed.
## 2026-07-11 — Security flow analysis

Implemented CLI `gograph flow` and MCP `gograph_flow`; persisted AST flow facts, interprocedural query-time propagation, sink-scoped sanitizer policy, config containment, tests, and public documentation. Updated project capability counts to 61 CLI-equivalent tools and 65 MCP endpoints. Added `security/flow-analysis.md`.
Flow self-analysis found and fixed external/local same-name resolution, multi-return contamination, and cross-caller return leakage. Indexed return values plus bounded 16-frame call-site matching reduced self-scan findings from 160 fabricated/noisy paths to 53 coherent review leads.
Verification: full tests and race tests passed; go vet, staticcheck, golangci-lint, go mod tidy -diff, govulncheck, grype, Hugo, make build, precise self-build, live text/JSON flow queries, and MCP/CLI parity contracts passed. Final precise index parsed 106/106 files and stored 1,013 flow functions. Policy check had zero errors and 74 pre-existing boundary warnings.
Normalized the security-flow page link into the index's Schemas and Security section after the initial governed append placed it below Logs.
## [2026-07-12] session | Preserve complete precise interface dispatch

- Replaced single-target interface enrichment with deterministic parallel call edges: one source-provenance-preserving edge per valid named in-repository CHA target.
- Added exact source columns and traversal-only synthetic wrapper-to-declared-method forwarding for promoted methods; presentation and source metrics deduplicate call expressions while traversal, reachability, impact, and orphan analysis retain every target.
- Added Interface.Method caller resolution through precise implementer edges, including embedded interfaces and promoted methods, with consistent bare, concrete dot, fully-qualified ID, exact, CLI, and MCP behavior.
- Persisted `ast`, `precise`, and `precise_fallback` status, exposed it through CLI/MCP stats, and retained additive graph-v2 wire compatibility with explicit older-reader caveats.
- Made MCP refresh precision-aware: source edits recompute the requested mode, later precise artifact publications are adopted even across overlapping build timestamps, AST-only artifacts cannot downgrade precise sessions, and fallback is surfaced as an analysis error.
- Updated help, capabilities, README, release notes, architectural/coding-agent guidance, and docs-site content with the representation, compatibility contract, and remaining CHA/static-analysis limits.
- Added focused real-build, search, graph, CLI, MCP-handler, refresh, reachability, promoted-method, determinism, and precision-status regressions.
- Verification passed: formatting, build, `go mod tidy -diff`, module verification, full unit and race suites, vet, staticcheck, golangci-lint, govulncheck, grype, coverage, and both fuzz targets. The precise self-build completed 115/115 parsed files with `precision: precise`; Hugo produced 32 pages and wiki generation produced 21 pages.

## [2026-07-12] maintenance | Normalize interface-dispatch log formatting

- Removed the extra trailing blank line left by the governed session append; no project facts changed.
## [2026-07-12] maintenance | Correct onboarding and product positioning

- Corrected all maintained Go-install examples to the executable package at `github.com/ozgurcd/gograph/cmd/gograph`; a build through that import path produced a runnable `gograph version vdev` binary.
- Replaced guessed-symbol Quick Start flows with repository-wide `build`, `stats`, `summary`, `hotspot`, and `flow` commands, followed by clearly labeled project-specific symbol placeholders.
- Removed unsupported absolute accuracy, hallucination-rate, and fixed token-savings claims from current site, plugin, hook, benchmark, and agent guidance. Historical snapshot counts remain labeled as point-in-time output comparisons with different semantics.
- Reframed gograph as complementary to `gopls` and targeted text search. Durable agent guidance now requires cross-checking AST/fallback, ambiguous, or known-missing results and forbids treating zero callers/orphans as proof.
- Removed the benchmark harness's fabricated 1,250-token gopls penalty; it now reports only observed latency and raw payload-size estimates with an explicit non-equivalence warning.
- Regenerated the tracked 32-page Hugo output. The independent render contains the corrected install path and no forbidden claim patterns.
- Updated GitHub repository homepage metadata from the repository URL to `https://gograph.identuum.ai` and verified it through GitHub.
- Verification passed: gofmt, module verify/tidy, build, unit tests, race tests, vet, staticcheck, golangci-lint, govulncheck, JSON validation, Hugo builds, diff checks, and live execution of every guaranteed Quick Start command.
- No commit or push was made. The GitHub Pages deployment will reflect the corrected source after the working tree is intentionally committed and pushed to `main`.
## [2026-07-12] maintenance | Normalize comparison wording

- Replaced the final “token cost comparison” table label with neutral output-shape and analysis-caveat language.
- Reworded the historical case study's composite-command section to describe combined evidence categories without claiming a fixed number of replaced calls.
- Regenerated the 32-page Hugo output and a clean independent render after the wording changes.
## [2026-07-12] maintenance | Re-audit private codebase findings

- Re-audited all eight findings and five future risks in `docs/private/gograph-findings.md` against v1.4.96 and remediation commit `7a4592a`.
- Current status: five findings resolved and three partially resolved; four future risks resolved in core scope and precise implementer fallback remains partial.
- Confirmed remaining correctness gaps: selector-name collisions in AST callback recovery, package-insensitive changed-route handler matching, incomplete graphs accepted by `gate`, and package-insensitive legacy/AST implementer fallback.
- Recorded optional hardening for MCP refresh throughput, scanner error propagation, extractor coverage, baseline cancellation tests, and release verification.
- Updated `project.md` so durable guidance discloses these limitations.
- Verification passed: `go test ./... -count=1`, focused graph/parser/MCP/search/baseline tests, race tests for CLI/MCP, and clean `gofmt -l .`.

## [2026-07-12] maintenance | Normalize findings re-audit log formatting

- Consumed the extra trailing blank line left by the previous governed append as the separator for this maintenance entry; no project facts changed.

## 2026-07-12 — Official MCP Registry and MCPB publication

- Added standards-compliant `io.github.ozgurcd/gograph` Registry metadata and deterministic binary MCPBs for darwin/linux/windows on amd64/arm64.
- Pinned and vendored Registry 2025-12-11 and MCPB 0.4 schemas, with strict layout, target, version, hash, argv, and native MCP contract validation.
- Added a verified GoReleaser pipeline that preserves ordinary archives and Homebrew, then publishes through least-privilege GitHub OIDC using pinned `mcp-publisher v1.7.9`.
- Documented Registry preview CPU-selection limitations and retained the local stdio/no-hosted-telemetry model.

## [2026-07-12] maintenance | Close Registry publication log formatting

- Consumed the governed append's trailing blank line as this entry's separator; no release facts changed.
## [2026-07-13] release | Publish official MCP Registry entry

- Published `io.github.ozgurcd/gograph` 1.5.0 as an active/latest official Registry entry through GitHub OIDC.
- Created immutable tag `v1.5.0` at `e4f96315ec4edb805dddbdd584fffbc022f18c6d` and GitHub release `https://github.com/ozgurcd/gograph/releases/tag/v1.5.0`.
- The initial tag run failed before publication because release tests lacked `bin/gograph`; commit `4299e2806a87c43343584f941159a413ade156d3` added that prerequisite and a safe existing-tag dispatch path. The tag was not moved or recreated.
- Successful run `29242849952` verified the tag, published ordinary assets and six MCPBs, reconciled Homebrew, authenticated with pinned `mcp-publisher v1.7.9`, and verified Registry activation.
- Independent verification downloaded all 14 assets, matched all 12 checksummed artifacts plus GitHub digests for `server.json` and `checksums.txt`, initialized the downloaded native MCPB with 65 tools, and confirmed the v1.5.0 Homebrew formula.
## [2026-07-13] release | Restore automatic patch release workflow

- Restored the maintainer UX to `git commit` followed by `make release`, with the next patch version selected automatically and no `VERSION` argument.
- Added a fail-closed release coordinator that owns exact metadata changes, validates official remote and immutable state, creates an annotated tag, and atomically pushes the captured commit plus that tag.
- Added dry-run, compare-and-swap rollback, same-version resume/no-op behavior, and focused regression coverage for failure and recovery paths.
- Expanded the pre-tag gate with module verification/tidiness, `go vet`, and a pinned GoReleaser v2.17.0 non-publishing snapshot alongside the full tests, race tests, lint, security, MCPB, smoke, and documentation checks.
- Full `make release-verify MCPB_OUTPUT=.release-work/full-verify` passed without creating a commit, tag, push, GitHub release, or Registry version.

## [2026-07-13] maintenance | Close automatic release log formatting

- Consumed the governed append's trailing blank line as this entry's separator; no release facts changed.

## [2026-07-13] maintenance | Allow releases from fast-forward working branches

- Corrected `make release` so any clean attached branch whose HEAD descends from fetched official `main` can be the release source; local `main` may remain stale and untouched.
- Kept remote `main` as the fixed publication target and atomically pushes only the captured verified commit plus the new tag; the source branch ref is never pushed by the coordinator.
- Added fail-closed validation for a single official fetch URL and single effective official push URL, exact metadata modes, detached HEAD, branch/HEAD races, preflight and concurrent remote divergence, feature-branch retries, and missing-tag recovery when remote `main` already contains the release commit.
- Verified the repository's current topology (`agent/mcp-registry-publishing` ahead of `origin/main`) and confirmed the final full non-publishing release gate passes, including unit/race tests, vet, lint/static/security checks, six MCPBs, MCP smoke, docs, and GoReleaser snapshot.
- No release commit, version tag, push, GitHub release, or Registry publication was created during this correction.
## [2026-07-13] maintenance | Fix hook-guard alternation parsing

- Fixed GitHub issue #27: basic-grep `\|` alternations now preserve every identifier while extended grep/ripgrep, literal pipes, fixed strings, and shell pipelines retain their real semantics.
- Added a deterministic direct-command lexer/classifier with known option arity, ordered selectors, case toggles, and descriptor-aware redirection; unsupported dynamic syntax still fails open.
- Added unit/E2E regressions for the reporter's command and updated maintained docs/release notes.
- Verification passed: precise 133-file build, graph checks, full unit/race/lint/static/security suites, coverage, fuzzing, and Hugo.

## [2026-07-13] maintenance | Close hook-guard log formatting

- Consumed the governed append's trailing blank line as this entry's separator; no project facts changed.

## [2026-07-14] maintenance | Merge hook-guard alternation fix

- Merged PR #28 at `f2d8de75c6ba16d72ffbccad5657a19e7650fdd9`; the `Closes #27` trailer closed the reporter's issue and a follow-up comment thanked @serdardalgic.
- Final PR CI run 29308791305 passed unit/E2E tests, race, vet, static analysis, vulnerability checks, all six MCPBs, native MCP smoke, and documentation.
- Corrected ordinary CI to verify candidate MCPBs against ephemeral metadata rendered from those exact artifacts. Release-time `mcpb-verify`, committed `server.json`, byte comparisons, and immutable v1.5.1 hashes remain unchanged and fail closed.

## 2026-07-15 — Build-context-aware file selection (issue #30)

- Added a shared cmd/go-resolved build-context snapshot for GOENV/GOFLAGS tags, GOOS/GOARCH, cgo, compiler, tool tags, and release tags.
- Scanner selection now uses go/build.Context.ImportDir; inactive constrained/platform/cgo files contribute no graph records, while active constrained tests and invalid files remain available to AST analysis.
- Precise loading receives the same pinned environment and build tags; the coverage invariant and repository-wide fallback remain unchanged.
- Regressions cover the ignored package-main tool, MCP plan/review/check, custom and legacy tags, platform files, cgo, tests, explicit ignore activation, and nested-module fallback.

## [2026-07-18] maintenance | Harden build-context-aware file selection

- Completed GitHub issue #30 with one cmd/go-resolved configuration shared by AST scanning and precise package loading.
- Excluded inactive modern and legacy build constraints, GOOS/GOARCH and cgo files, generated sources, wildcard-excluded directories, Git-ignored paths, and Go 1.26 go.mod ignore paths while retaining active constrained tests.
- Added selected-file and module-selection freshness fingerprints so MCP/CLI refresh detects additions, deletions, active/inactive transitions, custom-tag changes, nested-module boundaries, and module identity changes.
- Preserved precise coverage reconciliation and repository-wide fallback for genuine package-loader gaps.
- Aligned module/GOPATH symbol identity and canonical path handling across explicit-root symlinks, module-subdirectory symlinks, invocation PWD changes, and symlinked go.mod files.
- Exact v1.5.2 reproduction indexed the ignored package-main tool and fell back; the patched build indexed only the active library, reached precise mode, and returned no main symbol.
- Focused regressions, full tests, race checks, lint/static/security checks, coverage, fuzzing, precise self-review, and the release pipeline are the required publication evidence.

## [2026-07-18] maintenance | Close build-context log formatting

- Consumed the governed append's trailing blank line as this entry's separator; no project facts changed.
## [2026-08-03] maintenance | Complete strict stale exit contract

- Reconciled PR #26 with current `main` without rebasing or discarding newer build-context freshness behavior.
- Made `gograph stale` return `0` when current, `2` when stale, and `1` for operational or JSON serialization errors in both text and JSON modes.
- Preserved JSON output failure precedence and classified the expected stale result as successful session telemetry.
- Added regression coverage for text/JSON exit codes, build-context-only staleness, JSON failure precedence, subdirectory behavior, and session audit accounting.
- Updated CLI help, user documentation, release notes, and the durable agent workflow contract, including safe `set -e` handling.
- Verification passed: focused and full tests, race detector, vet, Staticcheck, GolangCI-Lint, govulncheck, coverage, fuzzing, Hugo, precise gograph review, and the CI-equivalent MCPB build/verify/smoke workflow.
## [2026-08-03] maintenance | Add opt-in MCP refresh artifact persistence

- Added `gograph mcp [path] --persist-refresh` to publish the latest successful startup build or stale MCP refresh to `.gograph`; default and fixed plugin/MCPB launches remain in-memory-only.
- Unified current CLI and MCP artifact writers behind a cross-process lock, freshness and precision guards, previous-graph baseline derivation, complete graph/report staging, and graph-last commit-marker publication.
- Preserved authoritative manual build semantics while preventing failed precise retries and background AST/fallback refreshes from replacing a fresh precise artifact.
- Made startup publication failures fatal and later failures visible and retryable from the valid pending in-memory graph without a forced rebuild.
- Updated CLI/MCP contracts, privacy and integration documentation, release notes, and durable project guidance. The behavior is explicitly latest-state publication, not a branch-indexed cache, and MCP persistence does not edit `.gitignore`.
- Verification passed: full unit and race suites, fuzzing, build, vet, Staticcheck, GolangCI-Lint, module verification/tidiness, coverage, docs build, Windows cross-compilation, and govulncheck; independent API, test, and safety reviews found no blockers.
- Reframed GitHub issue #32 with the implemented scope, acceptance criteria, non-goals, and crash-window limitation, and posted a verification comment. The issue remains open because no commit or push was made.

## [2026-08-03] maintenance | Normalize MCP persistence log formatting

- Consumed the governed append's trailing blank line as this entry's separator; no project or issue facts changed.

## [2026-08-04] maintenance | Code-first CLI/MCP parity and refresh publication

- Implemented opt-in `gograph mcp [path] --persist-refresh` publication with a cross-process `.gograph/.artifacts.lock`, graph-last bundle ordering, stale-candidate rejection, precision retention, baseline preservation, retry behavior, and a two-process contention regression. Default MCP and fixed Registry/plugin launches remain in-memory-only.
- Reconciled CLI and MCP behavior from current handlers: exact/qualified context resolution, duplicate-name plan contexts, Mermaid parity for eight graph tools, shared context/errorflow payloads, strict JSON/empty/error contracts, mutually exclusive command-specific output modes, integer-only MCP numeric parameters, strict CLI arity, and Windows-safe installer tests.
- Reconciled maintained documentation, integrations, generated Hugo output, curated wiki pages, and all 24 generated wiki pages against the final precise graph and live v1.5.3 distribution evidence. Documentation now records the retained lock file and qualifies same-directory rename guarantees on non-Unix platforms.
- Verification passed: precise build (152 files, 17 packages, 1636 symbols, 15440 calls), full unit tests, repository race tests, 65.6% coverage run, fuzz targets, go vet, staticcheck, golangci-lint, module verify/tidy, docs check, govulncheck (no reachable vulnerabilities), Windows amd64 CLI/release cross-builds, Linux amd64 CLI cross-build, and git diff hygiene.
- Gograph audit session `docsparity_20260803_231508` completed with plan and review rules satisfied (grade C; 76.8%, reduced by command composability).

- Index maintenance: added direct links for every generated package page after the final graph regeneration.

- Scrinium maintenance completed with all session requirements satisfied and append-only log hygiene restored.
- 2026-08-04: Replaced legacy traversal guards with source-policy v1 and os.Root-based confinement for scanner/parser/query reads, Go module/workspace metadata and local source-tree preflights, persisted graphs/baselines, project configs, artifacts, and repository mutations. Added workspace-member and symlink-root alias regressions; credited private disclosure by Dostxodjayev Abdullox (@squeeze440).
- 2026-08-04: Extended the precise/doc workspace source preflight to the workspace root (including workspace-vendor source) plus every confined member, added the linked workspace-vendor regression, and aligned CLI/MCP/help/site contracts with the code's metadata, scanner, and toolchain-preflight sequencing.
- 2026-08-04 — Hardened release verification after Grype's repository-wide scan treated seven stale ignored executables as current inputs and CLI integration tests reused an obsolete `bin/gograph-test`. `make test` now disables Go's test cache, builds a current native binary, and scans only `go.mod` plus that binary; CLI subprocess tests build one version-marked executable in a cleaned OS temp directory and use isolated fixtures. The release gate requires the exact six fresh GoReleaser archives and Grype-scans each archive, while CI repeats both gates with pinned Grype v0.116.1. Added fail-closed Make/workflow/archive-set contracts and updated maintainer/public documentation. Verified a precise 163-file self-index and uncommitted gograph review; focused CLI/release tests, cache-disabled unit and race suites, lint, staticcheck, govulncheck, exact native scans, and Hugo all pass. The unprepared `release-verify` correctly stopped at immutable v1.5.3 MCPB hash mismatch; the clean-commit `release-dry-run` is the final non-publishing end-to-end gate.
## 2026-08-04 — Issue #33 Windows subdirectory call queries

- Confirmed the v1.5.3 failure was caused by `Callers` and `Callees` rejecting every call-site path containing a backslash; on Windows this discarded all graph edges located below the build root even when `CalleeSymbolID` and `CallerSymbolID` were correct.
- The v1.5.4 repository-confinement change had already removed that platform-invalid filter. Made the intended contract explicit by centralizing optional call-site snippet reads: confined source-read failure cannot suppress a structurally valid result.
- Added a Windows-style `sub\\sub.go` regression covering fuzzy, exact, package-qualified, and fully-qualified caller/callee queries, plus an `impact` assertion limited to the actual transitive callers. CLI and MCP inherit the behavior from the shared search implementation.
- Verified targeted/current search, CLI, and MCP tests; search race tests; the full uncached unit/race/lint/static/vulnerability gate; module verification/tidiness; vet; coverage; fuzzing; documentation rendering; precise graph review; and Windows cross-compilation.
- 2026-08-04: Added a reproducible structural-evidence package: a controlled precise-analysis fixture and declarative suite, a tested runner with raw-output and digest provenance, the checked-in v1.5.5 result, a no-install Hugo demo, evidence/methodology documentation, and a reusable 1280x640 social/OG preview card. Verified all declared gograph evidence, full `go test ./...`, Hugo rendering, JavaScript syntax, identical report/demo data, image constraints, and a precise uncommitted gograph review. No fixed token, universal speed, hallucination-rate, or end-to-end agent-success claim was introduced.
- 2026-08-04: After publishing the evidence/demo work, GitHub CI identified staticcheck SA4006 in the new benchmark runner. Removed the dead exit-code assignment, regenerated the benchmark/demo JSON so runner provenance remains exact, and revalidated script tests, staticcheck, Hugo, JavaScript syntax, and diff hygiene before the follow-up push.
- 2026-08-04: A subsequent GitHub CI run passed staticcheck and then exposed three unchecked benchmark-runner cleanup errors under GolangCI-Lint. The runner now explicitly ignores best-effort temporary cleanup and reports close failures during write failure handling. Regenerated exact benchmark provenance and verified full local `golangci-lint run ./...`, `staticcheck ./...`, script tests, Hugo, and diff checks before republishing.
- 2026-08-04: Fixed the documentation-site Giscus regression reported from the live /demo/ page. Global comments now default to disabled. The comments partial fails the Hugo build if comments are enabled without all required Giscus repo, repoId, category, and categoryId values, preventing an unconfigured widget from reaching production. Verified the normal 35-page build emits no Giscus client or error text, the incomplete configuration fails closed, and a complete test configuration renders the expected client attributes.
- 2026-08-05: Replaced universal Scrinium startup with conditional durable-memory usage. Read-only, trivial, and ordinary non-durable work now incurs no Scrinium calls or wiki preload; governed sessions are reserved for maintained wiki writes and material architecture, security, release, governance, or external-source decisions. Added portable repository-relative instructions, limited reads to directly relevant pages, prohibited formatting-only log entries, documented generated-page freshness, and proposed the protected agent-rules replacement through a governed draft.

## 2026-08-05 — Digest freshness, incremental AST reuse, and grouped routes

- Replaced mtime-only source freshness with persisted SHA-256 content digests while retaining selected-file inventory and build-context checks; legacy graphs keep a documented mtime fallback until rebuilt.
- Added package-granular AST reuse: packages containing added, removed, or byte-changed files are fully reparsed, while unchanged packages reuse parser analysis. Precise builds always rerun repository-wide type/CHA/SSA enrichment from a reconstructed AST graph.
- Added constant nested route-prefix resolution for Gin, Echo, Fiber, and Chi Route closures. Dynamic prefixes remain conservative and retain only statically known path text.
- Kept CLI and MCP aligned through shared graph/search data, added parity and regression tests, exposed reuse work in stats, and updated maintained Markdown, Hugo source, generated site output, CLI help, MCP contracts, and project wiki pages.
- Verified with the full test/race/lint/staticcheck/vulnerability suite, release Go checks, coverage, fuzzing, docs build, Linux/Windows compilation, two precise self-builds (the second reused all 167 files), and gograph review --uncommitted.

## 2026-08-05 — Integrated concurrent remote main before release

- Merged remote PR #34 (`origin/main` at `7f6692b`) into local `main` without discarding the local digest/incremental-route implementation commit.
- Preserved the remote quick-start change to `gograph build . --precise` and corrected its adjacent description to say it builds a type-enriched precise graph.
- Verified the resulting v1.5.6 candidate with `make release-dry-run`; preflight, uncached tests, race detector, lint/static analysis, dependency and artifact vulnerability scans, docs, MCPB verification/smoke, and GoReleaser snapshot all passed. Dry-run metadata was restored and no refs were published.
