package rootfind

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestFindRoot_NoGographDir_FallsBackToCwd(t *testing.T) {
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	got := FindRoot()
	if got != "." {
		t.Errorf("FindRoot() = %q, want %q (no .gograph anywhere)", got, ".")
	}
}

func TestFindRoot_FromRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
		t.Fatalf("mkdir .gograph: %v", err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	got, _ := filepath.EvalSymlinks(FindRoot())
	want, _ := filepath.EvalSymlinks(root)
	if got != want {
		t.Errorf("FindRoot() = %q, want %q", got, want)
	}
}

func TestFindRoot_FromSubdirectory(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "internal", "pkg")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
		t.Fatalf("mkdir .gograph: %v", err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	if err := os.Chdir(subdir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	got, _ := filepath.EvalSymlinks(FindRoot())
	want, _ := filepath.EvalSymlinks(root)
	if got != want {
		t.Errorf("FindRoot() from subdirectory = %q, want %q", got, want)
	}
}

func TestFindRoot_IgnoresLinkedGographDirectory(t *testing.T) {
	outer := t.TempDir()
	inner := filepath.Join(outer, "repository")
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outer, ".gograph"), 0o755); err != nil {
		t.Fatalf("mkdir outer .gograph: %v", err)
	}
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatalf("mkdir inner repository: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(inner, ".gograph")); err != nil {
		t.Skipf("create linked .gograph: %v", err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(inner); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	got, _ := filepath.EvalSymlinks(FindRoot())
	want, _ := filepath.EvalSymlinks(outer)
	if got != want {
		t.Errorf("FindRoot() with linked child .gograph = %q, want real ancestor %q", got, want)
	}
}

func TestFindRootFrom(t *testing.T) {
	outer := t.TempDir()
	indexed := filepath.Join(outer, "indexed")
	if err := os.MkdirAll(filepath.Join(indexed, ".gograph"), 0o755); err != nil {
		t.Fatalf("mkdir indexed root: %v", err)
	}

	tests := []struct {
		name  string
		start string
		want  string
		ok    bool
	}{
		{name: "root", start: indexed, want: indexed, ok: true},
		{name: "nested directory", start: filepath.Join(indexed, "internal", "pkg"), want: indexed, ok: true},
		{name: "file path", start: filepath.Join(indexed, "internal", "pkg", "file.go"), want: indexed, ok: true},
		{name: "glob path", start: filepath.Join(indexed, "internal", "*.go"), want: indexed, ok: true},
		{name: "unindexed", start: filepath.Join(outer, "other", "file.go"), ok: false},
		{name: "empty", start: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := FindRootFrom(tt.start)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("FindRootFrom(%q) = (%q, %v), want (%q, %v)", tt.start, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestFindRepositoryRootIgnoresWorkspaceOnlyArtifactDirectory(t *testing.T) {
	root := t.TempDir()
	workspaceDir := filepath.Join(root, ".gograph")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "workspace.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := FindRepositoryRootFrom(child); ok {
		t.Fatal("workspace-only .gograph directory established a repository root")
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "graph.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := FindRepositoryRootFrom(child); ok {
		t.Fatal("invalid graph.json established a repository root")
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "graph.json"), []byte(`{"version":"`+graph.Version+`"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := FindRepositoryRootFrom(child); !ok || got != root {
		t.Fatalf("FindRepositoryRootFrom = (%q, %v), want (%q, true)", got, ok, root)
	}
}

func TestFindRepositoryRootUsesAnalysisInputsFromFilePath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "nested", "main.go")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package nested\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := FindRepositoryRootFrom(file); !ok || got != root {
		t.Fatalf("FindRepositoryRootFrom(file) = (%q, %v), want (%q, true)", got, ok, root)
	}
}

func TestFindRepositoryRootPrefersEnclosingGraphOverNestedModule(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gograph", "graph.json"), []byte(`{"version":"`+graph.Version+`"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(filepath.Join(nested, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "go.mod"), []byte("module example.com/nested\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := FindRepositoryRootFrom(filepath.Join(nested, "pkg")); !ok || got != root {
		t.Fatalf("FindRepositoryRootFrom(nested module) = (%q, %v), want enclosing graph (%q, true)", got, ok, root)
	}
}

func TestFindRepositoryRootDoesNotCrossNestedGitBoundary(t *testing.T) {
	outer := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outer, ".gograph"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outer, ".gograph", "graph.json"), []byte(`{"version":"`+graph.Version+`"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(outer, "nested")
	if err := os.MkdirAll(filepath.Join(nested, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "go.mod"), []byte("module example.com/nested\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := FindRepositoryRootFrom(nested); !ok || got != nested {
		t.Fatalf("FindRepositoryRootFrom(nested repository) = (%q, %v), want (%q, true)", got, ok, nested)
	}
}
