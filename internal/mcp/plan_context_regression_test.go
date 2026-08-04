package mcp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestMCPPlanWithContextDisambiguatesDuplicateSymbolNames(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"alpha/process.go": "package alpha\n\nfunc Process() { AlphaOnly() }\n",
		"beta/process.go":  "package beta\n\nfunc Process() { BetaOnly() }\n",
	}
	for name, contents := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	g := &graph.Graph{
		Version: graph.Version,
		Root:    root,
		Symbols: []graph.SymbolNode{
			{ID: "example.com/parity/alpha::Process", Name: "Process", Kind: graph.KindFunction, PackageName: "alpha", File: "alpha/process.go", Line: 3, EndLine: 3},
			{ID: "example.com/parity/beta::Process", Name: "Process", Kind: graph.KindFunction, PackageName: "beta", File: "beta/process.go", Line: 3, EndLine: 3},
		},
	}
	text := callTool(t, setupHandlers(t, g)["gograph_plan"], map[string]any{
		"symbol":       "Process",
		"with_context": true,
	})

	var response struct {
		InspectContexts []struct {
			Source string `json:"source"`
			Nodes  []struct {
				File string `json:"file"`
			} `json:"nodes"`
		} `json:"inspect_contexts"`
	}
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatalf("plan returned invalid JSON: %v\nresponse:\n%s", err, text)
	}
	if len(response.InspectContexts) != 2 {
		t.Fatalf("inspect contexts = %d, want one per duplicate-name symbol: %s", len(response.InspectContexts), text)
	}

	seen := make(map[string]string)
	for _, context := range response.InspectContexts {
		if len(context.Nodes) != 1 {
			t.Fatalf("inspect context remained ambiguous: %+v", context.Nodes)
		}
		seen[context.Nodes[0].File] = context.Source
	}
	if !strings.Contains(seen["alpha/process.go"], "AlphaOnly") || strings.Contains(seen["alpha/process.go"], "BetaOnly") {
		t.Fatalf("alpha context selected the wrong source: %q", seen["alpha/process.go"])
	}
	if !strings.Contains(seen["beta/process.go"], "BetaOnly") || strings.Contains(seen["beta/process.go"], "AlphaOnly") {
		t.Fatalf("beta context selected the wrong source: %q", seen["beta/process.go"])
	}
}
