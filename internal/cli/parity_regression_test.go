package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func writeCLIParityGraph(t *testing.T, g *graph.Graph) string {
	t.Helper()

	root := t.TempDir()
	g.Version = graph.Version
	g.Root = root
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gograph", "graph.json"), data, 0o640); err != nil {
		t.Fatal(err)
	}
	return root
}

func captureCLIParityOutput(t *testing.T, run func() int) (stdout, stderr string, code int) {
	t.Helper()

	oldStdout, oldStderr := os.Stdout, os.Stderr
	outReader, outWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errReader, errWriter, err := os.Pipe()
	if err != nil {
		_ = outReader.Close()
		_ = outWriter.Close()
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = outWriter, errWriter

	func() {
		defer func() {
			os.Stdout, os.Stderr = oldStdout, oldStderr
		}()
		code = run()
		_ = outWriter.Close()
		_ = errWriter.Close()
	}()

	out, readErr := io.ReadAll(outReader)
	if readErr != nil {
		t.Fatal(readErr)
	}
	errOut, readErr := io.ReadAll(errReader)
	if readErr != nil {
		t.Fatal(readErr)
	}
	_ = outReader.Close()
	_ = errReader.Close()
	return string(out), string(errOut), code
}

func runCLIParityInDir(t *testing.T, root string, run func() int) (stdout, stderr string, code int) {
	t.Helper()

	oldWorkingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(oldWorkingDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	}()
	return captureCLIParityOutput(t, run)
}

func TestCLIEndpointDepthIsClampedFromOneToTwenty(t *testing.T) {
	g := &graph.Graph{
		Packages: []graph.PackageNode{{ID: "sample", Name: "sample", ImportPathBestEffort: "example.com/parity", Dir: ".", Files: []string{"main.go"}}},
		Files:    []graph.FileNode{{ID: "main.go", Path: "main.go", PackageName: "sample"}},
		Routes:   []graph.HTTPRoute{{Method: "GET", Path: "/chain", Handler: "F0", File: "main.go", Line: 1}},
	}
	for i := 0; i <= 22; i++ {
		name := fmt.Sprintf("F%d", i)
		g.Symbols = append(g.Symbols, graph.SymbolNode{
			ID:          "example.com/parity::" + name,
			Name:        name,
			Kind:        graph.KindFunction,
			PackageName: "sample",
			File:        "main.go",
			Line:        i + 1,
			EndLine:     i + 1,
		})
		if i > 0 {
			caller := fmt.Sprintf("F%d", i-1)
			g.Calls = append(g.Calls, graph.CallEdge{
				CallerSymbolID: "example.com/parity::" + caller,
				CallerName:     caller,
				CalleeSymbolID: "example.com/parity::" + name,
				CalleeRaw:      name,
				File:           "main.go",
				Line:           i,
			})
		}
	}
	root := writeCLIParityGraph(t, g)

	maxDepth := func(flagValue string) int {
		stdout, stderr, code := runCLIParityInDir(t, root, func() int {
			return Run([]string{"endpoint", "F0", "--depth", flagValue, "--json"})
		})
		if code != 0 {
			t.Fatalf("endpoint --depth %s failed with code %d: %s", flagValue, code, stderr)
		}
		var envelope struct {
			Results []struct {
				CallChain []struct {
					Depth int `json:"depth"`
				} `json:"call_chain"`
			} `json:"results"`
		}
		if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
			t.Fatalf("endpoint --depth %s did not return JSON: %v\nstdout:\n%s\nstderr:\n%s", flagValue, err, stdout, stderr)
		}
		if len(envelope.Results) != 1 {
			t.Fatalf("endpoint --depth %s returned %d slices, want 1: %s", flagValue, len(envelope.Results), stdout)
		}
		max := 0
		for _, step := range envelope.Results[0].CallChain {
			if step.Depth > max {
				max = step.Depth
			}
		}
		return max
	}

	if got := maxDepth("0"); got != 1 {
		t.Fatalf("endpoint --depth 0 reached depth %d, want lower clamp 1", got)
	}
	if got := maxDepth("99"); got != 20 {
		t.Fatalf("endpoint --depth 99 reached depth %d, want upper clamp 20", got)
	}
}

