package wiki

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestGenerateSupportsRealAbsoluteOutput(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/wiki\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "generated", "wiki")
	if _, err := New(&graph.Graph{Root: root}).Generate(output); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(output, "overview.md")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("generated overview = %v, %v", info, err)
	}
}

func TestGenerateRejectsLinkedOutputAndPage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/wiki\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	linkedOutput := filepath.Join(t.TempDir(), "wiki-link")
	if err := os.Symlink(outside, linkedOutput); err != nil {
		t.Skipf("create output symlink: %v", err)
	}
	if _, err := New(&graph.Graph{Root: root}).Generate(linkedOutput); err == nil {
		t.Fatal("Generate accepted linked output directory")
	}
	if _, err := os.Stat(filepath.Join(outside, "overview.md")); !os.IsNotExist(err) {
		t.Fatalf("linked output received generated page: %v", err)
	}

	realOutput := t.TempDir()
	outsidePage := filepath.Join(outside, "outside.md")
	if err := os.WriteFile(outsidePage, []byte("KEEP"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsidePage, filepath.Join(realOutput, "overview.md")); err != nil {
		t.Skipf("create page symlink: %v", err)
	}
	if _, err := New(&graph.Graph{Root: root}).Generate(realOutput); err == nil {
		t.Fatal("Generate accepted linked page")
	}
	data, err := os.ReadFile(outsidePage)
	if err != nil || string(data) != "KEEP" {
		t.Fatalf("outside page = %q, %v", data, err)
	}
}

func TestGenerateConfinesRelativeOutputToGraphRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/wiki\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked-output")); err != nil {
		t.Skipf("create output ancestor symlink: %v", err)
	}

	generator := New(&graph.Graph{Root: root})
	if _, err := generator.Generate(filepath.Join("linked-output", "wiki")); err == nil {
		t.Fatal("Generate accepted a linked relative output ancestor")
	}
	if _, err := os.Stat(filepath.Join(outside, "wiki", "overview.md")); !os.IsNotExist(err) {
		t.Fatalf("linked ancestor received generated page: %v", err)
	}
	if _, err := generator.Generate(filepath.Join("..", "outside-wiki")); err == nil {
		t.Fatal("Generate accepted relative output traversal")
	}
	if _, err := generator.Generate("safe-wiki"); err != nil {
		t.Fatalf("Generate rejected safe relative output: %v", err)
	}
	if info, err := os.Stat(filepath.Join(root, "safe-wiki", "overview.md")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("generated rooted overview = %v, %v", info, err)
	}
}

func TestGeneratePrunesOnlyObsoleteGeneratedPackagePages(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/wiki\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	generated := New(&graph.Graph{
		Root: root,
		Symbols: []graph.SymbolNode{{
			ID: "example.com/wiki/old::Old", Kind: graph.KindFunction, Name: "Old",
		}},
	})
	if _, err := generated.Generate("llm-wiki"); err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	packageDirectory := filepath.Join(root, "llm-wiki", "packages")
	customPath := filepath.Join(packageDirectory, "custom.md")
	readmePath := filepath.Join(packageDirectory, "README.md")
	if err := os.WriteFile(customPath, []byte("# Maintained by a person\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(readmePath, []byte("# Package notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := New(&graph.Graph{Root: root}).Generate("llm-wiki"); err != nil {
		t.Fatalf("second Generate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(packageDirectory, "old.md")); !os.IsNotExist(err) {
		t.Fatalf("obsolete generated page still exists: %v", err)
	}
	for _, path := range []string{customPath, readmePath} {
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("maintained package file %s = %v, %v", path, info, err)
		}
	}
}

func TestValidPageFilenameRejectsCrossPlatformTraversal(t *testing.T) {
	for _, name := range []string{"", ".", "../outside.md", "packages/../outside.md", "/outside.md", `packages\..\outside.md`, `C:\outside.md`} {
		if validPageFilename(name) {
			t.Errorf("validPageFilename(%q) = true", name)
		}
	}
	for _, name := range []string{"overview.md", "packages/internal-search.md"} {
		if !validPageFilename(name) {
			t.Errorf("validPageFilename(%q) = false", name)
		}
	}
}
