package cli

import (
	"context"

	"github.com/ozgurcd/gograph/internal/baseline"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/rootfind"
)

// BuildBaselineGraphFromGitRef loads a saved graph or extracts a Git ref using
// the current gograph project root. It is kept as the CLI-facing compatibility
// wrapper; callers with a loaded graph should pass its root explicitly below.
func BuildBaselineGraphFromGitRef(ref string, buildGraph func(string) (*graph.Graph, error)) (*graph.Graph, error) {
	return BuildBaselineGraphFromGitRefAtRoot(rootfind.FindRepositoryRoot(), ref, buildGraph)
}

func BuildBaselineGraphFromGitRefAtRoot(root, ref string, buildGraph func(string) (*graph.Graph, error)) (*graph.Graph, error) {
	return baseline.Build(context.Background(), root, ref, buildGraph)
}
