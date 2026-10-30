package sourcefs

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Concurrent moves to one destination: exactly one succeeds and no file is
// overwritten, even when every mover passed validation before any committed.
func TestMoveRegularFileConcurrentMovesNeverReplace(t *testing.T) {
	const movers = 8
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range movers {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("src%d", i)), []byte(fmt.Sprint(i)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()

	var arrived sync.WaitGroup
	arrived.Add(movers)
	release := make(chan struct{})
	moveBeforeCommit = func() { arrived.Done(); <-release }
	t.Cleanup(func() { moveBeforeCommit = nil })

	errs := make([]error, movers)
	var done sync.WaitGroup
	for i := range movers {
		done.Add(1)
		go func() {
			defer done.Done()
			errs[i] = reader.MoveRegularFile(fmt.Sprintf("src%d", i), "archive/dst")
		}()
	}
	arrived.Wait()
	close(release)
	done.Wait()

	successes, kept := 0, 0
	for i, err := range errs {
		if err == nil {
			successes++
			continue
		}
		if _, statErr := os.Stat(filepath.Join(root, fmt.Sprintf("src%d", i))); statErr == nil {
			kept++
		}
	}
	if successes != 1 || kept != movers-1 {
		t.Fatalf("successes = %d, sources kept = %d; want 1 and %d", successes, kept, movers-1)
	}
}

// A destination directory swapped for an in-root link after validation must
// not redirect the move away from its validated directory.
func TestMoveRegularFileIgnoresSwappedDestinationDirectory(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "elsewhere")
	archive := filepath.Join(root, "archive")
	for _, dir := range []string{archive, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()

	moveBeforeCommit = func() {
		if err := os.Rename(archive, archive+"-real"); err != nil {
			t.Error(err)
		}
		if err := os.Symlink("elsewhere", archive); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { moveBeforeCommit = nil })
	moveErr := reader.MoveRegularFile("log", "archive/log")

	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("move followed the swapped link: %v landed outside (err=%v)", entries[0].Name(), moveErr)
	}
	_, inReal := os.Stat(filepath.Join(archive+"-real", "log"))
	_, inSource := os.Stat(filepath.Join(root, "log"))
	if inReal != nil && inSource != nil {
		t.Fatalf("file lost: neither moved into the validated directory nor kept (err=%v)", moveErr)
	}
}
