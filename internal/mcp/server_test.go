package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
	mcppkg "github.com/ozgurcd/gograph/internal/mcp"
	"github.com/ozgurcd/gograph/internal/search"
)

// mockRebuild always returns the same graph
func mockRebuild(g *graph.Graph) func() (*graph.Graph, error) {
	return func() (*graph.Graph, error) {
		return g, nil
	}
}

func mockBuildGraph() func(string) (*graph.Graph, error) {
	return func(string) (*graph.Graph, error) {
		return &graph.Graph{}, nil
	}
}

func mockBuildBaseline() func(context.Context, string) (*graph.Graph, error) {
	return func(context.Context, string) (*graph.Graph, error) {
		return &graph.Graph{}, nil
	}
}

func setupHandlers(t *testing.T, g *graph.Graph) map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return setupHandlersWithBuilders(t, g, mockBuildGraph(), mockBuildBaseline())
}

func setupHandlersWithBuilders(
	t *testing.T,
	g *graph.Graph,
	buildGraph func(string) (*graph.Graph, error),
	buildBaseline func(context.Context, string) (*graph.Graph, error),
) map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	t.Helper()

	prev := mcppkg.ExposeToolsForTesting
	m := make(map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error))
	mcppkg.ExposeToolsForTesting = m
	t.Cleanup(func() {
		mcppkg.ExposeToolsForTesting = prev
	})

	mcppkg.NewServer(g, mockRebuild(g), buildGraph, buildBaseline, "dev")
	return m
}

func TestNewServer(t *testing.T) {
	g := &graph.Graph{}
	s := mcppkg.NewServer(g, mockRebuild(g), mockBuildGraph(), mockBuildBaseline(), "dev")
	if s == nil {
		t.Fatal("expected NewServer to return a valid server instance")
	}
}

func TestDocRefusesRepositorySourceSymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside.go")
	const sentinel = "BENIGN-EXTERNAL-SENTINEL"
	if err := os.WriteFile(outside, []byte("package outside\n// "+sentinel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.go")); err != nil {
		t.Skipf("create source symlink: %v", err)
	}

	handlers := setupHandlers(t, &graph.Graph{Root: root})
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"query": "fmt.Errorf"}
	result, err := handlers["gograph_doc"](context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("gograph_doc accepted repository source symlink: %+v", result)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "unsafe repository source") || strings.Contains(text, sentinel) {
		t.Fatalf("gograph_doc refusal = %q", text)
	}
}

func TestDocRefusesLinkedToolchainMetadata(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/docmetadata\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package docmetadata\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside-sum")
	const sentinel = "BENIGN-LINKED-SUM-SENTINEL"
	if err := os.WriteFile(outside, []byte(sentinel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "go.sum")); err != nil {
		t.Skipf("create go.sum symlink: %v", err)
	}

	handlers := setupHandlers(t, &graph.Graph{Root: root})
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"query": "fmt.Errorf"}
	result, err := handlers["gograph_doc"](context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("gograph_doc accepted linked go.sum: %+v", result)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "unsafe repository source") || strings.Contains(text, sentinel) {
		t.Fatalf("gograph_doc metadata refusal = %q", text)
	}
}

func TestDocRefusesLinkedWorkspaceMember(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	outsideMember := filepath.Join(base, "outside-member")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideMember, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26\n\nuse ./member\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package workspace\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const sentinel = "BENIGN-OUTSIDE-WORKSPACE-MEMBER"
	if err := os.WriteFile(filepath.Join(outsideMember, "go.mod"), []byte("module example.com/"+sentinel+"\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideMember, filepath.Join(root, "member")); err != nil {
		t.Skipf("create workspace member symlink: %v", err)
	}
	t.Setenv("GOENV", "off")
	t.Setenv("GOWORK", "auto")
	t.Setenv("GOTOOLCHAIN", "local")

	handlers := setupHandlers(t, &graph.Graph{Root: root})
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"query": "fmt.Errorf"}
	result, err := handlers["gograph_doc"](context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("gograph_doc accepted linked workspace member: %+v", result)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "unsafe repository source") || strings.Contains(text, sentinel) {
		t.Fatalf("gograph_doc workspace refusal = %q", text)
	}
}

func TestDocRefusesLinkedSourceInSiblingWorkspaceMember(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	selected := filepath.Join(workspace, "selected")
	sibling := filepath.Join(workspace, "sibling")
	for _, directory := range []string{selected, sibling} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "go.work"), []byte("go 1.26\n\nuse (\n\t./selected\n\t./sibling\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(selected, "go.mod"), []byte("module example.com/selected\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(selected, "main.go"), []byte("package selected\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "go.mod"), []byte("module example.com/sibling\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const sentinel = "BENIGN-SIBLING-WORKSPACE-SOURCE"
	outside := filepath.Join(base, "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n// "+sentinel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(sibling, "linked.go")); err != nil {
		t.Skipf("create sibling source symlink: %v", err)
	}
	t.Setenv("GOENV", "off")
	t.Setenv("GOWORK", "auto")
	t.Setenv("GOTOOLCHAIN", "local")

	handlers := setupHandlers(t, &graph.Graph{Root: selected})
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"query": "fmt.Errorf"}
	result, err := handlers["gograph_doc"](context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("gograph_doc accepted linked sibling workspace source: %+v", result)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "unsafe repository source") || strings.Contains(text, sentinel) {
		t.Fatalf("gograph_doc sibling-source refusal = %q", text)
	}
}

