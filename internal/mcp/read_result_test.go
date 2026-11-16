package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/graphstate"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestMCPReadAnswerAndRefusal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fixture.go"), []byte("package fixture\nfunc Answer() int { return 42 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &graph.Graph{Root: root, Symbols: []graph.SymbolNode{{ID: "example.com/fixture::Answer", Name: "Answer", Kind: graph.KindFunction, File: "fixture.go", Line: 2, EndLine: 2}}}
	handlers := setupHandlers(t, g)
	for _, tool := range []string{"gograph_source", "gograph_context"} {
		for _, symbol := range []string{"Answer", "example.com/fixture::Answer", "Absent"} {
			t.Run(tool+"/"+symbol, func(t *testing.T) {
				req := mcp.CallToolRequest{}
				req.Params.Arguments = map[string]any{"symbol": symbol}
				result, err := handlers[tool](context.Background(), req)
				if err != nil || result == nil {
					t.Fatalf("read: %v, %v", result, err)
				}
				if result.StructuredContent != nil {
					t.Fatalf("read repeated its answer as structuredContent: %#v", result.StructuredContent)
				}
				text := readText(result)
				if symbol != "Absent" {
					if result.IsError || !strings.Contains(text, "func Answer() int { return 42 }") {
						t.Fatalf("read lost source: %s", text)
					}
					expected, err := search.Source(g, root, symbol)
					if err != nil {
						t.Fatal(err)
					}
					if tool == "gograph_source" && text != expected {
						t.Fatalf("source differs from shared CLI source: %q, want %q", text, expected)
					}
					if tool == "gograph_context" {
						var body map[string]any
						if err := json.Unmarshal([]byte(text), &body); err != nil || body["node"] == nil || body["source"] != expected {
							t.Fatalf("context lost node or source: %s, %v", text, err)
						}
					}
				} else if !result.IsError || !strings.Contains(text, "Absent") || !strings.Contains(text, "not found") {
					t.Fatalf("read lost refusal: isError=%v %s", result.IsError, text)
				}
				if readState(t, result).SchemaVersion == "" {
					t.Fatalf("read lost provenance: %#v", result.Meta)
				}
			})
		}
	}
}

func TestMCPReadReportsIndexedButUnreadableSource(t *testing.T) {
	root := t.TempDir()
	g := &graph.Graph{Root: root, Build: &graph.BuildMetadata{Complete: true, Precision: graph.PrecisionAST}, Symbols: []graph.SymbolNode{{ID: "example.com/fixture::MissingFile", Name: "MissingFile", Kind: graph.KindFunction, File: "missing.go", Line: 1, EndLine: 1}}}
	handlers := setupHandlers(t, g)
	for _, tool := range []string{"gograph_source", "gograph_context"} {
		t.Run(tool, func(t *testing.T) {
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]any{"symbol": "MissingFile"}
			result, err := handlers[tool](context.Background(), req)
			if err != nil || result == nil {
				t.Fatalf("read: %v %v", result, err)
			}
			text := readText(result)
			reason := text
			if tool == "gograph_context" {
				var body struct {
					SourceError string `json:"source_error"`
				}
				if err := json.Unmarshal([]byte(text), &body); err != nil {
					t.Fatalf("context text is not JSON: %v %s", err, text)
				}
				reason = body.SourceError
			}
			state := readState(t, result)
			if !strings.Contains(reason, "missing.go") || state.ReadDiagnostic != reason || state.Precision != "ast" {
				t.Fatalf("indexed unreadable source must be disclosed in text and _meta: %s, %+v", text, state)
			}
		})
	}
}

func readText(result *mcp.CallToolResult) string {
	var texts []string
	for _, content := range result.Content {
		if text, ok := mcp.AsTextContent(content); ok {
			texts = append(texts, text.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func readState(t *testing.T, result *mcp.CallToolResult) graphstate.State {
	t.Helper()
	if result.Meta == nil {
		t.Fatal("result has no _meta")
	}
	data, err := json.Marshal(result.Meta.AdditionalFields["gograph_graph_state"])
	if err != nil {
		t.Fatal(err)
	}
	var state graphstate.State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state
}
