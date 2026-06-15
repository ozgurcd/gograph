package cli_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/ozgurcd/gograph/internal/cli"
)

func TestBuildGraph(t *testing.T) {
	// Create a temporary directory with a dummy Go file
	tmpDir := t.TempDir()
	dummyGo := filepath.Join(tmpDir, "main.go")
	content := `package main
import "fmt"
func main() {
	fmt.Println("Hello")
}
`
	if err := os.WriteFile(dummyGo, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create dummy file: %v", err)
	}

	g, err := cli.BuildGraph(tmpDir)
	if err != nil {
		t.Fatalf("BuildGraph failed: %v", err)
	}

	if g == nil {
		t.Fatal("expected non-nil graph")
	}
	if g.Root != tmpDir {
		t.Errorf("expected root %s, got %s", tmpDir, g.Root)
	}
	if len(g.Packages) == 0 {
		t.Fatal("expected at least one package")
	}
	if len(g.Files) == 0 {
		t.Fatal("expected at least one file")
	}
	if g.Files[0].PackageName != "main" {
		t.Errorf("expected package main, got %s", g.Files[0].PackageName)
	}

	// Check if the call was captured
	foundCall := false
	for _, call := range g.Calls {
		if call.CalleeRaw == "fmt.Println" {
			foundCall = true
		}
	}
	if !foundCall {
		t.Error("expected to find fmt.Println call in the graph")
	}
}

// TestAllCommandsRegistered parses the Run() switch statement in cli.go via
// go/ast and asserts every canonical CLI command has a registered case.
// This prevents the class of bug where a command is documented but never wired.
func TestAllCommandsRegistered(t *testing.T) {
	// Canonical list: every user-facing command that must be in the Run() switch.
	// Maintenance rule: when you add a command to help/capabilities, add it here too.
	want := []string{
		"build",
		"query",
		"focus",
		"node",
		"source",
		"public",
		"fields",
		"embeds",
		"imports",
		"callers",
		"callees",
		"impact",
		"implementers",
		"interfaces",
		"path",
		"stale",
		"stats",
		"orphans",
		"godobj",
		"complexity",
		"diagram",
		"coupling",
		"context",
		"hotspot",
		"httpcalls",
		"deps",
		"dependents",
		"changes",
		"capabilities",
		"mcp",
		"routes",
		"sql",
		"errors",
		"envs",
		"concurrency",
		"tests",
		"constructors",
		"literals",
		"usages",
		"returnusage",
		"schema",
		"globals",
		"mocks",
		"trace",
		"arity",
		"mutate",
		"skeleton",
		"api",
		"contract",
		"errorflow",
		"fixtures",
		"plan",
		"review",
		"risk",
		"boundaries",
		"endpoint",
		"explain",
		"gate",
		"snapshot",
		"check",
		"add-claude-plugin",
		"hook-guard",
		"--json",
		"--files-only",
		"--mermaid",
		"session",
		"--session",
		"wiki",
		"summary",
		"untested",
		"doc",
		"-i",
		"--intention",
		// aliases
		"help",
		"--help",
		"-h",
		"version",
		"--version",
		"-v",
	}

	// Locate cli.go relative to this test file.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	cliPath := filepath.Join(filepath.Dir(thisFile), "cli.go")

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, cliPath, nil, 0)
	if err != nil {
		t.Fatalf("failed to parse cli.go: %v", err)
	}

	// Walk the AST and collect all case clause string literals inside Run() and dispatch().
	registered := make(map[string]bool)
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		if fn.Name.Name != "Run" && fn.Name.Name != "dispatch" {
			return true
		}
		// Found Run or dispatch — now collect all CaseClause string values within it.
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			cc, ok := inner.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range cc.List {
				if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					// Strip surrounding quotes.
					val := lit.Value[1 : len(lit.Value)-1]
					registered[val] = true
				}
			}
			return true
		})
		return false
	})

	sort.Strings(want)
	var missing []string
	for _, cmd := range want {
		if !registered[cmd] {
			missing = append(missing, cmd)
		}
	}
	if len(missing) > 0 {
		t.Errorf("the following commands are documented but NOT registered in Run():\n  %v\nAdd a case for each in the Run() switch in cli.go.", missing)
	}

	// Inverse check: warn about cases in Run() not in the canonical list.
	wantSet := make(map[string]bool, len(want))
	for _, w := range want {
		wantSet[w] = true
	}
	var extra []string
	for cmd := range registered {
		if !wantSet[cmd] {
			extra = append(extra, cmd)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		t.Errorf("the following cases exist in Run() but are NOT in the canonical want list in this test:\n  %v\nAdd them to the want slice above.", extra)
	}
}
