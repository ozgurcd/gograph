package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	protocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	workspacegraph "github.com/ozgurcd/gograph/internal/workspace"
)

// ExposeWorkspaceToolsForTesting allows contract tests to invoke workspace
// handlers without starting stdio transport.
var ExposeWorkspaceToolsForTesting map[string]func(context.Context, protocol.CallToolRequest) (*protocol.CallToolResult, error)

func NewWorkspaceServer(root, version string) *server.MCPServer {
	s := server.NewMCPServer("gograph-workspace", version, server.WithToolCapabilities(true))
	add := func(tool protocol.Tool, handler func(context.Context, protocol.CallToolRequest) (*protocol.CallToolResult, error)) {
		readOnly, destructive, idempotent, openWorld := true, false, true, false
		tool.Annotations.ReadOnlyHint = &readOnly
		tool.Annotations.DestructiveHint = &destructive
		tool.Annotations.IdempotentHint = &idempotent
		tool.Annotations.OpenWorldHint = &openWorld
		s.AddTool(tool, handler)
		if ExposeWorkspaceToolsForTesting != nil {
			ExposeWorkspaceToolsForTesting[tool.Name] = handler
		}
	}

	statusTool := protocol.NewTool("gograph_workspace_status",
		protocol.WithDescription("Inspect every configured member graph and the derived workspace overlay. Returns per-member availability, freshness, fingerprints, analysis capabilities, advisory Git revision, and aggregate complete/partial/cannot_evaluate state. Read-only; never refreshes members or writes the overlay."),
	)
	add(statusTool, func(ctx context.Context, _ protocol.CallToolRequest) (*protocol.CallToolResult, error) {
		manifest, _, err := workspacegraph.LoadManifest(root)
		if err != nil {
			return protocol.NewToolResultError(err.Error()), nil
		}
		return workspaceJSONResult(workspacegraph.InspectStatus(ctx, root, manifest))
	})

	queryTool := protocol.NewTool("gograph_workspace_query",
		protocol.WithDescription("Search symbols, packages, modules, and HTTP contracts across one resolution scope. Repository graphs remain authoritative and the workspace overlay must match their exact current artifacts. Read-only."),
		protocol.WithString("term", protocol.Required(), protocol.Description("Search term")),
		protocol.WithString("scope", protocol.Description("Resolution scope; required when multiple scopes exist and no default_scope is configured")),
	)
	add(queryTool, func(ctx context.Context, request protocol.CallToolRequest) (*protocol.CallToolResult, error) {
		loaded, scope, args, err := loadWorkspaceMCPRequest(ctx, root, request)
		if err != nil {
			return protocol.NewToolResultError(err.Error()), nil
		}
		term, _ := args["term"].(string)
		term = strings.TrimSpace(term)
		if term == "" {
			return protocol.NewToolResultError("term is required"), nil
		}
		return workspaceJSONResult(workspacegraph.Query(loaded, scope, term))
	})

	pathTool := protocol.NewTool("gograph_workspace_path",
		protocol.WithDescription("Find the shortest traversable path across repository-local calls, workspace-resolved Go calls, and first-class HTTP contract nodes. Exact edges are used by default; ambiguous/possible edges require include_possible=true. Read-only."),
		protocol.WithString("from", protocol.Required(), protocol.Description("Source selector; repo:symbol is accepted as display/query syntax")),
		protocol.WithString("to", protocol.Required(), protocol.Description("Destination selector; repo:symbol is accepted as display/query syntax")),
		protocol.WithString("scope", protocol.Description("Resolution scope")),
		protocol.WithBoolean("include_possible", protocol.Description("Include ambiguous and possible edges for exploratory traversal")),
	)
	add(pathTool, func(ctx context.Context, request protocol.CallToolRequest) (*protocol.CallToolResult, error) {
		loaded, scope, args, err := loadWorkspaceMCPRequest(ctx, root, request)
		if err != nil {
			return protocol.NewToolResultError(err.Error()), nil
		}
		from, _ := args["from"].(string)
		to, _ := args["to"].(string)
		if from == "" || to == "" {
			return protocol.NewToolResultError("from and to are required"), nil
		}
		result, err := workspacegraph.Path(loaded, scope, from, to, workspaceBoolArg(args, "include_possible"))
		if err != nil {
			return protocol.NewToolResultError(err.Error()), nil
		}
		return workspaceJSONResult(result)
	})

	impactTool := protocol.NewTool("gograph_workspace_impact",
		protocol.WithDescription("Traverse exact virtual calls backwards across repositories and HTTP contracts to find the upstream blast radius of a uniquely resolved symbol. Ambiguous/possible edges are excluded unless include_possible=true. Read-only."),
		protocol.WithString("target", protocol.Required(), protocol.Description("Target selector; repo:symbol is accepted as display/query syntax")),
		protocol.WithString("scope", protocol.Description("Resolution scope")),
		protocol.WithBoolean("include_possible", protocol.Description("Include ambiguous and possible edges for exploratory traversal")),
	)
	add(impactTool, func(ctx context.Context, request protocol.CallToolRequest) (*protocol.CallToolResult, error) {
		loaded, scope, args, err := loadWorkspaceMCPRequest(ctx, root, request)
		if err != nil {
			return protocol.NewToolResultError(err.Error()), nil
		}
		target, _ := args["target"].(string)
		if target == "" {
			return protocol.NewToolResultError("target is required"), nil
		}
		result, err := workspacegraph.Impact(loaded, scope, target, workspaceBoolArg(args, "include_possible"))
		if err != nil {
			return protocol.NewToolResultError(err.Error()), nil
		}
		return workspaceJSONResult(result)
	})
	return s
}

func ServeWorkspace(root, version string) error {
	return server.ServeStdio(NewWorkspaceServer(root, version))
}

func loadWorkspaceMCPRequest(ctx context.Context, root string, request protocol.CallToolRequest) (*workspacegraph.LoadedWorkspace, workspacegraph.ScopeOverlay, map[string]any, error) {
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		return nil, workspacegraph.ScopeOverlay{}, nil, fmt.Errorf("invalid arguments")
	}
	loaded, err := workspacegraph.Load(ctx, root)
	if err != nil {
		return nil, workspacegraph.ScopeOverlay{}, nil, err
	}
	scopeName, _ := args["scope"].(string)
	scope, err := workspacegraph.SelectScope(loaded, scopeName)
	return loaded, scope, args, err
}

func workspaceJSONResult(value any) (*protocol.CallToolResult, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return protocol.NewToolResultError(err.Error()), nil
	}
	return protocol.NewToolResultText(string(data)), nil
}

func workspaceBoolArg(args map[string]any, name string) bool {
	value, _ := args[name].(bool)
	return value
}