func TestDocRejectsFilesystemQuery(t *testing.T) {
	root := t.TempDir()
	handlers := setupHandlers(t, &graph.Graph{Root: root})
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"query": "../outside.go"}
	result, err := handlers["gograph_doc"](context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("gograph_doc accepted filesystem query: %+v", result)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "filesystem paths") {
		t.Fatalf("gograph_doc filesystem refusal = %q", text)
	}
}

func TestStatsRejectsLinkedPersistedGraph(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
		t.Fatal(err)
	}
	outsideGraph := filepath.Join(base, "outside-graph.json")
	loaded := &graph.Graph{
		Version: graph.Version,
		Root:    root,
		Build: &graph.BuildMetadata{
			SourcePolicyVersion: graph.CurrentSourcePolicyVersion,
			Precision:           graph.PrecisionAST,
		},
		Symbols: []graph.SymbolNode{{Name: "OutsideOne"}, {Name: "OutsideTwo"}},
	}
	data, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideGraph, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideGraph, filepath.Join(root, ".gograph", "graph.json")); err != nil {
		t.Skipf("create persisted graph symlink: %v", err)
	}
	fallback := &graph.Graph{
		Version: graph.Version,
		Root:    root,
		Build:   &graph.BuildMetadata{Precision: graph.PrecisionAST},
		Symbols: []graph.SymbolNode{{Name: "FallbackOnly"}},
	}
	handlers := setupHandlers(t, fallback)
	text := callTool(t, handlers["gograph_stats"], nil)
	var stats search.StatsResult
	if err := json.Unmarshal([]byte(text), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Symbols != 1 {
		t.Fatalf("stats symbols = %d, want fallback graph count 1", stats.Symbols)
	}
}

func TestSourceRejectsPoisonedGraphSymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside.go")
	const sentinel = "BENIGN-EXTERNAL-SENTINEL"
	if err := os.WriteFile(outside, []byte("package outside\nfunc OutsideOnly() string { return \""+sentinel+"\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.go")); err != nil {
		t.Skipf("create source symlink: %v", err)
	}
	g := &graph.Graph{
		Root: root,
		Symbols: []graph.SymbolNode{{
			ID: "example.com/security::OutsideOnly", Name: "OutsideOnly", Kind: graph.KindFunction,
			File: "linked.go", Line: 2, EndLine: 2,
		}},
	}
	handlers := setupHandlers(t, g)
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"symbol": "OutsideOnly"}
	result, err := handlers["gograph_source"](context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("gograph_source accepted poisoned graph: %+v", result)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if strings.Contains(text, sentinel) {
		t.Fatalf("gograph_source disclosed external sentinel: %q", text)
	}
}

func TestWikiConfinesRelativeOutputToGraphRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/wiki\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked-output")); err != nil {
		t.Skipf("create output ancestor symlink: %v", err)
	}
	handlers := setupHandlers(t, &graph.Graph{Root: root})

	for _, output := range []string{filepath.Join("linked-output", "wiki"), filepath.Join("..", "outside-wiki")} {
		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]any{"output": output}
		result, err := handlers["gograph_wiki"](context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError {
			t.Fatalf("gograph_wiki accepted unsafe relative output %q", output)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "wiki", "overview.md")); !os.IsNotExist(err) {
		t.Fatalf("linked ancestor received MCP wiki page: %v", err)
	}
}

func callTool(t *testing.T, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("tool handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error result: %s", res.Content[0].(mcp.TextContent).Text)
	}
	return res.Content[0].(mcp.TextContent).Text
}

func TestMCPCallTraversalOptionsMatchCLI(t *testing.T) {
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: "example::A", Name: "A", Kind: graph.KindFunction, File: "a.go"},
			{ID: "example::B", Name: "B", Kind: graph.KindFunction, File: "b.go"},
			{ID: "example::C", Name: "C", Kind: graph.KindFunction, File: "c.go"},
			{ID: "example::TestC", Name: "TestC", Kind: graph.KindFunction, File: "c_test.go"},
		},
		Calls: []graph.CallEdge{
			{CallerSymbolID: "example::A", CallerName: "A", CalleeSymbolID: "example::B", CalleeRaw: "B", File: "a.go"},
			{CallerSymbolID: "example::B", CallerName: "B", CalleeSymbolID: "example::C", CalleeRaw: "C", File: "b.go"},
			{CallerSymbolID: "example::TestC", CallerName: "TestC", CalleeSymbolID: "example::C", CalleeRaw: "C", File: "c_test.go"},
		},
	}
	handlers := setupHandlers(t, g)

	callers := callTool(t, handlers["gograph_callers"], map[string]any{
		"function": "C",
		"depth":    float64(2),
		"no_tests": true,
		"exact":    true,
	})
	for _, want := range []string{"A", "B"} {
		if !strings.Contains(callers, want) {
			t.Errorf("depth-2 callers missing %q: %s", want, callers)
		}
	}
	if strings.Contains(callers, "TestC") {
		t.Errorf("no_tests callers included TestC: %s", callers)
	}

	callees := callTool(t, handlers["gograph_callees"], map[string]any{
		"function": "A",
		"depth":    float64(2),
	})
	for _, want := range []string{"B", "C"} {
		if !strings.Contains(callees, want) {
			t.Errorf("depth-2 callees missing %q: %s", want, callees)
		}
	}
}

