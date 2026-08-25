package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestBuildGraphReusesOnlyUnchangedPackagesByContentDigest(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/incremental\n\ngo 1.23\n")
	writeTestFile(t, filepath.Join(root, "alpha", "a.go"), "package alpha\nfunc A() int { return 1 }\n")
	writeTestFile(t, filepath.Join(root, "alpha", "b.go"), "package alpha\nfunc B() int { return A() }\n")
	writeTestFile(t, filepath.Join(root, "beta", "c.go"), "package beta\nfunc C() int { return 3 }\n")

	config, configErr := resolveBuildConfig(root)
	first, err := buildGraphWithConfig(root, config, configErr)
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	if first.Build.ReusedFiles != 0 || first.Build.RebuiltPackages != 2 {
		t.Fatalf("first build reuse metadata = reused %d, rebuilt %d; want 0, 2", first.Build.ReusedFiles, first.Build.RebuiltPackages)
	}

	changed := filepath.Join(root, "alpha", "a.go")
	info, err := os.Stat(changed)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, changed, "package alpha\nfunc A() int { return 10 }\n")
	if err := os.Chtimes(changed, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}

	second, err := buildGraphWithConfig(root, config, configErr, first)
	if err != nil {
		t.Fatalf("incremental build: %v", err)
	}
	if second.Build.ReusedFiles != 1 {
		t.Fatalf("reused files = %d, want only beta/c.go", second.Build.ReusedFiles)
	}
	if second.Build.RebuiltPackages != 1 {
		t.Fatalf("rebuilt packages = %d, want alpha only", second.Build.RebuiltPackages)
	}
	if second.Build.ParsedFiles != 3 || len(second.Files) != 3 {
		t.Fatalf("incremental graph coverage = parsed %d files %d, want 3/3", second.Build.ParsedFiles, len(second.Files))
	}
	for _, file := range second.Files {
		if file.ContentDigest == "" {
			t.Fatalf("file %s has no content digest", file.Path)
		}
	}
	if !second.Build.Complete {
		t.Fatalf("incremental build is unexpectedly partial: %#v", second.Build)
	}
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildGraphUnchangedTreeReusesEveryFile(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/reuse\n\ngo 1.23\n")
	writeTestFile(t, filepath.Join(root, "main.go"), "package reuse\nfunc Stable() {}\n")
	config, configErr := resolveBuildConfig(root)
	first, err := buildGraphWithConfig(root, config, configErr)
	if err != nil {
		t.Fatal(err)
	}
	// Ensure correctness is independent of GeneratedAt and filesystem clock
	// granularity; byte identity alone allows reuse.
	first.GeneratedAt = time.Now().Add(-24 * time.Hour)
	second, err := buildGraphWithConfig(root, config, configErr, first)
	if err != nil {
		t.Fatal(err)
	}
	if second.Build.ReusedFiles != 1 || second.Build.RebuiltPackages != 0 {
		t.Fatalf("reuse metadata = reused %d, rebuilt %d; want 1, 0", second.Build.ReusedFiles, second.Build.RebuiltPackages)
	}
}

func TestIncrementalASTReuseRecomputesPreciseEnrichment(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/precisereuse\n\ngo 1.23\n")
	writeTestFile(t, filepath.Join(root, "contract", "runner.go"), "package contract\ntype Runner interface { Run() }\nfunc Use(r Runner) { r.Run() }\n")
	writeTestFile(t, filepath.Join(root, "service", "service.go"), "package service\ntype Service struct{}\nfunc (Service) Run() {}\n")

	config, configErr := resolveBuildConfig(root)
	first, err := buildGraphWithConfig(root, config, configErr)
	if err != nil {
		t.Fatal(err)
	}
	if err := enrichGraphPreciselyWithConfig(root, first, config, configErr); err != nil {
		t.Fatal(err)
	}
	if len(first.Implements) == 0 {
		t.Fatalf("first precise graph has no implementation evidence: %#v", first)
	}

	second, err := buildGraphWithConfig(root, config, configErr, first)
	if err != nil {
		t.Fatal(err)
	}
	if second.Build.ReusedFiles != 2 || len(second.Implements) != 0 {
		t.Fatalf("reconstructed AST base = reused %d implements %d; want 2/0", second.Build.ReusedFiles, len(second.Implements))
	}
	if err := enrichGraphPreciselyWithConfig(root, second, config, configErr); err != nil {
		t.Fatal(err)
	}
	sortGraph(first)
	sortGraph(second)
	firstCalls, _ := json.Marshal(first.Calls)
	secondCalls, _ := json.Marshal(second.Calls)
	if string(firstCalls) != string(secondCalls) {
		t.Fatalf("precise calls changed after AST reuse:\nfirst:  %s\nsecond: %s", firstCalls, secondCalls)
	}
	firstImplements, _ := json.Marshal(first.Implements)
	secondImplements, _ := json.Marshal(second.Implements)
	if string(firstImplements) != string(secondImplements) {
		t.Fatalf("precise implementations changed after AST reuse:\nfirst:  %s\nsecond: %s", firstImplements, secondImplements)
	}
}

func TestIncrementalPreciseTestDispatchDoesNotMultiplyEdges(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/testreuse\n\ngo 1.23\n")
	writeTestFile(t, filepath.Join(root, "runner.go"), `package testreuse

type Runner interface { Run() }
type A struct{}
func (*A) Run() {}
type B struct{}
func (*B) Run() {}
type C struct{}
func (*C) Run() {}
`)
	writeTestFile(t, filepath.Join(root, "runner_test.go"), `package testreuse

import "testing"

func exercise(r Runner) { r.Run() }
func selected(t *testing.T) Runner {
	if t.Name() == "A" { return &A{} }
	return &B{}
}
func TestDispatch(t *testing.T) {
	r := selected(t)
	r.Run()
	exercise(r)
}
`)

	config, configErr := resolveBuildConfig(root)
	var previous *graph.Graph
	var expected []byte
	for build := 1; build <= 4; build++ {
		current, err := buildGraphWithConfig(root, config, configErr, previous)
		if err != nil {
			t.Fatalf("build %d AST: %v", build, err)
		}
		if build > 1 && len(current.TestEdges) != 3 {
			t.Fatalf("build %d restored %d parser test edges, want 3", build, len(current.TestEdges))
		}
		if err := enrichGraphPreciselyWithConfig(root, current, config, configErr); err != nil {
			t.Fatalf("build %d precise: %v", build, err)
		}
		sortGraph(current)
		encoded, err := json.Marshal(current.TestEdges)
		if err != nil {
			t.Fatal(err)
		}
		if build == 1 {
			expected = encoded
		} else if string(encoded) != string(expected) {
			t.Fatalf("build %d test edges changed after reuse:\nfirst: %s\nnow:   %s", build, expected, encoded)
		}
		if len(current.TestEdges) != 5 {
			t.Fatalf("build %d precise test edges = %d, want stable 5", build, len(current.TestEdges))
		}
		previous = current
	}
}
