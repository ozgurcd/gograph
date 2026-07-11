package baseline_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/baseline"
	"github.com/ozgurcd/gograph/internal/graph"
)

func TestBuildExtractsProjectSubtreeAtGitRef(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "outside.go"), "package outside\n")
	writeFile(t, filepath.Join(project, "go.mod"), "module example.com/api\n")
	writeFile(t, filepath.Join(project, "main.go"), "package main\n\nfunc Baseline() {}\n")
	runGit(t, root, "init", "--quiet")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "--quiet", "-m", "baseline")
	writeFile(t, filepath.Join(project, "main.go"), "package main\n\nfunc Current() {}\n")

	var buildRoot string
	g, err := baseline.Build(context.Background(), project, "HEAD", func(path string) (*graph.Graph, error) {
		buildRoot = path
		data, readErr := os.ReadFile(filepath.Join(path, "main.go"))
		if readErr != nil {
			return nil, readErr
		}
		if !strings.Contains(string(data), "Baseline") {
			t.Fatalf("expected committed source, got %s", data)
		}
		if _, statErr := os.Stat(filepath.Join(path, "outside.go")); !os.IsNotExist(statErr) {
			t.Fatalf("baseline build escaped project subtree: %v", statErr)
		}
		return &graph.Graph{Root: path}, nil
	})
	if err != nil {
		t.Fatalf("build baseline: %v", err)
	}
	if g.Root != buildRoot {
		t.Fatalf("graph root = %q, want %q", g.Root, buildRoot)
	}
	if _, err := os.Stat(buildRoot); !os.IsNotExist(err) {
		t.Fatalf("temporary baseline directory was not removed: %v", err)
	}
}

func TestBuildRejectsUnsafeRef(t *testing.T) {
	_, err := baseline.Build(context.Background(), t.TempDir(), "main;echo bad", func(string) (*graph.Graph, error) {
		return &graph.Graph{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "unsafe baseline ref") {
		t.Fatalf("expected unsafe-ref error, got %v", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