func TestMCPStaleInspectsPersistedIndexWithoutRefresh(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &graph.Graph{Root: root, GeneratedAt: time.Now().Add(-time.Hour)}

	prev := mcppkg.ExposeToolsForTesting
	handlers := make(map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error))
	mcppkg.ExposeToolsForTesting = handlers
	t.Cleanup(func() { mcppkg.ExposeToolsForTesting = prev })
	var rebuilds atomic.Int32
	mcppkg.NewServer(g, func() (*graph.Graph, error) {
		rebuilds.Add(1)
		return &graph.Graph{Root: root, GeneratedAt: time.Now()}, nil
	}, mockBuildGraph(), mockBuildBaseline(), "dev")

	text := callTool(t, handlers["gograph_stale"], nil)
	if !strings.Contains(text, `"is_stale": true`) {
		t.Fatalf("expected stale persisted index, got %s", text)
	}
	if !strings.Contains(text, `"changed_files": [`) || !strings.Contains(text, `"build_context_changed": false`) {
		t.Fatalf("stale MCP contract omitted stable collection/context fields: %s", text)
	}
	if got := rebuilds.Load(); got != 0 {
		t.Fatalf("gograph_stale rebuilt %d time(s), want 0", got)
	}
}

func TestMCPSummaryUsesReachabilityOrphans(t *testing.T) {
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: "example::main", Name: "main", Kind: graph.KindFunction, File: "main.go"},
			{ID: "example::live", Name: "live", Kind: graph.KindFunction, File: "main.go"},
			{ID: "example::deadA", Name: "deadA", Kind: graph.KindFunction, File: "main.go"},
			{ID: "example::deadB", Name: "deadB", Kind: graph.KindFunction, File: "main.go"},
		},
		Calls: []graph.CallEdge{
			{CallerSymbolID: "example::main", CallerName: "main", CalleeSymbolID: "example::live", CalleeRaw: "live", File: "main.go"},
			{CallerSymbolID: "example::deadA", CallerName: "deadA", CalleeSymbolID: "example::deadB", CalleeRaw: "deadB", File: "main.go"},
		},
	}
	text := callTool(t, setupHandlers(t, g)["gograph_summary"], nil)
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode summary: %v\n%s", err, text)
	}
	if got := int(out["orphan_count"].(float64)); got != 2 {
		t.Fatalf("summary orphan_count = %d, want 2 reachability orphans", got)
	}
}

func TestMCPBoundaryCreateAndSessionsUseGraphRoot(t *testing.T) {
	root := t.TempDir()
	g := &graph.Graph{Root: root}
	handlers := setupHandlers(t, g)

	created := callTool(t, handlers["gograph_boundaries_create"], nil)
	if !strings.Contains(created, `"created": true`) {
		t.Fatalf("unexpected boundaries_create response: %s", created)
	}
	if _, err := os.Stat(filepath.Join(root, ".gograph", "boundaries.json")); err != nil {
		t.Fatalf("boundary config was not written under graph root: %v", err)
	}

	callTool(t, handlers["gograph_session_create"], map[string]any{"custom_word": "rooted"})
	if _, err := os.Stat(filepath.Join(root, ".gograph", "active_session.json")); err != nil {
		t.Fatalf("session pointer was not written under graph root: %v", err)
	}
	callTool(t, handlers["gograph_session_end"], nil)
}

func TestMCPResponseSerialization(t *testing.T) {
	resp := mcppkg.MCPResponse{
		Summary: "Test plan",
		Findings: []search.Result{
			{Name: "Handler", Kind: "function"},
		},
		Risk: map[string]any{
			"public_api": "yes",
			"sql":        "no",
		},
		Tests:       []string{"auth_test.go"},
		TestResults: []search.Result{{Name: "auth_test.go", Kind: "file"}},
		Source:      "func ValidateToken() {}",
		Limitations: []string{"No SSA tracking"},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal MCPResponse: %v", err)
	}

	jsonStr := string(data)
	expectedKeys := []string{
		`"summary":"Test plan"`,
		`"findings":[{"kind":"function","name":"Handler"}]`,
		`"risk":{"public_api":"yes","sql":"no"}`,
		`"tests":["auth_test.go"]`,
		`"test_results":[{"kind":"file","name":"auth_test.go"}]`,
		`"source":"func ValidateToken() {}"`,
		`"limitations":["No SSA tracking"]`,
	}

	for _, k := range expectedKeys {
		if !strings.Contains(jsonStr, k) {
			t.Errorf("expected JSON to contain %s, got %s", k, jsonStr)
		}
	}
}

func TestAllToolAnnotations(t *testing.T) {
	g := &graph.Graph{}
	s := mcppkg.NewServer(g, mockRebuild(g), mockBuildGraph(), mockBuildBaseline(), "dev")

	tools := s.ListTools()
	if len(tools) == 0 {
		t.Fatal("no tools registered")
	}

	expectedMutating := map[string]struct {
		destructive bool
		idempotent  bool
	}{
		"gograph_boundaries_create": {destructive: false, idempotent: false},
		"gograph_session_create":    {destructive: false, idempotent: false},
		"gograph_session_end":       {destructive: false, idempotent: false},
		"gograph_session_cleanup":   {destructive: true, idempotent: true},
		"gograph_wiki":              {destructive: true, idempotent: true},
	}
	for name, st := range tools {
		ann := st.Tool.Annotations
		behavior, mutating := expectedMutating[name]
		if ann.ReadOnlyHint == nil || *ann.ReadOnlyHint == mutating {
			t.Errorf("tool %q: ReadOnlyHint = %v, mutating = %t", name, ann.ReadOnlyHint, mutating)
		}
		wantDestructive := mutating && behavior.destructive
		if ann.DestructiveHint == nil || *ann.DestructiveHint != wantDestructive {
			t.Errorf("tool %q: DestructiveHint = %v, want %t", name, ann.DestructiveHint, wantDestructive)
		}
		wantIdempotent := true
		if mutating {
			wantIdempotent = behavior.idempotent
		}
		if ann.IdempotentHint == nil || *ann.IdempotentHint != wantIdempotent {
			t.Errorf("tool %q: IdempotentHint = %v, want %t", name, ann.IdempotentHint, wantIdempotent)
		}
		wantOpenWorld := name == "gograph_doc"
		if ann.OpenWorldHint == nil || *ann.OpenWorldHint != wantOpenWorld {
			t.Errorf("tool %q: OpenWorldHint = %v, want %t", name, ann.OpenWorldHint, wantOpenWorld)
		}
	}
}

