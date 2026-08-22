package moduleinventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestDiscoverAndVerifyModulesFromRepositoryFiles(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.27.0\n")
	write(t, filepath.Join(root, "nested", "go.mod"), "module example.com/nested\n\ngo 1.27.0\n")
	packages := []graph.PackageNode{{ID: "root", Dir: "."}, {ID: "nested", Dir: "nested/pkg"}}
	modules, err := Discover(root, packages)
	if err != nil || len(modules) != 2 {
		t.Fatalf("Discover = %+v, %v", modules, err)
	}
	if _, err := Verify(root, packages, modules); err != nil {
		t.Fatalf("Verify matching modules: %v", err)
	}
	forged := append([]graph.ModuleNode(nil), modules...)
	forged[0].Path = "example.com/forged"
	if _, err := Verify(root, packages, forged); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("Verify forged modules error = %v", err)
	}
}

func TestDiscoverRejectsUnsafePackageDirectoryAndInvalidModule(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "go 1.27.0\n")
	if _, err := Discover(root, nil); err == nil || !strings.Contains(err.Error(), "module directive") {
		t.Fatalf("invalid module error = %v", err)
	}
	if _, err := Discover(root, []graph.PackageNode{{ID: "escape", Dir: "../outside"}}); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("unsafe package error = %v", err)
	}
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
