package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/cli"
	"github.com/ozgurcd/gograph/internal/graph"
	mcppkg "github.com/ozgurcd/gograph/internal/mcp"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestCLIAndMCPGroupedRouteParity(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/routes\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `package routes
func register(router *Router) {
	v1 := router.Group("/api/v1")
	users := v1.Group("/users")
	users.POST("/:id", UpdateUser)
}
`
	if err := os.WriteFile(filepath.Join(root, "routes.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := cli.BuildGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	cliResults := search.Routes(g)
	if len(cliResults) != 1 || cliResults[0].Name != "POST /api/v1/users/:id" {
		t.Fatalf("CLI route results = %#v", cliResults)
	}
	mcpText := callTool(t, setupHandlers(t, g)["gograph_routes"], nil)
	if !strings.Contains(mcpText, cliResults[0].Name) || !strings.Contains(mcpText, "UpdateUser") {
		t.Fatalf("MCP route output does not match CLI route evidence:\n%s", mcpText)
	}
}

func parityRegressionGraph(t *testing.T) *graph.Graph {
	t.Helper()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/parity\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	return &graph.Graph{
		Version: graph.Version,
		Root:    root,
		Packages: []graph.PackageNode{
			{ID: "alpha", Name: "alpha", ImportPathBestEffort: "example.com/parity/alpha", Dir: "alpha", Files: []string{"alpha/start.go"}},
			{ID: "beta", Name: "beta", ImportPathBestEffort: "example.com/parity/beta", Dir: "beta", Files: []string{"beta/middle.go"}},
		},
		Files: []graph.FileNode{
			{ID: "alpha/start.go", Path: "alpha/start.go", PackageName: "alpha"},
			{ID: "beta/middle.go", Path: "beta/middle.go", PackageName: "beta"},
		},
		Symbols: []graph.SymbolNode{
			{ID: "example.com/parity/alpha::Start", Name: "Start", Kind: graph.KindFunction, PackageName: "alpha", File: "alpha/start.go", Line: 1, EndLine: 1},
			{ID: "example.com/parity/beta::Middle", Name: "Middle", Kind: graph.KindFunction, PackageName: "beta", File: "beta/middle.go", Line: 1, EndLine: 1},
			{ID: "example.com/parity/beta::End", Name: "End", Kind: graph.KindFunction, PackageName: "beta", File: "beta/middle.go", Line: 2, EndLine: 2},
			{ID: "example.com/parity/alpha::ZeroParams", Name: "ZeroParams", Kind: graph.KindFunction, PackageName: "alpha", File: "alpha/start.go", Line: 3, EndLine: 3, Arity: 0},
			{ID: "example.com/parity/alpha::Target", Name: "Target", Kind: graph.KindFunction, PackageName: "alpha", File: "alpha/missing.go", Line: 1, EndLine: 1},
			{ID: "example.com/parity/beta::Target", Name: "Target", Kind: graph.KindFunction, PackageName: "beta", File: "beta/missing.go", Line: 2, EndLine: 2},
		},
		Calls: []graph.CallEdge{
			{CallerSymbolID: "example.com/parity/alpha::Start", CallerName: "Start", CalleeSymbolID: "example.com/parity/beta::Middle", CalleeRaw: "Middle", File: "alpha/start.go", Line: 1},
			{CallerSymbolID: "example.com/parity/beta::Middle", CallerName: "Middle", CalleeSymbolID: "example.com/parity/beta::End", CalleeRaw: "End", File: "beta/middle.go", Line: 1},
		},
		Imports: []graph.ImportEdge{
			{FromFile: "alpha/start.go", FromPackage: "alpha", ImportPath: "example.com/parity/beta"},
		},
		Routes: []graph.HTTPRoute{
			{Method: "GET", Path: "/middle", Handler: "Middle", File: "beta/middle.go", Line: 1},
		},
	}
}

func TestMCPMermaidParityIsAdvertisedAndExecutable(t *testing.T) {
	g := parityRegressionGraph(t)
	server := mcppkg.NewServer(g, mockRebuild(g), mockBuildGraph(), mockBuildBaseline(), "dev")
	handlers := setupHandlers(t, g)

	cases := []struct {
		tool           string
		args           map[string]any
		normalResponse bool
	}{
		{tool: "gograph_callers", args: map[string]any{"function": "Middle"}, normalResponse: true},
		{tool: "gograph_callees", args: map[string]any{"function": "Start"}, normalResponse: true},
		{tool: "gograph_impact", args: map[string]any{"symbol": "Middle"}, normalResponse: true},
		{tool: "gograph_endpoint", args: map[string]any{"query": "Middle"}},
		{tool: "gograph_dependents", args: map[string]any{"package": "beta"}, normalResponse: true},
		{tool: "gograph_deps", args: map[string]any{"package": "alpha"}},
		{tool: "gograph_path", args: map[string]any{"from": "Start", "to": "End"}},
		{tool: "gograph_coupling", args: map[string]any{"internal_only": true}},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			registered, ok := server.ListTools()[tc.tool]
			if !ok {
				t.Fatalf("tool %q is not registered", tc.tool)
			}
			property, ok := registered.Tool.InputSchema.Properties["mermaid"]
			if !ok {
				t.Fatalf("tool %q schema does not advertise mermaid", tc.tool)
			}
			encoded, err := json.Marshal(property)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(encoded), `"type":"boolean"`) {
				t.Fatalf("tool %q mermaid schema = %s, want a boolean", tc.tool, encoded)
			}
			if tc.normalResponse && !strings.Contains(string(encoded), "normal response") {
				t.Fatalf("tool %q Mermaid description does not describe replacement of the normal response: %s", tc.tool, encoded)
			}

			tc.args["mermaid"] = true
			text := callTool(t, handlers[tc.tool], tc.args)
			if !strings.HasPrefix(text, "```mermaid\nflowchart ") || !strings.Contains(text, "-->") || !strings.HasSuffix(text, "```") {
				t.Fatalf("tool %q mermaid output is not a populated fenced flowchart:\n%s", tc.tool, text)
			}
		})
	}
}

