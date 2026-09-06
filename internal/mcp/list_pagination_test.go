package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
	mcppkg "github.com/ozgurcd/gograph/internal/mcp"
	"github.com/ozgurcd/gograph/internal/search"
)

func decodeResultRows(t *testing.T, text string) []search.Result {
	t.Helper()
	var page struct {
		Results []search.Result `json:"results"`
	}
	if err := json.Unmarshal([]byte(text), &page); err != nil {
		t.Fatalf("invalid native result page: %v: %s", err, text)
	}
	return page.Results
}

func TestEveryPaginatedMCPToolAdvertisesSharedBounds(t *testing.T) {
	g := &graph.Graph{}
	server := mcppkg.NewServer(g, mockRebuild(g), mockBuildGraph(), mockBuildBaseline(), "test")
	for _, command := range search.ListPaginationCommands {
		tool, ok := server.ListTools()["gograph_"+command]
		if !ok {
			t.Fatalf("missing tool for %s", command)
		}
		limit, err := json.Marshal(tool.Tool.InputSchema.Properties["limit"])
		if err != nil {
			t.Fatal(err)
		}
		var bounds struct{ Default, Minimum, Maximum int }
		if err := json.Unmarshal(limit, &bounds); err != nil {
			t.Fatal(err)
		}
		if bounds.Default != search.DefaultResultsLimit || bounds.Minimum != 1 || bounds.Maximum != search.MaxResultsLimit {
			t.Errorf("%s uses different pagination bounds: %s", command, limit)
		}
		if _, ok := tool.Tool.InputSchema.Properties["cursor"]; !ok {
			t.Errorf("%s omits cursor", command)
		}
	}
}

func TestMCPQueryPagesNativeRowsBelowCompleteResponseCap(t *testing.T) {
	g := &graph.Graph{Root: t.TempDir()}
	for i := range 2782 {
		g.Symbols = append(g.Symbols, graph.SymbolNode{ID: fmt.Sprintf("example::Handler%04d", i), Name: fmt.Sprintf("Handler%04d", i), Kind: graph.KindFunction, File: "handlers.go", Line: i + 1})
	}
	handler := setupHandlers(t, g)["gograph_query"]
	cursor := ""
	seen := 0
	for {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]any{"term": "Handler", "cursor": cursor}
		result, err := handler(context.Background(), req)
		if err != nil || result.IsError {
			t.Fatalf("query: %+v %v", result, err)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) > 64*1024 {
			t.Fatalf("complete MCP response exceeds cap: %d", len(encoded))
		}
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var page search.ResultPage
		if err := json.Unmarshal(data, &page); err != nil {
			t.Fatal(err)
		}
		if page.Offset != seen || page.Total != 2782 || page.Returned < 1 {
			t.Fatalf("invalid page: %+v", page)
		}
		seen += page.Returned
		if !page.Truncated {
			break
		}
		cursor = page.NextCursor
	}
	if seen != 2782 {
		t.Fatalf("lost rows: %d", seen)
	}
}

func TestMCPQueryEscapedPayloadStaysBelowCompleteResponseCap(t *testing.T) {
	g := &graph.Graph{Root: t.TempDir()}
	for i := range 100 {
		name := fmt.Sprintf("Handler%04d", i) + strings.Repeat("\\\"", 200)
		g.Symbols = append(g.Symbols, graph.SymbolNode{ID: "example::" + name, Name: name, Kind: graph.KindFunction})
	}
	handler := setupHandlers(t, g)["gograph_query"]
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"term": "Handler"}
	result, err := handler(context.Background(), request)
	if err != nil || result.IsError {
		t.Fatalf("query failed: %v %+v", err, result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 64*1024 {
		t.Fatalf("escaping text plus structured content exceeds MCP cap: %d bytes", len(encoded))
	}
}