func TestCLIBoundariesJSONStillExitsNonzeroOnViolations(t *testing.T) {
	g := &graph.Graph{
		Packages: []graph.PackageNode{
			{ID: "api", Name: "api", ImportPathBestEffort: "example.com/parity/internal/api", Dir: "internal/api", Files: []string{"internal/api/handler.go"}},
			{ID: "db", Name: "db", ImportPathBestEffort: "example.com/parity/internal/db", Dir: "internal/db", Files: []string{"internal/db/db.go"}},
		},
		Imports: []graph.ImportEdge{{FromFile: "internal/api/handler.go", FromPackage: "api", ImportPath: "example.com/parity/internal/db"}},
	}
	root := writeCLIParityGraph(t, g)
	config := `{"layers":[{"name":"api","packages":["internal/api/**"],"may_import":[]}]}`
	if err := os.WriteFile(filepath.Join(root, ".gograph", "boundaries.json"), []byte(config), 0o640); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLIParityInDir(t, root, func() int {
		return Run([]string{"boundaries", "--json"})
	})
	if code == 0 {
		t.Fatalf("boundaries --json exited zero despite a violation:\n%s", stdout)
	}
	var envelope struct {
		Command string `json:"command"`
		Count   int    `json:"count"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("boundaries --json returned invalid JSON: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if envelope.Command != "boundaries" || envelope.Count != 1 {
		t.Fatalf("unexpected boundaries envelope: %s", stdout)
	}
}

func TestCLIPlanJSONIncludesRequestedInspectContexts(t *testing.T) {
	g := &graph.Graph{
		Packages: []graph.PackageNode{{ID: "sample", Name: "sample", ImportPathBestEffort: "example.com/parity", Dir: ".", Files: []string{"target.go"}}},
		Files:    []graph.FileNode{{ID: "target.go", Path: "target.go", PackageName: "sample"}},
		Symbols: []graph.SymbolNode{{
			ID: "example.com/parity::Target", Name: "Target", Kind: graph.KindFunction,
			PackageName: "sample", File: "target.go", Line: 3, EndLine: 3,
		}},
	}
	root := writeCLIParityGraph(t, g)
	if err := os.WriteFile(filepath.Join(root, "target.go"), []byte("package sample\n\nfunc Target() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLIParityInDir(t, root, func() int {
		return Run([]string{"plan", "Target", "--with-context", "--json"})
	})
	if code != 0 {
		t.Fatalf("plan --with-context --json failed with code %d: %s", code, stderr)
	}
	var envelope struct {
		Results struct {
			InspectContexts []struct {
				Symbol string            `json:"symbol"`
				Source string            `json:"source"`
				Nodes  []json.RawMessage `json:"nodes"`
			} `json:"inspect_contexts"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("plan returned invalid JSON: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if len(envelope.Results.InspectContexts) == 0 {
		t.Fatalf("plan --with-context --json omitted inspect_contexts: %s", stdout)
	}
	context := envelope.Results.InspectContexts[0]
	if context.Symbol != "Target" || len(context.Nodes) == 0 || !strings.Contains(context.Source, "func Target()") {
		t.Fatalf("plan returned an incomplete inspect context: %s", stdout)
	}
}

func TestCLIPlanWithContextDisambiguatesDuplicateSymbolNames(t *testing.T) {
	g := &graph.Graph{
		Packages: []graph.PackageNode{
			{ID: "alpha", Name: "alpha", ImportPathBestEffort: "example.com/parity/alpha", Dir: "alpha", Files: []string{"alpha/process.go"}},
			{ID: "beta", Name: "beta", ImportPathBestEffort: "example.com/parity/beta", Dir: "beta", Files: []string{"beta/process.go"}},
		},
		Files: []graph.FileNode{
			{ID: "alpha/process.go", Path: "alpha/process.go", PackageName: "alpha"},
			{ID: "beta/process.go", Path: "beta/process.go", PackageName: "beta"},
		},
		Symbols: []graph.SymbolNode{
			{ID: "example.com/parity/alpha::Process", Name: "Process", Kind: graph.KindFunction, PackageName: "alpha", File: "alpha/process.go", Line: 3, EndLine: 3},
			{ID: "example.com/parity/beta::Process", Name: "Process", Kind: graph.KindFunction, PackageName: "beta", File: "beta/process.go", Line: 3, EndLine: 3},
		},
	}
	root := writeCLIParityGraph(t, g)
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

	stdout, stderr, code := runCLIParityInDir(t, root, func() int {
		return Run([]string{"plan", "Process", "--with-context", "--json"})
	})
	if code != 0 {
		t.Fatalf("plan --with-context --json failed with code %d: %s", code, stderr)
	}
	var envelope struct {
		Results struct {
			InspectContexts []struct {
				Source string          `json:"source"`
				Nodes  []search.Result `json:"nodes"`
			} `json:"inspect_contexts"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("plan returned invalid JSON: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if len(envelope.Results.InspectContexts) != 2 {
		t.Fatalf("inspect contexts = %d, want one per duplicate-name symbol: %s", len(envelope.Results.InspectContexts), stdout)
	}

	seen := make(map[string]string)
	for _, context := range envelope.Results.InspectContexts {
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

func TestClaudeInstallerUsesSelfBootstrappingMCPArguments(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", home)

	configPath := getClaudeConfigPath()
	if configPath == "" {
		t.Skip("Claude Desktop config path is unsupported on this OS")
	}
	stdout, stderr, code := runCLIParityInDir(t, project, func() int {
		if err := installMCPServer(); err != nil {
			t.Errorf("installMCPServer: %v", err)
			return 1
		}
		return 0
	})
	if code != 0 {
		t.Fatalf("Claude MCP installation failed: %s", stderr)
	}
	if strings.Contains(stdout, "No graph found") || strings.Contains(stdout, "gograph build") {
		t.Fatalf("installer retained an obsolete pre-build warning despite MCP auto-bootstrap: %s", stdout)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCPServers map[string]struct {
			Args []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid Claude config: %v\n%s", err, data)
	}
	canonicalProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"mcp", canonicalProject}
	gotArgs := config.MCPServers["gograph"].Args
	if len(gotArgs) != len(wantArgs) || gotArgs[0] != wantArgs[0] || gotArgs[1] != wantArgs[1] {
		t.Fatalf("Claude gograph args = %q, want exactly %q", gotArgs, wantArgs)
	}
}

func TestGateTemplateDocumentsAutomaticPersistedBaseline(t *testing.T) {
	if strings.Contains(gateConfigTemplate, "snapshot save") {
		t.Fatalf("gate template still claims named snapshots establish its baseline:\n%s", gateConfigTemplate)
	}
	for _, phrase := range []string{
		"immediately preceding persisted graph",
		"first publication",
		"delta gates are skipped",
	} {
		if !strings.Contains(gateConfigTemplate, phrase) {
			t.Fatalf("gate template omitted %q:\n%s", phrase, gateConfigTemplate)
		}
	}
}

func TestCLIPathAndSkeletonHonorJSONOutput(t *testing.T) {
	g := &graph.Graph{
		Packages: []graph.PackageNode{{ID: "sample", Name: "sample", ImportPathBestEffort: "example.com/parity", Dir: ".", Files: []string{"main.go"}}},
		Files:    []graph.FileNode{{ID: "main.go", Path: "main.go", PackageName: "sample"}},
		Symbols: []graph.SymbolNode{
			{ID: "example.com/parity::Start", Name: "Start", Kind: graph.KindFunction, PackageName: "sample", File: "main.go", Line: 1, EndLine: 1, Signature: "func Start()"},
			{ID: "example.com/parity::End", Name: "End", Kind: graph.KindFunction, PackageName: "sample", File: "main.go", Line: 2, EndLine: 2, Signature: "func End()"},
		},
		Calls: []graph.CallEdge{{
			CallerSymbolID: "example.com/parity::Start", CallerName: "Start",
			CalleeSymbolID: "example.com/parity::End", CalleeRaw: "End", File: "main.go", Line: 1,
		}},
	}
	root := writeCLIParityGraph(t, g)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "path", args: []string{"path", "Start", "End", "--json"}},
		{name: "skeleton", args: []string{"skeleton", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runCLIParityInDir(t, root, func() int { return Run(tc.args) })
			if code != 0 {
				t.Fatalf("%s failed with code %d: %s", tc.name, code, stderr)
			}
			var envelope struct {
				Command string          `json:"command"`
				Status  string          `json:"status"`
				Results json.RawMessage `json:"results"`
			}
			if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
				t.Fatalf("%s did not return JSON: %v\n%s", tc.name, err, stdout)
			}
			if envelope.Command != tc.name || envelope.Status != "ok" || len(envelope.Results) == 0 {
				t.Fatalf("unexpected %s JSON envelope: %s", tc.name, stdout)
			}
		})
	}
}

func TestCLICallersExactMermaidAvoidsSubstringCollisions(t *testing.T) {
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: "example.com/parity::Load", Name: "Load", Kind: graph.KindFunction},
			{ID: "example.com/parity::Preload", Name: "Preload", Kind: graph.KindFunction},
			{ID: "example.com/parity::ExactCaller", Name: "ExactCaller", Kind: graph.KindFunction},
			{ID: "example.com/parity::CollisionCaller", Name: "CollisionCaller", Kind: graph.KindFunction},
		},
		Calls: []graph.CallEdge{
			{CallerSymbolID: "example.com/parity::ExactCaller", CallerName: "ExactCaller", CalleeSymbolID: "example.com/parity::Load", CalleeRaw: "Load"},
			{CallerSymbolID: "example.com/parity::CollisionCaller", CallerName: "CollisionCaller", CalleeSymbolID: "example.com/parity::Preload", CalleeRaw: "Preload"},
		},
	}
	root := writeCLIParityGraph(t, g)

	stdout, stderr, code := runCLIParityInDir(t, root, func() int {
		return Run([]string{"callers", "Load", "--exact", "--mermaid"})
	})
	if code != 0 {
		t.Fatalf("callers --exact --mermaid failed with code %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "ExactCaller") || strings.Contains(stdout, "CollisionCaller") || strings.Contains(stdout, "Preload") {
		t.Fatalf("CLI exact Mermaid response widened to a substring collision:\n%s", stdout)
	}
}