func TestMCPCallersExactMermaidAvoidsSubstringCollisions(t *testing.T) {
	g := &graph.Graph{
		Root: t.TempDir(),
		Symbols: []graph.SymbolNode{
			{ID: "example.com/parity::Load", Name: "Load", Kind: graph.KindFunction},
			{ID: "example.com/parity::Preload", Name: "Preload", Kind: graph.KindFunction},
			{ID: "example.com/parity::ExactCaller", Name: "ExactCaller", Kind: graph.KindFunction},
			{ID: "example.com/parity::CollisionCaller", Name: "CollisionCaller", Kind: graph.KindFunction},
		},
		Calls: []graph.CallEdge{
			{CallerSymbolID: "example.com/parity::ExactCaller", CallerName: "ExactCaller", CalleeSymbolID: "example.com/parity::Load", CalleeRaw: "Load"},
			{CallerSymbolID: "example.com/parity::CollisionCaller", CallerName: "CollisionCaller", CalleeSymbolID: "example.com/parity::Preload", CalleeRaw: "Preload"},
		},
	}
	text := callTool(t, setupHandlers(t, g)["gograph_callers"], map[string]any{
		"function": "Load",
		"exact":    true,
		"mermaid":  true,
	})
	if !strings.Contains(text, "ExactCaller") || strings.Contains(text, "CollisionCaller") || strings.Contains(text, "Preload") {
		t.Fatalf("MCP exact Mermaid response widened to a substring collision:\n%s", text)
	}
}

func TestMCPArityAcceptsZeroThreshold(t *testing.T) {
	g := parityRegressionGraph(t)
	handlers := setupHandlers(t, g)

	text := callTool(t, handlers["gograph_arity"], map[string]any{"min": float64(0)})
	if !strings.Contains(text, "ZeroParams") || !strings.Contains(text, "0 arguments") {
		t.Fatalf("min=0 omitted a zero-arity function:\n%s", text)
	}
}

func TestMCPCountAndDepthArgumentsAreIntegers(t *testing.T) {
	g := parityRegressionGraph(t)
	handlers := setupHandlers(t, g)
	registeredServer := mcppkg.NewServer(g, mockRebuild(g), mockBuildGraph(), mockBuildBaseline(), "dev")

	integerProperties := map[string][]string{
		"gograph_callers":  {"depth"},
		"gograph_callees":  {"depth"},
		"gograph_endpoint": {"depth"},
		"gograph_hotspot":  {"top"},
		"gograph_arity":    {"min"},
		"gograph_godobj":   {"methods", "fields", "calls", "top"},
		"gograph_diagram":  {"max_depth"},
		"gograph_untested": {"top"},
	}
	for toolName, properties := range integerProperties {
		registered, ok := registeredServer.ListTools()[toolName]
		if !ok {
			t.Fatalf("tool %q is not registered", toolName)
		}
		for _, propertyName := range properties {
			property, ok := registered.Tool.InputSchema.Properties[propertyName]
			if !ok {
				t.Fatalf("tool %q does not advertise %q", toolName, propertyName)
			}
			encoded, err := json.Marshal(property)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(encoded), `"type":"integer"`) {
				t.Errorf("tool %q property %q schema = %s, want integer", toolName, propertyName, encoded)
			}
		}
	}

	tests := []struct {
		name     string
		tool     string
		property string
		args     map[string]any
	}{
		{name: "callers depth", tool: "gograph_callers", property: "depth", args: map[string]any{"function": "Middle", "depth": 1.5}},
		{name: "callees depth", tool: "gograph_callees", property: "depth", args: map[string]any{"function": "Start", "depth": 1.5}},
		{name: "endpoint depth", tool: "gograph_endpoint", property: "depth", args: map[string]any{"query": "Middle", "depth": 1.5}},
		{name: "hotspot top", tool: "gograph_hotspot", property: "top", args: map[string]any{"top": 1.5}},
		{name: "arity minimum", tool: "gograph_arity", property: "min", args: map[string]any{"min": 1.5}},
		{name: "godobj methods", tool: "gograph_godobj", property: "methods", args: map[string]any{"methods": 1.5}},
		{name: "godobj fields", tool: "gograph_godobj", property: "fields", args: map[string]any{"fields": 1.5}},
		{name: "godobj calls", tool: "gograph_godobj", property: "calls", args: map[string]any{"calls": 1.5}},
		{name: "godobj top", tool: "gograph_godobj", property: "top", args: map[string]any{"top": 1.5}},
		{name: "diagram depth", tool: "gograph_diagram", property: "max_depth", args: map[string]any{"max_depth": 1.5}},
		{name: "untested top", tool: "gograph_untested", property: "top", args: map[string]any{"top": 1.5}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = test.args
			result, err := handlers[test.tool](context.Background(), request)
			if err != nil {
				t.Fatalf("tool handler returned error: %v", err)
			}
			if !result.IsError {
				t.Fatalf("tool accepted fractional %s: %+v", test.property, result.Content)
			}
			text := result.Content[0].(mcp.TextContent).Text
			if !strings.Contains(text, test.property+" must be an integer") {
				t.Fatalf("unexpected integer validation error: %s", text)
			}
		})
	}
}

