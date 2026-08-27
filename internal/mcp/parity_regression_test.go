package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
	cliPage, err := search.QueryRoutes(g, search.RouteQuery{})
	if err != nil {
		t.Fatal(err)
	}
	mcpText := callTool(t, setupHandlers(t, g)["gograph_routes"], nil)
	var mcpPage search.RoutePage
	if err := json.Unmarshal([]byte(mcpText), &mcpPage); err != nil {
		t.Fatalf("decode MCP route page: %v\n%s", err, mcpText)
	}
	cliJSON, err := json.Marshal(cliPage)
	if err != nil {
		t.Fatal(err)
	}
	mcpJSON, err := json.Marshal(mcpPage)
	if err != nil {
		t.Fatal(err)
	}
	if len(cliPage.Routes) != 1 || cliPage.Routes[0].Name != "POST /api/v1/users/:id" || string(mcpJSON) != string(cliJSON) {
		t.Fatalf("CLI/MCP route pages differ:\nCLI: %+v\nMCP: %+v", cliPage, mcpPage)
	}
}

func TestCLIAndMCPUntestedResolutionParity(t *testing.T) {
	const (
		exactID    = "example.com/parity::(*Exact).Run"
		possibleID = "example.com/parity::(*Possible).Run"
	)
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: exactID, Name: "Run", Receiver: "*Exact", Kind: graph.KindMethod, PackageName: "parity", File: "exact.go", Line: 10},
			{ID: possibleID, Name: "Run", Receiver: "*Possible", Kind: graph.KindMethod, PackageName: "parity", File: "possible.go", Line: 20},
		},
		Calls: []graph.CallEdge{
			{CalleeSymbolID: exactID, File: "caller.go", Line: 1},
			{CalleeSymbolID: possibleID, File: "caller.go", Line: 2},
		},
		TestEdges: []graph.TestEdge{
			{TestFunc: "TestExact", Target: "exact.Run", TargetSymbolID: exactID, Resolution: graph.CallResolutionStatic, File: "run_test.go", Line: 10},
			{TestFunc: "TestPossible", Target: "runner.Run", TargetSymbolID: possibleID, Resolution: graph.CallResolutionCHA, File: "run_test.go", Line: 20},
		},
	}

	want := search.Untested(g)
	if len(want) != 1 || want[0].TestResolution != "possible" || want[0].PossibleTestCount != 1 {
		t.Fatalf("CLI-native untested result = %#v", want)
	}
	text := callTool(t, setupHandlers(t, g)["gograph_untested"], nil)
	var got []search.UntestedResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode MCP untested result: %v\n%s", err, text)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MCP untested result = %#v, want CLI-native %#v", got, want)
	}
}

func TestCLIAndMCPTransitiveTestsParity(t *testing.T) {
	g := attributionParityGraph()
	want := search.TransitiveTests(g, "example.com/parity::HandleRevoke", false)
	text := callTool(t, setupHandlers(t, g)["gograph_tests"], map[string]any{
		"symbol":     "example.com/parity::HandleRevoke",
		"transitive": true,
	})
	var got search.TestsReport
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode MCP transitive tests result: %v\n%s", err, text)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MCP transitive tests result = %#v, want CLI-native %#v", got, want)
	}
}

