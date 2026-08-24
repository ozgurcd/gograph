---
title: "Agent Workflows"
weight: 4
description: "Recommended operational workflows for developers and AI agents using gograph."
---

To maximize efficiency, reduce latency, and guarantee safety when modifying codebases, we recommend following these standardized workflows. These steps can be programmed into AI system instructions or executed manually.

---

## Workflow 1: Onboarding to a New Repo

When opening an unfamiliar Go repository, use this workflow to get oriented in seconds:

1. **Verify Index Status**:
   ```bash
   gograph stats
   gograph stale
   ```
   If stale or missing, run `gograph build .`.
2. **Find High-Risk Hotspots**:
   ```bash
   gograph hotspot --top 10
   ```
   Identifies the most heavily referenced functions in the codebase.
3. **Map Package Coupling**:
   ```bash
   gograph coupling
   ```
   Gives a high-level table showing package stability and dependencies.
4. **Inspect the Global API Surface**:
   ```bash
   gograph skeleton
   ```
   Exposes every package signature with bodies stripped.

---

## Workflow 2: Safe-Edit Symbol Lifecycle

Before changing the signature or behavior of any function, method, or struct, follow this cycle:

```
[ plan <sym> ] ──► [ context <sym> ] ──► [ Edit Code ] ──► [ build . --precise ] ──► [ review --uncommitted ]
```

1. **Plan first**:
   ```bash
   gograph plan <symbol>
   ```
   This automatically checks for callers, associated tests, and risk profiles (e.g. database transactions or env reads).
2. **Extract Symbol Context**:
   ```bash
   gograph context <symbol>
   ```
   Bundles raw AST info, the exact source block of the target, and immediate dependencies in one call.
3. **Perform the Edit**: Modify the code as needed.
4. **Type-checked build**:
   ```bash
   gograph build . --precise
   ```
   Attempts type/load analysis, computes type-checked interface implementers, and retains every valid named in-repository CHA target at interface call sites. Check both fields in `gograph stats`: `precision: precise` confirms enrichment succeeded, while `precise_fallback` means the published AST graph could not be enriched. A failed retry retains an existing fresh successful precise artifact for the same selected sources instead of downgrading it. `build_status` independently reports whether AST parsing and selection were complete; parse failures or selection/security warnings make it partial.

   On a memory-constrained host, use `gograph build . --precise --memory-mode=low --max-memory=1GiB`.
   This preserves analysis semantics but may use more GC CPU because of
   aggressive reclamation. The value is a soft Go runtime memory target,
   not a hard RSS ceiling.
5. **Post-edit review**:
   ```bash
   gograph review --uncommitted
   ```
   Validates complexity drift, test coverage, and security risk introductions before making a commit.

---

## Workflow 3: Package-Level Refactoring

Before splitting, merging, or moving a Go package, execute this check:

1. **Discover all external consumers**:
   ```bash
   gograph dependents <package>
   ```
   Finds every other package that imports your target. A package with high fan-in (low instability) is difficult to change without sweeping breaking changes.
2. **Review public API contracts**:
   ```bash
   gograph public <package>
   ```
   Ensure you know exactly what symbols are exported and consumed externally.

---

## Workflow 4: Security Flow Review

Use the production-only scan first, then narrow by sink or source and inspect each reported path in source:

```bash
gograph flow --no-tests
gograph flow --sink process_execution --no-tests
gograph flow --source decoded_json --sink sql_query --no-tests --json
```

Treat findings as review leads. The analysis is interprocedural and path-insensitive, with call/return matching across up to 16 nested repository calls. When a project has a function that returns a validated or normalized value, declare it in `.gograph/flow.json`; scope it to the sink kind it actually protects. Do not mark a boolean/error-only validator as a sanitizer for an unchanged input value.

---

## Workflow 5: CI Quality Gate

Gate thresholds live in a regular, non-linked project-root `.gograph.yml`;
they are not command flags. Scaffold the file once, review its documented
defaults, and commit it:

```bash
gograph gate init
gograph complexity
gograph coupling
gograph godobj
```

Orphan and new-coupling limits compare the current publication with the
immediately preceding persisted graph embedded as its baseline. They are
skipped when no preceding graph exists. The CI job publishes a current graph
and then runs the gate:

```bash
gograph build . --precise
gograph gate
```

`gate` refuses stale graph data and evaluates only the thresholds present in
`.gograph.yml`. Package import-boundary policy is separate; run
`gograph boundaries` as another CI step when `boundaries.json` is used.