func TestMCPContextPreservesAmbiguityRoleAndSourceError(t *testing.T) {
	g := parityRegressionGraph(t)
	handlers := setupHandlers(t, g)

	text := callTool(t, handlers["gograph_context"], map[string]any{"symbol": "Target"})
	var response struct {
		Node        *search.Result  `json:"node"`
		Nodes       []search.Result `json:"nodes"`
		Role        string          `json:"role"`
		SourceError string          `json:"source_error"`
	}
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatalf("context returned invalid JSON: %v\n%s", err, text)
	}
	if response.Node == nil {
		t.Fatal("context omitted the backward-compatible first node")
	}
	if len(response.Nodes) != 2 {
		t.Fatalf("context nodes = %d, want both ambiguous Target symbols: %s", len(response.Nodes), text)
	}
	if response.Role == "" {
		t.Fatalf("context omitted top-level role: %s", text)
	}
	if response.SourceError == "" {
		t.Fatalf("context silently discarded its source extraction error: %s", text)
	}
}

func TestMCPErrorFlowAndTraceShareStructuredPayload(t *testing.T) {
	g := &graph.Graph{
		Root: t.TempDir(),
		Errors: []graph.ErrorEdge{
			{Function: "TestAuthorize", File: "auth_test.go", Line: 7, Message: "permission denied"},
		},
	}
	handlers := setupHandlers(t, g)
	errorflow := callTool(t, handlers["gograph_errorflow"], map[string]any{"term": "permission denied"})
	trace := callTool(t, handlers["gograph_trace"], map[string]any{"term": "permission denied"})
	if errorflow != trace {
		t.Fatalf("errorflow and trace payloads differ:\nerrorflow:\n%s\ntrace:\n%s", errorflow, trace)
	}
	if !strings.Contains(errorflow, `"test_results"`) || !strings.Contains(errorflow, "TestAuthorize") {
		t.Fatalf("structured related-test evidence is missing: %s", errorflow)
	}
}

func TestMCPCapabilityPrerequisiteReflectsPersistenceMode(t *testing.T) {
	g := parityRegressionGraph(t)
	previous := mcppkg.ExposeToolsForTesting
	defer func() { mcppkg.ExposeToolsForTesting = previous }()

	capabilityText := func(options ...mcppkg.ServerOptions) string {
		handlers := make(map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error))
		mcppkg.ExposeToolsForTesting = handlers
		mcppkg.NewServer(g, mockRebuild(g), mockBuildGraph(), mockBuildBaseline(), "dev", options...)
		return callTool(t, handlers["gograph_capabilities"], nil)
	}

	defaultText := capabilityText()
	persistText := capabilityText(mcppkg.ServerOptions{PersistRefresh: true})

	if !strings.Contains(defaultText, "auto-builds an in-memory graph when it is missing") {
		t.Fatalf("default capability prerequisite does not describe in-memory startup: %s", defaultText)
	}
	if strings.Contains(defaultText, "auto-builds and publishes graph.json") {
		t.Fatalf("default capability prerequisite falsely promises persistence: %s", defaultText)
	}
	if !strings.Contains(persistText, "auto-builds and publishes graph.json plus nine reports") {
		t.Fatalf("persist capability prerequisite does not describe publication: %s", persistText)
	}
	for _, phrase := range []string{
		"set mermaid=true to return Markdown-fenced Mermaid",
		"gograph_callers",
		"gograph_coupling",
		"one-element JSON array with query and raw-text output",
		"regular, repository-confined .gograph/graph.json",
		"older binaries do not enforce this contract",
		"function, method, struct, interface, type, variable, or constant",
		"UNKNOWN with score -1",
		"Filesystem-shaped queries are rejected",
	} {
		if !strings.Contains(defaultText, phrase) {
			t.Fatalf("capabilities omitted %q: %s", phrase, defaultText)
		}
	}
}
