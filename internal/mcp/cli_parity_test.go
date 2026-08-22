package mcp_test

import (
	"go/ast"
	"go/parser"
	"go/token"
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
		"build": true, "validate": true, "gate": true, "snapshot": true, "mcp": true,
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
}
