package mcp_test

import (
	"regexp"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	mcppkg "github.com/ozgurcd/gograph/internal/mcp"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestTaskIndexNamesRegisteredMCPTools(t *testing.T) {
	tools := mcppkg.NewServer(&graph.Graph{}, mockRebuild(&graph.Graph{}), mockBuildGraph(), mockBuildBaseline(), "dev").ListTools()
	name := regexp.MustCompile(`gograph_[a-z_]+`)
	for _, entry := range search.TaskIndex {
		for _, tool := range name.FindAllString(entry.MCP, -1) {
			if _, ok := tools[tool]; !ok {
				t.Errorf("task %q names unregistered MCP tool %q", entry.Task, tool)
			}
		}
	}
}
