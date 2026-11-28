package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/testmemory"
)

func writeTestMemoryFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"go.mod":              "module example.com/tm\n\ngo 1.22\n",
		"b/b.go":              "package b\n\nimport \"os\"\n\nfunc B() int { _ = os.Getenv(\"TM_MODE\"); return 1 }\n",
		"a/a.go":              "package a\n\nimport \"example.com/tm/b\"\n\nfunc A() int { return b.B() }\n",
		"a/a_test.go":         "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { _ = A() }\n\nfunc TestA2(t *testing.T) { _ = A() }\n",
		"a/testdata/in.txt":   "one\n",
		"c/c.go":              "package c\n\nfunc C() int { return 3 }\n",
		"c/c_test.go":         "package c\n\nimport \"testing\"\n\nfunc TestC(t *testing.T) { _ = C() }\n",
		"fixtures/shared.txt": "shared\n",
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
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) }); code != 0 {
		t.Fatalf("build: %s", stderr)
	}
}

const testMemoryEvents = `{"Action":"run","Package":"example.com/tm/a","Test":"TestA"}
{"Action":"pass","Package":"example.com/tm/a","Test":"TestA/sub"}
{"Action":"pass","Package":"example.com/tm/a","Test":"TestA"}
{"Action":"fail","Package":"example.com/tm/a","Test":"TestA2"}
{"Action":"pass","Package":"example.com/tm/c","Test":"TestC"}
{"Action":"pass","Package":"example.com/tm/a"}
`

