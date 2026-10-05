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

func answers179Fixture(t *testing.T, precise bool) (string, map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)) {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS("../../testdata/answers179")); err != nil {
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
		func(context.Context, string) (*graph.Graph, error) { return g, nil }, "1.7.8")
	return root, handlers
}

func TestAnswers179EndpointUnique(t *testing.T) {
	root, handlers := answers179Fixture(t, true)
	out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"endpoint", "DeleteFactory", "--json"}) })
	if code != 0 {
		t.Fatalf("CLI: %d %s %s", code, out, stderr)
	}
	var envelope struct {
		Results []struct {
			CallChain []struct {
				Symbol string `json:"symbol"`
			} `json:"call_chain"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Results) != 1 {
		t.Fatalf("endpoint count: %s", out)
	}
	for _, symbol := range []string{"deleteRecord", "auditRecord"} {
		count := 0
		for _, step := range envelope.Results[0].CallChain {
			if step.Symbol == symbol {
				count++
			}
		}
		if count != 1 {
			t.Errorf("CLI %s appears %d times, want 1", symbol, count)
		}
	}
	text := answers177MCP(t, handlers["gograph_endpoint"], map[string]any{"query": "DeleteFactory"})
	for _, symbol := range []string{"deleteRecord", "auditRecord"} {
		needle := `"symbol": "` + symbol + `"`
		if count := strings.Count(text, needle); count != 1 {
			t.Errorf("MCP %s appears %d times, want 1: %s", symbol, count, text)
		}
	}
}

func TestAnswers179RoutesPrecisionHint(t *testing.T) {
	for _, precise := range []bool{false, true} {
		t.Run(map[bool]string{false: "ast", true: "precise"}[precise], func(t *testing.T) {
			root, handlers := answers179Fixture(t, precise)
			for _, mode := range [][]string{{"routes", "DeleteFactory"}, {"routes", "DeleteFactory", "--json"}} {
				out, stderr, code := runCLIParityInDir(t, root, func() int { return Run(mode) })
				if code != 0 {
					t.Fatalf("CLI: %d %s %s", code, out, stderr)
				}
				assertAnswers179Hint(t, out, precise)
			}
			assertAnswers179Hint(t, answers177MCP(t, handlers["gograph_routes"], map[string]any{"term": "DeleteFactory"}), precise)
		})
	}
}

func assertAnswers179Hint(t *testing.T, out string, precise bool) {
	t.Helper()
	hint := "A precise build resolves statically known factory handlers; run gograph build . --precise"
	if strings.Contains(out, hint) == precise {
		t.Errorf("precision=%t hint mismatch: %s", precise, out)
	}
}

func TestAnswers179EnvironmentFlags(t *testing.T) {
	for _, precise := range []bool{false, true} {
		t.Run(map[bool]string{false: "ast", true: "precise"}[precise], func(t *testing.T) {
			root, handlers := answers179Fixture(t, precise)
			for _, name := range []string{"Lookup", "Get", "Literal", "NoEnvironment"} {
				for _, command := range []string{"plan", "review", "risk"} {
					out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{command, "answers179." + name, "--json"}) })
					if code != 0 {
						t.Fatalf("CLI: %d %s %s", code, out, stderr)
					}
					var envelope struct {
						Results json.RawMessage `json:"results"`
					}
					if err := json.Unmarshal([]byte(out), &envelope); err != nil {
						t.Fatal(err)
					}
					m := answers177MCP(t, handlers["gograph_"+command], map[string]any{"symbol": "answers179." + name})
					for transport, value := range map[string]string{"CLI": string(envelope.Results), "MCP": m} {
						var result struct {
							Envs    []string `json:"envs"`
							Env     []string `json:"env"`
							Results []struct {
								EnvCount int `json:"env_count"`
							} `json:"results"`
						}
						if err := json.Unmarshal([]byte(value), &result); err != nil {
							t.Fatalf("%s %s: %v %s", transport, command, err, value)
						}
						count := len(result.Envs)
						if transport == "MCP" {
							count = len(result.Env)
						}
						if command == "risk" {
							if len(result.Results) != 1 {
								t.Fatalf("risk: %s", value)
							}
							count = result.Results[0].EnvCount
						}
						if (count > 0) != (name != "NoEnvironment") {
							t.Errorf("%s %s %s env count=%d: %s", transport, command, name, count, value)
						}
					}
				}
			}
		})
	}
}
