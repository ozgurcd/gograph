package cli

import "github.com/ozgurcd/gograph/internal/graph"

func currentPolicyGraph(g *graph.Graph) *graph.Graph {
	if g.Build == nil {
		g.Build = &graph.BuildMetadata{}
	}
	g.Build.SourcePolicyVersion = graph.CurrentSourcePolicyVersion
	return g
}
