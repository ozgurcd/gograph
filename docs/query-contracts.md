# Query contracts for CLI and MCP

## File-qualified selectors

`path/to/file.go:Name` and `path/to/file.go:Receiver.Method` select indexed
declarations, as do the import-qualified forms printed by an ambiguous `source`
answer. CLI and MCP support them in query, node, source, context, callers,
callees, impact, explain, explore, plan, review, risk, identity, fields,
interfaces, implementers, mocks, tests (direct and transitive), and coverage.

Named limits: embeds, constructors, literals, returnusage, mutate, path and
endpoint reject file-qualified selectors. Their relationship records use
lexical names and cannot reliably bind that file's declaration; use the
documented name/type/field/route form. `usages` supports file-qualified variables
and constants, but returns a named limit for file-qualified types because type
references are lexical. This avoids presenting an empty answer as a complete
identity-based census. These limits also appear in capabilities.

Package selectors (focus, public, imports, deps, dependents, globals), route/SQL
filters, and lexical filters (envs, concurrency, errors, errorflow/trace, flow,
httpcalls) are not declaration selectors. The external `doc` command accepts
Go import/symbol syntax, not repository file selectors. Workspace selectors
use their separate member-and-stable-ID contract.

## Fields, routes and file lists

`query TokenTTLSecs` includes declared struct fields. `query --no-tests`
excludes rows in `_test.go` files before pagination; MCP uses `no_tests=true`.
`mutate Type.Field` in a precise graph recovers the type of assigned fields on
returned values. AST-only type inference remains limited for those values.
Known fields with no indexed mutation sites return a `mutation-resolution limit`
row, not empty success. Arbitrary pointer-argument writes (including database
Scan), aliases and reflective effects are not resolved. Capabilities expose this
boundary as `mutation_resolution`; no indexed mutation is not proof of immutability.

Route registrations with unresolved paths remain visible with an explicit
diagnostic. AST analysis resolves local constants and concatenations; precise
analysis resolves cross-file and imported constants. ServeMux method patterns
such as `"GET " + AdminPath` report the method separately from the path.
Runtime path expressions remain unresolved; analysis executes no target code.

`tests SYMBOL --transitive --files-only` prints each returned file once, sorted,
without test names or call paths. MCP's transitive report retains the same file
set in its test rows. Missing or ambiguous selectors are refused in files-only
mode, so an empty list does not hide a selector error.

## Freshness check context

Graph-state output and capabilities include `freshness_context`. CLI compares
the saved graph to the querying process's effective Go context, including
GOFLAGS; MCP uses its startup context and explicit startup tags when supplied.
Recorded directory exclusions remain in force. A different build context can
therefore make unchanged source appear stale. This diagnostic preserves that
fail-closed comparison; it does not automatically reuse saved tags or print
environment values. Reproduce the build's context when asking freshness
questions. MCP refreshes in its chosen startup context.

CLI and MCP share search implementations. CLI normally reads the persisted
repository graph; MCP refreshes before source-analysis queries. Compare results
from the same graph content and filters when testing parity. A running MCP
process does not load a replacement binary: restart it after upgrading and
check the `version` returned by `gograph_capabilities`.

## Bounded result lists

These commands share result pagination with their `gograph_<command>` tools:

`query`, `focus`, `node`, `callers`, `callees`, `implementers`, `mocks`,
`fields`, `envs`, `interfaces`, `concurrency`, `dependents`, `public`,
`embeds`, `imports`, `impact`, `errors`, `httpcalls`, `mutate`, `constructors`,
`usages`, `returnusage`, `literals`, `schema`, `globals`, `fixtures`, `orphans`,
and `boundaries`.

```sh
gograph query Handler --limit 100 --json
gograph query Handler --cursor '<next_cursor>' --json
```

The MCP equivalents use `limit` and `cursor`. Default limit is 100; valid
limits are 1–200. The native page budget is 16 KiB, allowing room for both MCP
JSON text and structured content, including additional JSON escaping and
provenance, within a 64 KiB response. The byte budget may return fewer rows
than the requested limit.

