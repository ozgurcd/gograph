package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestPreciseBuildWithExplicitTagsIncludesTaggedTests(t *testing.T) {
	setDeterministicBuildEnvironment(t, "")
	t.Setenv("GOFLAGS", "-tags=ambient_only")
	root := t.TempDir()
	writeBuildContextFixture(t, root, "go.mod", "module example.com/tagged\n\ngo 1.26\n")
	writeBuildContextFixture(t, root, "product.go", "package tagged\n\nfunc Product() {}\nfunc Used() { Product() }\n")
	writeBuildContextFixture(t, root, "ambient_only.go", "//go:build ambient_only\n\npackage tagged\n\nfunc AmbientOnly() { missingSymbol() }\n")
	writeBuildContextFixture(t, root, "without_integration.go", "//go:build !integration\n\npackage tagged\n\nfunc WithoutIntegration() {}\n")
	writeBuildContextFixture(t, root, "integration_test.go", `//go:build integration

package tagged

import "testing"

func TestIntegration(t *testing.T) { Product() }
`)

	if code := runBuild([]string{root, "--precise", "--tags", "integration"}); code != 0 {
		t.Fatalf("tagged precise build exit code = %d", code)
	}
	data, err := os.ReadFile(filepath.Join(root, graphFile))
	if err != nil {
		t.Fatal(err)
	}
	var built graph.Graph
	if err := json.Unmarshal(data, &built); err != nil {
		t.Fatal(err)
	}
	if built.Build.EffectivePrecision() != graph.PrecisionPrecise {
		t.Fatalf("precision = %s, want precise", built.Build.EffectivePrecision())
	}
	var files []string
	for _, file := range built.Files {
		files = append(files, file.Path)
	}
	if !slices.Contains(files, "integration_test.go") {
		t.Fatalf("tagged graph files = %v, want integration_test.go", files)
	}
	for _, inactive := range []string{"ambient_only.go", "without_integration.go"} {
		if slices.Contains(files, inactive) {
			t.Fatalf("explicit integration selection included %s: %v", inactive, files)
		}
	}
	foundTestCall := false
	for _, call := range built.Calls {
		if call.CallerName == "TestIntegration" && call.CalleeRaw == "Product" {
			foundTestCall = true
			break
		}
	}
	if !foundTestCall {
		t.Fatal("tagged precise graph did not attribute TestIntegration -> Product")
	}
	foundTestEdge := false
	for _, edge := range built.TestEdges {
		if edge.TestFunc == "TestIntegration" && edge.Target == "Product" && edge.TargetSymbolID != "" {
			foundTestEdge = true
			break
		}
	}
	if !foundTestEdge {
		t.Fatalf("tagged precise graph test edges = %+v, want exact TestIntegration -> Product", built.TestEdges)
	}
	for _, result := range search.Untested(&built) {
		if result.Name == "Product" {
			t.Fatalf("tagged test attribution left Product in untested results: %+v", result)
		}
	}
	config, err := resolveBuildConfigWithTags(root, []string{"integration"})
	if err != nil {
		t.Fatal(err)
	}
	if stale := search.StaleWithConfig(&built, root, config); stale.IsStale {
		t.Fatalf("matching tagged selection reported stale: %+v", stale)
	}
}

func TestBuildAndMCPTagArgumentsHaveParity(t *testing.T) {
	buildOptions, err := parseBuildArgs([]string{".", "--tags=second,integration", "--tags", "integration"})
	if err != nil {
		t.Fatal(err)
	}
	mcpOptions, err := parseMCPArgs([]string{".", "--tags", "integration,second"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(buildOptions.Tags, mcpOptions.Tags) {
		t.Fatalf("build tags %v differ from MCP tags %v", buildOptions.Tags, mcpOptions.Tags)
	}
	for _, args := range [][]string{{"--tags="}, {"--tags=integration||linux"}, {"--tags", "integration linux"}} {
		if _, err := parseBuildArgs(args); err == nil {
			t.Errorf("build accepted invalid tags %q", args)
		}
		if _, err := parseMCPArgs(args); err == nil {
			t.Errorf("MCP accepted invalid tags %q", args)
		}
	}
}
