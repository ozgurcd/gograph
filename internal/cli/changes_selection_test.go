package cli

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	protocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestChangesCLIAndMCPShareRecordedTaggedSelection(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/changes\n\ngo 1.27.0\n")
	writeTestFile(t, filepath.Join(root, "main.go"), "package app\nfunc Call() {}\n")
	before := "//go:build integration\n\npackage app\nfunc TestIntegration() { Call() }\n"
	writeTestFile(t, filepath.Join(root, "main_test.go"), before)
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=/dev/null", "-c", "user.name=Gograph Test", "-c", "user.email=gograph-test@example.invalid"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v\n%s", err, out)
		}
	}
	runGit("init", "-q")
	runGit("add", "go.mod", "main.go", "main_test.go")
	runGit("commit", "-qm", "baseline")
	stdout, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", ".", "--tags=integration"}) })
	if code != 0 {
		t.Fatalf("tagged build: %s\n%s", stdout, stderr)
	}
	g, err := loadGraph(root)
	if err != nil || g.Build.Selection == nil || !reflect.DeepEqual(g.Build.Selection.BuildTags, []string{"integration"}) {
		t.Fatalf("build omitted effective selection: %+v, %v", g, err)
	}
	writeTestFile(t, filepath.Join(root, "main_test.go"), before+"func TestAdded() { Call() }\n")
	t.Setenv("GOFLAGS", "-tags=unrelated")
	handlers := exposeMCPRefreshHandlers(t, g, func() (*graph.Graph, error) { return g, nil })
	for _, gitRef := range []string{"", "HEAD"} {
		args := []string{"changes", "--json"}
		if gitRef != "" {
			args = append(args, "--git", gitRef)
		}
		stdout, stderr, code := runCLIParityInDir(t, root, func() int { return Run(args) })
		if code != 0 {
			t.Fatalf("CLI changes %v: %s\n%s", args, stdout, stderr)
		}
		var envelope struct{ Results search.ChangesResult }
		if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Results.Evaluation != "complete" || len(envelope.Results.Symbols) != 1 || envelope.Results.Symbols[0].Name != "TestAdded" || envelope.Results.Symbols[0].Status != search.ChangeNew {
			t.Fatalf("tagged CLI changes %v: %s", args, stdout)
		}
		request := protocol.CallToolRequest{}
		request.Params.Arguments = map[string]any{"git_ref": gitRef}
		result, err := handlers["gograph_changes"](context.Background(), request)
		if err != nil || result.IsError {
			t.Fatalf("MCP changes: %+v, %v", result, err)
		}
		for name, value := range map[string]any{"text": json.RawMessage(mcpResultText(t, result)), "structured": result.StructuredContent} {
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var native search.ChangesResult
			if err := json.Unmarshal(data, &native); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(native, envelope.Results) {
				t.Fatalf("%s changes parity:\nCLI=%+v\nMCP=%+v", name, envelope.Results, native)
			}
		}
	}
}
