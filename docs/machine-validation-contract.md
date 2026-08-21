# Machine-readable structural validation contract

Status: implemented v1 contract in this repository.

## Decision summary

Gograph exposes one operation that binds a closed structural predicate to one
fresh repository graph and returns `pass`, `fail`, or `cannot_evaluate` without
requiring consumers to compose navigation commands.

The public surface is:

```text
gograph version --json
gograph validate --repo PATH --binding-json JSON --json
```

`validate` is a read-only, machine-oriented CLI operation. It
evaluates exactly one predicate against the trusted persisted graph selected by
`--repo`. It must not build, refresh, or publish graph artifacts. A missing,
stale, unsafe, partial, or insufficiently precise graph degrades to
`cannot_evaluate`.

The v1 predicate set is deliberately narrow:

- `symbol_exists`
- `package_imports`
- `call_edge_exists`
- `type_implements`

Reachability and a generic `edge_exists` predicate are deferred. A generic
relation enum would expose graph records whose completeness and assurance
semantics differ too much to share one safe negative-result rule.

## Current machine interfaces

### CLI JSON

Many current analysis commands accept `--json` and return the generic CLI
envelope defined by `internal/cli.Envelope`:

```json
{
  "schema_version": "1",
  "command": "callers",
  "query": "Target",
  "status": "ok",
  "count": 1,
  "results": []
}
```

The envelope distinguishes `ok`, `empty`, and `error`. It is useful for
automation and has regression coverage, but it is a query/presentation schema,
not a validation schema. In particular:

- `empty` does not distinguish a missing subject from an evaluated absent
  relationship;
- public query functions accept fuzzy, case-insensitive, or short-name forms;
- results generally omit the exact graph node and edge IDs needed to audit the
  match;
- the envelope does not contain the Gograph version, repository identity,
  graph fingerprint, freshness, precision, or predicate-specific completeness;
- hard errors use exit code 1, while `stale --json` uses exit code 2 for a stale
  result; there is no validation-wide outcome mapping;
- generic query JSON remains separate from `gograph version --json` and the
  closed validation schema.

`gograph stats --json` exposes graph schema version, generated time, precision,
complete/partial build status, parse-failure count, and graph counts.
`gograph stale --json` compares selected inventory, build context, and source
content to the persisted graph and exits 0 when current, 2 when stale, and 1 on
an operational error. These are necessary inputs, but composing them with a
later query has a time-of-check/time-of-use gap. The current stale function also
does not surface scanner errors returned while recomputing the inventory, so it
is not by itself a sufficient fail-closed validation boundary.

### MCP

The MCP server exposes structured query and analysis tools and refreshes many
source-analysis calls in memory. It reports precision and build health through
stats and freshness through stale. Its tool schemas are useful for agents, but
there is no validation tool, stable validation result schema, repository
fingerprint, or outcome model. MCP result payloads are capability-specific and
do not form an external validator protocol.

The initial external contract should remain subprocess/CLI based. Adding an MCP
validation tool is not required for Scrinium and would enlarge the first
compatibility surface. The reusable evaluator should remain transport-neutral
so MCP can expose the same request and result later if there is a concrete use.

### Persisted graph

`.gograph/graph.json` is machine-readable schema version 2. It contains stable
symbol IDs, import/call/implementation edges, build metadata, per-file SHA-256
digests, an effective build-context fingerprint, a validation source
fingerprint, a precision value, and a source-policy trust marker.

It is an internal persistence format, not the proposed public validation
protocol. Current readers intentionally tolerate additive fields and legacy v2
records, normalize missing precision to AST, and permit a legacy mtime
freshness fallback. They ignore the serialized repository root and re-anchor
the graph at the caller-selected load location. Those compatibility properties
are appropriate for navigation, but an external validator needs strict input,
explicit uncertainty, and a result tied to the exact artifact and current
source snapshot.

## Can Scrinium integrate today?

Yes, through the two versioned CLI commands in this document. Scrinium still
must not infer a validation result by chaining `stats`, `stale`, `node`,
`callers`, `deps`, or `implementers`.

For example, `callers Missing --exact --json` and a real symbol with no callers
can both return the same empty success. `deps` resolves package short names and
suffixes, and `implementers` falls back to package-insensitive AST duck typing
when precise edges are unavailable. These behaviors are useful for interactive
navigation but cannot safely establish an exact negative predicate.

