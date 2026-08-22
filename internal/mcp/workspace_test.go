package mcp_test

import (
	"context"
	"testing"

	protocol "github.com/mark3labs/mcp-go/mcp"
	mcppkg "github.com/ozgurcd/gograph/internal/mcp"
)

func TestWorkspaceMCPContractHasFourReadOnlyTools(t *testing.T) {
	handlers := make(map[string]func(context.Context, protocol.CallToolRequest) (*protocol.CallToolResult, error))
	mcppkg.ExposeWorkspaceToolsForTesting = handlers
	t.Cleanup(func() { mcppkg.ExposeWorkspaceToolsForTesting = nil })
	server := mcppkg.NewWorkspaceServer(t.TempDir(), "dev")
	tools := server.ListTools()
	want := []string{"gograph_workspace_status", "gograph_workspace_query", "gograph_workspace_path", "gograph_workspace_impact"}
	if len(tools) != len(want) || len(handlers) != len(want) {
		t.Fatalf("workspace tools = %v", tools)
	}
	for _, name := range want {
		tool, ok := tools[name]
		if !ok {
			t.Errorf("missing %s", name)
			continue
		}
		annotations := tool.Tool.Annotations
		if annotations.ReadOnlyHint == nil || !*annotations.ReadOnlyHint || annotations.DestructiveHint == nil || *annotations.DestructiveHint {
			t.Errorf("%s annotations = %+v", name, annotations)
		}
	}
}
