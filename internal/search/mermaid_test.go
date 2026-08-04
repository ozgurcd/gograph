package search_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestMermaidOutputGenerators(t *testing.T) {
	// 1. Setup a simple mock graph
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: "pkg/auth::CreateUser", Name: "CreateUser", PackageName: "auth"},
			{ID: "pkg/db::SaveUser", Name: "SaveUser", PackageName: "db"},
			{ID: "pkg/cli::Run", Name: "Run", PackageName: "cli"},
		},
		Imports: []graph.ImportEdge{
			{FromPackage: "cli", ImportPath: "pkg/auth", FromFile: "cli.go"},
			{FromPackage: "auth", ImportPath: "pkg/db", FromFile: "auth.go"},
		},
		Calls: []graph.CallEdge{
			{
				CallerSymbolID: "pkg/cli::Run",
				CallerName:     "Run",
				CalleeSymbolID: "pkg/auth::CreateUser",
				CalleeRaw:      "auth.CreateUser",
				File:           "cli.go",
				Line:           10,
			},
			{
				CallerSymbolID: "pkg/auth::CreateUser",
				CallerName:     "CreateUser",
				CalleeSymbolID: "pkg/db::SaveUser",
				CalleeRaw:      "db.SaveUser",
				File:           "auth.go",
				Line:           20,
			},
		},
		Routes: []graph.HTTPRoute{
			{Method: "POST", Path: "/api/users", Handler: "CreateUser", File: "main.go", Line: 5},
		},
	}

	// 2. Test DepsToMermaid
	t.Run("DepsToMermaid", func(t *testing.T) {
		depsRes := &search.DepsResult{
			Package:    "cli",
			Direct:     []string{"pkg/auth"},
			Transitive: []string{"pkg/db"},
		}
		got := search.DepsToMermaid(g, depsRes)
		if !strings.Contains(got, "flowchart TD") {
			t.Errorf("expected TD flowchart, got:\n%s", got)
		}
		if !strings.Contains(got, "-->") {
			t.Errorf("expected arrows, got:\n%s", got)
		}
	})

	// 3. Test DependentsToMermaid
	t.Run("DependentsToMermaid", func(t *testing.T) {
		results := []search.Result{
			{Name: "cli", Kind: "package"},
		}
		got := search.DependentsToMermaid("auth", results)
		if !strings.Contains(got, "flowchart TD") || !strings.Contains(got, "cli") {
			t.Errorf("unexpected output:\n%s", got)
		}
	})

	// 4. Test CouplingToMermaid
	t.Run("CouplingToMermaid", func(t *testing.T) {
		opts := search.CouplingOptions{IncludeStdlib: true}
		got := search.CouplingToMermaid(g, "", opts)
		if !strings.Contains(got, "cli") || !strings.Contains(got, "pkg/auth") {
			t.Errorf("unexpected coupling output:\n%s", got)
		}
	})

	// 5. Test CallersToMermaid
	t.Run("CallersToMermaid", func(t *testing.T) {
		got := search.CallersToMermaid(g, "SaveUser", 2, true, false)
		if !strings.Contains(got, "flowchart LR") || !strings.Contains(got, "CreateUser") {
			t.Errorf("unexpected callers output:\n%s", got)
		}
	})

	// 6. Test CalleesToMermaid
	t.Run("CalleesToMermaid", func(t *testing.T) {
		got := search.CalleesToMermaid(g, "Run", 2, true)
		if !strings.Contains(got, "flowchart LR") || !strings.Contains(got, "CreateUser") {
			t.Errorf("unexpected callees output:\n%s", got)
		}
	})

	// 7. Test PathToMermaid
	t.Run("PathToMermaid", func(t *testing.T) {
		chain := []search.Result{
			{Name: "Run"},
			{Name: "CreateUser"},
			{Name: "SaveUser"},
		}
		got := search.PathToMermaid(chain)
		if !strings.Contains(got, "flowchart LR") || !strings.Contains(got, "-->") {
			t.Errorf("unexpected path output:\n%s", got)
		}
	})

	// 8. Test ImpactToMermaid
	t.Run("ImpactToMermaid", func(t *testing.T) {
		got := search.ImpactToMermaid(g, "SaveUser", true)
		if !strings.Contains(got, "CreateUser") || !strings.Contains(got, "SaveUser") {
			t.Errorf("unexpected impact output:\n%s", got)
		}
	})

	// 9. Test EndpointToMermaid
	t.Run("EndpointToMermaid", func(t *testing.T) {
		slices := []search.EndpointSlice{
			{
				Route:   "POST /api/users",
				Handler: "CreateUser",
				CallChain: []search.ChainStep{
					{
						Symbol:  "CreateUser",
						Callees: []string{"SaveUser"},
					},
				},
			},
		}
		got := search.EndpointToMermaid(slices)
		if !strings.Contains(got, "flowchart TD") || !strings.Contains(got, "POST /api/users") {
			t.Errorf("unexpected endpoint output:\n%s", got)
		}
	})
}