func TestPersistRefreshUpdatesOnlyRefreshingToolContracts(t *testing.T) {
	g := &graph.Graph{}
	defaultTools := mcppkg.NewServer(g, mockRebuild(g), mockBuildGraph(), mockBuildBaseline(), "dev").ListTools()
	persistTools := mcppkg.NewServer(
		g,
		mockRebuild(g),
		mockBuildGraph(),
		mockBuildBaseline(),
		"dev",
		mcppkg.ServerOptions{PersistRefresh: true},
	).ListTools()

	nonRefreshing := map[string]bool{
		"gograph_capabilities":    true,
		"gograph_stale":           true,
		"gograph_stats":           true,
		"gograph_doc":             true,
		"gograph_session_create":  true,
		"gograph_session_end":     true,
		"gograph_session_audit":   true,
		"gograph_session_cleanup": true,
	}
	assertSameHint := func(toolName, hintName string, got, want *bool) {
		t.Helper()
		if got == nil || want == nil {
			if got != want {
				t.Errorf("tool %q: %s = %v, want %v", toolName, hintName, got, want)
			}
			return
		}
		if *got != *want {
			t.Errorf("tool %q: %s = %t, want %t", toolName, hintName, *got, *want)
		}
	}

	refreshingCount := 0
	for name, persisted := range persistTools {
		baseline, ok := defaultTools[name]
		if !ok {
			t.Errorf("persist-refresh server registered unexpected tool %q", name)
			continue
		}
		got := persisted.Tool.Annotations
		want := baseline.Tool.Annotations
		if nonRefreshing[name] {
			assertSameHint(name, "ReadOnlyHint", got.ReadOnlyHint, want.ReadOnlyHint)
			assertSameHint(name, "DestructiveHint", got.DestructiveHint, want.DestructiveHint)
			assertSameHint(name, "IdempotentHint", got.IdempotentHint, want.IdempotentHint)
			assertSameHint(name, "OpenWorldHint", got.OpenWorldHint, want.OpenWorldHint)
			if persisted.Tool.Description != baseline.Tool.Description {
				t.Errorf("non-refreshing tool %q description changed in persist mode", name)
			}
			continue
		}

		refreshingCount++
		if got.ReadOnlyHint == nil || *got.ReadOnlyHint {
			t.Errorf("refreshing tool %q: ReadOnlyHint = %v, want false", name, got.ReadOnlyHint)
		}
		if got.DestructiveHint == nil || !*got.DestructiveHint {
			t.Errorf("refreshing tool %q: DestructiveHint = %v, want true", name, got.DestructiveHint)
		}
		assertSameHint(name, "IdempotentHint", got.IdempotentHint, want.IdempotentHint)
		assertSameHint(name, "OpenWorldHint", got.OpenWorldHint, want.OpenWorldHint)

		description := persisted.Tool.Description
		if !strings.Contains(description, "--persist-refresh") || !strings.Contains(description, ".gograph") {
			t.Errorf("refreshing tool %q does not disclose persistence: %q", name, description)
		}
		for _, contradiction := range []string{
			"Read-only; no persistent side effects.",
			"Read-only; no side effects.",
			"Read-only apart from temporary baseline extraction.",
			"Read-only; archives only a temp directory that is removed after the call.",
		} {
			if strings.Contains(description, contradiction) {
				t.Errorf("refreshing tool %q retains contradictory description %q", name, contradiction)
			}
		}
	}
	if len(persistTools) != 67 || len(nonRefreshing) != 8 || refreshingCount != 59 {
		t.Fatalf("tool classification: total=%d non-refreshing=%d refreshing=%d, want 67/8/59", len(persistTools), len(nonRefreshing), refreshingCount)
	}
	boundariesCreate := persistTools["gograph_boundaries_create"].Tool.Annotations.IdempotentHint
	if boundariesCreate == nil || *boundariesCreate {
		t.Fatalf("gograph_boundaries_create IdempotentHint = %v, want false", boundariesCreate)
	}
}