func attributionParityGraph() *graph.Graph {
	const (
		testID    = "example.com/parity::TestRouter"
		routerID  = "example.com/parity::Router"
		handlerID = "example.com/parity::HandleRevoke"
	)
	return &graph.Graph{
		Build: &graph.BuildMetadata{Precision: graph.PrecisionPrecise, TestCallResolution: graph.TestCallResolutionTyped},
		Symbols: []graph.SymbolNode{
			{ID: testID, Name: "TestRouter", Kind: graph.KindFunction, PackageName: "parity", File: "router_test.go", Line: 10},
			{ID: routerID, Name: "Router", Kind: graph.KindFunction, PackageName: "parity", File: "router.go", Line: 20},
			{ID: handlerID, Name: "HandleRevoke", Kind: graph.KindFunction, PackageName: "parity", File: "handler.go", Line: 30},
		},
		TestEdges: []graph.TestEdge{{TestFunc: "TestRouter", File: "router_test.go", TargetSymbolID: routerID, Resolution: graph.CallResolutionStatic}},
		Calls:     []graph.CallEdge{{CallerSymbolID: routerID, CalleeSymbolID: handlerID, Resolution: graph.CallResolutionStatic}},
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

func TestMCPPathUsesSharedRankedSelection(t *testing.T) {
	g := parityRegressionGraph(t)
	const exactMidID = "example.com/parity/beta::ExactMid"
	g.Symbols = append(g.Symbols, graph.SymbolNode{ID: exactMidID, Name: "ExactMid", Kind: graph.KindFunction, PackageName: "beta", File: "beta/middle.go", Line: 3, EndLine: 3})
	g.Calls = []graph.CallEdge{
		{CallerSymbolID: "example.com/parity/alpha::Start", CallerName: "Start", CalleeSymbolID: "example.com/parity/beta::End", CalleeRaw: "End", File: "alpha/start.go", Line: 1, Resolution: graph.CallResolutionCHA},
		{CallerSymbolID: "example.com/parity/alpha::Start", CallerName: "Start", CalleeSymbolID: exactMidID, CalleeRaw: "ExactMid", File: "alpha/start.go", Line: 2, Resolution: graph.CallResolutionStatic},
		{CallerSymbolID: exactMidID, CallerName: "ExactMid", CalleeSymbolID: "example.com/parity/beta::End", CalleeRaw: "End", File: "beta/middle.go", Line: 3, Resolution: graph.CallResolutionStatic},
	}

	text := callTool(t, setupHandlers(t, g)["gograph_path"], map[string]any{"from": "Start", "to": "End"})
	var response struct {
		Found bool            `json:"found"`
		Steps []search.Result `json:"steps"`
	}
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatalf("decode ranked MCP path: %v\n%s", err, text)
	}
	if !response.Found || len(response.Steps) != 3 || response.Steps[1].Name != "ExactMid" {
		t.Fatalf("MCP did not prefer exact ranked path: %+v", response)
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

func TestMCPReverseAttributionAndIdentityContracts(t *testing.T) {
	g := parityRegressionGraph(t)
	g.Build = &graph.BuildMetadata{Precision: graph.PrecisionPrecise, TestCallResolution: graph.TestCallResolutionTyped}
	g.Symbols = append(g.Symbols,
		graph.SymbolNode{ID: "example.com/parity/alpha::TestStart", Name: "TestStart", Kind: graph.KindFunction, PackageName: "alpha", File: "alpha/start_test.go", Line: 10},
	)
	g.TestEdges = []graph.TestEdge{{
		TestFunc: "TestStart", Target: "Start", TargetSymbolID: "example.com/parity/alpha::Start",
		Resolution: graph.CallResolutionStatic, File: "alpha/start_test.go", Line: 11,
	}}
	g.Calls[0].Resolution = graph.CallResolutionStatic
	g.Calls[1].Resolution = graph.CallResolutionCHA
	handlers := setupHandlers(t, g)

	identityText := callTool(t, handlers["gograph_identity"], map[string]any{"symbol": "Start", "package": "alpha"})
	var identity search.IdentityReport
	if err := json.Unmarshal([]byte(identityText), &identity); err != nil {
		t.Fatalf("identity JSON: %v\n%s", err, identityText)
	}
	if identity.Status != "exact" || len(identity.Matches) != 1 || identity.Matches[0].StableID != "example.com/parity/alpha::Start" {
		t.Fatalf("identity = %#v", identity)
	}

	coverageText := callTool(t, handlers["gograph_coverage"], map[string]any{"test": "TestStart", "package": "alpha"})
	var coverage search.CoverageReport
	if err := json.Unmarshal([]byte(coverageText), &coverage); err != nil {
		t.Fatalf("coverage JSON: %v\n%s", err, coverageText)
	}
	if coverage.Status != "exact" || len(coverage.Symbols) != 3 {
		t.Fatalf("coverage = %#v", coverage)
	}
	if coverage.Symbols[0].Resolution != "exact" || coverage.Symbols[2].Resolution != "possible" {
		t.Fatalf("coverage propagation = %#v", coverage.Symbols)
	}

	exactText := callTool(t, handlers["gograph_coverage"], map[string]any{"test": "TestStart", "exact_only": true})
	if err := json.Unmarshal([]byte(exactText), &coverage); err != nil {
		t.Fatal(err)
	}
	if len(coverage.Symbols) != 2 {
		t.Fatalf("exact-only coverage = %#v", coverage.Symbols)
	}
}

func TestMCPUntestedExcludeArrayMatchesCLIPathSemantics(t *testing.T) {
	g := parityRegressionGraph(t)
	text := callTool(t, setupHandlers(t, g)["gograph_untested"], map[string]any{"exclude": []any{"beta/**"}})
	var results []search.UntestedResult
	if err := json.Unmarshal([]byte(text), &results); err != nil {
		t.Fatalf("untested JSON: %v\n%s", err, text)
	}
	for _, result := range results {
		if strings.HasPrefix(result.File, "beta/") {
			t.Fatalf("excluded result remained: %#v", result)
		}
	}

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"exclude": []any{float64(1)}}
	response, err := setupHandlers(t, g)["gograph_untested"](context.Background(), req)
	if err != nil || response == nil || !response.IsError {
		t.Fatalf("non-string exclude accepted: response=%#v err=%v", response, err)
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

func TestMCPUntestedExcludeSchemaIsStringArray(t *testing.T) {
	g := parityRegressionGraph(t)
	registered, ok := mcppkg.NewServer(g, mockRebuild(g), mockBuildGraph(), mockBuildBaseline(), "dev").ListTools()["gograph_untested"]
	if !ok {
		t.Fatal("gograph_untested is not registered")
	}
	property, ok := registered.Tool.InputSchema.Properties["exclude"]
	if !ok {
		t.Fatal("gograph_untested does not advertise exclude")
	}
	encoded, err := json.Marshal(property)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"type":"array"`) || !strings.Contains(string(encoded), `"items":{"type":"string"}`) {
		t.Fatalf("exclude schema = %s, want string array", encoded)
	}
}

func TestMCPTestsAdvertisesTransitiveParityParameters(t *testing.T) {
	g := parityRegressionGraph(t)
	registered, ok := mcppkg.NewServer(g, mockRebuild(g), mockBuildGraph(), mockBuildBaseline(), "dev").ListTools()["gograph_tests"]
	if !ok {
		t.Fatal("gograph_tests is not registered")
	}
	for _, property := range []string{"symbol", "transitive", "exact_only", "package"} {
		if _, ok := registered.Tool.InputSchema.Properties[property]; !ok {
			t.Errorf("gograph_tests does not advertise %s", property)
		}
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
		"typed_partial",
		"single visible implementation is never enough",
		"gograph.tests.v1 reverse attribution",
		"static attribution never proves runtime coverage",
		"reject artifacts larger than 512 MiB before allocation",
		"Typed-only test targets are recomputed",
		"does not build dependency SSA bodies",
	} {
		if !strings.Contains(defaultText, phrase) {
			t.Fatalf("capabilities omitted %q: %s", phrase, defaultText)
		}
	}
}
