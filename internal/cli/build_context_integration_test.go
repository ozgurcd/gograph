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

	if code := runBuild([]string{root, "--precise"}); code != 0 {
		t.Fatalf("runBuild --precise exit code = %d, want 0", code)
	}
	data, err := os.ReadFile(filepath.Join(root, graphFile))
	if err != nil {
		t.Fatalf("read persisted precise graph: %v", err)
	}
	var g graph.Graph
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatalf("decode persisted precise graph: %v", err)
	}
	if got := g.Build.EffectivePrecision(); got != graph.PrecisionPrecise {
		t.Fatalf("precision = %s, want %s", got, graph.PrecisionPrecise)
	}
	if g.Build.BuildContextFingerprint == "" {
		t.Fatal("precise graph did not record a build-context fingerprint")
	}
	if g.Build.ScannedFiles != 1 || g.Build.ParsedFiles != 1 || len(g.Files) != 1 || g.Files[0].Path != "library.go" {
		t.Fatalf("ignored tool affected file inventory: build=%+v files=%+v", g.Build, g.Files)
	}

	encoded, err := json.Marshal(&g)
	if err != nil {
		t.Fatal(err)
	}
	graphOutput := string(encoded)
	for _, inactiveValue := range []string{"tool.go", "Issue30IgnoredToolSentinel"} {
		if strings.Contains(graphOutput, inactiveValue) {
			t.Fatalf("inactive value %q leaked into graph output", inactiveValue)
		}
	}
	if results := search.Query(&g, []string{"main"}); len(results) != 0 {
		t.Fatalf("query for main returned ignored-tool data: %+v", results)
	}

	newer := g.GeneratedAt.Add(time.Minute)
	if err := os.Chtimes(toolPath, newer, newer); err != nil {
		t.Fatal(err)
	}
	if stale := search.Stale(&g, root); stale.IsStale {
		t.Fatalf("ignored tool made graph stale: %+v", stale)
	}

	builds := 0
	refresh := graphRefresher(
		&g,
		root,
		func(string) (*graph.Graph, error) { builds++; return nil, nil },
		func(string) (*graph.Graph, error) { builds++; return nil, nil },
	)
	handlers := exposeMCPRefreshHandlers(t, &g, refresh)
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
		"active.go":                     "package contextmatrix\n",
		"platform_amd64.go":             "package contextmatrix\n",
		"platform_arm64.go":             "package contextmatrix\n",
		"platform_linux.go":             "package contextmatrix\n",
		"platform_windows.go":           "package contextmatrix\n",
		"tag_active.go":                 "//go:build issue30_active\n\npackage contextmatrix\n",
		"tag_inactive.go":               "//go:build issue30_inactive\n\npackage contextmatrix\n",
		"legacy_active.go":              "// +build issue30_active\n\npackage contextmatrix\n",
		"legacy_inactive.go":            "// +build issue30_inactive\n\npackage contextmatrix\n",
		"cgo.go":                        "package contextmatrix\nimport \"C\"\n",
		"active_test.go":                "//go:build issue30_active\n\npackage contextmatrix\nfunc TestActive() {}\n",
		"inactive_test.go":              "//go:build issue30_inactive\n\npackage contextmatrix\nfunc TestInactive() {}\n",
		"release_tool_tag.go":           "//go:build go1.1 && amd64.v1\n\npackage contextmatrix\n",
		".scratch/hidden_dot.go":        "package scratch\nfunc HiddenDotSentinel() {}\n",
		"_scratch/hidden_underscore.go": "package scratch\nfunc HiddenUnderscoreSentinel() {}\n",
		"testdata/hidden_testdata.go":   "package testdata\nfunc HiddenTestdataSentinel() {}\n",
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
		"platform_amd64.go",
		"platform_linux.go",
		"release_tool_tag.go",
		"tag_active.go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("precise graph files = %v, want %v", got, want)
	}
	encoded, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	for _, inactive := range []string{"HiddenDotSentinel", "HiddenUnderscoreSentinel", "HiddenTestdataSentinel", "TestInactive"} {
		if strings.Contains(string(encoded), inactive) {
			t.Fatalf("inactive symbol %q leaked into precise graph", inactive)
		}
	}
	if !strings.Contains(string(encoded), "TestActive") {
		t.Fatal("active constrained test symbol was not indexed")
	}
}