func TestCapabilitiesReportRefreshPersistenceMode(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
	}{
		{name: "disabled by default"},
		{name: "enabled", enabled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previous := mcppkg.ExposeToolsForTesting
			handlers := make(map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error))
			mcppkg.ExposeToolsForTesting = handlers
			t.Cleanup(func() { mcppkg.ExposeToolsForTesting = previous })

			options := []mcppkg.ServerOptions(nil)
			if tt.enabled {
				options = append(options, mcppkg.ServerOptions{PersistRefresh: true})
			}
			mcppkg.NewServer(
				&graph.Graph{},
				mockRebuild(&graph.Graph{}),
				mockBuildGraph(),
				mockBuildBaseline(),
				"dev",
				options...,
			)

			text := callTool(t, handlers["gograph_capabilities"], nil)
			var payload map[string]any
			if err := json.Unmarshal([]byte(text), &payload); err != nil {
				t.Fatalf("decode capabilities: %v", err)
			}
			persistence, ok := payload["refresh_persistence"].(map[string]any)
			if !ok {
				t.Fatalf("refresh_persistence = %#v, want object", payload["refresh_persistence"])
			}
			if got, ok := persistence["enabled"].(bool); !ok || got != tt.enabled {
				t.Errorf("refresh_persistence.enabled = %#v, want %t", persistence["enabled"], tt.enabled)
			}
			if got := persistence["artifact_directory"]; got != ".gograph" {
				t.Errorf("refresh_persistence.artifact_directory = %#v, want .gograph", got)
			}
			if got, _ := persistence["scope"].(string); !strings.Contains(got, "not a multi-branch cache") {
				t.Errorf("refresh_persistence.scope = %q, want branch-cache limitation", got)
			}
			if got := persistence["artifact_set"]; got != "graph.json plus nine Markdown reports; .artifacts.lock remains as operational coordination state" {
				t.Errorf("refresh_persistence.artifact_set = %#v", got)
			}
			if got, ok := persistence["updates_gitignore"].(bool); !ok || got {
				t.Errorf("refresh_persistence.updates_gitignore = %#v, want false", persistence["updates_gitignore"])
			}
			for _, field := range []string{"failure_behavior", "tool_annotations"} {
				if got, _ := persistence[field].(string); got == "" {
					t.Errorf("refresh_persistence.%s = %#v, want non-empty contract", field, persistence[field])
				}
			}
		})
	}
}

func TestCapabilitiesReportAnalysisMemoryPolicy(t *testing.T) {
	previous := mcppkg.ExposeToolsForTesting
	handlers := make(map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error))
	mcppkg.ExposeToolsForTesting = handlers
	t.Cleanup(func() { mcppkg.ExposeToolsForTesting = previous })

	mcppkg.NewServer(
		&graph.Graph{},
		mockRebuild(&graph.Graph{}),
		mockBuildGraph(),
		mockBuildBaseline(),
		"dev",
		mcppkg.ServerOptions{
			MemoryMode:              "low",
			RequestedMaxMemoryBytes: 1 << 30,
			EffectiveMaxMemoryBytes: 768 << 20,
		},
	)
	text := callTool(t, handlers["gograph_capabilities"], nil)
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	memory, ok := payload["analysis_memory"].(map[string]any)
	if !ok {
		t.Fatalf("analysis_memory = %#v, want object", payload["analysis_memory"])
	}
	if memory["mode"] != "low" || memory["requested_max_memory_bytes"] != float64(1<<30) || memory["effective_max_memory_bytes"] != float64(768<<20) {
		t.Fatalf("analysis_memory = %#v", memory)
	}
	for _, field := range []string{"limit_semantics", "result_semantics"} {
		if value, _ := memory[field].(string); value == "" {
			t.Errorf("analysis_memory.%s is empty", field)
		}
	}
}

func TestToolDescriptionsUsePrecisionAwareRefreshContract(t *testing.T) {
	g := &graph.Graph{}
	s := mcppkg.NewServer(g, mockRebuild(g), mockBuildGraph(), mockBuildBaseline(), "dev")

	staleDescriptions := []string{
		"rebuilds its in-memory AST graph only when source changed",
		"refreshes in-memory AST analysis",
	}
	for name, registered := range s.ListTools() {
		for _, staleDescription := range staleDescriptions {
			if strings.Contains(registered.Tool.Description, staleDescription) {
				t.Errorf("tool %q still advertises the obsolete AST-only refresh policy %q", name, staleDescription)
			}
		}
	}
}

func TestConcurrentHandlersSerializeGraphRefresh(t *testing.T) {
	prev := mcppkg.ExposeToolsForTesting
	handlers := make(map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error))
	mcppkg.ExposeToolsForTesting = handlers
	t.Cleanup(func() { mcppkg.ExposeToolsForTesting = prev })

	var active atomic.Int32
	var maxActive atomic.Int32
	rebuild := func() (*graph.Graph, error) {
		current := active.Add(1)
		for {
			observed := maxActive.Load()
			if current <= observed || maxActive.CompareAndSwap(observed, current) {
				break
			}
		}
		defer active.Add(-1)
		time.Sleep(5 * time.Millisecond)
		return &graph.Graph{}, nil
	}
	mcppkg.NewServer(&graph.Graph{}, rebuild, mockBuildGraph(), mockBuildBaseline(), "dev")
	handler := handlers["gograph_routes"]
	if handler == nil {
		t.Fatal("gograph_routes handler not found")
	}

	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := handler(context.Background(), mcp.CallToolRequest{})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("handler returned error: %v", err)
		}
	}
	if got := maxActive.Load(); got != 1 {
		t.Fatalf("maximum concurrent graph rebuilds = %d, want 1", got)
	}
}

func TestGographAPI_Validation(t *testing.T) {
	handlers := setupHandlers(t, &graph.Graph{})
	apiHandler, ok := handlers["gograph_api"]
	if !ok {
		t.Fatal("gograph_api handler not found")
	}

	unsafeInputs := []string{
		"main; rm -rf /",
		"main && echo bad",
		"main | cat",
		"$(whoami)",
		"`whoami`",
		"main\nother",
		"",
		"--upload-pack=bad",
		"main:bad",
		"main{bad}",
		"main[bad]",
	}

	for _, input := range unsafeInputs {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]any{"since": input}

		res, err := apiHandler(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error from handler: %v", err)
		}
		if !res.IsError {
			t.Errorf("expected error for unsafe input %q, got success", input)
		} else {
			text := res.Content[0].(mcp.TextContent).Text
			if !strings.Contains(text, "invalid git reference") {
				t.Errorf("expected unsafe input error message, got %s", text)
			}
		}
	}

	safeInputs := []string{
		"main",
		"HEAD~1",
		"HEAD^",
		"origin/main",
		"feature/api-drift",
		"v1.4.41",
	}

	for _, input := range safeInputs {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]any{"since": input}

		res, err := apiHandler(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error from handler: %v", err)
		}
		// It might fail because the branch doesn't exist in the test repo,
		// but it shouldn't fail with "invalid since value".
		if res.IsError {
			text := res.Content[0].(mcp.TextContent).Text
			if strings.Contains(text, "invalid since value") {
				t.Errorf("expected safe input %q to pass validation, but got: %s", input, text)
			}
		}
	}
}

