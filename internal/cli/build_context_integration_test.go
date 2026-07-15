package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	mcpprotocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestIssue30IgnoredToolDoesNotPollutePreciseGraphOrMCP(t *testing.T) {
	setDeterministicBuildEnvironment(t, "")
	root := t.TempDir()
	writeBuildContextFixture(t, root, "go.mod", "module example.com/issue30\n\ngo 1.26\n")
	writeBuildContextFixture(t, root, "library.go", "package issue30\n\nfunc Active() {}\n")
	toolPath := writeBuildContextFixture(t, root, "tool.go", `//go:build ignore

package main

func main() { panic("Issue30IgnoredToolSentinel") }
`)

	g, err := buildPreciseGraph(root)
	if err != nil {
		skipCoverageCacheFallback(t, g)
		t.Fatalf("buildPreciseGraph: %v", err)
	}
	if got := g.Build.EffectivePrecision(); got != graph.PrecisionPrecise {
		t.Fatalf("precision = %s, want %s", got, graph.PrecisionPrecise)
	}
	if g.Build.ScannedFiles != 1 || g.Build.ParsedFiles != 1 || len(g.Files) != 1 || g.Files[0].Path != "library.go" {
		t.Fatalf("ignored tool affected file inventory: build=%+v files=%+v", g.Build, g.Files)
	}

	encoded, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	graphOutput := string(encoded)
	for _, inactiveValue := range []string{"tool.go", "Issue30IgnoredToolSentinel"} {
		if strings.Contains(graphOutput, inactiveValue) {
			t.Fatalf("inactive value %q leaked into graph output", inactiveValue)
		}
	}
	if results := search.Query(g, []string{"main"}); len(results) != 0 {
		t.Fatalf("query for main returned ignored-tool data: %+v", results)
	}

	newer := g.GeneratedAt.Add(time.Minute)
	if err := os.Chtimes(toolPath, newer, newer); err != nil {
		t.Fatal(err)
	}
	if stale := search.Stale(g, root); stale.IsStale {
		t.Fatalf("ignored tool made graph stale: %+v", stale)
	}

	builds := 0
	refresh := graphRefresher(
		g,
		root,
		func(string) (*graph.Graph, error) { builds++; return nil, nil },
		func(string) (*graph.Graph, error) { builds++; return nil, nil },
	)
	handlers := exposeMCPRefreshHandlers(t, g, refresh)
	requests := []struct {
		name      string
		arguments map[string]any
	}{
		{name: "gograph_plan", arguments: map[string]any{"symbol": "Active"}},
		{name: "gograph_review", arguments: map[string]any{"symbol": "Active"}},
		{name: "gograph_check", arguments: map[string]any{}},
	}
	for _, requestCase := range requests {
		handler := handlers[requestCase.name]
		if handler == nil {
			t.Fatalf("%s handler was not registered", requestCase.name)
		}
		request := mcpprotocol.CallToolRequest{}
		request.Params.Arguments = requestCase.arguments
		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("%s handler: %v", requestCase.name, err)
		}
		if result == nil || result.IsError {
			t.Fatalf("%s returned an MCP error: %s", requestCase.name, mcpResultText(t, result))
		}
	}
	if builds != 0 {
		t.Fatalf("ignored tool triggered %d MCP graph rebuild(s)", builds)
	}
}

