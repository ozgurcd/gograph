package cli

import (
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	mcpprotocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
)

func TestImpactExactOnlyHasCLIMCPAndMermaidParity(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example.com/p::Target", Name: "Target", Kind: graph.KindFunction, File: "main.go"},
		{ID: "example.com/p::ExactCaller", Name: "ExactCaller", Kind: graph.KindFunction, File: "main.go"},
		{ID: "example.com/p::PossibleCaller", Name: "PossibleCaller", Kind: graph.KindFunction, File: "main.go"},
	}, Calls: []graph.CallEdge{
		{CallerSymbolID: "example.com/p::ExactCaller", CalleeSymbolID: "example.com/p::Target", Resolution: graph.CallResolutionStatic},
		{CallerSymbolID: "example.com/p::PossibleCaller", CalleeSymbolID: "example.com/p::Target", Resolution: graph.CallResolutionCHA},
	}}
	root := writeCLIParityGraph(t, g)
	handlers := exposeMCPRefreshHandlers(t, g, func() (*graph.Graph, error) { return g, nil })
	for _, exactOnly := range []bool{false, true} {
		for _, mermaid := range []bool{false, true} {
			args := []string{"impact", "Target"}
			if exactOnly {
				args = append(args, "--exact-only")
			}
			if mermaid {
				args = append(args, "--mermaid")
			} else {
				args = append(args, "--json")
			}
			stdout, stderr, code := runCLIParityInDir(t, root, func() int { return Run(args) })
			if code != 0 {
				t.Fatalf("CLI error: %s %s", stderr, stdout)
			}
			request := mcpprotocol.CallToolRequest{}
			request.Params.Arguments = map[string]any{"symbol": "Target", "exact_only": exactOnly, "mermaid": mermaid}
			result, err := handlers["gograph_impact"](context.Background(), request)
			if err != nil || result.IsError {
				t.Fatalf("MCP error: %v %+v", err, result)
			}
			text := mcpResultText(t, result)
			if mermaid {
				if strings.TrimSpace(stdout) != strings.TrimSpace(text) {
					t.Fatalf("diagram parity failed: %s versus %s", stdout, text)
				}
				if strings.Contains(stdout, "PossibleCaller") == exactOnly {
					t.Fatalf("exact-only diagram selection is wrong: %s", stdout)
				}
				continue
			}
			cliRows, mcpRows := decodeCLIResultRows(t, stdout), decodeCLIResultRows(t, text)
			if !reflect.DeepEqual(cliRows, mcpRows) {
				t.Fatalf("result parity failed: %+v versus %+v", cliRows, mcpRows)
			}
			want := 2
			if exactOnly {
				want = 1
			}
			if len(cliRows) != want {
				t.Fatalf("returned %d rows; want %d", len(cliRows), want)
			}
			for _, row := range cliRows {
				if row.ResolutionStatus == "" || exactOnly && row.ResolutionStatus != "exact" {
					t.Fatalf("missing or wrong certainty: %+v", row)
				}
			}
		}
	}
}

func TestDeletedChangesCannotBecomeEmptyCLIMCPImpact(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/deleted\n\ngo 1.27.0\n")
	writeTestFile(t, filepath.Join(root, "main.go"), "package deleted\nfunc Gone() {}\nfunc Keep() {}\n")
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"commit", "-qm", "before"}} {
		command := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=/dev/null", "-c", "user.name=Gograph Test", "-c", "user.email=test@example.invalid"}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	writeTestFile(t, filepath.Join(root, "main.go"), "package deleted\nfunc Keep() {}\n")
	stdout, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) })
	if code != 0 {
		t.Fatalf("build: %s %s", stdout, stderr)
	}
	g, err := loadGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	handlers := exposeMCPRefreshHandlers(t, g, func() (*graph.Graph, error) { return g, nil })
	for _, command := range []string{"impact", "context", "plan", "review", "risk"} {
		for _, since := range []bool{false, true} {
			if since && command != "impact" {
				continue
			}
			args := []string{command, "--uncommitted", "--json"}
			arguments := map[string]any{"uncommitted": true}
			if since {
				args = []string{command, "--since", "HEAD", "--json"}
				arguments = map[string]any{"since": "HEAD"}
			}
			stdout, stderr, code = runCLIParityInDir(t, root, func() int { return Run(args) })
			if code == 0 || !strings.Contains(stdout+stderr, "historical caller evidence") {
				t.Fatalf("CLI %v silently accepted deletion: %d %s %s", args, code, stdout, stderr)
			}
			request := mcpprotocol.CallToolRequest{}
			request.Params.Arguments = arguments
			result, err := handlers["gograph_"+command](context.Background(), request)
			if err != nil || result == nil || !result.IsError || !strings.Contains(mcpResultText(t, result), "historical caller evidence") {
				t.Fatalf("MCP %s silently accepted deletion: %+v, %v", command, result, err)
			}
		}
	}
}

func TestChangeConsumerHelpDisclosesHistoricalCallerLimitation(t *testing.T) {
	for _, command := range []string{"impact", "context", "plan", "review", "risk", "check", "changes"} {
		stdout, stderr, code := captureCLIParityOutput(t, func() int { return Run([]string{command, "--help"}) })
		if code != 0 || !strings.Contains(stdout, "historical caller evidence") || !strings.Contains(stdout, "untracked") {
			t.Fatalf("%s help omitted selection contract: %d %s %s", command, code, stdout, stderr)
		}
	}
}
