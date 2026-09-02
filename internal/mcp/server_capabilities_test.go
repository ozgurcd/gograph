package mcp_test

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
)

func TestGographCapabilities(t *testing.T) {
	handlers := setupHandlers(t, &graph.Graph{})
	handler, ok := handlers["gograph_capabilities"]
	if !ok {
		t.Fatal("gograph_capabilities handler not found")
	}

	req := mcp.CallToolRequest{}
	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Content[0].(mcp.TextContent).Text)
	}

	text := res.Content[0].(mcp.TextContent).Text
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("expected JSON, got: %v", err)
	}
	if got, _ := out["version"].(string); got != "dev" {
		t.Fatalf("capabilities version = %q, want running server version dev", got)
	}

	// 1. Output includes every registered tool exactly once.
	tools, ok := out["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools array, got %v", out["tools"])
	}
	var capabilityNames []string
	for _, raw := range tools {
		entry, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("invalid capability entry: %#v", raw)
		}
		name, _ := entry["name"].(string)
		capabilityNames = append(capabilityNames, name)
	}
	var registeredNames []string
	for name := range handlers {
		registeredNames = append(registeredNames, name)
	}
	sort.Strings(capabilityNames)
	sort.Strings(registeredNames)
	if strings.Join(capabilityNames, "\n") != strings.Join(registeredNames, "\n") {
		t.Errorf("capabilities registry differs from live MCP registry\ncapabilities: %v\nregistered:   %v", capabilityNames, registeredNames)
	}
	transport, ok := out["transport_contract"].(map[string]any)
	if !ok {
		t.Fatalf("expected transport_contract object, got %T", out["transport_contract"])
	}
	if got := int(transport["project_server_tools"].(float64)); got != len(registeredNames) {
		t.Errorf("transport_contract project_server_tools = %d, registered = %d", got, len(registeredNames))
	}
	graphState, ok := out["graph_state"].(map[string]any)
	if !ok {
		t.Fatalf("expected graph_state object, got %T", out["graph_state"])
	}
	if graphState["schema"] != "gograph.graph-state.v1" || graphState["mcp_result_schema"] != "gograph.mcp-result.v1" {
		t.Fatalf("graph_state schemas = %#v", graphState)
	}
	for _, want := range []string{"gograph_workspace_status", "gograph_workspace_query", "gograph_workspace_path", "gograph_workspace_impact"} {
		if !jsonArrayContains(transport["workspace_server_tools"], want) {
			t.Errorf("transport_contract omits workspace MCP tool %q", want)
		}
	}
	for _, want := range []string{"build", "validate", "doctor", "workspace build/member refresh"} {
		if !jsonArrayContains(transport["cli_only_operations"], want) {
			t.Errorf("transport_contract omits CLI-only operation %q", want)
		}
	}

	toolsStr := string(text)
	expectedTools := []string{
		"gograph_capabilities", "gograph_explore", "gograph_context", "gograph_plan", "gograph_review",
		"gograph_errorflow", "gograph_api", "gograph_boundaries",
		"gograph_flow",
	}
	for _, tool := range expectedTools {
		if !strings.Contains(toolsStr, tool) {
			t.Errorf("expected tool %s not found in output", tool)
		}
	}

	// 2. Output includes recommended_workflows
	workflows, ok := out["recommended_workflows"].(map[string]any)
	if !ok || len(workflows) == 0 {
		t.Errorf("expected recommended_workflows object, got %v", workflows)
	}
	sessionStart, ok := workflows["session_start"].([]any)
	if !ok {
		t.Fatalf("expected session_start workflow array, got %T", workflows["session_start"])
	}
	wantStart := []string{
		"READ llm-wiki/index.md",
		"READ llm-wiki/project.md",
		"READ llm-wiki/agent-rules.md",
		"READ llm-wiki/agent-contract.md",
	}
	if len(sessionStart) < len(wantStart) {
		t.Fatalf("session_start workflow = %v, want at least %v", sessionStart, wantStart)
	}
	for i, want := range wantStart {
		if got, _ := sessionStart[i].(string); got != want {
			t.Errorf("session_start[%d] = %q, want %q", i, got, want)
		}
	}
	if !strings.Contains(toolsStr, "CLI gograph doctor --json") {
		t.Error("session_start workflow does not include the CLI installation preflight")
	}
	if strings.Contains(text, "llm-wiki/README.md") || strings.Contains(text, "llm-wiki/rules.md") {
		t.Errorf("capabilities retain paths for wiki pages that are not part of the current layout")
	}

	// 3. Output includes limitations
	limitations, ok := out["limitations"].([]any)
	if !ok || len(limitations) == 0 {
		t.Errorf("expected limitations array, got %v", limitations)
	}
	hasSSA := false
	for _, l := range limitations {
		lim := l.(string)
		if strings.Contains(lim, "SSA") {
			hasSSA = true
			break
		}
	}
	if !hasSSA {
		t.Errorf("expected SSA limitation text")
	}
	for _, want := range []string{
		"nearest real Git checkout",
		"unrelated regular-file or dangling links",
		"different effective GOWORK",
		"gograph doctor --json",
		"obsolete generator-owned package pages",
		"explore_response_modes",
		"compact=true",
		"deep=true",
		"same native gograph.explore.v1 value",
		"path_ranking",
		"exact before ambiguous/possible",
		"fewer cross-repository transitions",
		"gograph.graph-state.v1",
		"persisted|in_memory|unknown",
		"mismatched Go build context still fails closed",
		"production-only default",
		"next_cursor",
		"version_source",
		"sql_static_classification",
		"gograph.sql.v1",
		"PostgreSQL static SQL literals",
		"tests remain included unless no_tests=true",
		"CLI sql and MCP gograph_sql execute QuerySQL",
		"statically resolvable local or same-file package const/var declarations",
		"Receiver.Method and stable-ID selectors",
		"AST-discovered test-file fakes",
		"composite-literal construction",
		"not a route-row dump",
		"default .gograph/boundaries.json",
	} {
		if !strings.Contains(toolsStr, want) {
			t.Errorf("capabilities prerequisite omits %q", want)
		}
	}
}

func jsonArrayContains(raw any, want string) bool {
	items, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