func TestGographAPIPassesSavedGraphPathToBaselineBuilder(t *testing.T) {
	var baselineRef string
	buildBaseline := func(_ context.Context, ref string) (*graph.Graph, error) {
		baselineRef = ref
		return &graph.Graph{}, nil
	}
	root := t.TempDir()
	handlers := setupHandlersWithBuilders(t, &graph.Graph{Root: root}, mockBuildGraph(), buildBaseline)
	handler := handlers["gograph_api"]
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"since": "baselines/release candidate.json"}
	result, err := handler(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("gograph_api rejected saved graph path: %+v", result.Content)
	}
	if baselineRef != "baselines/release candidate.json" {
		t.Fatalf("baseline builder received %q", baselineRef)
	}
}

func TestGographCheckUsesBaselineBuilderForGitRefs(t *testing.T) {
	graphBuildCalls := 0
	buildGraph := func(string) (*graph.Graph, error) {
		graphBuildCalls++
		return &graph.Graph{}, nil
	}
	var baselineRef string
	buildBaseline := func(_ context.Context, ref string) (*graph.Graph, error) {
		baselineRef = ref
		return &graph.Graph{}, nil
	}
	root := t.TempDir()
	handlers := setupHandlersWithBuilders(t, &graph.Graph{Root: root}, buildGraph, buildBaseline)
	handler := handlers["gograph_check"]
	if handler == nil {
		t.Fatal("gograph_check handler not found")
	}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"since": "HEAD~1"}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("gograph_check: %v", err)
	}
	if result.IsError {
		t.Fatalf("gograph_check returned error: %+v", result.Content)
	}
	if baselineRef != "HEAD~1" {
		t.Fatalf("baseline builder received %q, want HEAD~1", baselineRef)
	}
	if graphBuildCalls != 0 {
		t.Fatalf("directory graph builder was called %d time(s) for a Git ref", graphBuildCalls)
	}
}

