# Reproducible Structural Evidence Benchmark

The benchmark suite in [`benchmarks/`](../benchmarks/) tests claims that can be
reviewed directly from a small Go fixture. It deliberately does **not** claim a
fixed token saving, agent success rate, or universal performance advantage.

The controlled `refactor-shop` fixture contains:

- a `ProductRepository` interface;
- two implicit implementations in separate packages;
- an interface-dispatched `Save` call;
- an HTTP caller, a service workflow, and a directly mapped unit test.

The expected evidence is declared in `benchmarks/suite.json`, beside the source
that establishes the ground truth. The runner checks every declared item
against raw command output and fails when a gograph workflow misses one.

## What is measured

For each scenario, the report records:

- the exact gograph binary version;
- the Git revision and dirty-worktree state of the harness;
- SHA-256 digests of the runner, suite, and fixture;
- fixture test, graph build, precision, and build-health status;
- command arguments and complete raw output;
- evidence found versus the manually reviewable ground truth;
- process-level tool-call count, output bytes, output lines, and median elapsed
  time across repeated runs.

Elapsed time is retained for reproducibility, not advertised as a universal
speed comparison. The fixture is intentionally small, operating-system caches
vary, and the workflows return different types of evidence.

## Reproduce the checked-in result

Prerequisites are Go, `rg`, and the current repository checkout. Build the
versioned binary first, then run:

```bash
make build
make benchmark
```

`make benchmark` runs the fixture tests, builds a precise graph, executes all
workflows three times, verifies the declared evidence, and writes:

- `benchmarks/results/gograph-v1.5.5.json` — the complete checked-in report;
- `docs-site/static/demo/data.json` — the identical data consumed by the
  verified-evidence view in the no-install public demo.

The demo's guided repository workspace reads its curated navigation and exact
fixture source snapshot from `docs-site/static/demo/workspace.json`. That tour
does not replace or modify the reproducible benchmark result; it links each
benchmark-backed investigation to the matching scenario in `data.json`.

To run the harness without replacing checked-in results:

```bash
go run ./scripts/benchmark.go \
  --suite benchmarks/suite.json \
  --gograph-bin bin/gograph \
  --runs 3
```

## Current v1.5.5 result

The checked-in report was produced by `gograph version v1.5.5-11363b9` with a
complete precise graph. All three gograph workflows recovered every declared
ground-truth item:

| Scenario | gograph | Text-search baseline | Process calls |
|---|---:|---:|---:|
| Implicit interface implementations | 2/2 | 0/2 | 1 vs 1 |
| Interface-dispatched caller | 2/2 | 1/2 | 1 vs 1 |
| Composed change context | 4/4 | 4/4 | 1 vs 3 |

This supports three narrow claims: precise gograph analysis can expose Go's
implicit interface relationships, can qualify an interface-dispatched caller,
and can compose source/caller/dependency/test evidence into one response on the
published fixture. It does not establish end-to-end agent accuracy or token
cost.

## Extending the evidence

New scenarios should add a fixture change that makes the expected relationship
obvious under manual review, declare evidence strings in `suite.json`, and
include a realistic comparison workflow. Do not add a marketing claim that the
runner does not actually validate.
