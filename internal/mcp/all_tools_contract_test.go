package mcp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestEveryMCPAnalysisToolExecutesItsDocumentedMode(t *testing.T) {
	root := t.TempDir()
	source := `package sample

type Config struct { Root string }
func Work() error { return nil }
func main() { _ = Work() }
`
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/audit\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gograph", "boundaries.json"), []byte(`{"layers":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	generated := time.Now().Add(time.Hour)
	g := &graph.Graph{
		Version:     graph.Version,
		Root:        root,
		GeneratedAt: generated,
		Build:       &graph.BuildMetadata{ScannedFiles: 2, ParsedFiles: 2, Complete: true},
		Packages: []graph.PackageNode{{
			ID: "sample", Name: "sample", ImportPathBestEffort: "example.com/audit", Dir: ".", Files: []string{"main.go"},
		}},
		Files: []graph.FileNode{{ID: "main.go", Path: "main.go", PackageName: "sample", Lines: 5}},
		Symbols: []graph.SymbolNode{
			{ID: "example.com/audit::main", Name: "main", Kind: graph.KindFunction, PackageName: "sample", File: "main.go", Line: 5, EndLine: 5, Signature: "func main()"},
			{ID: "example.com/audit::Work", Name: "Work", Kind: graph.KindFunction, PackageName: "sample", File: "main.go", Line: 4, EndLine: 4, Signature: "func Work() error"},
			{ID: "example.com/audit::NewConfig", Name: "NewConfig", Kind: graph.KindFunction, PackageName: "sample", File: "main.go", Line: 4, EndLine: 4, Signature: "func NewConfig() *Config"},
			{ID: "example.com/audit::Config", Name: "Config", Kind: graph.KindStruct, PackageName: "sample", File: "main.go", Line: 3, EndLine: 3, StructFields: []graph.StructField{{Name: "Root", Type: "string", Tag: `db:"workers"`}}, EmbeddedStructs: []string{"Base"}},
			{ID: "example.com/audit::Runner", Name: "Runner", Kind: graph.KindInterface, PackageName: "sample", File: "main.go", Line: 3, EndLine: 3, InterfaceMethods: map[string]string{"Run": "func()"}},
			{ID: "example.com/audit::Service", Name: "Service", Kind: graph.KindStruct, PackageName: "sample", File: "main.go", Line: 3, EndLine: 3},
			{ID: "example.com/audit::Global", Name: "Global", Kind: graph.KindVar, PackageName: "sample", File: "main.go", Line: 2, EndLine: 2},
			{ID: "example.com/audit::TestFixture", Name: "TestFixture", Kind: graph.KindStruct, PackageName: "sample", File: "main_test.go", Line: 3, EndLine: 3},
		},
		Calls:       []graph.CallEdge{{CallerSymbolID: "example.com/audit::main", CallerName: "main", CalleeSymbolID: "example.com/audit::Work", CalleeRaw: "Work", File: "main.go", Line: 5, ReturnUsage: "assigned"}},
		Imports:     []graph.ImportEdge{{FromFile: "main.go", FromPackage: "sample", ImportPath: "fmt"}},
		Routes:      []graph.HTTPRoute{{Method: "GET", Path: "/work", Handler: "Work", File: "main.go", Line: 5}},
		SQLs:        []graph.SQLEdge{{Query: "SELECT 1", Function: "Work", File: "main.go", Line: 4}},
		EnvReads:    []graph.EnvRead{{Key: "AUDIT_TOKEN", Accessor: "os.Getenv", Function: "Work", File: "main.go", Line: 4}},
		Errors:      []graph.ErrorEdge{{Message: "audit failed", Function: "Work", File: "main.go", Line: 4}},
		Concurrency: []graph.ConcurrencyNode{{Kind: "goroutine", Function: "Work", File: "main.go", Line: 4}},
		TestEdges:   []graph.TestEdge{{TestFunc: "TestWork", Target: "Work", File: "main_test.go", Line: 4}},
		Implements:  []graph.ImplementsEdge{{Interface: "Runner", Concrete: "Service"}},
		Mutations:   []graph.MutationEdge{{TypeName: "Config", Field: "Root", Function: "Work", File: "main.go", Line: 4}},
		Literals:    []graph.LiteralEdge{{TypeName: "Config", Function: "NewConfig", File: "main.go", Line: 4}},
		HTTPCalls:   []graph.HTTPCallEdge{{SourceFile: "main.go", SourceLine: 4, FunctionName: "Work", Method: "GET", URL: "https://example.test/work"}},
		FlowFunctions: []graph.FlowFunction{{
			ID: "example.com/audit::Work", Name: "Work", File: "main.go",
			Facts: []graph.FlowFact{
				{Kind: "source", Target: "token", SourceKind: "environment", Detail: "os.Getenv(AUDIT_TOKEN)", Line: 4},
				{Kind: "sink", Inputs: []string{"token"}, Callee: "os.WriteFile", SinkKind: "filesystem", Detail: "os.WriteFile(token, nil, 0600)", Line: 4},
			},
		}},
	}
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gograph", "graph.json"), data, 0o640); err != nil {
		t.Fatal(err)
	}

	handlers := setupHandlers(t, g)
	args := map[string]map[string]any{
		"gograph_query":             {"terms": []any{"Work", "Config"}},
		"gograph_focus":             {"package": "sample"},
		"gograph_callers":           {"function": "Work", "depth": float64(2), "no_tests": true},
		"gograph_callees":           {"function": "main", "depth": float64(2)},
		"gograph_implementers":      {"interface": "Runner"},
		"gograph_fields":            {"struct": "Config"},
		"gograph_source":            {"symbol": "Work"},
		"gograph_impact":            {"symbol": "Work"},
		"gograph_boundaries":        {"config": ".gograph/boundaries.json"},
		"gograph_boundaries_create": {"config": ".gograph/generated-boundaries.json"},
		"gograph_endpoint":          {"query": "Work", "depth": float64(3), "include_tests": true},
		"gograph_api":               {"since": "HEAD"},
		"gograph_context":           {"symbol": "Work", "exact": true},
		"gograph_plan":              {"symbol": "Work", "with_context": true},
		"gograph_review":            {"symbol": "Work"},
		"gograph_risk":              {"symbol": "Work"},
		"gograph_errorflow":         {"query": "audit failed", "no_tests": true},
		"gograph_flow":              {"source": "environment", "sink": "filesystem", "no_tests": true},
		"gograph_imports":           {"package": "fmt"},
		"gograph_dependents":        {"package": "fmt"},
		"gograph_sql":               {"term": "SELECT"},
		"gograph_errors":            {"term": "audit", "no_tests": true},
		"gograph_embeds":            {"struct": "Base"},
		"gograph_public":            {"package": "sample"},
		"gograph_usages":            {"type": "Config"},
		"gograph_literals":          {"struct": "Config"},
		"gograph_constructors":      {"struct": "Config"},
		"gograph_schema":            {"table": "workers"},
		"gograph_globals":           {"package": "sample"},
		"gograph_mocks":             {"interface": "Runner"},
		"gograph_explain":           {"symbol": "Work"},
		"gograph_node":              {"name": "Work"},
		"gograph_envs":              {"term": "AUDIT"},
		"gograph_interfaces":        {"struct": "Service"},
		"gograph_tests":             {"symbol": "Work"},
		"gograph_coverage":          {"test": "TestWork", "exact_only": false},
		"gograph_identity":          {"symbol": "Work"},
		"gograph_hotspot":           {"top": float64(5), "include_tests": false},
		"gograph_deps":              {"package": "sample", "transitive": true},
		"gograph_path":              {"from": "main", "to": "Work"},
		"gograph_complexity":        {"symbol": "Work"},
		"gograph_coupling":          {"package": "sample", "internal_only": true},
		"gograph_arity":             {"min": float64(1)},
		"gograph_concurrency":       {"term": "goroutine"},
		"gograph_httpcalls":         {"term": "GET"},
		"gograph_fixtures":          {"package": "sample"},
		"gograph_godobj":            {"methods": float64(0), "fields": float64(0), "calls": float64(0), "top": float64(0)},
		"gograph_returnusage":       {"function": "Work"},
		"gograph_mutate":            {"field": "Config.Root"},
		"gograph_trace":             {"term": "audit failed", "no_tests": true},
		"gograph_diagram":           {"group_by": "package", "max_depth": float64(2)},
		"gograph_check":             {},
		"gograph_wiki":              {"output": filepath.Join(root, "wiki")},
		"gograph_untested":          {"pkg": "sample", "top": float64(5)},
		"gograph_doc":               {"query": "fmt.Errorf"},
	}

	called := 0
	for name, handler := range handlers {
		if strings.HasPrefix(name, "gograph_session_") {
			continue
		}
		called++
		callTool(t, handler, args[name])
	}
	if called != 63 {
		t.Fatalf("executed %d non-session MCP tools, want 63", called)
	}
}