func TestTestMemorySkipsOnlyTestsThatPassedOnIdenticalInputs(t *testing.T) {
	t.Setenv(testmemory.DirEnv, t.TempDir())
	t.Setenv("TM_MODE", "")
	first, second := t.TempDir(), t.TempDir()
	writeTestMemoryFixture(t, first)
	writeTestMemoryFixture(t, second)
	events := filepath.Join(t.TempDir(), "events.json")
	if err := os.WriteFile(events, []byte(testMemoryEvents), 0o644); err != nil {
		t.Fatal(err)
	}
	runIn := func(root string, args ...string) (string, string, int) {
		return runCLIParityInDir(t, root, func() int { return Run(args) })
	}
	selectA := func(root string, extra ...string) (string, string) {
		out, stderr, code := runIn(root, append([]string{"tests", "b.B", "--format", "go-test", "--skip-passed"}, extra...)...)
		if code != 0 {
			t.Fatalf("selection failed: %s", stderr)
		}
		return out, stderr
	}

	if out, _ := selectA(first); !strings.Contains(out, "go test example.com/tm/a") {
		t.Fatalf("nothing recorded yet, every test runs: %s", out)
	}
	out, stderr, code := runIn(first, "tests", "--record", events)
	if code != 0 || !strings.Contains(out, "recorded 2 passed and forgot 1 failed test(s) in 2 package(s)") {
		t.Fatalf("record: code=%d out=%s err=%s", code, out, stderr)
	}
	out, stderr = selectA(first)
	if !strings.Contains(out, "-run '^(TestA2)$'") || strings.Contains(out, "TestA|") || !strings.Contains(stderr, "example.com/tm/a.TestA") {
		t.Fatalf("TestA passed and is skipped; failed TestA2 still runs: out=%s err=%s", out, stderr)
	}
	// Another checkout at another path with identical content reuses it.
	if out, _ = selectA(second); !strings.Contains(out, "-run '^(TestA2)$'") {
		t.Fatalf("identical inputs at another path: %s", out)
	}
	// JSON reports the skipped tests.
	if out, _, _ = runIn(first, "tests", "b.B", "--format", "go-test", "--skip-passed", "--json"); !strings.Contains(out, `"previously_passed": [`) || !strings.Contains(out, `"name": "TestA",`) {
		t.Fatalf("JSON previously_passed: %s", out)
	}
	// Different go test flags are different inputs.
	if out, _ = selectA(first, "--go-flags", "-race"); strings.Contains(out, "-run '^(TestA2)$'") {
		t.Fatalf("other go test flags must not reuse the pass: %s", out)
	}
	// An env var the closure reads by name is an input.
	t.Setenv("TM_MODE", "strict")
	if out, _ = selectA(first); strings.Contains(out, "-run '^(TestA2)$'") {
		t.Fatalf("a changed TM_MODE must not reuse the pass: %s", out)
	}
	t.Setenv("TM_MODE", "")
	// A changed imported package, testdata file, or named extra input changes the key.
	for _, change := range []struct{ file, body string }{
		{"a/testdata/in.txt", "two\n"},
		{"b/b.go", "package b\n\nimport \"os\"\n\nfunc B() int { _ = os.Getenv(\"TM_MODE\"); return 1 } // edited\n"},
	} {
		if err := os.WriteFile(filepath.Join(second, filepath.FromSlash(change.file)), []byte(change.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, _ = selectA(second); strings.Contains(out, "-run '^(TestA2)$'") {
			t.Fatalf("after editing %s the pass must not be reused: %s", change.file, out)
		}
	}
	if _, _, code = runIn(first, "tests", "--record", events, "--input", "fixtures"); code != 0 {
		t.Fatal("record with an extra input")
	}
	if out, _ = selectA(first, "--input", "fixtures"); !strings.Contains(out, "-run '^(TestA2)$'") {
		t.Fatalf("recorded with the same extra input: %s", out)
	}
	if err := os.WriteFile(filepath.Join(first, "fixtures", "shared.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _ = selectA(first, "--input", "fixtures"); strings.Contains(out, "-run '^(TestA2)$'") {
		t.Fatalf("a changed extra input must not reuse the pass: %s", out)
	}
	// An unrelated package keeps its pass.
	if out, stderr, _ = runIn(first, "tests", "C", "--format", "go-test", "--skip-passed"); strings.Contains(out, "example.com/tm/c") || !strings.Contains(stderr, "example.com/tm/c.TestC") {
		t.Fatalf("c is unaffected: out=%s err=%s", out, stderr)
	}
	// A later failure forgets the pass.
	failed := filepath.Join(t.TempDir(), "failed.json")
	if err := os.WriteFile(failed, []byte(`{"Action":"fail","Package":"example.com/tm/c","Test":"TestC"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, code = runIn(first, "tests", "--record", failed); code != 0 {
		t.Fatal("record failure")
	}
	if out, _, _ = runIn(first, "tests", "C", "--format", "go-test", "--skip-passed"); !strings.Contains(out, "example.com/tm/c") {
		t.Fatalf("a failed test runs again: %s", out)
	}
}

func TestTestMemoryRefusesUnboundedLayoutsAndStaleGraphs(t *testing.T) {
	t.Setenv(testmemory.DirEnv, t.TempDir())
	root := t.TempDir()
	writeTestMemoryFixture(t, root)
	events := filepath.Join(t.TempDir(), "events.json")
	if err := os.WriteFile(events, []byte(testMemoryEvents), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "c", "c.go"), []byte("package c\n\nfunc C() int { return 4 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"--no-refresh", "tests", "--record", events}) }); code == 0 || !strings.Contains(stderr, "stale") {
		t.Fatalf("a stale graph must refuse recording: code=%d err=%s", code, stderr)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/tm\n\ngo 1.22\n\nreplace example.com/x => ../x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"tests", "--record", events}) }); code == 0 || !strings.Contains(stderr, "local directory") {
		t.Fatalf("a local replace must refuse recording: code=%d err=%s", code, stderr)
	}
	for _, args := range [][]string{
		{"tests", "b.B", "--skip-passed"},
		{"tests", "b.B", "--format", "go-test", "--go-flags", "-race"},
		{"tests", "--record", events, "b.B"},
	} {
		if _, _, code := runCLIParityInDir(t, root, func() int { return Run(args) }); code == 0 {
			t.Errorf("%v must be refused", args)
		}
	}
}

func TestTestMemoryRefusesInputsTheKeyCannotSee(t *testing.T) {
	t.Setenv(testmemory.DirEnv, t.TempDir())
	root := t.TempDir()
	writeTestMemoryFixture(t, root)
	events := filepath.Join(t.TempDir(), "events.json")
	if err := os.WriteFile(events, []byte(testMemoryEvents), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, string, int) {
		return runCLIParityInDir(t, root, func() int { return Run(args) })
	}
	// An environment read without a literal name in the closure of a.
	if err := os.WriteFile(filepath.Join(root, "b", "env.go"), []byte("package b\n\nimport \"os\"\n\nfunc All() []string { return os.Environ() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, stderr, code := run("tests", "--record", events)
	if code != 0 || !strings.Contains(stderr, "not recorded: example.com/tm/a") || !strings.Contains(stderr, "os.Environ") {
		t.Fatalf("a's closure reads the whole environment, so a is not recorded: code=%d out=%s err=%s", code, out, stderr)
	}
	if !strings.Contains(out, "in 1 package(s)") {
		t.Fatalf("c is still recorded: %s", out)
	}
	if out, _, code = run("tests", "--record", events, "--trust-env"); code != 0 || !strings.Contains(out, "in 2 package(s)") {
		t.Fatalf("--trust-env accepts unnamed reads: code=%d out=%s", code, out)
	}
	if out, _, _ = run("tests", "b.B", "--format", "go-test", "--skip-passed"); strings.Contains(out, "-run '^(TestA2)$'") {
		t.Fatalf("a pass recorded with --trust-env is not used without it: %s", out)
	}
	if out, _, _ = run("tests", "b.B", "--format", "go-test", "--skip-passed", "--trust-env"); !strings.Contains(out, "-run '^(TestA2)$'") {
		t.Fatalf("a pass recorded with --trust-env is used with it: %s", out)
	}

	// An in-module import of a package the graph does not hold.
	if err := os.Remove(filepath.Join(root, "b", "env.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "gen"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gen", "gen.go"), []byte("package gen\n\nfunc G() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "c", "c.go"), []byte("package c\n\nimport \"example.com/tm/gen\"\n\nfunc C() int { return gen.G() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := run("build", ".", "--exclude-dirs=gen"); code != 0 {
		t.Fatal(stderr)
	}
	if _, stderr, _ = run("tests", "--record", events); !strings.Contains(stderr, "not recorded: example.com/tm/c") || !strings.Contains(stderr, "not in the graph") {
		t.Fatalf("an import of an excluded in-module package refuses memory: %s", stderr)
	}
}
