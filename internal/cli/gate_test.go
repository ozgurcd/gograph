package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestGatePass(t *testing.T) {
	tmpDir := t.TempDir()
	origWd, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(origWd); err != nil {
			t.Logf("restore chdir: %v", err)
		}
	}()

	g := &graph.Graph{
		Version:     graph.Version,
		GeneratedAt: time.Now(),
		Symbols: []graph.SymbolNode{
			{Name: "Foo", Kind: "function", File: "foo.go"},
		},
		Baseline: &graph.GraphBaseline{
			OrphanCount:   10,
			CouplingEdges: 20,
		},
	}
	if err := os.MkdirAll(".gograph", 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeJSON(".gograph/graph.json", currentPolicyGraph(g)); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}

	yml := `
max_complexity: 50
max_instability: 1.0
allow_new_orphans: false
max_new_coupling_edges: 5
`
	if err := os.WriteFile(".gograph.yml", []byte(yml), 0644); err != nil {
		t.Fatalf("write yml: %v", err)
	}

	if code := runGate(nil); code != 0 {
		t.Fatalf("expected gate to pass, got exit code %d", code)
	}
}

func TestGateFailOneViolation(t *testing.T) {
	tmpDir := t.TempDir()
	origWd, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(origWd); err != nil {
			t.Logf("restore chdir: %v", err)
		}
	}()

	g := &graph.Graph{
		Version:     graph.Version,
		GeneratedAt: time.Now(),
		Imports:     make([]graph.ImportEdge, 30), // 30 current edges
		Baseline: &graph.GraphBaseline{
			CouplingEdges: 20, // max new is 5, we have 10 new, this fails
		},
	}
	if err := os.MkdirAll(".gograph", 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeJSON(".gograph/graph.json", currentPolicyGraph(g)); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}

	yml := `
max_new_coupling_edges: 5
`
	if err := os.WriteFile(".gograph.yml", []byte(yml), 0644); err != nil {
		t.Fatalf("write yml: %v", err)
	}

	if code := runGate(nil); code != 1 {
		t.Fatalf("expected gate to fail, got exit code %d", code)
	}
}

func TestGateFailMultipleViolations(t *testing.T) {
	tmpDir := t.TempDir()
	origWd, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(origWd); err != nil {
			t.Logf("restore chdir: %v", err)
		}
	}()

	g := &graph.Graph{
		Version:     graph.Version,
		GeneratedAt: time.Now(),
		Symbols: []graph.SymbolNode{
			{Name: "Foo", Kind: "method", Receiver: "Bar"},
			{Name: "Foo2", Kind: "method", Receiver: "Bar"},
			{Name: "Foo3", Kind: "method", Receiver: "Bar"},
		},
		Imports: make([]graph.ImportEdge, 30),
		Baseline: &graph.GraphBaseline{
			CouplingEdges: 20, // 10 new, limit 5 -> violation
		},
	}
	if err := os.MkdirAll(".gograph", 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeJSON(".gograph/graph.json", currentPolicyGraph(g)); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}

	yml := `
max_god_object_methods: 2
max_new_coupling_edges: 5
`
	if err := os.WriteFile(".gograph.yml", []byte(yml), 0644); err != nil {
		t.Fatalf("write yml: %v", err)
	}

	if code := runGate(nil); code != 1 {
		t.Fatalf("expected gate to fail, got exit code %d", code)
	}
}

func TestGateRefusesStaleGraph(t *testing.T) {
	tmpDir := t.TempDir()
	origWd, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	if err := os.MkdirAll(".gograph", 0o750); err != nil {
		t.Fatal(err)
	}
	g := &graph.Graph{Version: graph.Version, GeneratedAt: time.Now().Add(-time.Hour), Root: tmpDir}
	if err := writeJSON(".gograph/graph.json", currentPolicyGraph(g)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".gograph.yml", []byte("max_complexity: 100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("main.go", []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := runGate(nil); code != 1 {
		t.Fatalf("stale gate exit code = %d, want 1", code)
	}
}

func TestGateInitRejectsLinkedConfig(t *testing.T) {
	root := t.TempDir()
	origWd, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Mkdir(filepath.Join(root, ".gograph"), 0o750); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.yml")
	if err := os.Symlink(outside, filepath.Join(root, ".gograph.yml")); err != nil {
		t.Skipf("create gate config symlink: %v", err)
	}
	if code := runGateInit(); code != 1 {
		t.Fatalf("gate init linked config exit = %d, want 1", code)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("outside gate config was created: %v", err)
	}
}

func TestGateRejectsLinkedConfig(t *testing.T) {
	root := t.TempDir()
	origWd, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Mkdir(filepath.Join(root, ".gograph"), 0o750); err != nil {
		t.Fatal(err)
	}
	const sentinel = "BENIGN-LINKED-GATE-CONFIG-SENTINEL"
	outside := filepath.Join(t.TempDir(), "outside.yml")
	if err := os.WriteFile(outside, []byte("# "+sentinel+"\nmax_complexity: 100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".gograph.yml")); err != nil {
		t.Skipf("create gate config symlink: %v", err)
	}

	stdout, stderr, code := captureCLIParityOutput(t, func() int { return runGate(nil) })
	if code != 1 || strings.Contains(stdout+stderr, sentinel) || !strings.Contains(stderr, "unsafe repository source path") {
		t.Fatalf("linked gate config result code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
