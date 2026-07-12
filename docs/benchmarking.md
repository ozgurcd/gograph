# Comparing Gograph and Gopls Workflows

[`gopls`](https://go.dev/gopls/features/mcp) is the Go project's
compiler-backed language server. It provides live diagnostics, navigation,
references, implementations, refactoring, and experimental MCP support.
gograph complements it with a persisted repository graph, composed
change-analysis responses, and policy-oriented queries.

The small harness in `scripts/benchmark.go` samples the wall-clock latency and
raw output size of one gograph command and one `gopls` CLI command. It does not
prove that either tool is faster, more accurate, or cheaper for a complete
agent task. The commands return different evidence, and results depend on
repository size, cache state, selected symbol, client behavior, and follow-up
reads.

## Prerequisites

1. Build `gograph` with `make build` (the default binary path is
   `bin/gograph`).
2. You must have `gopls` installed and available in your `$PATH`.

## Standard Execution

To sample `gograph context` and `gopls workspace_symbol`, pass a symbol name:

```bash
go run scripts/benchmark.go --sym "YourSymbolName"
```

Example:
```bash
go run scripts/benchmark.go --sym "Run"
```

Use `--gograph-bin` if the binary is elsewhere:

```bash
go run scripts/benchmark.go --gograph-bin /path/to/gograph --sym "Run"
```

## Sampling References

By default, the script runs `gopls workspace_symbol`. To sample `gopls
references` instead, provide its position-based target:

Use the `--gopls-target` flag for this:

```bash
go run scripts/benchmark.go --sym "Run" --gopls-target "/absolute/path/to/repo/file.go:40:6"
```

Only compare successful commands against the same checked-out source state.
`gograph context` and `gopls references` are still not semantically equivalent:
the former is a composed repository-graph response, while the latter is a live,
compiler-backed language operation.

## Reading the Output

The harness reports:

- command wall-clock time;
- raw output bytes divided by four as a rough token estimate; and
- command errors, if either invocation fails.

It deliberately applies no invented follow-up-read penalty. Real model tokens
depend on the model tokenizer and MCP/client envelope. End-to-end agent cost
also includes tool selection, subsequent reads, retries, and whether the task
was completed correctly.

For a defensible product evaluation, define representative tasks and manually
reviewed expected answers, then measure false positives, false negatives,
tool-call count, actual client tokens, wall-clock time, and task success across
multiple public repositories. Publish the fixtures and raw transcripts so the
comparison can be reproduced.