func TestGoModIgnoreMatchesPrecisePackageLoading(t *testing.T) {
	setDeterministicBuildEnvironment(t, "")
	root := t.TempDir()
	writeBuildContextFixture(t, root, "go.mod", `module example.com/modignore

go 1.26

ignore (
	ignored
	./anchored
)
`)
	writeBuildContextFixture(t, root, "root.go", "package modignore\nfunc Root() {}\n")
	writeBuildContextFixture(t, root, "ignored/noise.go", "package ignored\nfunc IgnoredSentinel() {}\n")
	writeBuildContextFixture(t, root, "deep/ignored/noise.go", "package ignored\nfunc DeepIgnoredSentinel() {}\n")
	writeBuildContextFixture(t, root, "anchored/noise.go", "package anchored\nfunc AnchoredIgnoredSentinel() {}\n")
	writeBuildContextFixture(t, root, "deep/anchored/keep.go", "package anchored\nfunc DeepAnchoredKept() {}\n")
	writeBuildContextFixture(t, root, "ignored2/keep.go", "package ignored2\nfunc SimilarNameKept() {}\n")

	g, err := buildPreciseGraph(root)
	if err != nil {
		skipCoverageCacheFallback(t, g)
		t.Fatalf("buildPreciseGraph: %v", err)
	}
	if got := g.Build.EffectivePrecision(); got != graph.PrecisionPrecise {
		t.Fatalf("precision = %s, want precise", got)
	}
	encoded, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, excluded := range []string{"IgnoredSentinel", "DeepIgnoredSentinel", "AnchoredIgnoredSentinel"} {
		if strings.Contains(text, excluded) {
			t.Fatalf("go.mod-ignored symbol %q leaked into graph", excluded)
		}
	}
	for _, included := range []string{"Root", "DeepAnchoredKept", "SimilarNameKept"} {
		if !strings.Contains(text, included) {
			t.Fatalf("eligible symbol %q was omitted from graph", included)
		}
	}
}

func TestGoModIgnoreMatchesPreciseLoadingFromModuleSubdirectory(t *testing.T) {
	setDeterministicBuildEnvironment(t, "")
	moduleRoot := t.TempDir()
	writeBuildContextFixture(t, moduleRoot, "go.mod", "module example.com/subdirignore\n\ngo 1.26\n\nignore ignored\n")
	scanRoot := filepath.Join(moduleRoot, "pkg")
	writeBuildContextFixture(t, scanRoot, "keep.go", "package pkg\nfunc Keep() {}\n")
	writeBuildContextFixture(t, scanRoot, "ignored/noise.go", "package ignored\nfunc ParentIgnoredSentinel() {}\n")

	g, err := buildPreciseGraph(scanRoot)
	if err != nil {
		skipCoverageCacheFallback(t, g)
		t.Fatalf("buildPreciseGraph from module subdirectory: %v", err)
	}
	if got := g.Build.EffectivePrecision(); got != graph.PrecisionPrecise {
		t.Fatalf("precision = %s, want precise", got)
	}
	encoded, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "ParentIgnoredSentinel") {
		t.Fatal("parent-module ignored symbol leaked into subdirectory graph")
	}
	if !strings.Contains(string(encoded), "Keep") {
		t.Fatal("eligible subdirectory symbol was omitted")
	}
	if len(g.Symbols) != 1 || g.Symbols[0].ID != "example.com/subdirignore/pkg::Keep" {
		t.Fatalf("subdirectory symbol identity = %+v, want module-qualified Keep", g.Symbols)
	}
}