func TestCallersToMermaidExactAvoidsSubstringCollisions(t *testing.T) {
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: "example.com/mermaid::Load", Name: "Load", Kind: graph.KindFunction},
			{ID: "example.com/mermaid::Preload", Name: "Preload", Kind: graph.KindFunction},
			{ID: "example.com/mermaid::ExactCaller", Name: "ExactCaller", Kind: graph.KindFunction},
			{ID: "example.com/mermaid::CollisionCaller", Name: "CollisionCaller", Kind: graph.KindFunction},
		},
		Calls: []graph.CallEdge{
			{CallerSymbolID: "example.com/mermaid::ExactCaller", CallerName: "ExactCaller", CalleeSymbolID: "example.com/mermaid::Load", CalleeRaw: "Load"},
			{CallerSymbolID: "example.com/mermaid::CollisionCaller", CallerName: "CollisionCaller", CalleeSymbolID: "example.com/mermaid::Preload", CalleeRaw: "Preload"},
		},
	}

	fuzzy := search.CallersToMermaid(g, "Load", 1, true, false)
	if !strings.Contains(fuzzy, "CollisionCaller") {
		t.Fatalf("fuzzy callers diagram did not include the substring collision:\n%s", fuzzy)
	}
	exact := search.CallersToMermaid(g, "Load", 1, true, true)
	if !strings.Contains(exact, "ExactCaller") || strings.Contains(exact, "CollisionCaller") || strings.Contains(exact, "Preload") {
		t.Fatalf("exact callers diagram did not preserve exact caller semantics:\n%s", exact)
	}
}

func TestCallersAndImpactMermaidUseTheirOwnDepthBounds(t *testing.T) {
	g := &graph.Graph{}
	for i := 0; i <= 21; i++ {
		name := fmt.Sprintf("Hop%02d", i)
		id := "example.com/depth::" + name
		g.Symbols = append(g.Symbols, graph.SymbolNode{ID: id, Name: name, Kind: graph.KindFunction})
		if i > 0 {
			previous := fmt.Sprintf("Hop%02d", i-1)
			g.Calls = append(g.Calls, graph.CallEdge{
				CallerSymbolID: id,
				CallerName:     name,
				CalleeSymbolID: "example.com/depth::" + previous,
				CalleeRaw:      previous,
			})
		}
	}

	callers := search.CallersToMermaid(g, "Hop00", 20, true, false)
	if !strings.Contains(callers, `["Hop10"]`) || strings.Contains(callers, `["Hop11"]`) {
		t.Fatalf("public callers diagram did not clamp depth to 10:\n%s", callers)
	}
	impact := search.ImpactToMermaid(g, "Hop00", true)
	if !strings.Contains(impact, `["Hop20"]`) || strings.Contains(impact, `["Hop21"]`) {
		t.Fatalf("impact diagram did not traverse exactly its 20-hop bound:\n%s", impact)
	}
}

