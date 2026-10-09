package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
	mcppkg "github.com/ozgurcd/gograph/internal/mcp"
	"github.com/ozgurcd/gograph/internal/session"
)

func answers1710Fixture(t *testing.T) (string, map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)) {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS("../../testdata/answers1710")); err != nil {
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
		func(context.Context, string) (*graph.Graph, error) { return g, nil }, "1.7.9")
	return root, handlers
}

func answers1710Git(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.test", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.test", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, output)
	}
}

func TestAnswers1710DeletedReview(t *testing.T) {
	root, handlers := answers1710Fixture(t)
	answers1710Git(t, root, "init", "-q")
	answers1710Git(t, root, "add", "go.mod", "a/current.go", "b/current.go", "auth/auth.go", "auth/auth_test.go")
	answers1710Git(t, root, "commit", "-qm", "fixture")
	if err := os.Remove(filepath.Join(root, "a/current.go")); err != nil {
		t.Fatal(err)
	}
	out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"review", "--uncommitted", "--json"}) })
	if code != 0 || !strings.Contains(out, "changes --git HEAD") || !strings.Contains(out, "a/current.go") {
		t.Errorf("CLI deletion: code=%d %s %s", code, out, stderr)
	}
	value := answers177MCP(t, handlers["gograph_review"], map[string]any{"uncommitted": true})
	if !strings.Contains(value, "changes --git HEAD") || !strings.Contains(value, "a/current.go") {
		t.Errorf("MCP deletion: %s", value)
	}
}

func TestAnswers1710IgnoredBuild(t *testing.T) {
	root := t.TempDir()
	answers1710Git(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("snapshot/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(root, "snapshot")
	if err := os.Mkdir(snapshot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(snapshot, os.DirFS("../../testdata/answers1710")); err != nil {
		t.Fatal(err)
	}
	out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", snapshot}) })
	if code == 0 || !strings.Contains(out+stderr, "ignored by") || !strings.Contains(out+stderr, "enclosing") {
		t.Errorf("ignored build: code=%d %s %s", code, out, stderr)
	}
}

func TestAnswers1710AuditInvocationErrors(t *testing.T) {
	root, handlers := answers1710Fixture(t)
	data, err := os.ReadFile(filepath.Join(root, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".gograph", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session_fixture.jsonl"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"session", "audit", "fixture", "--json"}) })
	if code != 0 {
		t.Fatalf("audit failed %s %s", out, stderr)
	}
	value := answers177MCP(t, handlers["gograph_session_audit"], map[string]any{"session_id": "fixture", "json": true})
	for _, result := range []string{out, value} {
		var report map[string]any
		if err := json.Unmarshal([]byte(result), &report); err != nil {
			t.Fatal(err)
		}
		if report["grade"] != "A (Highly Compliant)" || report["invocation_error_count"] != float64(2) || report["failure_count"] != float64(0) || report["composability_definition"] == nil {
			t.Errorf("invocation errors must not change grade: %s", result)
		}
	}
	data = []byte(strings.ReplaceAll(string(data), "\"invocation_error\"", "\"success\""))
	if err := os.WriteFile(filepath.Join(dir, "session_fixture.jsonl"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, _ = runCLIParityInDir(t, root, func() int { return Run([]string{"session", "audit", "fixture", "--json"}) })
	if !strings.Contains(out, "B (Good Compliance)") {
		t.Errorf("ordinary session grade changed: %s", out)
	}
}

func TestAnswers1710Selectors(t *testing.T) {
	root, handlers := answers1710Fixture(t)
	for _, selector := range []string{"a/current.go:Current", "example.test/answers1710/a.Current"} {
		out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"source", selector}) })
		if code != 0 || !strings.Contains(out, "return 1") || strings.Contains(out, "return 2") {
			t.Errorf("CLI selector %s: code=%d %s %s", selector, code, out, stderr)
		}
		value := answers177MCP(t, handlers["gograph_source"], map[string]any{"symbol": selector})
		if !strings.Contains(value, "return 1") || strings.Contains(value, "return 2") {
			t.Errorf("MCP selector %s: %s", selector, value)
		}
	}
	_, stderr, _ := runCLIParityInDir(t, root, func() int { return Run([]string{"source", "migrations.Current"}) })
	if !strings.Contains(stderr, "a/current.go:Current") || !strings.Contains(stderr, "b/current.go:Current") {
		t.Errorf("ambiguity omits executable selectors: %s", stderr)
	}
}

func TestAnswers1710ReviewTestsAndConstUses(t *testing.T) {
	root, handlers := answers1710Fixture(t)
	for _, command := range []string{"context", "review", "usages"} {
		selector, expected, arg := "auth.RepositoryVerifier.IntrospectToken", "auth/auth_test.go", "symbol"
		if command == "usages" {
			selector, expected, arg = "AuthenticatedClientKindAPIResource", "auth/auth.go", "type"
		}
		out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{command, selector, "--json"}) })
		if code != 0 || !strings.Contains(out, expected) {
			t.Errorf("CLI %s: code=%d %s %s", command, code, out, stderr)
		}
		value := answers177MCP(t, handlers["gograph_"+command], map[string]any{arg: selector})
		if !strings.Contains(value, expected) {
			t.Errorf("MCP %s: %s", command, value)
		}
	}
}

func TestAnswers1710InvocationTelemetry(t *testing.T) {
	t.Setenv("GOGRAPH_SESSION", "fixture")
	root, handlers := answers1710Fixture(t)
	dir := filepath.Join(root, ".gograph", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session_fixture.jsonl"), []byte("{\"type\":\"session_start\",\"session_id\":\"fixture\",\"created_at\":\"2026-01-01T00:00:00Z\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pointer, err := json.Marshal(session.ActiveSessionPointer{ActiveSessionID: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gograph", "active_session.json"), pointer, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"source", "auth.RepositoryVerifier.IntrospectToken"}, {"source", "auth/auth.go:", "--intention", "test malformed selector"}} {
		_, _, code := runCLIParityInDir(t, root, func() int { return Run(args) })
		if code == 0 {
			t.Fatal("invalid invocation passed")
		}
	}
	value := answers177MCP(t, handlers["gograph_source"], map[string]any{"symbol": "auth/auth.go:", "session_id": "fixture", "intention": "test malformed selector"})
	if !strings.Contains(value, "malformed selector") {
		t.Errorf("MCP malformed: %s", value)
	}
	data, err := os.ReadFile(filepath.Join(dir, "session_fixture.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), `"status":"invocation_error"`); got != 3 {
		t.Errorf("want 3 invocation errors, got %d: %s", got, data)
	}
}

func TestAnswers1710QualifiedSymbolTools(t *testing.T) {
	root, handlers := answers1710Fixture(t)
	for _, command := range []string{"context", "review", "plan", "tests", "usages"} {
		selector, arg, expected := "auth/auth.go:RepositoryVerifier.IntrospectToken", "symbol", "auth/auth_test.go"
		if command == "usages" {
			selector, arg, expected = "auth/auth.go:AuthenticatedClientKindAPIResource", "type", "auth/auth.go"
		}
		for _, selected := range []string{selector, "example.test/answers1710/" + selector} {
			out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{command, selected, "--json"}) })
			if code != 0 || !strings.Contains(out, expected) {
				t.Errorf("CLI %s %s: %d %s %s", command, selected, code, out, stderr)
			}
			value := answers177MCP(t, handlers["gograph_"+command], map[string]any{arg: selected})
			if !strings.Contains(value, expected) {
				t.Errorf("MCP %s %s: %s", command, selected, value)
			}
		}
	}
}
