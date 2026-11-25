# Security & Source Confinement Model

`gograph` performs local static analysis without executing target repository
binaries, tests, or application entry points, and without sending source code,
graphs, or telemetry to any external gograph service.

This document records the filesystem confinement, Go toolchain, artifact
publication, and freshness contracts enforced by the current binary.

---

## 1. Local Boundary & Go Toolchain Network Behavior

- **No gograph network calls:** `gograph` itself never transmits source,
  graphs, query results, or session metadata over the network.
- **Open-world Go toolchain:** `gograph` invokes your installed `go` binary
  using your environment:
  - Every `build` and `stale` check queries `go` for the effective build and
    module context.
  - `build --precise` loads packages through `go list` / `go/packages`.
  - `gograph doc` runs `go doc`.
  - Consequently, `cmd/go` may download missing modules or a newer Go toolchain
    according to your `GOPROXY`, `GOTOOLCHAIN`, and `GOFLAGS` settings.
- **Offline mode:** To keep runs strictly offline, set:
  ```bash
  GOPROXY=off GOTOOLCHAIN=local gograph build . --precise
  ```
  If a required module is absent from the local module cache, `--precise`
  falls back to the AST graph (`precision: precise_fallback`) rather than
  downloading.
- **cgo:** For packages using cgo, `--precise` invokes your C compiler to
  compile (never execute) the code.

---

## 2. Repository Source Confinement & Symlink Rules

- **Excluded links & special files:** Linked directories and linked or special
  files for extensions recognized by `go/build` are reported and excluded from
  indexing. Unrelated regular-file or dangling symlinks with non-Go extensions
  (such as YAML configs or TSV fixtures) are not Go tool inputs and do not
  block precise analysis.
- **Symlinked repository root:** An explicitly symlinked repository root
  remains supported. All descendant graph-directed source reads (`source`,
  caller/callee snippets, complexity, changed-file reads) use a shared
  repository-rooted filesystem handle that rejects symlink path components and
  accepts only regular files beneath the analyzed root.
- **Go metadata validation:** Linked or non-regular `go.mod`, `go.sum`,
  `go.work`, `go.work.sum`, and `vendor/modules.txt` files are rejected before
  `gograph` or `cmd/go` reads them.
- **`go.work` member confinement:** Applicable `go.work use` members may be
  sibling modules beneath the nearest real Git checkout. Outside Git, they
  remain confined beneath the workspace directory. Nested Git boundaries are
  never crossed, and every member directory, `go.mod`, and optional `go.sum` is
  validated before `cmd/go` starts.
- **Precise & `doc` preflight:** Before repository package type loading or
  `go doc`, `gograph` preflights the selected root plus its effective module
  root (or workspace root and every member tree), excluding `.git` and
  `.gograph`, and rejects source-tree links `cmd/go` may inspect. `doc` also
  rejects filesystem-shaped queries.
- **Excluded directories & skills:** `--exclude-dirs=dir1,dir2` omits literal
  repository-relative subtrees from AST and precise targets (no globs, absolute
  paths, or parent traversal). Since v1.7.2, `--exclude-dirs=.claude/skills`
  masks directory symlinks beneath that real directory during precise loading
  without following their targets. AI worktree directories (`.claude/`,
  `.cursor/`, `.agents/`) are excluded from indexing.
- **Sensitive files:** Indexing never intentionally scans `.env`, private keys,
  certificates, kubeconfigs, `.tfstate`, or credential files.

---

## 3. Artifact Publication, Locking & Atomicity

- **Target directory:** Build artifacts are written to `.gograph/`, which must
  be a real directory (linked or non-directory `.gograph` is refused).
- **Writer lock:** Writers coordinate through `.gograph/.artifacts.lock` (up to
  30s wait). An existing lock entry must be a regular file, never a symlink or
  special file. The lock file remains as operational coordination state.
- **Commit marker protocol:** Publication stages `graph.json` and the nine
  Markdown reports, replaces the nine reports first, and replaces `graph.json`
  last as the publication commit marker.
- **Platform atomicity caveats:** Same-directory rename replacement is atomic
  on Unix-like systems; Go does not guarantee atomic rename semantics on
  non-Unix platforms (including Windows). The ten-file bundle is not a single
  atomic filesystem transaction: a crash or I/O failure can leave newer reports
  alongside the previous `graph.json`. Consumers must treat `graph.json` as the
  authoritative marker.
- **Zero-file / parse failure safety:** If no Go files are found or zero files
  parse successfully, `build` exits without replacing existing artifacts.
- **`.gitignore` updates:** `gograph build` appends `.gograph/` to the
  enclosing Git root `.gitignore` (falling back to the target `.gitignore`
  outside Git) only when `.gitignore` is absent or a regular file; symlinks and
  special files are refused without modifying their target. Opt-in
  `gograph mcp --persist-refresh` never edits `.gitignore`.

---

## 4. Source-Policy Markers, SHA-256 Digests & Size Bounds

- **Source-policy marker:** Persisted `graph.json` artifacts and saved
  `.json` baselines must carry the exact current repository source-policy
  marker. Artifacts with a missing or unsupported marker are untrusted and
  rebuild-required. Older binaries do not enforce this confinement boundary and
  should not be used on untrusted repositories.
- **Trusted load root:** Serialized `root` fields inside `graph.json` or saved
  baselines are treated as metadata only and are replaced with the trusted load
  directory.
- **512 MiB cap:** Whole-artifact JSON reads (graph load, saved baselines,
  validation, MCP reload, workspace overlays) reject files larger than 512 MiB
  before allocation.
- **SHA-256 digests & incremental reuse:** Every parsed source file stores a
  SHA-256 content digest. Subsequent builds reparse changed packages and reuse
  parser-owned records for unchanged packages; `--precise` still recomputes
  repository-wide type/CHA/SSA enrichment so cross-package dispatch stays
  accurate. Typed-only test targets are recomputed rather than reused as parser
  facts.
- **Environment binding:** Persisted graphs are fingerprinted against their
  effective `GOWORK`, `GOFLAGS`, `--tags`, and `--exclude-dirs` selection. A
  context mismatch reports `stale` rather than silently serving incompatible
  facts.

---

## 5. Low-Memory Execution Policy

`--memory-mode=low` with optional `--max-memory=<size>` (e.g., `768MiB`,
`1GiB`) adjusts GC cadence, reclaims production type/SSA memory before typed
test loading, and avoids full AST JSON duplication.

- `--max-memory` is a soft Go runtime heap target (`GOMEMLIMIT`), **not** a
  hard RSS cap; mapped files, the executable, cgo, and `cmd/go` subprocesses
  can exceed it.
- Low-memory mode never silently lowers graph precision or omits facts.

---

## 6. Repository-Confined Configs & Session Metadata

- Default/relative configs (`gograph/boundaries.json`, `gograph/checks.json`,
  `gograph/flow.json`, project-root `.gograph.yml`), snapshots, and relative
  `gograph wiki` outputs require real directories and regular, non-linked
  files. Documented absolute config or wiki paths select explicit
  operator-chosen local destinations.
- Optional audit session metadata is stored locally under `.gograph/sessions/`
  and never logs raw query results. Session IDs are cooperative attribution
  selectors, not credentials or OS-user boundaries.
