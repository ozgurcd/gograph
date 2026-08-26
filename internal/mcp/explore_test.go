package mcp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
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
