package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
)

func TestMCPComposedQueriesRefreshTheirSnapshot(t *testing.T) {
	previous := ExposeToolsForTesting
	handlers := make(map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error))
	ExposeToolsForTesting = handlers
	t.Cleanup(func() { ExposeToolsForTesting = previous })
	makeGraph := func(path string) *graph.Graph {
		return &graph.Graph{
			Symbols: []graph.SymbolNode{
				{ID: "Target", Name: "Target", Kind: graph.KindFunction},
				{ID: "Handler", Name: "Handler", Kind: graph.KindFunction},
			},
			Calls:  []graph.CallEdge{{CallerSymbolID: "Handler", CallerName: "Handler", CalleeRaw: "Target"}},
			Routes: []graph.HTTPRoute{{Method: "GET", Path: path, Handler: "Handler"}},
		}
	}
	current := makeGraph("/before")
	NewServer(current, nil, nil, nil, "test", ServerOptions{RefreshContext: func(context.Context) (*graph.Graph, error) { return current, nil }})
	for _, path := range []string{"/before", "/after"} {
		current = makeGraph(path)
		for _, name := range []string{"gograph_plan", "gograph_review", "gograph_plan", "gograph_review"} {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]any{"symbol": "Target"}
			result, err := handlers[name](context.Background(), request)
			if err != nil || result == nil || result.IsError {
				t.Fatalf("%s failed: %v", name, err)
			}
			var response struct {
				Routes []string `json:"routes"`
			}
			content, ok := result.Content[0].(mcp.TextContent)
			if !ok {
				t.Fatalf("%s returned no text result", name)
			}
			if err := json.Unmarshal([]byte(content.Text), &response); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(response.Routes, []string{"GET " + path}) {
				t.Fatalf("%s returned stale or missing route: %v", name, response.Routes)
			}
		}
	}
}