Parsing the human `version` output, reading `graph.json` directly, or combining
separate command results in the Scrinium adapter would duplicate Gograph's
trust, matching, and freshness rules. Consumers should use the implemented
validation contract instead.

## Implemented contract pieces

The current implementation includes these independently necessary pieces:

- a JSON version command backed by the existing injected version;
- a strict, closed, versioned predicate request;
- exact case-sensitive resolution that does not use interactive fuzzy matching;
- predicate-specific subject, object, and relationship evidence;
- a single-operation freshness check that propagates scanner errors and closes
  the query-time race;
- a stable source-snapshot fingerprint and exact graph-artifact fingerprint;
- explicit graph completeness and predicate-specific negative-result rules;
- a stable `pass`/`fail`/`cannot_evaluate` result with reason codes and exit
  codes;
- bounded diagnostics and strict output conformance tests.

The persisted graph records most positive evidence needed for the four initial
predicates. The important missing work is trustworthy orchestration and safe
negative-result semantics, not a broader graph model. Because completeness is
currently repository-wide, an unrelated parse or selection failure will
conservatively produce `cannot_evaluate`. A future scoped-completeness model
could relax that limitation, but v1 must not guess that an unrelated-looking
failure is irrelevant.

## Scope and non-goals

The contract evaluates structural facts derived from selected Go source under
one recorded Go build context. It does not execute target code and does not
verify runtime behavior, authorization correctness, transaction semantics,
business invariants, or test outcomes.

A pass means only that the requested structural predicate holds under the
recorded Gograph analysis contract. It does not make a broader natural-language
claim true.

The first version does not support:

- arbitrary query strings or a Gograph query language;
- caller-provided commands, flags, build tags, file paths, or shell fragments;
- transitive reachability;
- routes, SQL, flow, error flow, mutation, test, or orphan heuristics;
- symbols outside the selected Go repository source set;
- other programming languages;
- graph creation or refresh as a side effect of validation.

## CLI contract

### Version

`gograph version --json` writes exactly one JSON document to stdout:

```json
{
  "schema_version": "gograph.version.v1",
  "version": "1.5.7"
}
```

The `version` value must come from the existing build/release injection
mechanism. Development builds must report their actual injected identifier; the
command must not manufacture a release version.

### Validation

```text
gograph validate --repo PATH --binding-json JSON --json
```

- `--repo` is required and is selected by the invoking application, never by
  claim content.
- `--binding-json` is required and contains one strict
  `gograph.binding.v1` document.
- `--json` is required in v1 so accidental human rendering cannot be parsed as
  the protocol.
- stdout contains exactly one JSON document. Concise operational diagnostics
  may be written to stderr, but stdout must contain no prose.
- no shell is invoked.
- the command reads only the existing trusted graph and repository inputs
  needed to establish freshness. It does not build or modify the repository.

Duplicate JSON keys, unknown fields, trailing documents, invalid UTF-8, invalid
enums, empty required values, and malformed IDs are invalid requests. The
decoder must be strict rather than silently normalizing them.

## Binding schema

The canonical binding schema is `gograph.binding.v1`:

```json
{
  "schema_version": "gograph.binding.v1",
  "predicate": "call_edge_exists",
  "subject": {
    "language": "go",
    "kind": "symbol",
    "id": "example.com/project/internal/http::(*Handler).ServeHTTP"
  },
  "object": {
    "language": "go",
    "kind": "symbol",
    "id": "example.com/project/internal/service::(*Service).Authorize"
  },
  "required_precision": "precise"
}
```

Fields and closed enums:

- `schema_version`: exactly `gograph.binding.v1`.
- `predicate`: `symbol_exists`, `package_imports`, `call_edge_exists`, or
  `type_implements`.
- `subject`: required typed reference.
- `object`: forbidden for `symbol_exists`; required for the other predicates.
- `required_precision`: `ast` or `precise`.

A reference contains exactly:

- `language`: `go`;
- `kind`: `symbol` or `package`;
- `id`: a module-qualified symbol ID or exact package import path.

Predicate constraints:

