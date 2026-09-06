package search

import (
	"context"
	"fmt"
	"go/token"
	"path/filepath"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/moduleinventory"
	sourceparser "github.com/ozgurcd/gograph/internal/parser"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

func buildDeclarationBaseline(ctx context.Context, root string, selections ...*graph.BuildSelection) (*graph.Graph, error) {
	var selection *graph.BuildSelection
	if len(selections) > 0 {
		selection = selections[0]
	}
	config, paths, errs := changesSelection(ctx, root, selection)
	if len(errs) > 0 {
		return nil, fmt.Errorf("baseline source selection: %v", errs)
	}
	g := &graph.Graph{Root: root, Build: &graph.BuildMetadata{Selection: graph.CaptureBuildSelection(config.BuildContext())}}
	for _, path := range paths {
		g.Packages = append(g.Packages, graph.PackageNode{Dir: filepath.Dir(graphFileRelative(root, path))})
	}
	modules, err := moduleinventory.Discover(root, g.Packages)
	if err != nil {
		return nil, err
	}
	g.Modules = modules
	reader, err := sourcefs.Open(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rel := graphFileRelative(root, path)
		data, err := reader.ReadFile(rel)
		if err != nil {
			return nil, err
		}
		parsed, err := sourceparser.ParseSource(token.NewFileSet(), rel, data, rel, changesImportPath(g, root, rel))
		if err != nil {
			return nil, err
		}
		parsed.File.ContentDigest = graph.SourceDigest(data)
		g.Files = append(g.Files, parsed.File)
		g.Symbols = append(g.Symbols, parsed.Symbols...)
	}
	return g, nil
}
