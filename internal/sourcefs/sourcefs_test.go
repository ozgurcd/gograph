package sourcefs

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReaderConfinesRepositorySource(t *testing.T) {
	base := t.TempDir()
	repository := filepath.Join(base, "repository")
	outside := filepath.Join(base, "outside.go")
	mustWrite(t, filepath.Join(repository, "nested", "inside.go"), "package nested\n")
	mustWrite(t, outside, "package outside\nconst Sentinel = \"OUTSIDE\"\n")
	if err := os.Symlink(outside, filepath.Join(repository, "linked.go")); err != nil {
		t.Skipf("create source symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join("nested", "inside.go"), filepath.Join(repository, "inside-link.go")); err != nil {
		t.Skipf("create in-repository source symlink: %v", err)
	}
	if err := os.Symlink(base, filepath.Join(repository, "linked-dir")); err != nil {
		t.Skipf("create directory symlink: %v", err)
	}

	reader, err := Open(repository)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()

	data, err := reader.ReadFile(filepath.Join("nested", "inside.go"))
	if err != nil || string(data) != "package nested\n" {
		t.Fatalf("read regular source = %q, %v", data, err)
	}

	for _, name := range []string{"", ".", "../outside.go", outside, "linked.go", "inside-link.go", filepath.Join("linked-dir", "outside.go"), "not-go.txt"} {
		t.Run(name, func(t *testing.T) {
			data, err := reader.ReadFile(name)
			if err == nil {
				t.Fatalf("ReadFile(%q) returned %q", name, data)
			}
			if name == "linked.go" || name == "inside-link.go" || name == filepath.Join("linked-dir", "outside.go") {
				if !errors.Is(err, ErrUnsafeSourcePath) {
					t.Fatalf("ReadFile(%q) error = %v, want ErrUnsafeSourcePath", name, err)
				}
			}
		})
	}
}

func TestReaderFollowsExplicitRepositoryRootSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires elevated Windows privileges")
	}
	realRoot := filepath.Join(t.TempDir(), "real")
	mustWrite(t, filepath.Join(realRoot, "main.go"), "package main\n")
	linkRoot := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Skipf("create root symlink: %v", err)
	}
	reader, err := Open(linkRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	if _, err := reader.ReadFile("main.go"); err != nil {
		t.Fatalf("read through explicit root symlink: %v", err)
	}
}

func TestReaderPreservesNotExist(t *testing.T) {
	reader, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	_, err = reader.ReadFile("missing.go")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadFile missing error = %v, want os.ErrNotExist", err)
	}
}

