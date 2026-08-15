---
title: "Evidence & Methodology"
description: "Reproduce gograph's structural evidence benchmark and inspect its complete raw results."
url: "/benchmarks/"
---

## Claims you can reproduce

gograph's checked-in benchmark uses a controlled Go fixture with manually
reviewable ground truth. It verifies implicit interface implementations,
interface-dispatched callers, a composed change-context workflow, and exact
AST-bounded source extraction.

The current v1.5.7 report was generated with a complete precise graph. Every
declared evidence item was found by the corresponding gograph workflow:

| Scenario | gograph evidence | Text-search evidence | Process calls |
|---|---:|---:|---:|
| Implicit interface implementations | 2/2 | 0/2 | 1 vs 1 |
| Interface-dispatched caller | 2/2 | 1/2 | 1 vs 1 |
| Composed change context | 4/4 | 4/4 | 1 vs 3 |
| Exact symbol source | 1/1 | 1/1 | 1 vs 1 |

[Explore the fixture and verified evidence →](/demo/)

## What the benchmark does not claim

This is a deterministic structural-evidence benchmark. It does not claim a
fixed token reduction, universal speedup, hallucination rate, or complete
end-to-end coding-agent success rate. The fixture is small, elapsed times depend
on the host, and text search returns different evidence.

## Reproduce it

```bash
git clone https://github.com/ozgurcd/gograph
cd gograph
make build
make benchmark
```

The harness records the binary version, source revision, runner, suite, and
fixture SHA-256 digests, graph precision and build health, complete raw outputs,
evidence coverage, output size, process-call count, and repeated timing.

Review the [suite and fixture](https://github.com/ozgurcd/gograph/tree/main/benchmarks),
the [complete v1.5.7 result](https://github.com/ozgurcd/gograph/blob/main/benchmarks/results/gograph-v1.5.7.json),
and the [methodology](https://github.com/ozgurcd/gograph/blob/main/docs/benchmarking.md).