func TestDiagramToMermaid(t *testing.T) {
	// Use the actual module path (github.com/ozgurcd/gograph) so that module/service
	// grouping — which calls ReadModulePath(".") at runtime — can strip the prefix correctly.
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: "github.com/ozgurcd/gograph/cmd::main", Name: "main", PackageName: "cmd"},
			{ID: "github.com/ozgurcd/gograph/internal/auth::CreateUser", Name: "CreateUser", PackageName: "auth"},
			{ID: "github.com/ozgurcd/gograph/internal/db::SaveUser", Name: "SaveUser", PackageName: "db"},
		},
		Imports: []graph.ImportEdge{
			{FromPackage: "cmd", ImportPath: "github.com/ozgurcd/gograph/internal/auth", FromFile: "main.go"},
			{FromPackage: "auth", ImportPath: "github.com/ozgurcd/gograph/internal/db", FromFile: "auth.go"},
			{FromPackage: "cmd", ImportPath: "fmt", FromFile: "main.go"}, // stdlib — filtered by default
		},
	}

	t.Run("PackageLevel", func(t *testing.T) {
		got := search.DiagramToMermaid(g, "package", 0, false)
		if !strings.Contains(got, "flowchart LR") {
			t.Errorf("expected LR flowchart, got:\n%s", got)
		}
		if !strings.Contains(got, "-->") {
			t.Errorf("expected edges, got:\n%s", got)
		}
		// stdlib edge must be excluded
		if strings.Contains(got, "fmt") {
			t.Errorf("stdlib should be excluded by default, got:\n%s", got)
		}
	})

	t.Run("IncludeStdlib", func(t *testing.T) {
		got := search.DiagramToMermaid(g, "package", 0, true)
		if !strings.Contains(got, "fmt") {
			t.Errorf("expected stdlib edge when includeStdlib=true, got:\n%s", got)
		}
	})

	t.Run("ModuleLevel", func(t *testing.T) {
		got := search.DiagramToMermaid(g, "module", 0, false)
		// internal/auth and internal/db both collapse to "internal"; cmd stays "cmd".
		if strings.Contains(got, "github.com/ozgurcd/gograph/internal/auth") {
			t.Errorf("expected package paths to be collapsed in module mode, got:\n%s", got)
		}
		// cmd→internal edge should appear; the internal→internal self-loop must not.
		if !strings.Contains(got, "cmd") {
			t.Errorf("expected cmd node in module diagram, got:\n%s", got)
		}
	})

	t.Run("MaxDepth1", func(t *testing.T) {
		got := search.DiagramToMermaid(g, "package", 1, false)
		// At depth 1, only cmd→auth should appear (cmd is the entry package).
		// auth→db is depth 2 and must be absent.
		if strings.Contains(got, "db") {
			t.Errorf("depth-1 diagram should not contain depth-2 node 'db', got:\n%s", got)
		}
	})

	t.Run("ServiceLevel", func(t *testing.T) {
		// service-level uses 2 path segments within the module, so
		// internal/auth and internal/db stay distinct (unlike module which merges both to "internal").
		got := search.DiagramToMermaid(g, "service", 0, false)
		// Full package paths should be collapsed.
		if strings.Contains(got, "github.com/ozgurcd/gograph/internal/auth") {
			t.Errorf("expected paths to be collapsed in service mode, got:\n%s", got)
		}
		// Two-segment groups should appear.
		if !strings.Contains(got, "internal/auth") && !strings.Contains(got, "internal/db") {
			t.Errorf("expected two-segment groups in service mode, got:\n%s", got)
		}
	})

	t.Run("FileLevel", func(t *testing.T) {
		got := search.DiagramToMermaid(g, "file", 0, false)
		if !strings.Contains(got, "flowchart LR") {
			t.Errorf("expected LR flowchart, got:\n%s", got)
		}
		// File nodes should appear (auth.go, main.go).
		if !strings.Contains(got, "auth.go") && !strings.Contains(got, "main.go") {
			t.Errorf("expected file nodes in file mode, got:\n%s", got)
		}
	})
}
