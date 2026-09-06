# Query contracts for CLI and MCP

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
declarations. Impact and other `--uncommitted` consumers explicitly refuse such
selections, ambiguous identities, and new declarations absent from the graph;
they do not report an empty successful result. Use `changes --git REF` to inspect
the complete declaration census. Rebuild before traversing newly added symbols;
rebuilding does not recover historical caller evidence for deletions.

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
