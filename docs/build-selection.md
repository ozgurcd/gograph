# Directory exclusions

Use directory exclusions when unrelated packages prevent precise analysis:

```bash
gograph build . --precise --strict --exclude-dirs=legacy,examples/broken
gograph mcp . --exclude-dirs=legacy,examples/broken
gograph stale --exclude-dirs=legacy,examples/broken --json
```

`--exclude-dirs dir1,dir2` and repeated flags are also accepted. Values are
literal paths relative to the analyzed repository, not the shell's working
directory. `legacy` excludes `legacy/` and its descendants, but not
`legacy-other/` or `service/legacy/`. Leading `./`, trailing `/`, ordering,
duplicates, and redundant children are normalized. Absolute paths, `..`
components, globs, empty entries, and excluding the repository root are rejected.
Use forward slashes in paths, including on Windows. Nonexistent paths are allowed
so a selection can survive branch changes.

Exclusions apply before AST parsing and before selecting precise production and
test packages. Precise analysis loads explicit remaining package directories
instead of `./...`. The remaining code must still compile. If an included package
imports an excluded package, Go still loads that dependency; exclusion cannot
hide its type errors. Exclusions do not disable source-path, symlink, module,
workspace, or other Go-input safety checks. Invalid required `go.mod` / `go.work`
metadata must still be corrected.

## Symlinked skills and tooling directories

Starting with v1.7.2, directory symlinks **beneath an explicitly excluded real
directory** no longer block precise builds or MCP refreshes. For example:

```bash
gograph build . --precise --strict --exclude-dirs=.claude/skills
gograph mcp /absolute/path/to/project --exclude-dirs=.claude/skills
```

This supports `.claude/skills/example -> ../../.agents/skills/example` and
links to external skill directories. Gograph does not traverse those targets;
a temporary Go deletion overlay makes the links absent during both production
and test package loading. A direct or transitive import through such a link
cannot supply Go code. Ordinary real excluded packages can still be imported
and must compile as described above.

The exclusion itself must name a real directory: exclude the parent of a link,
not the link itself. Linked Go/C/assembly files and Go metadata still fail
safety checks, including inside exclusions. Links outside the selected
exclusions still block precision. A separately configured Go `-overlay` in
`GOFLAGS` or `GOENV` cannot be combined with this masking and is rejected with
guidance, not silently overridden. No target links are removed or rewritten.
The standalone CLI/MCP `doc` operation keeps its independent strict preflight;
it does not apply build exclusions or this temporary overlay.

For Codex registration, see [Codex MCP setup](codex-integration.md).

## Freshness and refresh

The normalized list is stored in `build.selection.exclude_dirs` and bound into
both build-context and source fingerprints. Completeness describes the selected
code, not the excluded trees. Rebuild without the flag to restore the full
selection. Excluded source edits alone do not invalidate the selected graph;
as with other unindexed dependencies, rebuild after modifying an excluded package
that included code imports. Required module/build metadata remains fingerprinted.

CLI queries and ordinary `stale` checks describe the persisted selection. A
CLI query that finds the graph stale rebuilds it with that selection (and its
recorded tags and precision) before answering, unless `--no-refresh` or
`GOGRAPH_NO_REFRESH=1` is set.
`doctor --json` also reports and checks that selection in `repository.exclude_dirs`.
`stale --exclude-dirs=...` checks a different explicit selection when needed.
If using build tags, continue supplying the matching `--tags` value as well.

For MCP, supply the same exclusions at server startup. Automatic startup builds,
in-memory refreshes, optional `--persist-refresh` publication, and baselines keep
that startup selection. `gograph_capabilities.analysis_build_context.exclude_dirs`
reports it. Exclusions are a server-level setting, not per-query tool arguments.
Restart an existing MCP server after changing its startup configuration.

## Workspace members

Declare member-relative exclusions in `.gograph-workspace.yml`:

```yaml
schema_version: gograph.workspace-manifest.v1
name: example
repositories:
  - id: api
    path: services/api
    precision: precise
    exclude_dirs: [legacy, examples/broken]
```

Build the member with matching `--exclude-dirs`, or use
`gograph workspace build --refresh-members` to apply the manifest selection.
Workspace status and queries use the same selection through CLI and MCP.
Changing it invalidates member/overlay freshness. The normal workspace build
still does not mutate members without `--refresh-members`.

Machine validation does not silently adopt exclusions from an artifact as a
full-repository selection. A validation request that expects the full repository
rejects a graph built with exclusions as a selection mismatch.