func TestGographCheckRejectsLinkedDefaultConfig(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
		t.Fatal(err)
	}
	const sentinel = "BENIGN-LINKED-MCP-CHECK-SENTINEL"
	outside := filepath.Join(base, "outside.json")
	if err := os.WriteFile(outside, []byte(`{"checks":{"`+sentinel+`":"error"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".gograph", "checks.json")); err != nil {
		t.Skipf("create check config symlink: %v", err)
	}

	handler := setupHandlers(t, &graph.Graph{Root: root})["gograph_check"]
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{}
	result, err := handler(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("gograph_check accepted linked config: %+v", result)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if strings.Contains(text, sentinel) || !strings.Contains(text, "unsafe repository source path") {
		t.Fatalf("linked config refusal = %q", text)
	}
}

func TestGographErrorFlow(t *testing.T) {
	handlers := setupHandlers(t, &graph.Graph{})
	handler, ok := handlers["gograph_errorflow"]
	if !ok {
		t.Fatal("gograph_errorflow handler not found")
	}

	// 1. Accepts term
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"term": "ErrTest"}
	res, _ := handler(context.Background(), req)
	if res.IsError {
		t.Errorf("expected success with term, got error")
	}
	text := res.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "ErrorFlow Report for ErrTest") {
		t.Errorf("term not picked up: %s", text)
	}

	// 2. Accepts query
	req.Params.Arguments = map[string]any{"query": "ErrQuery"}
	res, _ = handler(context.Background(), req)
	text = res.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "ErrorFlow Report for ErrQuery") {
		t.Errorf("query not picked up: %s", text)
	}

	// 3. Query wins over term
	req.Params.Arguments = map[string]any{"query": "ErrWinner", "term": "ErrLoser"}
	res, _ = handler(context.Background(), req)
	text = res.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "ErrorFlow Report for ErrWinner") {
		t.Errorf("query did not win over term: %s", text)
	}

	// 4. Check for SSA limitation
	if !strings.Contains(text, "Errorflow uses heuristic static call-graph") || !strings.Contains(text, "It does not perform SSA") {
		t.Errorf("missing SSA limitation text: %s", text)
	}
}

func TestGographFlowStructured(t *testing.T) {
	g := &graph.Graph{Root: t.TempDir(), FlowFunctions: []graph.FlowFunction{{
		ID: "example.com/app::Run", Name: "Run", File: "run.go",
		Facts: []graph.FlowFact{
			{Kind: "source", Target: "command", SourceKind: "environment", Detail: "os.Getenv(RUN_COMMAND)", Line: 4},
			{Kind: "sink", Inputs: []string{"command"}, Callee: "exec.Command", SinkKind: "process_execution", Detail: "exec.Command(command)", Line: 5},
		},
	}}}
	handler := setupHandlers(t, g)["gograph_flow"]
	if handler == nil {
		t.Fatal("gograph_flow handler not found")
	}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"source": "environment", "sink": "process_execution", "no_tests": true}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("gograph_flow: %v", err)
	}
	if result.IsError {
		t.Fatalf("gograph_flow returned error: %+v", result.Content)
	}
	text := result.Content[0].(mcp.TextContent).Text
	var response struct {
		Count    int                 `json:"count"`
		Findings []search.FlowResult `json:"findings"`
	}
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatalf("decode gograph_flow response: %v\n%s", err, text)
	}
	if response.Count != 1 || len(response.Findings) != 1 {
		t.Fatalf("expected one flow finding, got %+v", response)
	}
	finding := response.Findings[0]
	if finding.Source.Kind != "environment" || finding.Sink.Kind != "process_execution" || finding.Confidence != "medium" {
		t.Fatalf("unexpected flow finding: %+v", finding)
	}
}

func TestGographBoundaries_Structured(t *testing.T) {
	handlers := setupHandlers(t, &graph.Graph{})
	handler := handlers["gograph_boundaries"]

	// Create empty boundaries config in a relative path
	tmpDir, _ := os.MkdirTemp("", "gograph-test-*")
	defer func() { _ = os.RemoveAll(tmpDir) }()
	tmpFile := filepath.Join(tmpDir, "boundaries.json")
	if err := os.WriteFile(tmpFile, []byte(`{"layers":[]}`), 0644); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"config": tmpFile}

	res, _ := handler(context.Background(), req)
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Content[0].(mcp.TextContent).Text)
	}
	text := res.Content[0].(mcp.TextContent).Text

	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("expected JSON output from gograph_boundaries, got: %s", text)
	}

	summary, _ := out["summary"].(string)
	if !strings.Contains(summary, "No boundary violations found.") || strings.Contains(summary, "Architecture is clean!") {
		t.Errorf("expected neutral summary, got %v", summary)
	}

	risk, ok := out["risk"].(map[string]any)
	if !ok {
		t.Fatalf("missing risk object")
	}
	if pass, _ := risk["pass"].(bool); !pass {
		t.Errorf("expected pass = true")
	}
}

func TestGographBoundariesAcceptsLocalFilenameContainingDotDot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(".gograph", "boundaries..json")
	if err := os.WriteFile(filepath.Join(root, name), []byte(`{"layers":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	handlers := setupHandlers(t, &graph.Graph{Root: root})
	result := callTool(t, handlers["gograph_boundaries"], map[string]any{"config": name})
	if !strings.Contains(result, `"pass": true`) {
		t.Fatalf("gograph_boundaries rejected harmless local filename: %s", result)
	}
}

func TestGographContext_Structured(t *testing.T) {
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: "TargetFunc", Name: "TargetFunc", Kind: graph.KindFunction, File: "auth.go", Line: 10},
			{ID: "TestTargetFunc", Name: "TestTargetFunc", Kind: graph.KindFunction, File: "auth_test.go", Line: 20},
		},
		Calls: []graph.CallEdge{
			{CallerName: "Handler", CalleeRaw: "TargetFunc", File: "api.go", Line: 5},
			{CallerName: "TestTargetFunc", CalleeRaw: "TargetFunc", File: "auth_test.go", Line: 21},
		},
		TestEdges: []graph.TestEdge{
			{TestFunc: "TestTargetFunc", Target: "TargetFunc", File: "auth_test.go", Line: 21},
		},
	}
	handlers := setupHandlers(t, g)
	handler := handlers["gograph_context"]

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"symbol": "TargetFunc"}

	res, _ := handler(context.Background(), req)
	text := res.Content[0].(mcp.TextContent).Text

	var out mcppkg.MCPResponse
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("expected JSON, got: %v", err)
	}

	if out.Node == nil || out.Node.Name != "TargetFunc" {
		t.Errorf("expected node to be set to TargetFunc, got %v", out.Node)
	}
	if len(out.Callers) != 2 {
		t.Errorf("expected Callers array with Handler and TestTargetFunc, got %v", out.Callers)
	}

	if len(out.TestResults) != 1 || out.TestResults[0].Name != "TestTargetFunc" {
		t.Errorf("expected structured test_results to contain TestTargetFunc, got %v", out.TestResults)
	}
}

func TestGographSessionMCP(t *testing.T) {
	// Clean up any existing active session pointer first
	_ = os.Remove(".gograph/active_session.json")
	_ = os.RemoveAll(".gograph/sessions")

	handlers := setupHandlers(t, &graph.Graph{})
	createHandler, ok := handlers["gograph_session_create"]
	if !ok {
		t.Fatal("gograph_session_create handler not found")
	}
	endHandler, ok := handlers["gograph_session_end"]
	if !ok {
		t.Fatal("gograph_session_end handler not found")
	}
	auditHandler, ok := handlers["gograph_session_audit"]
	if !ok {
		t.Fatal("gograph_session_audit handler not found")
	}

	// 1. Create a session
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"custom_word": "mcp_test"}
	res, err := createHandler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected create session error: %v", err)
	}
	if res.IsError {
		t.Fatalf("create session failed: %s", res.Content[0].(mcp.TextContent).Text)
	}
	createText := res.Content[0].(mcp.TextContent).Text
	if !strings.Contains(createText, "successfully created and activated") {
		t.Errorf("expected success message, got %s", createText)
	}

	// 2. Try creating session again (should fail because one is active)
	res2, err := createHandler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected create session error: %v", err)
	}
	if !res2.IsError {
		t.Error("expected create session to fail when active session exists")
	}

	// 3. End the session
	resEnd, err := endHandler(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected end session error: %v", err)
	}
	if resEnd.IsError {
		t.Fatalf("end session failed: %s", resEnd.Content[0].(mcp.TextContent).Text)
	}
	endText := resEnd.Content[0].(mcp.TextContent).Text
	if !strings.Contains(endText, "successfully ended") {
		t.Errorf("expected success end message, got %s", endText)
	}

	// 4. Run audit
	reqAudit := mcp.CallToolRequest{}
	reqAudit.Params.Arguments = map[string]any{"json": true}
	resAudit, err := auditHandler(context.Background(), reqAudit)
	if err != nil {
		t.Fatalf("unexpected audit error: %v", err)
	}
	if resAudit.IsError {
		t.Fatalf("audit failed: %s", resAudit.Content[0].(mcp.TextContent).Text)
	}
	auditText := resAudit.Content[0].(mcp.TextContent).Text

	// Verify JSON output format
	var out map[string]any
	if err := json.Unmarshal([]byte(auditText), &out); err != nil {
		t.Fatalf("expected JSON output from gograph_session_audit, got: %s", auditText)
	}
	if sID, ok := out["session_id"].(string); !ok || !strings.Contains(sID, "mcp_test") {
		t.Errorf("expected session_id to contain mcp_test, got %v", out["session_id"])
	}

	// 5. Run session cleanup
	cleanupHandler, ok := handlers["gograph_session_cleanup"]
	if !ok {
		t.Fatal("gograph_session_cleanup handler not found")
	}
	resCleanup, err := cleanupHandler(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected cleanup error: %v", err)
	}
	if resCleanup.IsError {
		t.Fatalf("cleanup failed: %s", resCleanup.Content[0].(mcp.TextContent).Text)
	}
	cleanupText := resCleanup.Content[0].(mcp.TextContent).Text
	if !strings.Contains(cleanupText, "Successfully deleted") {
		t.Errorf("expected successful cleanup message, got %s", cleanupText)
	}
}