func TestPreciseBuildFollowsExplicitRootSymlink(t *testing.T) {
	setDeterministicBuildEnvironment(t, "")
	realRoot := filepath.Join(t.TempDir(), "real")
	writeBuildContextFixture(t, realRoot, "go.mod", "module example.com/rootlink\n\ngo 1.26\n")
	writeBuildContextFixture(t, realRoot, "root.go", "package rootlink\nfunc RootLink() {}\n")
	linkRoot := filepath.Join(t.TempDir(), "linked-root")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Skipf("create root symlink: %v", err)
	}
	t.Setenv("PWD", filepath.Dir(linkRoot))

	g, err := buildPreciseGraph(linkRoot)
	if err != nil {
		skipCoverageCacheFallback(t, g)
		t.Fatalf("buildPreciseGraph through root symlink: %v", err)
	}
	if got := g.Build.EffectivePrecision(); got != graph.PrecisionPrecise || len(g.Files) != 1 || g.Files[0].Path != "root.go" {
		t.Fatalf("root-symlink precise graph = precision:%s files:%+v", got, g.Files)
	}
	if len(g.Symbols) != 1 || g.Symbols[0].ID != "example.com/rootlink::RootLink" {
		t.Fatalf("root-symlink symbol identity = %+v", g.Symbols)
	}
	t.Setenv("PWD", linkRoot)
	if stale := search.Stale(g, linkRoot); stale.IsStale {
		t.Fatalf("new graph built outside and queried inside root symlink was immediately stale: %+v", stale)
	}
}

func TestPreciseBuildAlignsSymlinkedModuleSubdirectoryIdentity(t *testing.T) {
	setDeterministicBuildEnvironment(t, "")
	moduleRoot := filepath.Join(t.TempDir(), "module")
	writeBuildContextFixture(t, moduleRoot, "go.mod", "module example.com/root\n\ngo 1.26\n\nignore ./pkg/ignored\n")
	packageRoot := filepath.Join(moduleRoot, "pkg")
	writeBuildContextFixture(t, packageRoot, "runner.go", `package pkg

type Runner interface { Run() }
type Impl struct{}
func (*Impl) Run() {}
func Invoke(r Runner) { r.Run() }
`)
	writeBuildContextFixture(t, packageRoot, "ignored/noise.go", "package ignored\nfunc SymlinkIgnoredSentinel() {}\n")
	linkRoot := filepath.Join(t.TempDir(), "linked-pkg")
	if err := os.Symlink(packageRoot, linkRoot); err != nil {
		t.Skipf("create package symlink: %v", err)
	}
	t.Setenv("PWD", filepath.Dir(linkRoot))

	g, err := buildPreciseGraph(linkRoot)
	if err != nil {
		skipCoverageCacheFallback(t, g)
		t.Fatalf("buildPreciseGraph through package symlink: %v", err)
	}
	if got := g.Build.EffectivePrecision(); got != graph.PrecisionPrecise {
		t.Fatalf("package-symlink precision = %s, want precise", got)
	}
	if len(g.Files) != 1 || g.Files[0].Path != "runner.go" {
		t.Fatalf("package-symlink selected files = %+v, want runner.go only", g.Files)
	}
	symbolIDs := make(map[string]struct{}, len(g.Symbols))
	for _, symbol := range g.Symbols {
		symbolIDs[symbol.ID] = struct{}{}
	}
	for _, edge := range g.Implements {
		if _, ok := symbolIDs[edge.InterfaceID]; !ok {
			t.Fatalf("package-symlink interface ID %q has no AST symbol", edge.InterfaceID)
		}
		if _, ok := symbolIDs[edge.ConcreteID]; !ok {
			t.Fatalf("package-symlink concrete ID %q has no AST symbol", edge.ConcreteID)
		}
	}
	for _, call := range g.Calls {
		if call.CalleeSymbolID != "" {
			if _, ok := symbolIDs[call.CalleeSymbolID]; !ok {
				t.Fatalf("package-symlink callee ID %q has no AST symbol", call.CalleeSymbolID)
			}
		}
	}
	if len(g.Implements) == 0 {
		t.Fatal("package-symlink fixture produced no precise implements edge")
	}
	t.Setenv("PWD", linkRoot)
	if stale := search.Stale(g, linkRoot); stale.IsStale {
		t.Fatalf("new graph built outside and queried inside package symlink was immediately stale: %+v", stale)
	}
}

