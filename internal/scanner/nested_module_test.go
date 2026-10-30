package scanner_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ozgurcd/gograph/internal/scanner"
)

// Like cmd/go expanding ./..., the AST walk does not enter a nested module,
// even one the root's go.work uses, matching precise loading.
func TestWalkWithConfigSkipsNestedModules(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.26\n")
	mustWrite(t, filepath.Join(root, "root.go"), "package fixture\n")
	mustWrite(t, filepath.Join(root, "menubar", "go.mod"), "module example.com/menubar\n\ngo 1.26\n")
	mustWrite(t, filepath.Join(root, "menubar", "menu.go"), "package menubar\n")
	mustWrite(t, filepath.Join(root, "menubar", "sub", "deep.go"), "package sub\n")

	paths, errs := scanner.WalkWithConfig(root, moduleConfig(root, true))
	if len(errs) != 0 {
		t.Fatalf("WalkWithConfig errors: %v", errs)
	}
	if got, want := relativePaths(t, root, paths), []string{"root.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected files = %v, want %v", got, want)
	}

	mustWrite(t, filepath.Join(root, "go.work"), "go 1.26\n\nuse (\n\t.\n\t./menubar\n)\n")
	paths, errs = scanner.WalkWithConfig(root, moduleConfig(root, true))
	if len(errs) != 0 {
		t.Fatalf("WalkWithConfig errors: %v", errs)
	}
	if got, want := relativePaths(t, root, paths), []string{"root.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected files with go.work = %v, want %v", got, want)
	}
}
