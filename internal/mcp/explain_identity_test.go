package mcp_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestMCPExplainAmbiguityIsNativeAndMatchesSearch(t *testing.T) {
	g := &graph.Graph{Root: t.TempDir(), Symbols: []graph.SymbolNode{
		{ID: "example/a::Save", Name: "Save", Kind: graph.KindFunction, File: "a.go"},
		{ID: "example/b::Save", Name: "Save", Kind: graph.KindFunction, File: "b.go"},
	}}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"symbol": "Save"}
	result, err := setupHandlers(t, g)["gograph_explain"](context.Background(), req)
	if err != nil || result == nil || result.IsError {
		t.Fatalf("explain: %+v %v", result, err)
	}
	want := search.Explain(g, "Save")
	for name, value := range map[string]any{"structured": result.StructuredContent, "text": json.RawMessage(result.Content[0].(mcp.TextContent).Text)} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var got search.ExplainResult
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(&got, want) {
			t.Fatalf("%s diverged from shared analysis: %+v", name, got)
		}
	}
	if result.Meta == nil {
		t.Fatal("native content lost graph provenance")
	}
}
