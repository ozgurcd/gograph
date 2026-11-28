package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/graphstore"
)

func writeStoreFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"go.mod":      "module example.com/store\n\ngo 1.22\n",
		"a/a.go":      "package a\n\ntype Runner interface{ Run() int }\n\ntype impl struct{}\n\nfunc (impl) Run() int { return 1 }\n\nfunc Call(r Runner) int { return r.Run() }\n\nfunc Entry() int { return Call(impl{}) }\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestEntry(t *testing.T) { _ = Entry() }\n",
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readGraphFile(t *testing.T, root string) *graph.Graph {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, graphFile))
	if err != nil {
		t.Fatal(err)
	}
	var g graph.Graph
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	return &g
}

func TestPreciseGraphIsSharedAcrossCheckoutsOfTheSameContent(t *testing.T) {
	storeDir := t.TempDir()
	t.Setenv(graphstore.DirEnv, storeDir)
	first, second := t.TempDir(), t.TempDir()
	writeStoreFixture(t, first)
	writeStoreFixture(t, second)

	out, stderr, code := runCLIParityInDir(t, first, func() int { return Run([]string{"build", ".", "--precise"}) })
	if code != 0 || !strings.Contains(out, "running type-checked precision analysis") {
		t.Fatalf("first precise build must analyze: code=%d out=%s err=%s", code, out, stderr)
	}
	entries, _ := filepath.Glob(filepath.Join(storeDir, "*.json"))
	if len(entries) != 1 {
		t.Fatalf("the precise graph must be stored once: %v", entries)
	}

	out, stderr, code = runCLIParityInDir(t, second, func() int { return Run([]string{"build", ".", "--precise"}) })
	if code != 0 || strings.Contains(out, "running type-checked precision analysis") || !strings.Contains(out, "reused the precise graph stored") {
		t.Fatalf("a second checkout of the same content must reuse it: code=%d out=%s err=%s", code, out, stderr)
	}
	a, b := readGraphFile(t, first), readGraphFile(t, second)
	if b.Root == a.Root || b.Build.EffectivePrecision() != graph.PrecisionPrecise {
		t.Fatalf("reused graph must be re-rooted and precise: root=%s precision=%s", b.Root, b.Build.EffectivePrecision())
	}
	if !reflect.DeepEqual(a.Calls, b.Calls) || !reflect.DeepEqual(a.TestEdges, b.TestEdges) || !reflect.DeepEqual(a.Symbols, b.Symbols) {
		t.Fatal("reused graph must hold the same call graph, symbols and test edges")
	}
	if out, _, code := runCLIParityInDir(t, second, func() int { return Run([]string{"callers", "impl.Run"}) }); code != 0 || !strings.Contains(out, "Call") {
		t.Fatalf("queries must answer from the reused graph: %s", out)
	}

	// Different content never reuses: an edit is analyzed afresh.
	if err := os.WriteFile(filepath.Join(second, "a", "b.go"), []byte("package a\n\nfunc Extra() int { return Entry() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, stderr, code = runCLIParityInDir(t, second, func() int { return Run([]string{"build", ".", "--precise"}) })
	if code != 0 || !strings.Contains(out, "running type-checked precision analysis") {
		t.Fatalf("changed content must be analyzed: code=%d out=%s err=%s", code, out, stderr)
	}
	// Different tags are a different build context.
	out, stderr, code = runCLIParityInDir(t, first, func() int { return Run([]string{"build", ".", "--precise", "--tags=integration"}) })
	if code != 0 || !strings.Contains(out, "running type-checked precision analysis") {
		t.Fatalf("other build tags must be analyzed: code=%d out=%s err=%s", code, out, stderr)
	}
}

func TestStoredGraphIsRejectedWhenItDoesNotMatchTheCheckout(t *testing.T) {
	storeDir := t.TempDir()
	t.Setenv(graphstore.DirEnv, storeDir)
	first, second := t.TempDir(), t.TempDir()
	writeStoreFixture(t, first)
	writeStoreFixture(t, second)
	if _, stderr, code := runCLIParityInDir(t, first, func() int { return Run([]string{"build", ".", "--precise"}) }); code != 0 {
		t.Fatal(stderr)
	}
	entries, _ := filepath.Glob(filepath.Join(storeDir, "*.json"))
	if len(entries) != 1 {
		t.Fatalf("entries: %v", entries)
	}
	// A corrupted entry is ignored and replaced, never trusted.
	if err := os.WriteFile(entries[0], []byte(`{"version":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, stderr, code := runCLIParityInDir(t, second, func() int { return Run([]string{"build", ".", "--precise"}) })
	if code != 0 || !strings.Contains(out, "running type-checked precision analysis") {
		t.Fatalf("a corrupt entry must not be used: code=%d out=%s err=%s", code, out, stderr)
	}
	if g := readGraphFile(t, second); g.Build.EffectivePrecision() != graph.PrecisionPrecise {
		t.Fatalf("precision: %s", g.Build.EffectivePrecision())
	}
}

func TestGraphStoreEvictsLeastRecentlyUsedEntriesAndStaysUnderItsCap(t *testing.T) {
	dir := t.TempDir()
	store := &graphstore.Store{Dir: dir, Max: 250}
	for _, key := range []string{"a", "b", "c"} {
		if err := store.Save(key, []byte(strings.Repeat("x", 100))); err != nil {
			t.Fatal(err)
		}
		if key == "a" {
			// Touch a so b is the least recently used when c arrives.
			if _, ok := store.Load("a"); !ok {
				t.Fatal("load a")
			}
		}
	}
	names := func() []string {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		var out []string
		for _, m := range matches {
			out = append(out, filepath.Base(m))
		}
		return out
	}
	got := names()
	if len(got) != 2 {
		t.Fatalf("cap 250 holds two 100-byte entries: %v", got)
	}
	if _, ok := store.Load("c"); !ok {
		t.Fatal("the entry just saved is never evicted")
	}
	if err := store.Save("huge", []byte(strings.Repeat("x", 300))); err == nil {
		t.Fatal("an entry larger than the cap must be refused")
	}
}