func TestPreciseBuildKeepsSymlinkedGoModIdentityAligned(t *testing.T) {
	setDeterministicBuildEnvironment(t, "")
	base := t.TempDir()
	repository := filepath.Join(base, "repository")
	sharedMod := writeBuildContextFixture(t, filepath.Join(base, "shared"), "base.mod", "module example.com/modlink\n\ngo 1.26\n")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sharedMod, filepath.Join(repository, "go.mod")); err != nil {
		t.Skipf("create go.mod symlink: %v", err)
	}
	writeBuildContextFixture(t, repository, "service.go", `package modlink

type Service interface { Run() }
type Implementation struct{}
func (*Implementation) Run() {}
func Invoke(service Service) { service.Run() }
`)

	g, err := buildPreciseGraph(repository)
	if err != nil {
		skipCoverageCacheFallback(t, g)
		t.Fatalf("buildPreciseGraph with symlinked go.mod: %v", err)
	}
	if got := g.Build.EffectivePrecision(); got != graph.PrecisionPrecise {
		t.Fatalf("symlinked-go.mod precision = %s, want precise", got)
	}
	symbolIDs := make(map[string]struct{}, len(g.Symbols))
	for _, symbol := range g.Symbols {
		symbolIDs[symbol.ID] = struct{}{}
		if !strings.HasPrefix(symbol.ID, "example.com/modlink::") {
			t.Fatalf("symbol ID %q does not use module identity", symbol.ID)
		}
	}
	if len(g.Implements) == 0 {
		t.Fatal("symlinked-go.mod fixture produced no precise implements edge")
	}
	for _, edge := range g.Implements {
		if _, ok := symbolIDs[edge.InterfaceID]; !ok {
			t.Fatalf("interface ID %q has no AST symbol", edge.InterfaceID)
		}
		if _, ok := symbolIDs[edge.ConcreteID]; !ok {
			t.Fatalf("concrete ID %q has no AST symbol", edge.ConcreteID)
		}
	}
}

func TestModuleIgnoreAtExplicitRootMatchesPackageLoading(t *testing.T) {
	setDeterministicBuildEnvironment(t, "")
	moduleRoot := t.TempDir()
	writeBuildContextFixture(t, moduleRoot, "go.mod", "module example.com/ignoredroot\n\ngo 1.26\n\nignore ./ignored\n")
	scanRoot := filepath.Join(moduleRoot, "ignored")
	writeBuildContextFixture(t, scanRoot, "noise.go", "package ignored\nfunc IgnoredRootSentinel() {}\n")

	g, err := buildPreciseGraph(scanRoot)
	if err == nil || g != nil || !strings.Contains(err.Error(), "no Go files") {
		t.Fatalf("ignored explicit root graph=%+v err=%v, want no-file rejection", g, err)
	}
}

