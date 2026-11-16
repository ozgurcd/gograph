package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	mcppkg "github.com/ozgurcd/gograph/internal/mcp"
	"github.com/ozgurcd/gograph/internal/search"
)

// Tool definitions are sent to the client once per session and stay in its
// context, so shared facts are stated once in the server instructions.
func TestToolDefinitionsStayWithinTokenBudget(t *testing.T) {
	server := mcppkg.NewServer(&graph.Graph{}, mockRebuild(&graph.Graph{}), mockBuildGraph(), mockBuildBaseline(), "dev")
	var tools []any
	for _, tool := range server.ListTools() {
		tools = append(tools, tool.Tool)
		for _, repeated := range []string{
			"Read-only; no side effects.",
			"Read-only; no persistent side effects.",
			"The MCP server checks freshness",
			"The MCP server refreshes",
			search.CurrentChangeSelectionContract,
		} {
			if strings.Contains(tool.Tool.Description, repeated) {
				t.Errorf("%s repeats the shared fact %q", tool.Tool.Name, repeated)
			}
		}
	}
	data, err := json.Marshal(map[string]any{"tools": tools})
	if err != nil {
		t.Fatal(err)
	}
	// 143 KB before the shared facts moved to the instructions.
	const budget = 100 * 1024
	if len(data) > budget {
		t.Fatalf("tools/list is %d bytes, budget %d", len(data), budget)
	}

	response := server.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`))
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var initialized struct {
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(encoded, &initialized); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"checks source freshness", "read-only unless their annotations say otherwise", "next_cursor", search.CurrentChangeSelectionContract, "_meta.gograph_graph_state"} {
		if !strings.Contains(initialized.Result.Instructions, want) {
			t.Errorf("server instructions omit %q: %s", want, initialized.Result.Instructions)
		}
	}
}

func TestRepositoryReportsDefaultToTopTwenty(t *testing.T) {
	root := t.TempDir()
	var source strings.Builder
	source.WriteString("package sample\n")
	g := &graph.Graph{Root: root}
	for i := 0; i < 30; i++ {
		name := fmt.Sprintf("F%02d", i)
		line := strings.Count(source.String(), "\n") + 1
		source.WriteString("func " + name + "() {}\n")
		g.Symbols = append(g.Symbols, graph.SymbolNode{ID: "example.com/sample::" + name, Name: name, Kind: graph.KindFunction, PackageName: "sample", File: "sample.go", Line: line, EndLine: line})
		g.Calls = append(g.Calls, graph.CallEdge{CallerName: "main", CalleeRaw: name, CalleeSymbolID: "example.com/sample::" + name, File: "main.go", Line: i + 1})
	}
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(source.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	handlers := setupHandlers(t, g)
	for _, tool := range []string{"gograph_complexity", "gograph_untested"} {
		for _, test := range []struct {
			args map[string]any
			want int
		}{{nil, 20}, {map[string]any{"top": 5}, 5}, {map[string]any{"top": 0}, 30}} {
			var rows []json.RawMessage
			text := callTool(t, handlers[tool], test.args)
			if err := json.Unmarshal([]byte(text), &rows); err != nil {
				t.Fatalf("%s %v: %v\n%s", tool, test.args, err, text)
			}
			if len(rows) != test.want {
				t.Errorf("%s %v returned %d rows, want %d", tool, test.args, len(rows), test.want)
			}
		}
	}
}
