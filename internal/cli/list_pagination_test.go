package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	mcpprotocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func decodeCLIResultRows(t *testing.T, text string) []search.Result {
	t.Helper()
	var page struct {
		Results []search.Result `json:"results"`
	}
	if err := json.Unmarshal([]byte(text), &page); err != nil {
		t.Fatal(err)
	}
	return page.Results
}

func TestEveryPaginatedCommandHelpNamesTheContinuationContract(t *testing.T) {
	for _, command := range search.ListPaginationCommands {
		stdout, stderr, code := captureCLIParityOutput(t, func() int { return Run([]string{command, "--help"}) })
		if code != 0 {
			t.Fatalf("%s help: %s", command, stderr)
		}
		for _, term := range []string{"--limit", "--cursor", "next_cursor", "truncated", "snapshot", "16 KiB"} {
			if !strings.Contains(stdout, term) {
				t.Errorf("%s help omits %q", command, term)
			}
		}
	}
}

func TestListPaginationCLIMCPCursorsRoundTrip(t *testing.T) {
	g := &graph.Graph{}
	for i := 0; i < 321; i++ {
		name := fmt.Sprintf("Handler%04d", i)
		g.Symbols = append(g.Symbols, graph.SymbolNode{ID: "example.com/app::" + name, Name: name, Kind: graph.KindFunction, File: "handlers.go", Line: i + 1})
	}
	root := writeCLIParityGraph(t, g)
	handlers := exposeMCPRefreshHandlers(t, g, func() (*graph.Graph, error) { return g, nil })
	cursor := ""
	seen := 0
	for {
		args := []string{"query", "Handler", "--json", "--limit", "73"}
		if cursor != "" {
			args = append(args, "--cursor", cursor)
		}
		stdout, stderr, code := runCLIParityInDir(t, root, func() int { return Run(args) })
		if code != 0 {
			t.Fatalf("CLI code=%d stderr=%s output=%s", code, stderr, stdout)
		}
		request := mcpprotocol.CallToolRequest{}
		request.Params.Arguments = map[string]any{"term": "Handler", "limit": 73, "cursor": cursor}
		result, err := handlers["gograph_query"](context.Background(), request)
		if err != nil || result.IsError {
			t.Fatalf("MCP error: %v, result=%+v", err, result)
		}
		var cliPage, mcpPage map[string]json.RawMessage
		if err := json.Unmarshal([]byte(stdout), &cliPage); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(mcpResultText(t, result)), &mcpPage); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"command", "status", "results", "count", "total", "returned", "truncated", "next_cursor", "limit", "offset"} {
			var a, b any
			if err := json.Unmarshal(cliPage[key], &a); err != nil {
				t.Fatalf("CLI missing %s: %s", key, stdout)
			}
			if err := json.Unmarshal(mcpPage[key], &b); err != nil {
				t.Fatalf("MCP missing %s", key)
			}
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("%s differs: CLI=%v MCP=%v", key, a, b)
			}
		}
		rows := decodeCLIResultRows(t, stdout)
		for i, row := range rows {
			if row.Name != fmt.Sprintf("Handler%04d", seen+i) {
				t.Fatalf("lost or duplicated row at %d: %+v", seen+i, row)
			}
		}
		seen += len(rows)
		// Alternate the issuing transport: a token emitted by either interface
		// must be accepted by both interfaces on the next iteration.
		source := cliPage
		if seen%2 == 0 {
			source = mcpPage
		}
		if err := json.Unmarshal(source["next_cursor"], &cursor); err != nil {
			t.Fatal(err)
		}
		if cursor == "" {
			break
		}
	}
	if seen != 321 {
		t.Fatalf("visited %d of 321 symbols", seen)
	}
}