func TestNestedModuleBoundaryChangesMakeGraphStale(t *testing.T) {
	setDeterministicBuildEnvironment(t, "")
	root := t.TempDir()
	writeBuildContextFixture(t, root, "go.mod", "module example.com/boundary\n\ngo 1.26\n")
	writeBuildContextFixture(t, root, "parent.go", "package boundary\nfunc Parent() {}\n")
	writeBuildContextFixture(t, root, "nested/child.go", "package nested\nfunc Child() {}\n")

	preciseGraph, err := buildPreciseGraph(root)
	if err != nil {
		skipCoverageCacheFallback(t, preciseGraph)
		t.Fatalf("initial precise graph: %v", err)
	}
	boundaryPath := writeBuildContextFixture(t, root, "nested/go.mod", "module example.com/nested\n\ngo 1.26\n")
	if stale := search.Stale(preciseGraph, root); !stale.IsStale || !stale.BuildContextChanged || len(stale.ChangedFiles) != 0 {
		t.Fatalf("added nested module boundary was not a context-only freshness change: %+v", stale)
	}

	fallbackGraph, err := buildPreciseGraph(root)
	if err == nil || fallbackGraph == nil || fallbackGraph.Build.EffectivePrecision() != graph.PrecisionFallback {
		t.Fatalf("nested module boundary did not preserve genuine fallback: graph=%+v err=%v", fallbackGraph, err)
	}
	if err := os.Remove(boundaryPath); err != nil {
		t.Fatal(err)
	}
	if stale := search.Stale(fallbackGraph, root); !stale.IsStale || !stale.BuildContextChanged || len(stale.ChangedFiles) != 0 {
		t.Fatalf("removed nested module boundary was not a context-only freshness change: %+v", stale)
	}
}

func TestGO111MODULEOffKeepsScannerAndPreciseIdentityAligned(t *testing.T) {
	setDeterministicBuildEnvironment(t, "")
	t.Setenv("GO111MODULE", "off")
	root := t.TempDir()
	writeBuildContextFixture(t, root, "go.mod", "module example.com/wrong-if-used\n\ngo 1.26\n\nignore ignored\n")
	writeBuildContextFixture(t, root, "root.go", `package offids

type Runner interface { Run() }
type Impl struct{}
func (Impl) Run() {}
func Invoke(r Runner) { r.Run() }
`)
	writeBuildContextFixture(t, root, "ignored/noise.go", "package ignored\nfunc IncludedWithModulesOff() {}\n")

	g, err := buildPreciseGraph(root)
	if err != nil {
		skipCoverageCacheFallback(t, g)
		t.Fatalf("GO111MODULE=off precise graph: %v", err)
	}
	if got := g.Build.EffectivePrecision(); got != graph.PrecisionPrecise || len(g.Files) != 2 {
		t.Fatalf("module-off graph = precision:%s files:%+v", got, g.Files)
	}
	symbolIDs := make(map[string]struct{}, len(g.Symbols))
	for _, symbol := range g.Symbols {
		symbolIDs[symbol.ID] = struct{}{}
		if strings.HasPrefix(symbol.ID, "example.com/wrong-if-used::") {
			t.Fatalf("module-off symbol used go.mod identity: %s", symbol.ID)
		}
	}
	for _, edge := range g.Implements {
		if _, ok := symbolIDs[edge.InterfaceID]; !ok {
			t.Fatalf("precise interface ID %q has no AST symbol", edge.InterfaceID)
		}
		if _, ok := symbolIDs[edge.ConcreteID]; !ok {
			t.Fatalf("precise concrete ID %q has no AST symbol", edge.ConcreteID)
		}
	}
	if len(g.Implements) == 0 {
		t.Fatal("module-off fixture produced no precise implements edge")
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
	writeBuildContextFixture(t, root, "nested/go.mod", "module example.com/child\n\ngo 1.26\n\nignore ignored\n")
	writeBuildContextFixture(t, root, "nested/child.go", "package child\nfunc Child() {}\n")
	writeBuildContextFixture(t, root, "nested/ignored/noise.go", "package ignored\nfunc NestedIgnoredSentinel() {}\n")

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
	encoded, marshalErr := json.Marshal(g)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(encoded), "NestedIgnoredSentinel") {
		t.Fatal("nested module ignore directive leaked into fallback graph")
	}
}

func setDeterministicBuildEnvironment(t *testing.T, tag string) {
	t.Helper()
	t.Setenv("GOENV", "off")
	t.Setenv("GOWORK", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GO111MODULE", "")
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