| Predicate | Subject | Object | Minimum precision |
| --- | --- | --- | --- |
| `symbol_exists` | symbol | absent | `ast` |
| `package_imports` | package | package | `ast` |
| `call_edge_exists` | function/method symbol | function/method symbol | `precise` |
| `type_implements` | named concrete type symbol | named interface symbol | `precise` |

The predicate itself names the relation; a separate `relation` field is
deliberately omitted because it would be redundant and could become
inconsistent. Exact case-sensitive IDs are required. Display names, fuzzy
selectors, file-plus-line references, and ephemeral graph positions are not
accepted.

The validator should include the SHA-256 of the canonical binding JSON as
`request.binding_fingerprint`. Canonicalization uses the fixed field order
above, UTF-8, no insignificant whitespace, and a final newline before hashing.

## Result schema

Every syntactically recognized validation invocation returns
`gograph.validation.v1`, including invalid bindings and operational failures:

```json
{
  "schema_version": "gograph.validation.v1",
  "command": "validate",
  "gograph_version": "1.5.7",
  "generated_at": "2026-08-20T20:30:00Z",
  "repository": {
    "root": "/work/project",
    "source_fingerprint": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  },
  "analysis": {
    "graph_schema_version": "2",
    "source_policy_version": 1,
    "mode": "precise",
    "precision": "precise",
    "completeness": "complete",
    "freshness": "current",
    "build_context_fingerprint": "...",
    "graph_fingerprint": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    "graph_generated_at": "2026-08-20T20:29:00Z"
  },
  "request": {
    "binding_fingerprint": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    "binding": {
      "schema_version": "gograph.binding.v1",
      "predicate": "call_edge_exists",
      "subject": {
        "language": "go",
        "kind": "symbol",
        "id": "example.com/project/internal/http::(*Handler).ServeHTTP"
      },
      "object": {
        "language": "go",
        "kind": "symbol",
        "id": "example.com/project/internal/service::(*Service).Authorize"
      },
      "required_precision": "precise"
    }
  },
  "evaluation": {
    "outcome": "pass",
    "reason": "predicate_passed",
    "diagnostics": []
  },
  "evidence": {
    "resolved_subject": {
      "id": "example.com/project/internal/http::(*Handler).ServeHTTP",
      "kind": "symbol",
      "symbol_kind": "method",
      "locations": [{"path": "internal/http/handler.go", "line": 42}]
    },
    "resolved_object": {
      "id": "example.com/project/internal/service::(*Service).Authorize",
      "kind": "symbol",
      "symbol_kind": "method",
      "locations": [{"path": "internal/service/service.go", "line": 75}]
    },
    "matched_relations": [
      {
        "kind": "call_edge_exists",
        "subject_id": "example.com/project/internal/http::(*Handler).ServeHTTP",
        "object_id": "example.com/project/internal/service::(*Service).Authorize",
        "classification": "resolved_static",
        "locations": [{
          "path": "internal/http/handler.go",
          "line": 58,
          "column": 22
        }]
      }
    ]
  }
}
```

All objects are closed. Optional fields may be omitted only where the schema
explicitly permits it:

- `git_revision` is optional informational metadata. The v1 implementation does
  not invoke Git merely to populate it, and Git is not required for validation.
- repository and analysis fingerprints are omitted only when they cannot be
  computed; that condition forces `cannot_evaluate`.
- resolved object is absent where the predicate has no object; evidence arrays
  are present as empty arrays when no relation or location was established.
- source locations are repository-relative and may omit line/column only when
  Gograph has no more precise source coordinate.

Evidence arrays use stable lexical ordering. Diagnostics contain `code` and
`message`, with optional repository-relative `path` and symbol `id`. V1 should
cap diagnostics at 16 entries and each message at 512 UTF-8 bytes. It must not
emit source bodies, full compiler output, environment values, or target logs.

For an invalid request that cannot be echoed safely, `request` may contain only
a hash of the received bytes. The result still uses the validation schema and
reason `invalid_request`.

## Symbol identity model

The durable Go symbol identity is the existing `SymbolNode.ID` form:

```text
<module-qualified-package-import-path>::<declaration>
<module-qualified-package-import-path>::(<receiver>).<method>
```

Examples:

```text
example.com/project/internal/auth::Service
example.com/project/internal/auth::(*Service).Validate
example.com/project/internal/auth::ValidateToken
```

