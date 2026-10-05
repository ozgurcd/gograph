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
)

func answers177Fixture(t *testing.T) (string, map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)) {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS("../../testdata/answers177")); err != nil {
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
		func(context.Context, string) (*graph.Graph, error) { return g, nil }, "1.7.6")
	return root, handlers
}

func TestAnswers177VersionDrift(t *testing.T) {
	root, handlers := answers177Fixture(t)
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "gograph")
	cmd := exec.Command("go", "build", "-ldflags=-X main.version=1.7.7", "-o", bin, "./cmd/gograph")
	cmd.Dir = "../../testdata/versionprobe"
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "GOWORK=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build version fixture: %v: %s", err, output)
	}
	t.Setenv("PATH", binDir)
	oldVersion := Version
	Version = "1.7.6"
	t.Cleanup(func() { Version = oldVersion })
	stdout, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"version"}) })
	if code != 0 || !strings.Contains(stdout, "1.7.7") || !strings.Contains(strings.ToLower(stdout), "restart") {
		t.Errorf("CLI code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	text := answers177MCP(t, handlers["gograph_capabilities"], nil)
	var doc struct {
		Installation struct {
			InstalledVersion string `json:"installed_version"`
			RestartRequired  bool   `json:"restart_required"`
		} `json:"installation"`
	}
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Installation.InstalledVersion != "1.7.7" || !doc.Installation.RestartRequired {
		t.Errorf("MCP installation = %+v", doc.Installation)
	}
	Version = "0.0.0-test.fixed"
	stdout, stderr, code = runCLIParityInDir(t, root, func() int { return Run([]string{"version"}) })
	if code != 0 || stdout != "gograph version v0.0.0-test.fixed\n" || stderr != "" {
		t.Errorf("development version code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "version-probe-was-executed")); !os.IsNotExist(err) {
		t.Errorf("version probe executed or stat failed: %v", err)
	}
}

func answers177MCP(t *testing.T, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) string {
	t.Helper()
	request := mcp.CallToolRequest{}
	request.Params.Arguments = args
	result, err := handler(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, content := range result.Content {
		if value, ok := content.(mcp.TextContent); ok {
			text.WriteString(value.Text)
		}
	}
	return text.String()
}

func TestAnswers177PackageQualifiedFieldTests(t *testing.T) {
	root, handlers := answers177Fixture(t)
	for _, tc := range []struct{ symbol, want, unwanted string }{
		{"service.OIDCLoginService.InitiateLogin", "TestFieldLogin", "TestFieldCallback"},
		{"service.OIDCCallbackService.HandleCallback", "TestFieldCallback", "TestFieldLogin"},
	} {
		t.Run(tc.symbol, func(t *testing.T) {
			stdout, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"tests", tc.symbol}) })
			if code != 0 || !strings.Contains(stdout, tc.want) || strings.Contains(stdout, tc.unwanted) {
				t.Errorf("CLI code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			text := answers177MCP(t, handlers["gograph_tests"], map[string]any{"symbol": tc.symbol})
			if !strings.Contains(text, tc.want) || strings.Contains(text, tc.unwanted) {
				t.Errorf("MCP = %q", text)
			}
		})
	}
}

func TestAnswers177ExactSourceWins(t *testing.T) {
	root, handlers := answers177Fixture(t)
	stdout, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"source", "domain.ValidatePassword"}) })
	if code != 0 || !strings.Contains(stdout, "func ValidatePassword()") || strings.Contains(stdout, "ValidatePasswordPolicy") {
		t.Errorf("CLI code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	text := answers177MCP(t, handlers["gograph_source"], map[string]any{"symbol": "domain.ValidatePassword"})
	if !strings.Contains(text, "func ValidatePassword()") || strings.Contains(text, "ValidatePasswordPolicy") {
		t.Errorf("MCP = %q", text)
	}
}

func TestAnswers177GetterParameters(t *testing.T) {
	root, handlers := answers177Fixture(t)
	for _, key := range []string{"IDENTUUM_IDP_ALLOW_MULTI_REPLICA", "IDENTUUM_IDP_TEST_ALLOW_PRIVATE_UPSTREAM_ISSUER"} {
		stdout, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"envs", key}) })
		if code != 0 || !strings.Contains(stdout, key) || !strings.Contains(stdout, "parameter") {
			t.Errorf("CLI key=%s code=%d stdout=%q stderr=%q", key, code, stdout, stderr)
		}
		text := answers177MCP(t, handlers["gograph_envs"], map[string]any{"term": key})
		if !strings.Contains(text, key) || !strings.Contains(text, "parameter") {
			t.Errorf("MCP key=%s answer=%q", key, text)
		}
	}
	text := answers177MCP(t, handlers["gograph_envs"], map[string]any{"term": "NOT_AN_ENV"})
	if strings.Contains(text, "NOT_AN_ENV") {
		t.Errorf("unbound getter was claimed as environment read: %q", text)
	}
}

func TestAnswers177VariableUses(t *testing.T) {
	root, handlers := answers177Fixture(t)
	stdout, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"usages", "ErrLoginRateLimited"}) })
	if code != 0 || !strings.Contains(stdout, "IsLimited") || strings.Contains(stdout, "Shadow") {
		t.Errorf("CLI code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	text := answers177MCP(t, handlers["gograph_usages"], map[string]any{"type": "ErrLoginRateLimited"})
	if !strings.Contains(text, "IsLimited") || strings.Contains(text, "Shadow") {
		t.Errorf("MCP = %q", text)
	}
}

func TestAnswers177FactoryHandlerLimit(t *testing.T) {
	root, handlers := answers177Fixture(t)
	stdout, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"routes", "/health"}) })
	if code != 0 || !strings.Contains(stdout, "factory call") || !strings.Contains(stdout, "returned handler") {
		t.Errorf("CLI code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	text := answers177MCP(t, handlers["gograph_routes"], map[string]any{"term": "/health"})
	if !strings.Contains(text, "factory call") || !strings.Contains(text, "returned handler") {
		t.Errorf("MCP = %q", text)
	}
	capabilities := answers177MCP(t, handlers["gograph_capabilities"], nil)
	if !strings.Contains(capabilities, "returned handler is not resolved") {
		t.Errorf("MCP capabilities omit the factory-return limitation")
	}
}

func TestAnswers177SessionVerbHelp(t *testing.T) {
	for _, verb := range []string{"create", "end", "audit", "cleanup"} {
		stdout, stderr, code := runCLIParityInDir(t, t.TempDir(), func() int { return Run([]string{"session", verb, "--help"}) })
		if code != 0 || !strings.Contains(stdout, "gograph session "+verb) || !strings.Contains(stdout, "OPTIONS") || !strings.Contains(stdout, "DESCRIPTION") {
			t.Errorf("verb=%s code=%d stdout=%q stderr=%q", verb, code, stdout, stderr)
		}
	}
}
