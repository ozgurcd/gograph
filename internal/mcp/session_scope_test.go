package mcp_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/session"
)

func TestCallerSessionIsolationMCP(t *testing.T) {
	root := t.TempDir()
	id, err := session.StartSessionAt(root, "owner")
	if err != nil {
		t.Fatal(err)
	}
	handlers := setupHandlers(t, &graph.Graph{Root: root})
	call := func(name string, args map[string]any, wantError bool) string {
		t.Helper()
		req := mcp.CallToolRequest{}
		req.Params.Arguments = args
		result, err := handlers[name](context.Background(), req)
		if err != nil || result.IsError != wantError {
			t.Fatalf("%s: err=%v result=%+v", name, err, result)
		}
		return result.Content[0].(mcp.TextContent).Text
	}
	path := filepath.Join(root, ".gograph", "sessions", "session_"+id+".jsonl")
	before, _ := os.ReadFile(path)
	call("gograph_query", map[string]any{"terms": []any{"missing"}}, false)
	for _, name := range []string{"gograph_session_end", "gograph_session_cleanup", "gograph_wiki", "gograph_boundaries_create"} {
		if text := call(name, nil, true); !strings.Contains(text, id) {
			t.Fatal("refusal must name active session")
		}
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("non-owner MCP call changed owner's audit bytes")
	}
	call("gograph_query", map[string]any{"session_id": id, "terms": []any{"missing"}}, true)
	call("gograph_query", map[string]any{"session_id": id, "intention": "owner query", "terms": []any{"missing"}}, false)
	text := call("gograph_session_create", map[string]any{"custom_word": "second"}, false)
	id2 := strings.Split(text, "\"")[1]
	call("gograph_session_end", map[string]any{"session_id": id}, false)
	call("gograph_query", map[string]any{"session_id": id2, "intention": "second owner", "terms": []any{"missing"}}, false)
	call("gograph_session_end", map[string]any{"session_id": id2}, false)
}
