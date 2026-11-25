package search

import (
	"context"
	"os"
	"strings"

	"github.com/ozgurcd/gograph/internal/buildctx"
	"github.com/ozgurcd/gograph/internal/graph"
)

// StaleForQuery checks a graph for a query command. Explicit tags (CLI
// --tags) select the build context. Without them, when GOFLAGS sets no build
// tags, the graph's recorded tags are used, so a graph built with
// --tags=integration is not reported stale just because the querying shell
// lacks GOFLAGS=-tags=integration. A process that sets its own tags keeps
// them, and a tag set that differs from the recorded one is stale.
func StaleForQuery(g *graph.Graph, root string, explicitTags []string) StaleResult {
	var recorded *graph.BuildSelection
	if g != nil && g.Build != nil {
		recorded = g.Build.Selection
	}
	tags := explicitTags
	if len(tags) == 0 {
		if recorded == nil || len(recorded.BuildTags) == 0 || GOFLAGSSetsBuildTags(os.Getenv("GOFLAGS")) {
			return Stale(g, root)
		}
		tags = recorded.BuildTags
	}
	var excluded []string
	if recorded != nil {
		excluded = recorded.ExcludeDirs
	}
	if _, err := buildctx.NormalizeBuildTags(tags); err != nil {
		return StaleResult{IsStale: true, BuildContextChanged: true}
	}
	if _, err := buildctx.NormalizeExcludeDirs(excluded); err != nil {
		return StaleResult{IsStale: true, BuildContextChanged: true}
	}
	// As for recorded exclusions, a toolchain query failure falls back to the
	// default context with these tags; the freshness comparison reports it.
	config, _ := buildctx.ResolveOrDefaultWithOptions(context.Background(), root, buildctx.ResolveOptions{BuildTags: tags, ExcludeDirs: excluded})
	return StaleWithConfig(g, root, config)
}

// GOFLAGSSetsBuildTags reports whether a GOFLAGS value sets -tags.
func GOFLAGSSetsBuildTags(goflags string) bool {
	for _, field := range strings.Fields(goflags) {
		name, _, _ := strings.Cut(strings.TrimLeft(field, "-"), "=")
		if name == "tags" && strings.HasPrefix(field, "-") {
			return true
		}
	}
	return false
}
