# Architecture Boundary Enforcement

`gograph boundaries` enforces package-import constraints recorded in the
current graph. It acts as an automated guardrail against new indexed imports
that violate the repository's configured layers.

## Why Use It?

AI coding agents (and human developers under a deadline) often take the path of least resistance. For example, if a handler needs data, an AI might inject an SQL query directly into the HTTP layer rather than calling the repository layer. 

By enforcing boundaries, you can ensure that:
- `handlers` can only import `services` or `models`.
- `services` can only import `repositories` or `models`.
- `repositories` are the only layer allowed to import `database/sql` or `pgx`.

If an illegal import is detected, `gograph boundaries` exits with a non-zero code, failing your CI/CD pipeline or explicitly rejecting the AI's proposed code change.

## 1. Generating a Baseline (`--create`)

Most real-world codebases do not have perfectly clean architecture. If you wrote a strict boundary file by hand, it would likely throw hundreds of violations.

Instead, use `gograph boundaries` to generate a perfect baseline of your **current** tech debt:

```bash
gograph boundaries --create
```

This will automatically:

1. Group packages under `internal/`, `pkg/`, and `cmd/` by their first two
   directories (for example `internal/handler`). Other packages use their first
   directory, and the repository-root package uses `.`.
2. Record selected non-standard-library imports between those layers. External
   imports are deduplicated to host/owner patterns such as
   `github.com/gin-gonic/**`.
3. Generate `.gograph/boundaries.json`. The output must stay inside the graph
   root; only real parent directories are created, and creation rejects linked
   or special entries and refuses to overwrite an existing file.

Boundary reads apply the same graph-root containment and require a regular,
non-linked file (including no linked ancestor). Because this file maps your
*exact current state*, running `gograph boundaries` immediately afterward will
yield **0 violations**.

## 2. Enforcing Tech Debt Reduction

Once you have your baseline `boundaries.json`, you can systematically eliminate tech debt:
1. Open `.gograph/boundaries.json`.
2. Locate the `may_import` array for the `internal/handler` layer.
3. Find an illegal import (e.g., `"internal/repository/**"`) and **delete it**.
4. Run `gograph boundaries`.

It will report indexed import sites where the handler violates the configured
rule. Once those sites are fixed, `gograph boundaries` can enforce the same
policy in CI for future indexed imports. Generated, build-selected, or
otherwise excluded files remain subject to the documented scanner limits.

## 3. The `boundaries.json` Schema

```json
{
  "layers": [
    {
      "name": "internal_domain",
      "packages": [
        "internal/domain/**"
      ],
      "may_import": []
    },
    {
      "name": "internal_handler",
      "packages": [
        "internal/handler/**"
      ],
      "may_import": [
        "internal/domain/**",
        "internal/service/**",
        "github.com/gin-gonic/**"
      ]
    }
  ]
}
```

### Matching Rules
- `internal/domain/**`: Matches `internal/domain` and all subdirectories recursively.
- `internal/domain/*`: Matches exactly one level deep.
- `github.com/gin-gonic/**`: A rule for external/third-party imports.

### Implicit Allowances
To prevent the JSON file from becoming an unreadable mess, `gograph` uses intelligent implicit allowances:
1. **Standard-library heuristic:** An import whose first path segment contains
   no dot is treated as standard library and is always permitted (for example
   `fmt` or `net/http`). This is intentionally simple and can also exempt a
   dotless local/module import path.
2. **Same package directory:** An import resolved to the exact same package
   directory as the importing file is implicitly allowed.
3. **Same Layer:** A file is allowed to import another file within the exact same layer (e.g., `internal/domain/models` importing `internal/domain/errors`), provided they both match the same layer's `packages` definition.

## CI/CD and Agent Integration

For maximum effectiveness, add the check to your `Makefile` or CI pipeline:

```bash
gograph build .
gograph boundaries
```

If you are using Claude Code or Cursor, instruct the agent directly in `CLAUDE.md` or `.cursorrules`:

> *"After modifying any code, you MUST run `gograph boundaries`. If it exits with an error, your change violated clean architecture. Re-read the boundaries.json file and refactor your code until the command passes."*