This identity remains meaningful across graph rebuilds, file moves, and line
changes as long as the module path, package path, receiver, and declaration
name remain the same. It intentionally changes when the program-level identity
changes. Generic functions and methods use the source declaration identity,
not an instantiated SSA identity.

Package identity is its exact Go import path. File path and line are evidence
locations, not identity. Internal array positions, map order, and any future
database IDs are not durable bindings.

Gograph currently falls back to a pseudo import path derived from an absolute
directory when no module/import identity can be resolved. IDs with that
fallback are machine-local and must be rejected for `gograph.binding.v1` with
`symbol_identity_unstable`. V1 therefore requires module-backed Go identities.

Machine validation must use byte-exact, case-sensitive IDs. It must not reuse
the interactive resolver's fuzzy, suffix, short-name, or case-folded matches.
More than one graph node with the same requested ID is
`cannot_evaluate/symbol_ambiguous`, not a selected first match.

## Precision semantics

Gograph currently records three actual precision states:

- `ast`: syntactic parsing and heuristic extraction only;
- `precise`: the AST graph was enriched after successful repository-wide
  `go/packages`, `go/types`, SSA, and CHA analysis;
- `precise_fallback`: precise analysis was requested but failed, leaving AST
  evidence and a visible warning.

Build completeness is a separate dimension. `complete` currently means all
selected files parsed and no scanner/build warnings or failures were recorded;
otherwise the graph is `partial`. Machine validation must preserve that
separation.

For v1, an evaluated negative result is safe only under these rules:

- `symbol_exists`: a current, complete AST-or-better graph covers the selected
  Go source inventory. An exact missing symbol may return `fail`.
- `package_imports`: a current, complete AST-or-better graph covers all selected
  import declarations. An absent direct import may return `fail`.
- `type_implements`: a current, complete `precise` graph and exact named
  in-repository types are required. Absence may then return `fail` under Go's
  type system for that build context.
- `call_edge_exists`: a current, complete `precise` graph and exact
  in-repository function/method IDs are required. A present non-synthetic exact
  edge may return `pass`. A negative result may return `fail` only when the
  evaluator can establish that the subject's relevant call sites were resolved
  within the supported static call model. If unresolved function values or
  other unsupported dispatch could affect the predicate, return
  `cannot_evaluate/analysis_incomplete`.

`precise` is not a runtime guarantee. CHA conservatively includes possible
interface targets and can over-approximate actual execution. Reflection,
`unsafe`, plugins, unresolved function values, unnamed concrete types,
module-external implementations, and code excluded by the selected build
context remain outside the guarantee. Call evidence labels its classification
as `resolved_static` or `cha_possible_target`; a CHA possible target does not mean that runtime
dispatch necessarily reaches that target.

`precise_fallback` never satisfies `required_precision: precise`. Partial,
fallback, stale, and unknown states never support evaluated absence.

## Freshness and repository identity

Git commit identity alone is insufficient because the worktree may be dirty,
Git may be unavailable, and the selected Go build context can change without a
commit. V1 should use a deterministic Gograph source fingerprint as the
authoritative structural snapshot identity.

`repository.source_fingerprint` is a 64-character lowercase hexadecimal
SHA-256 over a versioned,
canonical manifest containing:

1. the source-fingerprint schema marker and current source-policy version;
2. the normalized effective Go selection inputs that affect the graph
   (GOOS, GOARCH, cgo, compiler, build/tool/release tags, module mode and module
   path), excluding machine-specific absolute directory spellings;
3. the safely selected repository-relative Go file inventory in lexical order,
   with the SHA-256 of each exact file's bytes;
4. the exact safely read Go module/workspace/vendor metadata that affects
   package selection and type loading, identified by repository- or
   workspace-relative path;
5. the selection-policy inputs needed to reproduce exclusions.

The fingerprint's scope is the Go structural-analysis snapshot, not every byte
in the repository. A Markdown-only edit need not invalidate it. The precise
manifest format must be versioned and covered by golden tests before it becomes
public.

`analysis.graph_fingerprint` is the full SHA-256 of the exact trusted persisted
`graph.json` bytes used for the result. It identifies the artifact; it does not
replace the source fingerprint.

A result is `freshness: current` only if all of the following hold in one
validation operation:

