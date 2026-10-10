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
	"github.com/ozgurcd/gograph/internal/search"
)

func sessions2Fixture(t *testing.T) (string, *graph.Graph, map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)) {
	t.Helper()
	t.Setenv("GOGRAPH_SESSION", "")
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS("../../testdata/sessions2")); err != nil {
		t.Fatal(err)
	}
	g, err := buildPreciseGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	if g.Build == nil || g.Build.Precision != graph.PrecisionMode("precise") {
		t.Fatal("fixture must use precise constant identity evidence")
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
	handlers := make(map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error))
	previous := mcppkg.ExposeToolsForTesting
	mcppkg.ExposeToolsForTesting = handlers
	t.Cleanup(func() { mcppkg.ExposeToolsForTesting = previous })
	mcppkg.NewServer(g, func() (*graph.Graph, error) { return g, nil }, BuildGraph,
		func(context.Context, string) (*graph.Graph, error) { return g, nil }, "fixture")
	return root, g, handlers
}

func TestSessions2ConstantReferences(t *testing.T) {
	root, g, handlers := sessions2Fixture(t)
	for _, name := range []string{"AuthPolicyLocalOnly", "Plain"} {
		selector := "domain." + name
		uses := search.Usages(g, selector)
		if len(uses) != 4 {
			t.Errorf("%s: want comparison, case, literal, argument uses; got %+v", selector, uses)
		}
		for _, use := range uses {
			if use.Kind != "const" || strings.Contains(use.Detail, "Shadow") {
				t.Errorf("constant identity confused: %+v", use)
			}
		}
		for _, command := range []string{"usages", "query"} {
			t.Run(command+"/"+name, func(t *testing.T) {
				out, stderr, code := runCLIParityInDir(t, root, func() int {
					return Run([]string{command, selector, "--json"})
				})
				if code != 0 {
					t.Fatalf("CLI %s: %d %s %s", command, code, out, stderr)
				}
				arg := "type"
				if command == "query" {
					arg = "terms"
				}
				var value any = selector
				if command == "query" {
					value = []string{selector}
				}
				mcpOut := answers177MCP(t, handlers["gograph_"+command], map[string]any{arg: value})
				for _, output := range []string{out, mcpOut} {
					for _, function := range []string{"Compare", "Choose", "Construct", "Argument"} {
						if !strings.Contains(output, "referenced in "+function) {
							t.Errorf("%s omitted %s: %s", command, function, output)
						}
					}
					if strings.Contains(output, "referenced in Shadow") {
						t.Error("local shadow must not be a package constant usage")
					}
				}
			})
		}
	}
}

func TestSessions2BlankAssertions(t *testing.T) {
	for _, named := range []bool{false, true} {
		t.Run(map[bool]string{false: "blank-only", true: "mixed"}[named], func(t *testing.T) {
			testSessions2BlankAssertions(t, named)
		})
	}
}

func testSessions2BlankAssertions(t *testing.T, named bool) {
	root, _, _ := sessions2Fixture(t)
	answers1710Git(t, root, "init", "-q")
	answers1710Git(t, root, "add", "go.mod", "domain/policy.go", "service.go")
	answers1710Git(t, root, "commit", "-qm", "fixture")
	path := filepath.Join(root, "service.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(data), "var _ I = (*T)(nil)", "var _ I = T{}", 1)
	if named {
		changed = strings.Replace(changed, "func Argument() { accepts(domain.AuthPolicyLocalOnly, domain.Plain) }",
			"func Argument() { accepts(domain.AuthPolicyFederated, domain.Plain) }", 1)
	}
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	g, err := buildPreciseGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gograph", "graph.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	handlers := make(map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error))
	mcppkg.ExposeToolsForTesting = handlers
	mcppkg.NewServer(g, func() (*graph.Graph, error) { return g, nil }, BuildGraph,
		func(context.Context, string) (*graph.Graph, error) { return g, nil }, "fixture")
	for _, command := range []string{"plan", "review"} {
		t.Run(command, func(t *testing.T) {
			out, stderr, code := runCLIParityInDir(t, root, func() int {
				return Run([]string{command, "--uncommitted", "--json"})
			})
			if code != 0 || named && !strings.Contains(out, "Argument") {
				t.Errorf("CLI %s: %d %s %s", command, code, out, stderr)
			}
			mcpOut := answers177MCP(t, handlers["gograph_"+command], map[string]any{"uncommitted": true})
			if named && !strings.Contains(mcpOut, "Argument") || strings.Contains(mcpOut, "ambiguous graph identity") {
				t.Errorf("MCP %s: %s", command, mcpOut)
			}
			if command == "review" {
				for _, output := range []string{out, mcpOut} {
					if !strings.Contains(output, "Blank-identifier declarations") {
						t.Errorf("review must disclose skipped declarations: %s", output)
					}
				}
			}
		})
	}
}