Every page has `command`, `status`, `limit`, `offset`, `count`, `total`,
`returned`, `truncated`, `next_cursor`, and `results`. `count` equals
`returned`, not `total`. Empty pages contain an empty result array and all
pagination fields. The last page has `truncated=false` and an empty cursor.
Never interpret a single page as a complete census without checking these fields.

MCP uses native `gograph.results.v1` JSON text and structured content. CLI
`--json` retains its standard envelope and result array, exposing the same
pagination fields directly. Equivalent CLI and MCP queries can exchange cursors.
CLI text prints the page and a continuation instruction. `--files-only` remains
a complete deduplicated file census; it is not a dump of every result row.
Do not combine row pagination with `--files-only` or `--mermaid`.

Cursors bind to graph content, command, and filtered result identities. They
are not reusable offsets. Graph or result changes reject an old cursor with
restart guidance. Changing page size is allowed. No result cell is silently
shortened: an individual oversized row is refused with narrowing/source guidance.

`routes` and `sql` retain their specialized `gograph.routes.v1` and
`gograph.sql.v1` pages and 64 KiB native budgets. Their cursors also bind to
the graph snapshot and normalized filters. Other specialized commands retain
their documented schemas and limits; they do not implicitly gain these flags.

## Identity, certainty, and changes

`impact` returns canonical `stable_id` and `resolution_status` (`exact` or
`possible`). Default repository impact conservatively includes possible paths,
but labels them; `--exact-only` / MCP `exact_only=true` excludes any path that
depends on possible evidence. An independent all-exact path can establish an
exact result. Resolved call identities never fall back to another symbol's
matching display name. Mermaid uses dotted edges for possible paths and does
not silently stop at 20 hops. Workspace traversal retains its separate,
exact-by-default `--include-possible` policy.

`explain` uses `gograph.explain.v1`. Ambiguous names return `status=ambiguous`
and sorted `candidates`; no arbitrary candidate gets a narrative. Select a
canonical identity to disambiguate. Supporting SQL/environment/concurrency facts
must match the selected declaration's file and range. Resolved call/test targets
take precedence over raw names; unresolved references require a unique lexical
package/import match. Ambiguous references and dynamic route factories are not
presented as established handler relationships.

`changes` uses `gograph.changes.v1`. It compares declaration fingerprints,
including function bodies, and ignores formatting/comment-only changes to a
declaration. A changed file does not make every symbol in it modified. Removed
declarations can be detected inside a surviving file. Const-group fingerprints
are conservative because implicit values and `iota` depend on their group.

Statuses are `new`, `modified`, `deleted`, `excluded`, or `unknown`. An existing
file no longer selected for analysis is excluded, not deleted. Unsafe paths and
parse failures are diagnostics, not evidence of deletion. Legacy baselines
without declaration digests cannot prove which declarations changed and report
unknown instead. `--git REF` / MCP `git_ref` builds a confined declaration
baseline from the reference without compiling application code.

New graph build metadata records platform/compiler/CGO selection and effective
build/tool/release tags, without serializing filesystem authority. Both modes
reuse that selection instead of silently inheriting a different `GOFLAGS`.
An older persisted baseline without selection metadata is explicitly partial.
Current module ownership is rediscovered: changing a module path or introducing
a nested module can produce removed/added identities even when Go source bytes
are unchanged. `package_name` distinguishes external-test and renamed-package
declarations. Repeated `init`/blank declarations are retained, and changing
their initialization order is reported. A source/selection race invalidates
the comparison instead of publishing a mixed-snapshot change list.

Aggregate evaluation is `complete`, `partial`, or `cannot_evaluate`. CLI exits
2 for an incomplete evaluation; MCP exposes the same evaluation and diagnostics
in its native result. Do not treat incomplete output as a clean change census.
Change-based impact refuses incomplete comparisons. `--uncommitted` consumers
use the same declaration comparison against `HEAD`, including untracked selected
Go files, instead of matching Git hunk lines to old symbol ranges. Nested analysis
roots do not include sibling repositories' changes.

