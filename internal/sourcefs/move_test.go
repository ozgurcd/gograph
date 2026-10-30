package sourcefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMoveRegularFileStaysConfined(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()

	if err := reader.MoveRegularFile("a/log", "a/archive/log"); err != nil {
		t.Fatalf("move: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "a", "archive", "log")); err != nil || string(data) != "x" {
		t.Fatalf("moved file = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, "a", "log")); !os.IsNotExist(err) {
		t.Fatalf("source survived: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "a", "log"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := reader.MoveRegularFile("a/log", "a/archive/log"); err == nil {
		t.Fatal("existing destination replaced")
	}
	if err := reader.MoveRegularFile("a/log", "../escape"); err == nil {
		t.Fatal("destination escaped the root")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "a", "linked")); err != nil {
		t.Fatal(err)
	}
	if err := reader.MoveRegularFile("a/log", "a/linked/log"); err == nil {
		t.Fatal("destination followed a linked directory")
	}
	if err := os.Symlink(filepath.Join(outside, "f"), filepath.Join(root, "a", "link")); err != nil {
		t.Fatal(err)
	}
	if err := reader.MoveRegularFile("a/link", "a/archive/link"); err == nil {
		t.Fatal("linked source moved")
	}
}
