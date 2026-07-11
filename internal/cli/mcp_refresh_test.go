package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestGraphRefresherPreservesPreciseGraphUntilSourceChanges(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	if err := os.WriteFile(source, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initial := &graph.Graph{
		Root:        root,
		GeneratedAt: time.Now().Add(time.Second),
		Implements:  []graph.ImplementsEdge{{Interface: "Runner", Concrete: "Service"}},
	}
	builds := 0
	refresh := graphRefresher(initial, root, func(string) (*graph.Graph, error) {
		builds++
		return &graph.Graph{Root: root, GeneratedAt: time.Now().Add(time.Hour)}, nil
	})

	got, err := refresh()
	if err != nil {
		t.Fatal(err)
	}
	if got != initial || len(got.Implements) != 1 || builds != 0 {
		t.Fatalf("unchanged source discarded initial precision: graph=%p builds=%d implements=%v", got, builds, got.Implements)
	}

	newer := initial.GeneratedAt.Add(time.Second)
	if err := os.Chtimes(source, newer, newer); err != nil {
		t.Fatal(err)
	}
	got, err = refresh()
	if err != nil {
		t.Fatal(err)
	}
	if got == initial || builds != 1 {
		t.Fatalf("changed source did not rebuild once: graph=%p builds=%d", got, builds)
	}
	if _, err := refresh(); err != nil {
		t.Fatal(err)
	}
	if builds != 1 {
		t.Fatalf("unchanged refreshed graph rebuilt again: builds=%d", builds)
	}
}