- `.gograph/graph.json` is a confined regular file under the explicit
  repository root;
- graph schema and source-policy versions are supported;
- build metadata exists and every selected graph file has a content digest;
- current build-context and selected-inventory calculation completes without
  scanner errors;
- current inventory, metadata, and byte digests match the graph;
- the source fingerprint is unchanged when checked after predicate evaluation.

The source fingerprint is persisted in graph build metadata and compared with
the freshly computed manifest. Legacy mtime fallback is not sufficient for
machine validation. A legacy graph without the persisted source fingerprint or
complete digests returns `cannot_evaluate` and requires a rebuild.
Scanner errors make freshness `unknown`, not current. A source change during
evaluation returns `cannot_evaluate/graph_stale` rather than a result for a
mixed snapshot.

Git revision and dirty state may be reported when available, but Git failure
does not prevent validation when the source fingerprint is available. Scrinium
must bind its validator input to the explicit repository root and compare the
returned source and graph fingerprints on revalidation.

## Outcome and exit-code mapping

| Outcome | Meaning | Exit code |
| --- | --- | --- |
| `pass` | Gograph evaluated the selected structural predicate and it holds. | 0 |
| `fail` | Gograph evaluated the predicate with sufficient completeness to establish that it does not hold. | 1 |
| `cannot_evaluate` | Gograph could not produce a trustworthy yes/no result, or the request was invalid. | 2 |

Exit code is transport redundancy, not the source of truth. Consumers must
parse the document and require these exact combinations. A valid document with
an inconsistent exit code is an adapter-level `cannot_evaluate`. Unexpected
process failure or malformed output is also `cannot_evaluate`; it must not be
converted to `fail`.

## Stable reason codes

V1 should define this closed set initially:

| Reason | Outcome | Use |
| --- | --- | --- |
| `predicate_passed` | pass | Requested predicate holds. |
| `predicate_failed` | fail | Predicate was evaluated and does not hold when no narrower failure code applies. |
| `symbol_not_found` | fail or cannot_evaluate | Fail only when complete analysis establishes absence; otherwise cannot-evaluate. |
| `symbol_ambiguous` | cannot_evaluate | Exact identity resolves to multiple records. |
| `package_not_found` | fail or cannot_evaluate | Same completeness rule as symbol absence. |
| `relation_not_found` | fail or cannot_evaluate | Fail only when predicate-specific absence is conclusive. |
| `graph_missing` | cannot_evaluate | Trusted persisted graph does not exist. |
| `graph_invalid` | cannot_evaluate | Graph cannot be decoded or violates structural invariants. |
| `graph_schema_unsupported` | cannot_evaluate | Persisted graph schema is unsupported. |
| `source_policy_unsupported` | cannot_evaluate | Graph lacks the current source-policy marker. |
| `graph_stale` | cannot_evaluate | Source or selection inputs differ or changed during evaluation. |
| `precision_insufficient` | cannot_evaluate | Actual precision is below the binding requirement. |
| `analysis_incomplete` | cannot_evaluate | Build or predicate-specific coverage cannot establish yes/no. |
| `symbol_identity_unstable` | cannot_evaluate | Binding uses a machine-local fallback identity. |
| `unsupported_predicate` | cannot_evaluate | Predicate is outside the closed v1 set. |
| `unsupported_language` | cannot_evaluate | Reference language is not Go. |
| `repository_mismatch` | cannot_evaluate | Selected or resolved repository context does not match the request. |
| `invalid_request` | cannot_evaluate | Invocation or strict binding validation failed. |
| `internal_error` | cannot_evaluate | Unexpected Gograph failure without a trustworthy predicate result. |

The schema may add reason codes only in a new schema version or through an
explicit compatibility rule. Human messages are diagnostics, not stable API.

## Security constraints

- Resolve `--repo` explicitly, re-anchor persisted graph state to that trusted
  root, and reuse Gograph's rooted regular-file and source-policy checks.
- Never accept a repository path from the binding.
- Never invoke a shell.
- Do not accept arbitrary Gograph commands, query fragments, build tags,
  executable arguments, source paths, or environment overrides.
- Treat all binding strings as data and validate them against closed schemas
  before graph lookup.
- Do not automatically run `gograph build`; validation is read-only and missing
  state is uncertainty.
