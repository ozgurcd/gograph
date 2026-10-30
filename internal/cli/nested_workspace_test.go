package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

// A go.work that uses a nested module does not change selection: precise
// loading of ./... never includes nested modules, so the AST walk skips them
// too and strict precision holds under every GOWORK setting.
func TestNestedWorkspaceMemberKeepsStrictPrecision(t *testing.T) {
	for _, gowork := range []string{"", "off"} {
		t.Run("GOWORK="+gowork, func(t *testing.T) {
			setDeterministicBuildEnvironment(t, "")
			if gowork != "" {
				t.Setenv("GOWORK", gowork)
			}
			root := t.TempDir()
			writeBuildContextFixture(t, root, "go.mod", "module example.com/review\n\ngo 1.26\n")
			writeBuildContextFixture(t, root, "root.go", "package review\nfunc Root() {}\n")
			writeBuildContextFixture(t, root, "go.work", "go 1.26\n\nuse (\n\t.\n\t./nested\n)\n")
			writeBuildContextFixture(t, root, "nested/go.mod", "module example.test/nested\n\ngo 1.26\n")
			writeBuildContextFixture(t, root, "nested/nested.go", "package nested\nfunc NestedSentinel() {}\n")

			g, err := buildPreciseGraph(root)
			if err != nil {
				skipCoverageCacheFallback(t, g)
				t.Fatalf("strict precise build failed: %v", err)
			}
			if g == nil || g.Build.EffectivePrecision() == graph.PrecisionFallback {
				t.Fatalf("graph is not precise: %+v", g)
			}
			encoded, _ := json.Marshal(g)
			if strings.Contains(string(encoded), "NestedSentinel") {
				t.Fatal("nested workspace member entered the AST selection")
			}
		})
	}
}
