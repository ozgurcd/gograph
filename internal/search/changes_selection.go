package search

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/ozgurcd/gograph/internal/buildctx"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/moduleinventory"
	"github.com/ozgurcd/gograph/internal/repositoryfingerprint"
	"github.com/ozgurcd/gograph/internal/scanner"
)

func changesSelection(ctx context.Context, root string, selection *graph.BuildSelection) (buildctx.Config, []string, []error) {
	if err := ctx.Err(); err != nil {
		return buildctx.Config{}, nil, []error{err}
	}
	config, configErr := buildctx.ResolveOrDefault(ctx, root)
	if selection != nil {
		config = config.WithBuildContext(selection.Apply(config.BuildContext()))
	}
	paths, errs := scanner.WalkWithConfig(root, config)
	if configErr != nil {
		errs = append(errs, fmt.Errorf("changes build context: %w", configErr))
	}
	if err := ctx.Err(); err != nil {
		errs = append(errs, err)
	}
	return config, paths, errs
}

func changesCurrentInventory(root string, paths []string, previous *graph.Graph) (*graph.Graph, error) {
	current := &graph.Graph{}
	for _, path := range paths {
		current.Packages = append(current.Packages, graph.PackageNode{Dir: filepath.Dir(graphFileRelative(root, path))})
	}
	modules, err := moduleinventory.Discover(root, current.Packages)
	if err != nil {
		return nil, err
	}
	current.Modules = modules
	// Preserve pre-module/handcrafted graph naming only if neither side has
	// module authority. A removed or renamed go.mod must not retain old IDs.
	if len(modules) == 0 && len(previous.Modules) == 0 {
		current.Symbols, current.Packages = previous.Symbols, previous.Packages
	}
	return current, nil
}

func graphSelection(g *graph.Graph) *graph.BuildSelection {
	if g != nil && g.Build != nil {
		return g.Build.Selection
	}
	return nil
}

func verifyChangesObservation(ctx context.Context, root string, selection *graph.BuildSelection, fingerprint string) error {
	config, paths, errs := changesSelection(ctx, root, selection)
	if len(errs) > 0 {
		return fmt.Errorf("verify source after declaration comparison: %v", errs)
	}
	current, err := repositoryfingerprint.Compute(ctx, root, config, paths)
	if err != nil {
		return fmt.Errorf("verify source after declaration comparison: %w", err)
	}
	if current.Fingerprint != fingerprint {
		return fmt.Errorf("source selection or content changed during declaration comparison; retry on a stable checkout")
	}
	return nil
}
