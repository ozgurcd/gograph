package mcp_test

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

func setupHandlers(t *testing.T, g *graph.Graph) map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	t.Helper()

	prev := mcppkg.ExposeToolsForTesting
	m := make(map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error))
	mcppkg.ExposeToolsForTesting = m
	t.Cleanup(func() {
		mcppkg.ExposeToolsForTesting = prev
	})

	mcppkg.NewServer(g, mockRebuild(g), mockBuildGraph(), "dev")
	return m
}

func TestNewServer(t *testing.T) {
	g := &graph.Graph{}
	s := mcppkg.NewServer(g, mockRebuild(g), mockBuildGraph(), "dev")
	if s == nil {
		t.Fatal("expected NewServer to return a valid server instance")
	}
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
	s := mcppkg.NewServer(g, mockRebuild(g), mockBuildGraph(), "dev")

	tools := s.ListTools()
	if len(tools) == 0 {
		t.Fatal("no tools registered")
	}

	for name, st := range tools {
		ann := st.Tool.Annotations
		if ann.ReadOnlyHint == nil || !*ann.ReadOnlyHint {
			t.Errorf("tool %q: ReadOnlyHint must be true, got %v", name, ann.ReadOnlyHint)
		}
		if ann.DestructiveHint == nil || *ann.DestructiveHint {
			t.Errorf("tool %q: DestructiveHint must be false, got %v", name, ann.DestructiveHint)
		}
		if ann.IdempotentHint == nil || !*ann.IdempotentHint {
			t.Errorf("tool %q: IdempotentHint must be true, got %v", name, ann.IdempotentHint)
		}
		if ann.OpenWorldHint == nil || *ann.OpenWorldHint {
			t.Errorf("tool %q: OpenWorldHint must be false, got %v", name, ann.OpenWorldHint)
		}
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