func TestBuildContextMatchesPrecisePackageLoading(t *testing.T) {
	setDeterministicBuildEnvironment(t, "issue30_active")
	root := t.TempDir()
	writeBuildContextFixture(t, root, "go.mod", "module example.com/contextmatrix\n\ngo 1.26\n")
	fixtures := map[string]string{
		"active.go":           "package contextmatrix\n",
		"platform_linux.go":   "package contextmatrix\n",
		"platform_windows.go": "package contextmatrix\n",
		"tag_active.go":       "//go:build issue30_active\n\npackage contextmatrix\n",
		"tag_inactive.go":     "//go:build issue30_inactive\n\npackage contextmatrix\n",
		"legacy_active.go":    "// +build issue30_active\n\npackage contextmatrix\n",
		"legacy_inactive.go":  "// +build issue30_inactive\n\npackage contextmatrix\n",
		"cgo.go":              "package contextmatrix\nimport \"C\"\n",
		"active_test.go":      "//go:build issue30_active\n\npackage contextmatrix\nfunc TestActive() {}\n",
		"inactive_test.go":    "//go:build issue30_inactive\n\npackage contextmatrix\nfunc TestInactive() {}\n",
		"release_tool_tag.go": "//go:build go1.1 && amd64.v1\n\npackage contextmatrix\n",
	}
	for name, content := range fixtures {
		writeBuildContextFixture(t, root, name, content)
	}

	g, err := buildPreciseGraph(root)
	if err != nil {
		skipCoverageCacheFallback(t, g)
		t.Fatalf("buildPreciseGraph: %v", err)
	}
	if got := g.Build.EffectivePrecision(); got != graph.PrecisionPrecise {
		t.Fatalf("precision = %s, want precise", got)
	}
	got := make([]string, 0, len(g.Files))
	for _, file := range g.Files {
		got = append(got, filepath.Base(file.Path))
	}
	sort.Strings(got)
	want := []string{
		"active.go",
		"active_test.go",
		"legacy_active.go",
		"platform_linux.go",
		"release_tool_tag.go",
		"tag_active.go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("precise graph files = %v, want %v", got, want)
	}
}

func TestExplicitIgnoreTagMatchesGoToolchain(t *testing.T) {
	setDeterministicBuildEnvironment(t, "ignore")
	root := t.TempDir()
	writeBuildContextFixture(t, root, "go.mod", "module example.com/explicitignore\n\ngo 1.26\n")
	writeBuildContextFixture(t, root, "library.go", "package explicitignore\nfunc Library() {}\n")
	writeBuildContextFixture(t, root, "tool.go", "//go:build ignore\n\npackage main\nfunc main() {}\n")

	g, err := buildPreciseGraph(root)
	if err == nil {
		t.Fatal("explicit ignore tag unexpectedly accepted conflicting active packages")
	}
	skipCoverageCacheFallback(t, g)
	if g == nil || g.Build.EffectivePrecision() != graph.PrecisionFallback || g.Build.ScannedFiles != 2 {
		t.Fatalf("explicit ignore tag did not activate both files and fall back: graph=%+v", g)
	}
	if !strings.Contains(err.Error(), "found packages") {
		t.Fatalf("explicit ignore tag returned an unexpected Go-tool error: %v", err)
	}
}

func TestBuildPreciseGraphKeepsFallbackForNestedModuleCoverageGap(t *testing.T) {
	setDeterministicBuildEnvironment(t, "")
	root := t.TempDir()
	writeBuildContextFixture(t, root, "go.mod", "module example.com/parent\n\ngo 1.26\n")
	writeBuildContextFixture(t, root, "parent.go", "package parent\nfunc Parent() {}\n")
	writeBuildContextFixture(t, root, "nested/go.mod", "module example.com/child\n\ngo 1.26\n")
	writeBuildContextFixture(t, root, "nested/child.go", "package child\nfunc Child() {}\n")

	g, err := buildPreciseGraph(root)
	if err == nil {
		t.Fatal("buildPreciseGraph unexpectedly accepted a nested-module coverage gap")
	}
	skipCoverageCacheFallback(t, g)
	if g == nil || g.Build.EffectivePrecision() != graph.PrecisionFallback {
		t.Fatalf("nested-module graph did not record precise_fallback: graph=%+v", g)
	}
	if !strings.Contains(err.Error(), "omitted 1 indexed production file") || !strings.Contains(err.Error(), filepath.Join("nested", "child.go")) {
		t.Fatalf("unexpected coverage error: %v", err)
	}
}

func setDeterministicBuildEnvironment(t *testing.T, tag string) {
	t.Helper()
	t.Setenv("GOENV", "off")
	t.Setenv("GOWORK", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOOS", "linux")
	t.Setenv("GOARCH", "amd64")
	t.Setenv("CGO_ENABLED", "0")
	if tag == "" {
		t.Setenv("GOFLAGS", "")
	} else {
		t.Setenv("GOFLAGS", "-tags="+tag)
	}
}

func writeBuildContextFixture(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
