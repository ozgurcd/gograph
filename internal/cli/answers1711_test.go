package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
	mcppkg "github.com/ozgurcd/gograph/internal/mcp"
)

func answers1711Fixture(t *testing.T) (string, map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)) {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS("../../testdata/answers1711")); err != nil {
		t.Fatal(err)
	}
	g, err := buildPreciseGraph(root)
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
		func(context.Context, string) (*graph.Graph, error) { return g, nil }, "1.7.10")
	return root, handlers
}

func TestAnswers1711Routes(t *testing.T) {
	root, handlers := answers1711Fixture(t)
	out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"routes", "--json"}) })
	if code != 0 {
		t.Fatalf("CLI: %d %s", code, stderr)
	}
	value := answers177MCP(t, handlers["gograph_routes"], map[string]any{})
	var cliPage struct {
		Results json.RawMessage `json:"results"`
	}
	var cliRows, mcpRows any
	if err := json.Unmarshal([]byte(out), &cliPage); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(cliPage.Results, &cliRows); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(value), &mcpRows); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cliRows, mcpRows) {
		t.Fatal("CLI and MCP route pages differ")
	}
	for _, answer := range []string{out, value} {
		for _, want := range []string{"GET /admin", "POST /admin/resources", "DELETE /resources", "unresolved path"} {
			if !strings.Contains(answer, want) {
				t.Errorf("missing %q in route answer: %s", want, answer)
			}
		}
	}
}

func TestAnswers1711Fields(t *testing.T) {
	root, handlers := answers1711Fixture(t)
	for _, command := range []string{"query", "mutate"} {
		for _, typ := range []string{"APIResource", "Client"} {
			selector, arg := "TokenTTLSecs", "term"
			if command == "mutate" {
				selector, arg = typ+".TokenTTLSecs", "field"
			}
			out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{command, selector, "--json"}) })
			if code != 0 {
				t.Fatalf("CLI: %d %s", code, stderr)
			}
			var envelope struct {
				Count int `json:"count"`
			}
			if err := json.Unmarshal([]byte(out), &envelope); err != nil {
				t.Fatal(err)
			}
			wantCount := 2
			if command == "mutate" {
				wantCount = 1
			}
			if envelope.Count != wantCount {
				t.Errorf("%s expected %d rows, got %d", command, wantCount, envelope.Count)
			}
			value := answers177MCP(t, handlers["gograph_"+command], map[string]any{arg: selector})
			for _, answer := range []string{out, value} {
				if !strings.Contains(answer, typ+".TokenTTLSecs") || !strings.Contains(answer, "app.go") {
					t.Errorf("%s missing field %s: %s", command, typ, answer)
				}
			}
		}
	}
}

func TestAnswers1711Selectors(t *testing.T) {
	root, handlers := answers1711Fixture(t)
	for _, command := range []string{"source", "context", "callers", "callees", "plan", "review", "risk", "identity", "query"} {
		selector, arg := "app.go:Update", "symbol"
		if command == "query" {
			arg = "term"
		}
		if command == "callers" || command == "callees" {
			arg = "function"
		}
		out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{command, selector, "--json"}) })
		value := answers177MCP(t, handlers["gograph_"+command], map[string]any{arg: selector})
		for _, answer := range []string{out, value} {
			if code != 0 || !strings.Contains(answer, "app.go") || strings.Contains(answer, `"status": "empty"`) || strings.Contains(answer, "not found") {
				t.Errorf("%s selector: %d %s %s", command, code, answer, stderr)
			}
		}
	}
}

func TestAnswers1711MutationLimit(t *testing.T) {
	root, handlers := answers1711Fixture(t)
	out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"mutate", "LoadedClient.Lifetime", "--json"}) })
	value := answers177MCP(t, handlers["gograph_mutate"], map[string]any{"field": "LoadedClient.Lifetime"})
	for _, answer := range []string{out, value} {
		if code != 0 || !strings.Contains(answer, "mutation-resolution limit") || !strings.Contains(answer, "LoadedClient.Lifetime") || !strings.Contains(answer, "pointer arguments") {
			t.Errorf("missing named mutation limit: %d %s %s", code, answer, stderr)
		}
	}
}

