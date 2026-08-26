package mcp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
	mcppkg "github.com/ozgurcd/gograph/internal/mcp"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestMCPExploreReturnsSameNativeComposedResult(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.go"), []byte("package sample\n\nfunc Target() {}\n\nfunc Caller() { Target() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &graph.Graph{
		Root: root,
		Symbols: []graph.SymbolNode{
			{ID: "example.com/explore::Target", Name: "Target", Kind: graph.KindFunction, File: "target.go", Line: 3, EndLine: 3},
			{ID: "example.com/explore::Caller", Name: "Caller", Kind: graph.KindFunction, File: "target.go", Line: 5, EndLine: 5},
		},
		Calls: []graph.CallEdge{{
			CallerName: "Caller", CallerSymbolID: "example.com/explore::Caller",
			CalleeRaw: "Target", CalleeSymbolID: "example.com/explore::Target",
			File: "target.go", Line: 5,
		}},
		TestEdges: []graph.TestEdge{{TestFunc: "TestTarget", Target: "Target", File: "target_test.go", Line: 3}},
	}

	text := callTool(t, setupHandlers(t, g)["gograph_explore"], map[string]any{
		"query": "Target",
		"limit": 1,
		"exact": true,
	})
	var result search.ExploreResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("gograph_explore returned invalid JSON: %v\n%s", err, text)
	}
	if result.SchemaVersion != search.ExploreSchemaVersion || result.SelectedSymbol != "Target" {
		t.Fatalf("unexpected result identity: %#v", result)
	}
	if result.Context == nil || !strings.Contains(result.Context.Source, "func Target()") {
		t.Fatalf("MCP explore omitted source context: %#v", result.Context)
	}
	if len(result.Context.Callers) != 1 || result.Context.Callers[0].Name != "Caller" {
		t.Fatalf("MCP explore callers = %#v, want Caller", result.Context.Callers)
	}
	if len(result.Context.TestResults) != 1 || result.Context.TestResults[0].Name != "TestTarget" {
		t.Fatalf("MCP explore tests = %#v, want TestTarget", result.Context.TestResults)
	}
	if len(result.Impact) != 1 || result.Impact[0].Name != "Caller" {
		t.Fatalf("MCP explore impact = %#v, want Caller", result.Impact)
	}

	want := search.Explore(g, root, "Target", search.ExploreOptions{Limit: 1, Exact: true})
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("MCP native explore payload diverged from shared CLI analysis\ngot:  %s\nwant: %s", gotJSON, wantJSON)
	}
}

func TestMCPExploreRejectsMissingOrInvalidQuery(t *testing.T) {
	handler := setupHandlers(t, &graph.Graph{})["gograph_explore"]
	for _, args := range []map[string]any{{}, {"query": "   "}, {"query": 42}} {
		request := protocol.CallToolRequest{}
		request.Params.Arguments = args
		result, err := handler(t.Context(), request)
		if err != nil {
			t.Fatalf("handler returned protocol error: %v", err)
		}
		if !result.IsError {
			t.Fatalf("arguments %#v unexpectedly succeeded: %#v", args, result)
		}
	}
}

func TestMCPExploreClampsExplicitLimitToOne(t *testing.T) {
	text := callTool(t, setupHandlers(t, &graph.Graph{})["gograph_explore"], map[string]any{
		"query": "missing",
		"limit": 0,
	})
	var result search.ExploreResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("gograph_explore returned invalid JSON: %v\n%s", err, text)
	}
	if result.Limit != 1 {
		t.Fatalf("explicit zero limit = %d, want clamp to 1", result.Limit)
	}
}

