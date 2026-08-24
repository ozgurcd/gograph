package precise

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/packages"
)

const repositorySSAGuardFixture = `package guardfixture

import "strings"

func Normalize(value string) string {
	return strings.TrimSpace(value)
}
`

// TestBuildRepositorySSAExcludesDependencyBodies guards the scoped-SSA
// invariant directly instead of imposing a machine-specific allocation
// ceiling. Repository functions must have bodies, while imported dependencies
// retain only the package/type/function stubs needed to resolve local calls.
//
// This is one layer of the v1.6.3 memory regression coverage. Incremental
// test-edge stability and bounded artifact reads guard the reported ~106 GB
// failure path; repository-scoped SSA separately avoids dependency-body memory
// and source-less dependency-wrapper edges.
func TestBuildRepositorySSAExcludesDependencyBodies(t *testing.T) {
	root := writePreciseFixture(t, "example.com/guardfixture", "guard.go", repositorySSAGuardFixture)
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.com/guardfixture\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	initial, err := packages.Load(&packages.Config{
		Mode: packages.LoadAllSyntax,
		Dir:  root,
	}, "./...")
	if err != nil {
		t.Fatalf("packages.Load: %v", err)
	}
	if err := packageLoadError(initial); err != nil {
		t.Fatal(err)
	}
	if len(initial) != 1 {
		t.Fatalf("loaded %d initial packages, want 1", len(initial))
	}

	prog := buildRepositorySSA(initial)
	localPackage := prog.Package(initial[0].Types)
	if localPackage == nil {
		t.Fatal("repository SSA package is missing")
	}
	localFunction := localPackage.Func("Normalize")
	if localFunction == nil || len(localFunction.Blocks) == 0 {
		t.Fatalf("repository function body was not built: %#v", localFunction)
	}

	dependency := initial[0].Imports["strings"]
	if dependency == nil || dependency.Types == nil {
		t.Fatal("strings dependency was not loaded")
	}
	dependencyPackage := prog.Package(dependency.Types)
	if dependencyPackage == nil {
		t.Fatal("strings SSA package stub is missing")
	}
	dependencyFunction := dependencyPackage.Func("TrimSpace")
	if dependencyFunction == nil {
		t.Fatal("strings.TrimSpace SSA function stub is missing")
	}
	if len(dependencyFunction.Blocks) != 0 {
		t.Fatalf("dependency function body was built; repository-scoped SSA requires a stub, got %d blocks", len(dependencyFunction.Blocks))
	}
}
