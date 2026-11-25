---
title: "CI Architecture Gates for Go Repositories"
weight: 3
date: 2026-11-25
lastmod: 2026-11-25
description: "Enforce Go package boundaries, instability limits, cyclomatic complexity ceilings, and API drift checks in GitHub Actions with gograph."
---

Linters such as `golangci-lint` catch local defects and style issues inside individual files, whereas architectural drift occurs across package boundaries: an HTTP handler imports a database package directly, a core struct accumulates dozens of methods, or a pull request raises cyclomatic complexity in a hot path.

`gograph` provides deterministic CLI gates that enforce repository-wide structure directly from the compiled call and import graph.

---

## 1. Package Boundary Enforcement (`gograph boundaries`)

Define allowed import layers in a committed `gograph/boundaries.json` file so local runs and CI enforce the same architecture.

### Bootstrap a Baseline from Existing Code

On an existing repository, writing strict rules from scratch often fails on legacy imports. Generate a baseline matching current imports first:

```bash
gograph build . --precise
gograph boundaries --create
```

This writes `gograph/boundaries.json` grouping `internal/`, `pkg/`, and `cmd/` layers with zero initial violations. To tighten architecture incrementally, remove an unwanted import pattern from a layer's `may_import` list and commit the file.

### Example `gograph/boundaries.json`

```json
{
  "layers": [
    {
      "name": "domain",
      "packages": ["internal/domain/**"],
      "may_import": []
    },
    {
      "name": "service",
      "packages": ["internal/service/**"],
      "may_import": [
        "internal/domain/**",
        "internal/repository/**"
      ]
    },
    {
      "name": "handler",
      "packages": ["internal/handler/**"],
      "may_import": [
        "internal/domain/**",
        "internal/service/**",
        "github.com/gin-gonic/**"
      ]
    }
  ]
}
```

- Standard-library imports (first path segment without a dot) and same-layer imports are implicitly permitted.
- `gograph boundaries` exits non-zero and prints the violating file, line number, and import path on any breach.

---

## 2. Complexity, Instability and God-Object Gates (`gograph gate`)

`gograph gate` evaluates thresholds from a committed project-root `.gograph.yml` file and fails closed if `.gograph/graph.json` is stale.

### Initialize `.gograph.yml`

```bash
gograph gate init
```

Every key in `.gograph.yml` is optional; omit a key to disable that gate:

```yaml
# .gograph.yml — quality gates enforced by 'gograph gate'
max_complexity: 25
max_instability: 0.75
max_god_object_methods: 20
allow_new_orphans: false
max_new_coupling_edges: 5
```

Inspect current repository metrics before setting limits:

```bash
gograph complexity --no-tests --top 15
gograph coupling --internal-only
gograph godobj
```

> **Baseline-aware limits:** `allow_new_orphans` and `max_new_coupling_edges` compare against the immediately preceding persisted graph baseline and are skipped when no prior baseline exists.

---

## 3. Policy Checks, API Drift and Structural Predicates

For pull-request enforcement against `main`, combine `gograph api` and `gograph check`:

```bash
# Detect exported API additions, removals, and signature changes since main
gograph api --since origin/main

# Evaluate repository policy checks (boundaries, complexity, arity, changed-export tests)
gograph check --since origin/main
```

For machine-verifiable invariants in CI scripts, use `gograph validate` with closed structural predicates (`imports_only_from`, `callers_only_from`, `call_arg_constant`):

```bash
gograph validate --repo . --binding-json '{
  "schema_version": "gograph.binding.v1",
  "predicate": "callers_only_from",
  "subject": {"language": "go", "kind": "symbol", "id": "os/exec::Command"},
  "allow": [{"language": "go", "kind": "package", "id": "example.com/app/internal/runner"}],
  "required_precision": "precise"
}' --json
```

Exit codes are strictly tri-state: `0` (pass), `1` (conclusive violation with exact file:line rows), and `2` (cannot evaluate or invalid request).

---

## 4. GitHub Actions Workflow Example

Add an architecture-gate job in `.github/workflows/architecture.yml`:

```yaml
name: Go Architecture Gates

on:
  pull_request:
    branches: [main]
  push:
    branches: [main]

jobs:
  architecture:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Install gograph
        run: go install github.com/ozgurcd/gograph/cmd/gograph@latest

      - name: Build strict precise graph
        run: gograph build . --precise --strict

      - name: Enforce package boundaries
        run: gograph boundaries

      - name: Enforce quality gate thresholds
        run: gograph gate

      - name: Check exported API drift on pull requests
        if: github.event_name == 'pull_request'
        run: gograph api --since origin/main
```

### Why `--precise --strict` in CI

By default, `gograph build . --precise` falls back to AST-only mode (`precision: precise_fallback`) and exits `0` when a package fails to compile. Passing `--strict` ensures CI fails loudly instead of evaluating gates on a degraded graph.

---

## Related Documentation

- [Command Reference](/docs/command-reference/) — full flags for `boundaries`, `gate`, `check`, `api`, `validate`, `coupling`, and `complexity`
- [Tool Comparisons](/docs/comparisons/) — how `gograph` complements `golangci-lint` in CI
- [Engineering Workflows](/docs/workflows/) — pre-edit planning and post-edit review playbooks