// TestMCPSessionTelemetry_PlanAndReviewIncrementCounters is the regression test
// for the bug where gograph_session_audit reported Total Commands: 0,
// Plan Rule Run: false, and Review Rule Run: false even after the coding agent
// invoked gograph_plan and gograph_review via MCP.
//
// Root cause: the MCP tool handlers called search.Plan / search.Review directly
// and completely bypassed session.LogCommand. The fix wraps every addTool
// registration with a telemetry shim that records the command name, elapsed
// time, and success/failure into the active session — identical to what the
// CLI Run() function does.
func TestMCPSessionTelemetry_PlanAndReviewIncrementCounters(t *testing.T) {
	// Isolate session files under a temp directory so this test cannot
	// corrupt a real developer session and is safe in parallel CI.
	tmpDir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir to tmp: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(origDir); err != nil {
			t.Logf("warning: could not restore wd: %v", err)
		}
	})

	handlers := setupHandlers(t, &graph.Graph{})

	for _, name := range []string{
		"gograph_session_create",
		"gograph_plan",
		"gograph_review",
		"gograph_session_end",
		"gograph_session_audit",
	} {
		if handlers[name] == nil {
			t.Fatalf("handler %q not found — tool not registered", name)
		}
	}

	// 1. Start a session.
	createReq := mcp.CallToolRequest{}
	createReq.Params.Arguments = map[string]any{"custom_word": "regression_test"}
	createRes, err := handlers["gograph_session_create"](context.Background(), createReq)
	if err != nil || createRes.IsError {
		t.Fatalf("session_create failed: err=%v", err)
	}

	// 2. Call gograph_plan — simulates the coding agent using plan via MCP.
	planReq := mcp.CallToolRequest{}
	planReq.Params.Arguments = map[string]any{"symbol": "Run"}
	_, _ = handlers["gograph_plan"](context.Background(), planReq)

	// 3. Call gograph_review — simulates the coding agent using review via MCP.
	reviewReq := mcp.CallToolRequest{}
	reviewReq.Params.Arguments = map[string]any{"symbol": "Run"}
	_, _ = handlers["gograph_review"](context.Background(), reviewReq)

	// 4. End the session.
	endRes, err := handlers["gograph_session_end"](context.Background(), mcp.CallToolRequest{})
	if err != nil || endRes.IsError {
		t.Fatalf("session_end failed: err=%v", err)
	}

	// 5. Audit — assertion heart of the regression test.
	auditReq := mcp.CallToolRequest{}
	auditReq.Params.Arguments = map[string]any{"json": true}
	auditRes, err := handlers["gograph_session_audit"](context.Background(), auditReq)
	if err != nil {
		t.Fatalf("session_audit error: %v", err)
	}
	if auditRes.IsError {
		t.Fatalf("session_audit failed: %s", auditRes.Content[0].(mcp.TextContent).Text)
	}

	auditText := auditRes.Content[0].(mcp.TextContent).Text
	var report map[string]any
	if err := json.Unmarshal([]byte(auditText), &report); err != nil {
		t.Fatalf("audit output is not JSON: %s", auditText)
	}

	// total_commands must be >= 2 (plan + review were logged).
	if tc, _ := report["total_commands"].(float64); tc < 2 {
		t.Errorf("total_commands = %.0f, want >= 2\nFull audit: %s", tc, auditText)
	}

	// plan_run must be true.
	if planRun, _ := report["plan_run"].(bool); !planRun {
		t.Errorf("plan_run = false, want true after gograph_plan via MCP\nFull audit: %s", auditText)
	}

	// review_run must be true.
	if reviewRun, _ := report["review_run"].(bool); !reviewRun {
		t.Errorf("review_run = false, want true after gograph_review via MCP\nFull audit: %s", auditText)
	}

	// Grade must not be F when both plan and review ran.
	if grade, _ := report["grade"].(string); strings.HasPrefix(grade, "F") {
		t.Errorf("grade = %q, want anything better than F\nFull audit: %s", grade, auditText)
	}
}