Current-graph traversal cannot reconstruct historical callers of deleted
declarations. Impact and other traversal consumers explicitly refuse such
selections, ambiguous identities, and new declarations absent from the graph;
they do not report an empty successful result. Use `changes --git REF` to inspect
the complete declaration census. Rebuild before traversing newly added symbols;
rebuilding does not recover historical caller evidence for deletions.

`review --uncommitted` is a scoped exception: it includes the declaration census
from `changes --git HEAD`, names deleted declarations, and states that historical
callers, tests and risk are not evaluated. Surviving declarations retain their
normal current-graph review. Incomplete comparisons still fail.

## HTTP extraction and workspace resolution

See [dynamic HTTP URL bases](workspaces.md#dynamic-http-url-bases) for the shared
HTTP extraction and workspace-resolution contract: explicit lexical-base
mappings, static suffixes, scope-isolated service ownership, possible-only request
construction, filtered `http_unresolved` query diagnostics, and verified status
counts. Unresolved evidence never participates in traversal. Member graphs need
`net_http_v2` extraction facts and overlays use `http-contract-v2`.

## Request snapshots and cancellation


MCP serializes graph refresh/publication, not independent query execution.
Each query retains its selected immutable graph and graph-state provenance
throughout the request. Refreshing another request cannot change that identity.
Cancellation reaches Go build-context resolution, package loading, and checks
between parsing/enrichment stages. Long non-context-aware phases finish before
their next check; cancellation is cooperative, not an immediate memory kill.

Canceled build work is not adopted as a successful degraded graph. Artifact
publication checks cancellation while acquiring its lock and before committing
staged artifacts. Once a commit starts, its artifact set finishes; cancellation
does not promise rollback of an operation already committing.

SQL classification, sorted routes, reverse call adjacency, and test attribution
indexes are reused within an immutable query snapshot. The server retains its
current fingerprint's indexes; in-flight queries may temporarily retain an
older snapshot. This is not a persistent multi-branch cache or an unbounded
query-result memo table.

Workspace loads cache only successful deterministic-overlay verification
receipts, in a fixed 16-entry cache. Every load still verifies current member
source paths, freshness, module ownership, and artifact bytes. A cache hit
cannot authorize a stale, substituted, or unsafe member.


## Source and context reads

CLI `source SYMBOL --json` and `context SYMBOL --json` retain their existing
version-1 CLI envelopes. MCP `gograph_source` and `gograph_context` now supply
`gograph.read.v1` structured content in place of the provenance-only legacy
companion. This is a new read schema, not a silent change to
`gograph.mcp-result.v1`. Existing MCP text answers are preserved.

The read schema carries `command`, `status` (`ok`, `refused`, or `partial`),
`graph_state`, and the answer: `source` for source; the existing context fields
(`node`, `nodes`, `source`, `role`, callers, callees, tests) for context.
A refused read has `reason` and MCP `isError: true`. A missing context is now a
named refusal on both transports, not a successful empty object. A partial
context retains its node evidence and carries `source_error` plus `reason`.

Source selection refuses ambiguity and lists candidate identities and locations;
select a fully qualified identity to disambiguate. Source reads retain the
regular, repository-confined .go-file boundary. Missing files, unsafe paths,
and invalid indexed line ranges are explicit refusals. The complete source block
has a 65536-byte budget; an oversized block is refused with its actual byte size
and the limit, never truncated. AST precision can serve source; precise analysis
is not required. Metadata or precision alone never proves that source is readable.

CLI and MCP graph state include additive `read_diagnostic` when a read refuses
or a context's indexed node cannot supply source. This reports the finding that
an indexed symbol could not be read without mislabeling analysis precision or
freshness. It is scoped to that request, not a claim that every indexed source
file was audited. Non-read tools retain their existing schemas and behavior.

After upgrading, restart MCP servers and check `gograph_capabilities.version`.
Consumers that prefer structured content must accept `gograph.read.v1`; legacy
text consumers retain the existing source/context success content.
