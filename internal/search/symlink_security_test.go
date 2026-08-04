package search_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestPoisonedGraphCannotReadSourceSymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	outside := filepath.Join(base, "outside.go")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc Caller() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const sentinel = "BENIGN-EXTERNAL-SENTINEL"
	if err := os.WriteFile(outside, []byte("package outside\nfunc OutsideOnly() string { return \""+sentinel+"\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.go")); err != nil {
		t.Skipf("create source symlink: %v", err)
	}

	g := &graph.Graph{
		Root: root,
		Symbols: []graph.SymbolNode{
			{ID: "example.com/repo::OutsideOnly", Name: "OutsideOnly", Kind: graph.KindFunction, File: "linked.go", Line: 2, EndLine: 2},
			{ID: "example.com/repo::Caller", Name: "Caller", Kind: graph.KindFunction, File: "main.go", Line: 2, EndLine: 2},
		},
		Calls: []graph.CallEdge{{
			CallerSymbolID: "example.com/repo::Caller",
			CallerName:     "Caller",
			CalleeRaw:      "OutsideOnly",
			CalleeSymbolID: "example.com/repo::OutsideOnly",
			File:           "linked.go",
			Line:           2,
		}},
	}

	source, err := search.Source(g, root, "OutsideOnly")
	if err == nil {
		t.Fatalf("Source returned poisoned data: %q", source)
	}
	if strings.Contains(source, sentinel) || strings.Contains(err.Error(), sentinel) {
		t.Fatalf("Source disclosed external sentinel: source=%q err=%v", source, err)
	}

	callers := search.Callers(g, "OutsideOnly", true, true)
	if len(callers) != 1 {
		t.Fatalf("Callers returned %d structural results, want 1", len(callers))
	}
	if strings.Contains(callers[0].Detail, sentinel) || strings.Contains(callers[0].Detail, "return") {
		t.Fatalf("Callers disclosed external snippet: %q", callers[0].Detail)
	}
	calls := search.Callees(g, "Caller", true, true)
	if len(calls) != 1 {
		t.Fatalf("Callees returned %d structural results, want 1", len(calls))
	}
	if strings.Contains(calls[0].Detail, sentinel) || strings.Contains(calls[0].Detail, "return") {
		t.Fatalf("Callees disclosed external snippet: %q", calls[0].Detail)
	}
	complexity := search.Complexity(g, "OutsideOnly")
	if len(complexity) != 1 || complexity[0].Score != -1 || complexity[0].Label != "UNKNOWN" {
		t.Fatalf("Complexity poisoned-source result = %+v, want UNKNOWN", complexity)
	}
}

func TestSourceSearchesPastUnsafeAmbiguousMatches(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "safe.go"), []byte("package repository\nfunc SafeChoice() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\nfunc SafeChoice() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := &graph.Graph{Root: root}
	for i := 0; i < 5; i++ {
		name := filepath.Join(root, "linked-"+string(rune('a'+i))+".go")
		if err := os.Symlink(outside, name); err != nil {
			t.Skipf("create source symlink: %v", err)
		}
		g.Symbols = append(g.Symbols, graph.SymbolNode{
			ID: "unsafe", Name: "SafeChoice", Kind: graph.KindFunction,
			File: filepath.Base(name), Line: 2, EndLine: 2,
		})
	}
	g.Symbols = append(g.Symbols, graph.SymbolNode{
		ID: "safe", Name: "SafeChoice", Kind: graph.KindFunction,
		File: "safe.go", Line: 2, EndLine: 2,
	})

	source, err := search.Source(g, root, "SafeChoice")
	if err != nil {
		t.Fatalf("Source skipped later safe match: %v", err)
	}
	if !strings.Contains(source, "func SafeChoice() {}") {
		t.Fatalf("Source result = %q, want safe match", source)
	}
}

func TestCreateBoundariesRejectsLinkedOutput(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	outDir := filepath.Join(base, "outside")
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(outDir, "boundaries.json")
	if err := os.Symlink(outside, filepath.Join(root, ".gograph", "boundaries.json")); err != nil {
		t.Skipf("create boundary config symlink: %v", err)
	}

	err := search.CreateBoundaries(&graph.Graph{Root: root}, ".gograph/boundaries.json")
	if err == nil {
		t.Fatal("CreateBoundaries accepted linked output")
	}
	if _, statErr := os.Stat(outside); !os.IsNotExist(statErr) {
		t.Fatalf("outside boundary target was created: %v", statErr)
	}
}

func TestBoundariesAcceptsPhysicalAliasOfGraphRoot(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("physical /var alias is specific to macOS")
	}
	root := t.TempDir()
	physicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(physicalRoot) == filepath.Clean(root) {
		t.Skip("temporary directory has no physical path alias")
	}
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gograph", "boundaries.json"), []byte(`{"layers":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &graph.Graph{Root: root}
	if _, err := search.Boundaries(g, filepath.Join(physicalRoot, ".gograph", "boundaries.json")); err != nil {
		t.Fatalf("read boundaries through physical root alias: %v", err)
	}
	created := filepath.Join(physicalRoot, ".gograph", "created.json")
	if err := search.CreateBoundaries(g, created); err != nil {
		t.Fatalf("create boundaries through physical root alias: %v", err)
	}
	if info, err := os.Stat(filepath.Join(root, ".gograph", "created.json")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("created boundary = %v, %v", info, err)
	}
}

func TestBoundariesAcceptsLocalFilenameContainingDotDot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(".gograph", "boundaries..json")
	if err := os.WriteFile(filepath.Join(root, name), []byte(`{"layers":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := search.Boundaries(&graph.Graph{Root: root}, name); err != nil {
		t.Fatalf("Boundaries rejected harmless local filename: %v", err)
	}
}