func TestMCPExploreCompactAndDeepModes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.go"), []byte("package sample\n\nfunc Target() {}\n\nfunc Caller() { Target() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &graph.Graph{
		Root:     root,
		Packages: []graph.PackageNode{{ID: "sample", Name: "sample", ImportPathBestEffort: "example.com/explore", Dir: ".", Files: []string{"target.go"}}},
		Files:    []graph.FileNode{{ID: "target.go", Path: "target.go", PackageName: "sample"}},
		Symbols: []graph.SymbolNode{
			{ID: "example.com/explore::Target", Name: "Target", Kind: graph.KindFunction, PackageName: "sample", File: "target.go", Line: 3, EndLine: 3},
			{ID: "example.com/explore::Caller", Name: "Caller", Kind: graph.KindFunction, PackageName: "sample", File: "target.go", Line: 5, EndLine: 5},
		},
		Calls: []graph.CallEdge{{CallerName: "Caller", CallerSymbolID: "example.com/explore::Caller", CalleeRaw: "Target", CalleeSymbolID: "example.com/explore::Target", File: "target.go", Line: 5, Resolution: graph.CallResolutionStatic}},
	}
	handler := setupHandlers(t, g)["gograph_explore"]

	compactText := callTool(t, handler, map[string]any{"query": "Target", "compact": true})
	wantCompact, err := json.MarshalIndent(search.Explore(g, root, "Target", search.ExploreOptions{Mode: search.ExploreModeCompact}), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if compactText != string(wantCompact) {
		t.Fatalf("compact MCP/CLI-core parity mismatch\nMCP:\n%s\nshared:\n%s", compactText, wantCompact)
	}
	var compact search.ExploreResult
	if err := json.Unmarshal([]byte(compactText), &compact); err != nil {
		t.Fatalf("compact MCP explore returned invalid JSON: %v\n%s", err, compactText)
	}
	if compact.Mode != search.ExploreModeCompact || compact.Limit != search.CompactExploreLimit || compact.Context == nil || compact.Context.Source != "" || len(compact.Impact) != 0 {
		t.Fatalf("compact MCP result = %#v", compact)
	}

	deepText := callTool(t, handler, map[string]any{"query": "Target", "deep": true})
	wantDeep, err := json.MarshalIndent(search.Explore(g, root, "Target", search.ExploreOptions{Mode: search.ExploreModeDeep}), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if deepText != string(wantDeep) {
		t.Fatalf("deep MCP/CLI-core parity mismatch\nMCP:\n%s\nshared:\n%s", deepText, wantDeep)
	}
	var deep search.ExploreResult
	if err := json.Unmarshal([]byte(deepText), &deep); err != nil {
		t.Fatalf("deep MCP explore returned invalid JSON: %v\n%s", err, deepText)
	}
	if deep.Mode != search.ExploreModeDeep || deep.Limit != search.DeepExploreLimit || deep.Deep == nil || deep.Deep.Explanation == nil {
		t.Fatalf("deep MCP result = %#v", deep)
	}
}

func TestMCPExploreRejectsConflictingModes(t *testing.T) {
	handler := setupHandlers(t, &graph.Graph{})["gograph_explore"]
	request := protocol.CallToolRequest{}
	request.Params.Arguments = map[string]any{"query": "Target", "compact": true, "deep": true}
	result, err := handler(t.Context(), request)
	if err != nil {
		t.Fatalf("handler returned protocol error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("conflicting MCP explore modes unexpectedly succeeded: %#v", result)
	}
	text := result.Content[0].(protocol.TextContent).Text
	if !strings.Contains(text, "mutually exclusive") {
		t.Fatalf("unexpected conflict error: %s", text)
	}
}

func TestMCPExploreAdvertisesBooleanModeParameters(t *testing.T) {
	g := &graph.Graph{}
	server := mcppkg.NewServer(g, mockRebuild(g), mockBuildGraph(), mockBuildBaseline(), "dev")
	registered, ok := server.ListTools()["gograph_explore"]
	if !ok {
		t.Fatal("gograph_explore is not registered")
	}
	for _, name := range []string{"compact", "deep"} {
		property, ok := registered.Tool.InputSchema.Properties[name]
		if !ok {
			t.Fatalf("gograph_explore does not advertise %q", name)
		}
		encoded, err := json.Marshal(property)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), `"type":"boolean"`) {
			t.Fatalf("gograph_explore %s schema = %s, want boolean", name, encoded)
		}
	}
}