func TestReaderReadsRegularRepositoryArtifact(t *testing.T) {
	repository := t.TempDir()
	mustWrite(t, filepath.Join(repository, ".gograph", "graph.json"), "{\"version\":1}\n")
	reader, err := Open(repository)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()

	data, err := reader.ReadRegularFile(filepath.Join(".gograph", "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{\"version\":1}\n" {
		t.Fatalf("artifact data = %q", data)
	}
}

func TestReaderValidatesRegularRepositoryMetadata(t *testing.T) {
	base := t.TempDir()
	repository := filepath.Join(base, "repository")
	mustWrite(t, filepath.Join(repository, "go.sum"), "regular metadata\n")
	if err := os.Mkdir(filepath.Join(repository, "member"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(base, "outside.sum"), "outside metadata\n")
	outsideDirectory := filepath.Join(base, "outside-member")
	if err := os.Mkdir(outsideDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "outside.sum"), filepath.Join(repository, "go.work.sum")); err != nil {
		t.Skipf("create metadata symlink: %v", err)
	}
	if err := os.Symlink(outsideDirectory, filepath.Join(repository, "linked-member")); err != nil {
		t.Skipf("create member symlink: %v", err)
	}

	reader, err := Open(repository)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	if err := reader.ValidateRegularFile("go.sum"); err != nil {
		t.Fatalf("validate regular metadata: %v", err)
	}
	if err := reader.ValidateRegularFile("missing.sum"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("validate missing metadata error = %v, want os.ErrNotExist", err)
	}
	if err := reader.ValidateRegularFile("go.work.sum"); !errors.Is(err, ErrUnsafeSourcePath) {
		t.Fatalf("validate linked metadata error = %v, want ErrUnsafeSourcePath", err)
	}
	for _, name := range []string{".", "member"} {
		if err := reader.ValidateDirectory(name); err != nil {
			t.Fatalf("validate regular directory %q: %v", name, err)
		}
	}
	if err := reader.ValidateDirectory("linked-member"); !errors.Is(err, ErrUnsafeSourcePath) {
		t.Fatalf("validate linked directory error = %v, want ErrUnsafeSourcePath", err)
	}
}

func TestReaderRejectsLinkedRepositoryArtifact(t *testing.T) {
	base := t.TempDir()
	repository := filepath.Join(base, "repository")
	mustWrite(t, filepath.Join(repository, "placeholder"), "inside")
	mustWrite(t, filepath.Join(base, "outside.json"), "outside")
	linkedDirectoryTarget := filepath.Join(base, "linked-artifacts")
	mustWrite(t, filepath.Join(linkedDirectoryTarget, "graph.json"), "outside-directory")

	if err := os.Mkdir(filepath.Join(repository, ".gograph"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "outside.json"), filepath.Join(repository, ".gograph", "graph.json")); err != nil {
		t.Skipf("create graph symlink: %v", err)
	}
	reader, err := Open(repository)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadRegularFile(filepath.Join(".gograph", "graph.json")); !errors.Is(err, ErrUnsafeSourcePath) {
		_ = reader.Close()
		t.Fatalf("linked graph error = %v, want ErrUnsafeSourcePath", err)
	}
	_ = reader.Close()

	if err := os.RemoveAll(filepath.Join(repository, ".gograph")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(linkedDirectoryTarget, filepath.Join(repository, ".gograph")); err != nil {
		t.Skipf("create artifact directory symlink: %v", err)
	}
	reader, err = Open(repository)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	if _, err := reader.ReadRegularFile(filepath.Join(".gograph", "graph.json")); !errors.Is(err, ErrUnsafeSourcePath) {
		t.Fatalf("linked artifact directory error = %v, want ErrUnsafeSourcePath", err)
	}
}

func TestReaderConfinesRepositoryMutations(t *testing.T) {
	base := t.TempDir()
	repository := filepath.Join(base, "repository")
	if err := os.Mkdir(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(repository)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()

	if err := reader.EnsureRealDirectory(filepath.Join(".gograph", "sessions"), 0o750); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(".gograph", "sessions", "session_safe.jsonl")
	if err := reader.WriteRegularFile(name, []byte("start\n"), 0o640, true); err != nil {
		t.Fatal(err)
	}
	if err := reader.WriteRegularFile(name, []byte("replace"), 0o640, true); !errors.Is(err, os.ErrExist) {
		t.Fatalf("exclusive rewrite error = %v, want os.ErrExist", err)
	}
	if err := reader.AppendRegularFile(name, []byte("end\n")); err != nil {
		t.Fatal(err)
	}
	data, err := reader.ReadRegularFile(name)
	if err != nil || string(data) != "start\nend\n" {
		t.Fatalf("mutated data = %q, %v", data, err)
	}
	entries, err := reader.ReadDirectory(filepath.Join(".gograph", "sessions"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "session_safe.jsonl" {
		t.Fatalf("directory entries = %+v, %v", entries, err)
	}
	if err := reader.RemoveRegularFile(name); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadRegularFile(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed file error = %v, want os.ErrNotExist", err)
	}
}

func TestReaderMutationRejectsRepositoryLinks(t *testing.T) {
	base := t.TempDir()
	repository := filepath.Join(base, "repository")
	outsideDir := filepath.Join(base, "outside")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outsideDir, "sentinel")
	if err := os.WriteFile(outsideFile, []byte("KEEP"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(repository, "linked-dir")); err != nil {
		t.Skipf("create directory symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join(outsideDir, "created"), filepath.Join(repository, "linked-file")); err != nil {
		t.Skipf("create dangling file symlink: %v", err)
	}

	reader, err := Open(repository)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{name: "directory", run: func() error { return reader.EnsureRealDirectory(filepath.Join("linked-dir", "nested"), 0o750) }},
		{name: "ancestor write", run: func() error {
			return reader.WriteRegularFile(filepath.Join("linked-dir", "sentinel"), []byte("CHANGED"), 0o640, false)
		}},
		{name: "dangling write", run: func() error { return reader.WriteRegularFile("linked-file", []byte("CREATED"), 0o640, false) }},
		{name: "ancestor read dir", run: func() error { _, err := reader.ReadDirectory("linked-dir"); return err }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(); !errors.Is(err, ErrUnsafeSourcePath) {
				t.Fatalf("operation error = %v, want ErrUnsafeSourcePath", err)
			}
		})
	}
	data, err := os.ReadFile(outsideFile)
	if err != nil || string(data) != "KEEP" {
		t.Fatalf("outside sentinel = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "created")); !os.IsNotExist(err) {
		t.Fatalf("dangling link target was created: %v", err)
	}
}

func TestAtomicReplaceRegularFileIsConfinedAndReplacesBytes(t *testing.T) {
	base := t.TempDir()
	repository := filepath.Join(base, "repository")
	mustWrite(t, filepath.Join(repository, ".gograph", "workspace.json"), "old")
	reader, err := Open(repository)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	if err := reader.AtomicReplaceRegularFile(".gograph/workspace.json", []byte("new\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	data, err := reader.ReadRegularFile(".gograph/workspace.json")
	if err != nil || string(data) != "new\n" {
		t.Fatalf("atomic data = %q, %v", data, err)
	}
	outside := filepath.Join(base, "outside")
	mustWrite(t, outside, "KEEP")
	if err := os.Remove(filepath.Join(repository, ".gograph", "workspace.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repository, ".gograph", "workspace.json")); err != nil {
		t.Skipf("create artifact symlink: %v", err)
	}
	if err := reader.AtomicReplaceRegularFile(".gograph/workspace.json", []byte("CHANGED"), 0o640); !errors.Is(err, ErrUnsafeSourcePath) {
		t.Fatalf("atomic linked destination error = %v, want ErrUnsafeSourcePath", err)
	}
	outsideData, err := os.ReadFile(outside)
	if err != nil || string(outsideData) != "KEEP" {
		t.Fatalf("outside data = %q, %v", outsideData, err)
	}
}

func mustWrite(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
