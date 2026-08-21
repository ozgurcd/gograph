package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/ozgurcd/gograph/internal/buildctx"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/repositoryfingerprint"
)

func refreshCompleteGraphSourceFingerprint(root string, g *graph.Graph, config buildctx.Config) error {
	if g == nil || g.Build == nil || !g.Build.Complete {
		return nil
	}
	paths := make([]string, 0, len(g.Files))
	for _, file := range g.Files {
		name := file.Path
		if !filepath.IsAbs(name) {
			name = filepath.Join(root, name)
		}
		paths = append(paths, name)
	}
	identity, err := repositoryfingerprint.Compute(context.Background(), root, config, paths)
	if err != nil {
		return fmt.Errorf("refresh repository source fingerprint: %w", err)
	}
	g.Build.SourceFingerprint = identity.Fingerprint
	return nil
}
