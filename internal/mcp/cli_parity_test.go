package mcp_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	mcppkg "github.com/ozgurcd/gograph/internal/mcp"
)

func TestCLIAndMCPQueryAnalysisParity(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	cliPath := filepath.Join(filepath.Dir(file), "..", "cli", "cli.go")
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, cliPath, nil, 0)
	if err != nil {
		t.Fatalf("parse CLI registry: %v", err)
	}
	cliCommands := make(map[string]bool)
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "dispatch" {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				if literal, ok := expr.(*ast.BasicLit); ok && literal.Kind == token.STRING {
					cliCommands[strings.Trim(literal.Value, `"`)] = true
				}
			}
			return true
		})
	}

	server := mcppkg.NewServer(&graph.Graph{}, mockRebuild(&graph.Graph{}), mockBuildGraph(), mockBuildBaseline(), "dev")
	mcpTools := make(map[string]bool)
	for name := range server.ListTools() {
		mcpTools[name] = true
		mappedCLI := strings.TrimPrefix(name, "gograph_")
		switch {
		case name == "gograph_boundaries_create":
			mappedCLI = "boundaries"
		case strings.HasPrefix(name, "gograph_session_"):
			mappedCLI = "session"
		}
		if !cliCommands[mappedCLI] {
			t.Errorf("MCP tool %q has no CLI feature equivalent %q", name, mappedCLI)
		}
	}

	intentionalCLIOnly := map[string]bool{
		"build": true, "validate": true, "doctor": true, "gate": true, "snapshot": true, "mcp": true,
		"workspace":         true, // namespace with its own four-tool workspace MCP server
		"add-claude-plugin": true, "hook-guard": true,
		"help": true, "--help": true, "-h": true,
		"version": true, "--version": true, "-v": true,
	}
	aliases := map[string]string{"contract": "api", "--session": "session"}
	for command := range cliCommands {
		if intentionalCLIOnly[command] {
			continue
		}
		if canonical := aliases[command]; canonical != "" {
			command = canonical
		}
		if command == "session" {
			for _, name := range []string{"gograph_session_create", "gograph_session_end", "gograph_session_audit", "gograph_session_cleanup"} {
				if !mcpTools[name] {
					t.Errorf("CLI session lifecycle is missing MCP tool %q", name)
				}
			}
			continue
		}
		if !mcpTools["gograph_"+command] {
			t.Errorf("CLI query/analysis command %q has no MCP equivalent", command)
		}
	}

	commandReference := readParityDoc(t, "..", "..", "docs-site", "content", "docs", "command-reference.md")
	agentGuide := readParityDoc(t, "..", "..", "docs", "coding-agent-usage.md")
	for name := range mcpTools {
		for path, content := range map[string]string{
			"docs-site/content/docs/command-reference.md": commandReference,
			"docs/coding-agent-usage.md":                  agentGuide,
		} {
			if !strings.Contains(content, "`"+name+"`") {
				t.Errorf("%s does not document registered MCP tool %q", path, name)
			}
		}
	}

	for command := range cliCommands {
		switch command {
		case "contract", "--session", "--help", "-h", "--version", "-v":
			continue
		case "workspace":
			if !strings.Contains(commandReference, "gograph workspace") {
				t.Error("command reference does not document the workspace CLI namespace")
			}
		case "help", "version":
			if !strings.Contains(commandReference, "### version and help") {
				t.Errorf("command reference does not document CLI command %q", command)
			}
		default:
			if !strings.Contains(commandReference, "### "+command+"\n") {
				t.Errorf("command reference does not document CLI command %q", command)
			}
		}
	}

	for _, name := range []string{
		"gograph_workspace_status",
		"gograph_workspace_query",
		"gograph_workspace_path",
		"gograph_workspace_impact",
	} {
		if !strings.Contains(commandReference, "`"+name+"`") {
			t.Errorf("command reference does not document workspace MCP tool %q", name)
		}
		if !strings.Contains(agentGuide, "`"+name+"`") {
			t.Errorf("coding-agent guide does not document workspace MCP tool %q", name)
		}
	}
}

func readParityDoc(t *testing.T, elems ...string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(append([]string{filepath.Dir(parityTestFile(t))}, elems...)...))
	if err != nil {
		t.Fatalf("read parity documentation: %v", err)
	}
	return string(content)
}

func parityTestFile(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return file
}
