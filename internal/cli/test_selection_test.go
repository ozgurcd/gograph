package cli

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	protocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestTestsUncommittedPrintsRunnableSelectionWithMCPParity(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/sel\n\ngo 1.27.0\n")
	writeTestFile(t, filepath.Join(root, "app.go"), "package app\n\nfunc Call() int { return 1 }\n\nfunc Other() int { return 2 }\n")
	writeTestFile(t, filepath.Join(root, "app_test.go"), "package app\n\nimport \"testing\"\n\n"+
		"func TestCall(t *testing.T) { _ = Call() }\n\nfunc TestCallMore(t *testing.T) { _ = Other() }\n\nfunc TestOther(t *testing.T) { _ = Other() }\n")
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=/dev/null", "-c", "user.name=Gograph Test", "-c", "user.email=gograph-test@example.invalid"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v\n%s", err, out)
		}
	}
	runGit("init", "-q")
	runGit("add", "go.mod", "app.go", "app_test.go")
	runGit("commit", "-qm", "baseline")
	if stdout, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) }); code != 0 {
		t.Fatalf("build: %s\n%s", stdout, stderr)
	}
	writeTestFile(t, filepath.Join(root, "app.go"), "package app\n\nfunc Call() int { return 3 }\n\nfunc Other() int { return 2 }\n")

	stdout, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"tests", "--uncommitted", "--format", "go-test"}) })
	if code != 0 {
		t.Fatalf("tests --uncommitted: %s\n%s", stdout, stderr)
	}
	if want := "go test example.com/sel -run '^(TestCall)$'\n"; stdout != want {
		t.Fatalf("go-test output = %q, want %q", stdout, want)
	}

	stdout, stderr, code = runCLIParityInDir(t, root, func() int { return Run([]string{"tests", "Other", "--format", "run"}) })
	if code != 0 || !strings.Contains(stdout, "-run '^(TestCallMore|TestOther)$'") {
		t.Fatalf("tests Other --format run: %s\n%s", stdout, stderr)
	}

	stdout, stderr, code = runCLIParityInDir(t, root, func() int { return Run([]string{"tests", "--uncommitted", "--json"}) })
	if code != 0 {
		t.Fatalf("tests --uncommitted --json: %s\n%s", stdout, stderr)
	}
	var envelope struct{ Results search.TestSelection }
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Results.SchemaVersion != search.TestSelectionSchemaVersion || envelope.Results.TestCount != 1 {
		t.Fatalf("CLI selection = %+v", envelope.Results)
	}

	g, err := loadGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	handlers := exposeMCPRefreshHandlers(t, g, func() (*graph.Graph, error) { return g, nil })
	request := protocol.CallToolRequest{}
	request.Params.Arguments = map[string]any{"uncommitted": true}
	result, err := handlers["gograph_tests"](context.Background(), request)
	if err != nil || result.IsError {
		t.Fatalf("MCP tests uncommitted: %+v, %v", result, err)
	}
	var native search.TestSelection
	if err := json.Unmarshal([]byte(mcpResultText(t, result)), &native); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(native, envelope.Results) {
		t.Fatalf("tests selection parity:\nCLI=%+v\nMCP=%+v", envelope.Results, native)
	}
}

func TestTestsSelectionFlagValidation(t *testing.T) {
	for _, args := range [][]string{
		{"tests", "--uncommitted", "--since", "HEAD"},
		{"tests", "--uncommitted", "Call"},
		{"tests", "--since"},
		{"tests", "--format", "xml", "Call"},
		{"tests", "--format", "go-test"},
		{"tests", "--exact-only"},
	} {
		if _, _, code := runCLIParityInDir(t, t.TempDir(), func() int { return Run(args) }); code == 0 {
			t.Errorf("%v succeeded, want usage error", args)
		}
	}
}
