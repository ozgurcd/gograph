package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
	mcppkg "github.com/ozgurcd/gograph/internal/mcp"
)

func answers178Fixture(t *testing.T, precise bool) (string, map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)) {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS("../../testdata/answers178")); err != nil {
		t.Fatal(err)
	}
	build := BuildGraph
	if precise {
		build = buildPreciseGraph
	}
	g, err := build(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gograph", "graph.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	previous := mcppkg.ExposeToolsForTesting
	handlers := make(map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error))
	mcppkg.ExposeToolsForTesting = handlers
	t.Cleanup(func() { mcppkg.ExposeToolsForTesting = previous })
	mcppkg.NewServer(g, func() (*graph.Graph, error) { return g, nil }, BuildGraph,
		func(context.Context, string) (*graph.Graph, error) { return g, nil }, "1.7.7")
	return root, handlers
}

func TestAnswers178ASTEmptyWarning(t *testing.T) {
	root, handlers := answers178Fixture(t, false)
	for _, tc := range []struct{ command, term, argument string }{
		{"tests", "answers178.Service.Callback", "symbol"},
		{"envs", "FIXTURE_SETTING", "term"},
		{"usages", "ErrLimited", "type"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			for _, mode := range []string{"text", "json"} {
				args := []string{tc.command, tc.term}
				if mode == "json" {
					args = append(args, "--json")
				}
				out, stderr, code := runCLIParityInDir(t, root, func() int { return Run(args) })
				if code != 0 || !strings.Contains(out, "may be incomplete") || !strings.Contains(out, "gograph build . --precise") {
					t.Errorf("%s: code=%d out=%s stderr=%s", mode, code, out, stderr)
				}
				if mode == "json" {
					var doc struct {
						Count   int   `json:"count"`
						Results []any `json:"results"`
					}
					if err := json.Unmarshal([]byte(out), &doc); err != nil {
						t.Fatal(err)
					}
					if doc.Count != 0 || len(doc.Results) != 0 {
						t.Errorf("warning must not be a result: %s", out)
					}
				}
			}
			text := answers177MCP(t, handlers["gograph_"+tc.command], map[string]any{tc.argument: tc.term})
			if !strings.Contains(text, "may be incomplete") || !strings.Contains(text, "gograph build . --precise") {
				t.Errorf("MCP: %s", text)
			}
			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]any{tc.argument: tc.term}
			result, err := handlers["gograph_"+tc.command](context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "may be incomplete") || !strings.Contains(string(data), "gograph build . --precise") {
				t.Errorf("MCP structured warning missing: %s", data)
			}
		})
	}
}

func TestAnswers178FactoryReturns(t *testing.T) {
	root, handlers := answers178Fixture(t, true)
	for _, name := range []string{"DeleteFactory", "MissingFactory", "NamedFactory", "nested.Factory"} {
		out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"routes", name}) })
		if code != 0 || strings.Contains(out, "dynamic handler") || !strings.Contains(out, "returned handler") {
			t.Errorf("CLI %s code=%d out=%s stderr=%s", name, code, out, stderr)
		}
		text := answers177MCP(t, handlers["gograph_routes"], map[string]any{"term": name})
		if strings.Contains(text, "dynamic handler") || !strings.Contains(text, "returned handler") {
			t.Errorf("MCP %s: %s", name, text)
		}
	}
	out, _, code := runCLIParityInDir(t, root, func() int { return Run([]string{"endpoint", "DeleteFactory", "--json"}) })
	if code != 0 || !strings.Contains(out, "deleteRecord") || strings.Contains(out, "setupOnly") || !strings.Contains(out, "returned_handler") {
		t.Errorf("CLI endpoint: %s", out)
	}
	text := answers177MCP(t, handlers["gograph_endpoint"], map[string]any{"query": "DeleteFactory"})
	if !strings.Contains(text, "deleteRecord") || strings.Contains(text, "setupOnly") || !strings.Contains(text, "returned_handler") {
		t.Errorf("MCP endpoint: %s", text)
	}
	text = answers177MCP(t, handlers["gograph_routes"], map[string]any{"term": "UnknownFactory"})
	var envelope struct {
		Results []struct {
			EnvReads  []string `json:"env_reads"`
			CallChain []struct {
				Symbol string `json:"symbol"`
			} `json:"call_chain"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Results) != 1 || len(envelope.Results[0].EnvReads) != 1 || envelope.Results[0].EnvReads[0] != "HANDLER_SETTING" {
		t.Errorf("missing handler environment evidence: %s", out)
	}
	if len(envelope.Results) == 1 {
		found := false
		for _, step := range envelope.Results[0].CallChain {
			found = found || step.Symbol == "deleteRecord"
			if step.Symbol == "setupOnly" {
				t.Error("factory setup leaked into handler chain")
			}
		}
		if !found {
			t.Errorf("missing handler call chain: %s", out)
		}
	}
	if !strings.Contains(text, "dynamic handler") {
		t.Errorf("unknown return must stay unresolved: %s", text)
	}
}

func TestAnswers178PreciseAnswers(t *testing.T) {
	root, handlers := answers178Fixture(t, true)
	for _, tc := range []struct{ command, term, arg, want string }{
		{"tests", "answers178.Service.Callback", "symbol", "TestField"},
		{"envs", "FIXTURE_SETTING", "term", "read via parameter"},
		{"usages", "ErrLimited", "type", "useError"},
	} {
		out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{tc.command, tc.term, "--json"}) })
		if code != 0 || !strings.Contains(out, tc.want) || strings.Contains(out, "may be incomplete") {
			t.Errorf("CLI %s: %d %s %s", tc.command, code, out, stderr)
		}
		text := answers177MCP(t, handlers["gograph_"+tc.command], map[string]any{tc.arg: tc.term})
		if !strings.Contains(text, tc.want) || strings.Contains(text, "may be incomplete") {
			t.Errorf("MCP %s: %s", tc.command, text)
		}
	}
}

func TestAnswers178FalseRoutes(t *testing.T) {
	for _, precise := range []bool{false, true} {
		name := "ast"
		if precise {
			name = "precise"
		}
		t.Run(name, func(t *testing.T) {
			root, handlers := answers178Fixture(t, precise)
			out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"routes", "--json"}) })
			if code != 0 {
				t.Fatalf("CLI: %d %s", code, stderr)
			}
			var doc struct {
				Results struct {
					Total int `json:"total"`
				} `json:"results"`
			}
			if err := json.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatal(err)
			}
			if doc.Results.Total != 5 || strings.Contains(out, "org_id") || strings.Contains(out, "client_id") {
				t.Errorf("CLI false routes: %s", out)
			}
			text := answers177MCP(t, handlers["gograph_routes"], nil)
			var page struct {
				Total int `json:"total"`
			}
			if err := json.Unmarshal([]byte(text), &page); err != nil {
				t.Fatal(err)
			}
			if page.Total != 5 || strings.Contains(text, "org_id") || strings.Contains(text, "client_id") {
				t.Errorf("MCP false routes: %s", text)
			}
			for _, path := range []string{"/api/:id", "/api/fallback", "/api/unknown", "/api/named", "/api/nested"} {
				if !strings.Contains(out, path) || !strings.Contains(text, path) {
					t.Errorf("lost real route %s", path)
				}
			}
		})
	}
}
