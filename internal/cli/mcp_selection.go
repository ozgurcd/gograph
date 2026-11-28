package cli

import (
	"os"
	"path/filepath"

	"github.com/ozgurcd/gograph/internal/search"
)

// adoptRecordedMCPSelection gives an MCP scope started without --tags (and
// without GOFLAGS -tags) the build tags its persisted graph was built with,
// like CLI queries. Otherwise a --tags=integration graph reads as stale and
// the server rebuilds it in the default context, dropping the tagged files.
// Excluded directories stay an explicit startup choice.
func adoptRecordedMCPSelection(options mcpOptions) mcpOptions {
	if len(options.Tags) > 0 || search.GOFLAGSSetsBuildTags(os.Getenv("GOFLAGS")) {
		return options
	}
	absRoot, err := filepath.Abs(projectRoot(options.Root))
	if err != nil {
		return options
	}
	g, err := loadGraphIgnoringRev(absRoot)
	if err != nil || g.Build == nil || g.Build.Selection == nil {
		return options
	}
	if tags := g.Build.Selection.BuildTags; len(tags) > 0 {
		options.Tags = append([]string(nil), tags...)
	}
	return options
}
