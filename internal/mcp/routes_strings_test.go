package mcp_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
)

func routeStringsGraph(t *testing.T) *graph.Graph {
	t.Helper()
	root := t.TempDir()
	source := "package app\n\nfunc Register(r Router) {\n\tr.GET(\"/api/v1/users/:id\", GetUser)\n\tclient.Get(\"/api/idp/upgrade/status\")\n}\n"
	if err := os.WriteFile(filepath.Join(root, "routes.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return &graph.Graph{
		Root:  root,
		Build: &graph.BuildMetadata{Complete: true, Precision: graph.PrecisionAST},
		Files: []graph.FileNode{{ID: "routes.go", Path: "routes.go", PackageName: "app", Lines: 6}},
		Symbols: []graph.SymbolNode{
			{ID: "example.com/app::Register", Name: "Register", Kind: graph.KindFunction, PackageName: "app", File: "routes.go", Line: 3, EndLine: 6},
		},
		Routes: []graph.HTTPRoute{{Method: "GET", Path: "/api/v1/users/:id", Handler: "GetUser", File: "routes.go", Line: 4}},
	}
}

func callToolResult(t *testing.T, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("tool handler returned error: %v", err)
	}
	return result
}

func TestRoutesMatchAnyPathParameterSpelling(t *testing.T) {
	handlers := setupHandlers(t, routeStringsGraph(t))
	for _, term := range []string{"/api/v1/users/{id}", "/api/v1/users/:userID", "GET /api/v1/users/*rest", "/API/V1/USERS"} {
		text := callTool(t, handlers["gograph_routes"], map[string]any{"term": term})
		if !strings.Contains(text, `"total":1`) {
			t.Fatalf("routes %q must match the :id route: %s", term, text)
		}
	}
	text := callTool(t, handlers["gograph_routes"], map[string]any{"query": "/api/v1/users/{id}"})
	if !strings.Contains(text, `"total":1`) || !strings.Contains(text, `"term":"/api/v1/users/{id}"`) {
		t.Fatalf("query must be a filtering alias of term, not ignored: %s", text)
	}
	conflict := callToolResult(t, handlers["gograph_routes"], map[string]any{"term": "/a", "query": "/b"})
	if !conflict.IsError {
		t.Fatal("conflicting term and query must be refused")
	}
}

func TestEmptyRouteAndQueryAnswersNameTheirGraph(t *testing.T) {
	g := routeStringsGraph(t)
	handlers := setupHandlers(t, g)
	text := callTool(t, handlers["gograph_routes"], map[string]any{"term": "/api/setup/license"})
	if !strings.Contains(text, "0 of 1 indexed routes matched") || !strings.Contains(text, g.Root) || !strings.Contains(text, "not proof of absence") {
		t.Fatalf("an empty route page must name the graph root and indexed total: %s", text)
	}
	text = callTool(t, handlers["gograph_query"], map[string]any{"term": "NoSuchThing"})
	if !strings.Contains(text, g.Root) || !strings.Contains(text, "not proof of absence") {
		t.Fatalf("an empty query must name the graph root: %s", text)
	}
}

func TestQueryFindsRoutePatterns(t *testing.T) {
	handlers := setupHandlers(t, routeStringsGraph(t))
	for _, args := range []map[string]any{{"term": "/api/v1/users/{id}"}, {"query": "/api/v1/users"}} {
		text := callTool(t, handlers["gograph_query"], args)
		if !strings.Contains(text, `"kind":"route"`) || !strings.Contains(text, "GET /api/v1/users/:id") {
			t.Fatalf("query %v must return the route row: %s", args, text)
		}
	}
}

func TestStringsFindsRawLiteralsWithEnclosingFunction(t *testing.T) {
	handlers := setupHandlers(t, routeStringsGraph(t))
	text := callTool(t, handlers["gograph_strings"], map[string]any{"term": "/api/idp/upgrade"})
	if !strings.Contains(text, `"kind":"string"`) || !strings.Contains(text, "upgrade/status") || !strings.Contains(text, "string literal in Register") || !strings.Contains(text, `"line":5`) {
		t.Fatalf("strings must report the literal, line, and enclosing function: %s", text)
	}
	text = callTool(t, handlers["gograph_strings"], map[string]any{"terms": []any{"/api/v1/users/{id}"}})
	if !strings.Contains(text, `"line":4`) {
		t.Fatalf("route-shaped literal terms must match any parameter spelling: %s", text)
	}
	if result := callToolResult(t, handlers["gograph_strings"], map[string]any{}); !result.IsError {
		t.Fatal("strings without a term must be refused")
	}
}
