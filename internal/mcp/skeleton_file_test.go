package mcp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
)

func TestMCPSkeletonFileSelection(t *testing.T) {
	g := &graph.Graph{
		Root: t.TempDir(),
		Files: []graph.FileNode{
			{Path: "internal/api/api.go", PackageName: "api"},
			{Path: "tools/devseed/main.go", PackageName: "main"},
		},
		Symbols: []graph.SymbolNode{
			{Name: "Serve", Kind: graph.KindFunction, PackageName: "api", File: "internal/api/api.go", Signature: "func Serve()"},
			{Name: "main", Kind: graph.KindFunction, PackageName: "main", File: "tools/devseed/main.go", Signature: "func main()"},
		},
	}
	handler := setupHandlers(t, g)["gograph_skeleton"]
	text := callTool(t, handler, map[string]any{"file": "tools/devseed/main.go"})
	if !strings.HasPrefix(text, "package main\n") || !strings.Contains(text, "func main()") || strings.Contains(text, "Serve") {
		t.Fatalf("wrong file skeleton: %s", text)
	}
	text = callTool(t, handler, map[string]any{})
	if !strings.Contains(text, "func Serve()") || !strings.Contains(text, "func main()") {
		t.Fatalf("repository skeleton changed: %s", text)
	}
	for _, file := range []any{"missing.go", "main.go", "", 42, "../main.go", "/tmp/main.go"} {
		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]any{"file": file}
		result, err := handler(context.Background(), request)
		if err != nil || result == nil || !result.IsError {
			t.Fatalf("invalid selector %v did not return a tool error: %v %v", file, result, err)
		}
	}
}