- Do not read source outside the selected repository/workspace confinement
  already enforced by Gograph.
- Do not expose full source, compiler output, environment values, dependency
  cache paths, or unbounded diagnostics in the result.
- Recheck repository fingerprint after evaluation to avoid returning evidence
  across a concurrent source change.

## Conformance tests

Gograph should publish hermetic fixture tests for:

- `gograph.version.v1`, including the existing injected version value;
- strict `gograph.binding.v1` decoding, duplicate keys, unknown fields,
  malformed/trailing JSON, invalid enums, and unstable IDs;
- strict `gograph.validation.v1` shape and deterministic ordering;
- one pass and one evaluated fail for each v1 predicate;
- `cannot_evaluate` for missing, malformed, unsupported-policy, and unsupported
  graph schemas;
- stale graph, scanner error, missing digest, changed build context, and a
  repository change during evaluation;
- AST, precise, precise-fallback, partial, and unknown analysis states;
- insufficient precision and predicate-specific incomplete call resolution;
- exact case-sensitive symbol resolution, duplicate/ambiguous IDs, missing
  symbols under complete and incomplete analysis, and package-name collisions;
- repository root re-anchoring, symlink/special-file refusal, and repository
  mismatch;
- source and graph fingerprint determinism, metadata changes, dirty Git state,
  and operation without Git;
- bounded diagnostics and no human prose or logs in JSON stdout;
- exit 0/pass, exit 1/fail, and exit 2/cannot-evaluate or invalid invocation;
- cancellation or repository mutation during a deliberately blocked
  validation fixture, where applicable.

Fixtures for calls must distinguish direct static calls, CHA interface
candidates, unresolved function values, synthetic forwarding edges, and an
absent edge. Implementation fixtures must cover value and pointer method sets,
embedded interfaces, and same-named types in different packages.

## Implementation

The implementation follows these boundaries:

1. Transport-neutral request, result, strict decoding, reason, and evidence
   types in a focused internal validation package. Accept `context.Context`
   through the evaluator and freshness work. Do not put JSON rendering in
   graph/search algorithms.
2. Exact, case-sensitive predicate evaluation uses `internal/graph` data.
   Reuse parser and precise records, but do not call the existing fuzzy public
   search helpers as the validation authority.
3. Strict persisted-graph loading, source-snapshot and exact
   graph-artifact fingerprints, scanner-error propagation, and pre/post
   freshness checks. Keep existing navigation compatibility unchanged.
4. `version --json` and the CLI-only `validate` adapter provide deterministic
   JSON rendering and the outcome/exit mapping above.
5. Conformance fixtures cover strict schemas, predicate completeness,
   fingerprints, JSON-only output, and exit codes.
6. README, help, capabilities, and release notes document the public contract.
   Add MCP parity only if a real MCP consumer is identified; the evaluator must
   already be reusable without CLI types.

This is more than a cosmetic compatibility flag. In particular,
predicate-specific completeness and a stable source fingerprint do not exist
today. Implementing only a JSON wrapper around current query commands would
produce false confidence and should be rejected.

## Limitations Scrinium must know

- Gograph validates structural predicates only. It cannot verify runtime or
  business behavior.
- A current graph represents one Go build context. Files excluded by build
  constraints, generated-file policy, module selection, or ignore rules are
  outside the result.
- V1 validation requires every applicable local module/workspace source root
  to remain beneath the explicit `--repo` root. A build context that would read
  a parent or sibling source tree returns `cannot_evaluate/repository_mismatch`
  rather than widening the validator's repository authority.
- `precise` means successful Go type/SSA/CHA enrichment for the selected
  repository context; it does not mean runtime-complete.
- A CHA call edge can be a possible interface target rather than a definitely
  executed call.
- Positive and negative call evidence must respect unresolved dispatch limits;
  uncertainty is `cannot_evaluate`, never an invented fail.
- V1 binds only module-backed in-repository Go symbols. External symbols,
  absolute-path fallback identities, unnamed types, and other languages are
  unsupported.
- Reachability is intentionally absent from v1.
- Reducing a natural-language claim to one structural predicate is a Scrinium
  modeling decision. Gograph proves only the predicate it was asked to
  evaluate, not the surrounding claim.