func TestAnswers1711SelectorCensus(t *testing.T) {
	root, handlers := answers1711Fixture(t)
	for _, test := range []struct{ command, selector, arg, want string }{
		{"node", "app.go:APIResource", "name", "APIResource"},
		{"impact", "app.go:Update", "symbol", "Handler"},
		{"explain", "app.go:Update", "symbol", "Update"},
		{"explore", "app.go:Update", "query", "Update"},
		{"fields", "app.go:APIResource", "struct", "TokenTTLSecs"},
		{"interfaces", "app.go:APIResource", "struct", "Resource"},
		{"implementers", "app.go:Resource", "interface", "APIResource"},
		{"mocks", "app.go:Resource", "interface", "FakeResource"},
		{"usages", "app.go:APIResource", "type", "usages file-selector limit"},
		{"tests", "app.go:Update", "symbol", "TestUpdate"},
		{"coverage", "app_test.go:TestUpdate", "test", "Update"},
	} {
		t.Run(test.command, func(t *testing.T) {
			out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{test.command, test.selector, "--json"}) })
			value := answers177MCP(t, handlers["gograph_"+test.command], map[string]any{test.arg: test.selector})
			for _, answer := range []string{out, value} {
				if code != 0 || !strings.Contains(answer, test.want) || strings.Contains(answer, `"status": "not_found"`) || strings.Contains(answer, `"found": false`) || strings.Contains(answer, `"status":"empty"`) || strings.Contains(answer, `"status": "empty"`) {
					t.Errorf("%s: code=%d %s %s", test.command, code, answer, stderr)
				}
			}
		})
	}
}

func TestAnswers1711SelectorLimits(t *testing.T) {
	root, handlers := answers1711Fixture(t)
	for _, command := range []string{"embeds", "constructors", "literals", "returnusage", "mutate", "path", "endpoint"} {
		t.Run(command, func(t *testing.T) {
			selector := "app.go:APIResource"
			args := []string{command, selector, "--json"}
			parameter := map[string]string{"embeds": "struct", "constructors": "struct", "literals": "type", "returnusage": "function", "mutate": "field", "path": "from", "endpoint": "query"}[command]
			parameters := map[string]any{parameter: selector}
			if command == "path" {
				args = []string{command, selector, "Update", "--json"}
				parameters["to"] = "Update"
			}
			out, stderr, code := runCLIParityInDir(t, root, func() int { return Run(args) })
			value := answers177MCP(t, handlers["gograph_"+command], parameters)
			if code == 0 {
				t.Errorf("unsupported selector silently succeeded: %s", out)
			}
			for _, answer := range []string{out + stderr, value} {
				if !strings.Contains(answer, "file-selector limit") {
					t.Errorf("%s needs named limit: %s", command, answer)
				}
			}
		})
	}
}

func TestAnswers1711Capabilities(t *testing.T) {
	root, handlers := answers1711Fixture(t)
	out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"capabilities"}) })
	if code != 0 {
		t.Fatalf("capabilities: %d %s", code, stderr)
	}
	value := answers177MCP(t, handlers["gograph_capabilities"], nil)
	for _, answer := range []string{out, value} {
		for _, want := range []string{"freshness_context", "GOFLAGS", "startup tags", "file_selector_limits", "returnusage", "Usages supports file-qualified variables/constants", "mutation_resolution", "pointer arguments"} {
			if !strings.Contains(answer, want) {
				t.Errorf("capabilities missing %q", want)
			}
		}
	}
}

func TestAnswers1711QueryNoTests(t *testing.T) {
	root, handlers := answers1711Fixture(t)
	out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"query", "Update", "--no-tests", "--json"}) })
	value := answers177MCP(t, handlers["gograph_query"], map[string]any{"term": "Update", "no_tests": true})
	for _, answer := range []string{out, value} {
		if code != 0 || !strings.Contains(answer, "app.go") || strings.Contains(answer, "app_test.go") || strings.Contains(answer, "Update --no-tests") {
			t.Errorf("no-tests: %d %s %s", code, answer, stderr)
		}
	}
}

func TestAnswers1711TestFiles(t *testing.T) {
	root, handlers := answers1711Fixture(t)
	out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"tests", "Update", "--transitive", "--files-only"}) })
	if code != 0 || out != "app_test.go\n" {
		t.Errorf("files-only: %d %q %s", code, out, stderr)
	}
	value := answers177MCP(t, handlers["gograph_tests"], map[string]any{"symbol": "Update", "transitive": true})
	var report struct {
		Tests []struct {
			File string `json:"file"`
		} `json:"tests"`
	}
	if err := json.Unmarshal([]byte(value), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Tests) != 2 {
		t.Fatalf("MCP tests: %s", value)
	}
	for _, test := range report.Tests {
		if test.File != strings.TrimSpace(out) {
			t.Errorf("CLI/MCP file mismatch: %s", value)
		}
	}
}

func TestAnswers1711FreshnessContext(t *testing.T) {
	t.Setenv("GOFLAGS", "-tags=census")
	root, handlers := answers1711Fixture(t)
	t.Setenv("GOFLAGS", "")
	out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"query", "Tagged", "--json"}) })
	if code != 0 {
		t.Fatalf("CLI: %d %s", code, stderr)
	}
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"term": "Tagged"}
	result, err := handlers["gograph_query"](context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, answer := range []string{out, string(data)} {
		if !strings.Contains(answer, "freshness_context") {
			t.Errorf("freshness has no named check context: %s", answer)
		}
	}
}
