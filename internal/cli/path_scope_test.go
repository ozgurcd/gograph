package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQueryAndStringsPathScopesToFileOrSubtree(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":              "module example.com/scope\n\ngo 1.22\n",
		"main.go":             "package main\n\nfunc runMain() string { return \"scope-literal main\" }\n\nfunc main() { _ = runMain() }\n",
		"internal/app/app.go": "package app\n\nfunc RunApp() string { return \"scope-literal app\" }\n",
		"internal/app/aux.go": "package app\n\nfunc RunAux() string { return \"scope-literal aux\" }\n",
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) }); code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}

	check := func(args []string, want, absent []string) {
		t.Helper()
		out, stderr, code := runCLIParityInDir(t, root, func() int { return Run(args) })
		if code != 0 {
			t.Fatalf("%v failed: code=%d out=%s err=%s", args, code, out, stderr)
		}
		for _, w := range want {
			if !strings.Contains(out, w) {
				t.Errorf("%v: missing %q in %s", args, w, out)
			}
		}
		for _, a := range absent {
			if strings.Contains(out, a) {
				t.Errorf("%v: %q is outside the --path scope: %s", args, a, out)
			}
		}
	}
	check([]string{"query", "run", "--path", "internal/app/app.go"}, []string{"RunApp"}, []string{"RunAux", "runMain"})
	check([]string{"query", "run", "--path=internal/app"}, []string{"RunApp", "RunAux"}, []string{"runMain"})
	check([]string{"query", "run", "--path", "."}, []string{"RunApp", "RunAux", "runMain"}, nil)
	check([]string{"strings", "scope-literal", "--path", "internal/app/aux.go"}, []string{"scope-literal aux"}, []string{"scope-literal app", "scope-literal main"})
	check([]string{"query", "run", "--path", filepath.Join(root, "internal", "app")}, []string{"RunApp", "RunAux"}, []string{"runMain"})
}

func TestStringsAndQueryAcceptDashTermsAfterSeparator(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":  "module example.com/dash\n\ngo 1.22\n",
		"main.go": "package main\n\nfunc flags() []string { return []string{\"--log-file\", \"--json\"} }\n\nfunc main() { _ = flags() }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) }); code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}
	for _, args := range [][]string{
		{"strings", "--", "--log-file"},
		{"strings", "--no-tests", "--", "--log-file"},
	} {
		out, stderr, code := runCLIParityInDir(t, root, func() int { return Run(args) })
		if code != 0 || !strings.Contains(out, `"--log-file"`) {
			t.Errorf("%v: code=%d out=%s err=%s", args, code, out, stderr)
		}
	}
	// --json after "--" is a search term, not the global output flag.
	out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"strings", "--", "--json"}) })
	if code != 0 || !strings.Contains(out, `[string] "--json"`) || strings.Contains(out, `"schema_version"`) {
		t.Errorf("strings -- --json: code=%d out=%s err=%s", code, out, stderr)
	}
	if _, _, code := runCLIParityInDir(t, root, func() int { return Run([]string{"strings", "--log-file"}) }); code == 0 {
		t.Error("an unseparated dash term must still be refused as an unknown flag")
	}
}

func TestSourceAcceptsSeveralSelectors(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":  "module example.com/multi\n\ngo 1.22\n",
		"main.go": "package main\n\nfunc slotHostDefault() string { return \"default\" }\n\nfunc slotHostOverride() string { return \"override\" }\n\nfunc main() { _, _ = slotHostDefault(), slotHostOverride() }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) }); code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}
	out, stderr, code := runCLIParityInDir(t, root, func() int {
		return Run([]string{"source", "slotHostDefault", "slotHostOverride"})
	})
	if code != 0 || !strings.Contains(out, `return "default"`) || !strings.Contains(out, `return "override"`) {
		t.Fatalf("source with two selectors: code=%d out=%s err=%s", code, out, stderr)
	}
	out, stderr, code = runCLIParityInDir(t, root, func() int {
		return Run([]string{"source", "slotHostDefault", "missingSelector"})
	})
	if code == 0 || !strings.Contains(out, `return "default"`) || !strings.Contains(stderr, "missingSelector") {
		t.Fatalf("a failed selector must fail the command after printing the others: code=%d out=%s err=%s", code, out, stderr)
	}
	out, stderr, code = runCLIParityInDir(t, root, func() int {
		return Run([]string{"source", "slotHostDefault", "slotHostOverride", "--json"})
	})
	if code != 0 || !strings.Contains(out, `"selector": "slotHostOverride"`) || !strings.Contains(out, `"count": 2`) {
		t.Fatalf("source --json with two selectors: code=%d out=%s err=%s", code, out, stderr)
	}
}

func TestTestsSelectionAcceptsATestFunctionName(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/named\n\ngo 1.22\n",
		"run.go":      "package named\n\nfunc Run() int { return 1 }\n",
		"run_test.go": "package named\n\nimport \"testing\"\n\nfunc TestRun(t *testing.T) { _ = Run() }\n\nfunc TestOther(t *testing.T) {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) }); code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}
	out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"tests", "TestRun", "--format", "go-test"}) })
	if code != 0 || strings.TrimSpace(out) != "go test example.com/named -run '^(TestRun)$'" {
		t.Fatalf("tests <TestName> --format go-test: code=%d out=%q err=%s", code, out, stderr)
	}
	if _, _, code := runCLIParityInDir(t, root, func() int { return Run([]string{"tests", "TestMissing", "--format", "go-test"}) }); code == 0 {
		t.Fatal("an unknown name must still fail")
	}
}

func TestSourceLinesReadsDeclarationAndFileRegions(t *testing.T) {
	root := t.TempDir()
	content := "package lines\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\nfunc Long() string {\n\ta := \"one\"\n\tb := \"two\"\n\tc := \"three\"\n\treturn fmt.Sprint(strings.ToUpper(a), b, c)\n}\n"
	files := map[string]string{
		"go.mod":   "module example.com/lines\n\ngo 1.22\n",
		"lines.go": content,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) }); code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}
	run := func(args ...string) (string, string, int) {
		return runCLIParityInDir(t, root, func() int { return Run(args) })
	}
	out, stderr, code := run("source", "Long", "--lines", "10-11")
	if code != 0 || !strings.Contains(out, "(lines.go:10-11)") || !strings.Contains(out, `b := "two"`) || strings.Contains(out, `a := "one"`) {
		t.Fatalf("declaration region: code=%d out=%s err=%s", code, out, stderr)
	}
	out, stderr, code = run("source", "lines.go", "--lines=3-6")
	if code != 0 || !strings.Contains(out, "import (") || !strings.Contains(out, `"strings"`) {
		t.Fatalf("file region with imports: code=%d out=%s err=%s", code, out, stderr)
	}
	out, _, _ = run("source", "Long", "--lines", "1-30")
	if !strings.Contains(out, "(lines.go:8-13)") {
		t.Fatalf("a range wider than the declaration is clamped to it: %s", out)
	}
	for _, args := range [][]string{
		{"source", "Long", "--lines", "1-3"},
		{"source", "Long", "--lines", "9-x"},
		{"source", "Long", "Other", "--lines", "9-10"},
	} {
		if _, _, code := run(args...); code == 0 {
			t.Errorf("%v must fail", args)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "lines.go"), []byte(content+"\n// edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Automatic refresh would rebuild first; --no-refresh keeps the refusal.
	if _, stderr, code := run("--no-refresh", "source", "lines.go", "--lines", "1-2"); code == 0 || !strings.Contains(stderr, "changed after the graph was built") {
		t.Fatalf("a changed file must be refused: code=%d err=%s", code, stderr)
	}
}

func TestReviewSinceReviewsCommittedChanges(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(message string) {
		t.Helper()
		runGitForJSONTest(t, root, "add", ".")
		runGitForJSONTest(t, root, "-c", "user.name=gograph test", "-c", "user.email=gograph@example.invalid", "-c", "commit.gpgSign=false", "commit", "-m", message)
	}
	write("go.mod", "module example.com/since\n\ngo 1.22\n")
	write("a.go", "package since\n\nfunc Stable() int { return 1 }\n\nfunc Changed() int { return 1 }\n")
	runGitForJSONTest(t, root, "init")
	commit("initial")
	write("a.go", "package since\n\nfunc Stable() int { return 1 }\n\nfunc Changed() int { return 2 }\n")
	commit("change")
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) }); code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}

	out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"review", "--since", "HEAD~1"}) })
	if code != 0 || !strings.Contains(out, "Changes since HEAD~1") || !strings.Contains(out, "Changed") || strings.Contains(out, "Stable") {
		t.Fatalf("review --since HEAD~1: code=%d out=%s err=%s", code, out, stderr)
	}
	out, stderr, code = runCLIParityInDir(t, root, func() int { return Run([]string{"review", "--since=HEAD", "--json"}) })
	if code != 0 || !strings.Contains(out, `"status": "empty"`) {
		t.Fatalf("review --since=HEAD on a clean tree must be empty: code=%d out=%s err=%s", code, out, stderr)
	}
	if _, _, code := runCLIParityInDir(t, root, func() int { return Run([]string{"review", "--since", "--json"}) }); code == 0 {
		t.Fatal("review --since without a ref must fail")
	}
}

func TestPathAcceptsDeclarationSelectors(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":          "module example.com/paths\n\ngo 1.22\n",
		"main.go":         "package main\n\nimport \"example.com/paths/lib\"\n\nfunc resolve() int { return 0 }\n\nfunc Run() int { return resolve() + lib.Entry() }\n\nfunc main() { _ = Run() }\n",
		"lib/lib.go":      "package lib\n\nfunc Entry() int { return resolve() }\n\nfunc resolve() int { return 2 }\n",
		"lib/lib_test.go": "package lib\n\nimport \"testing\"\n\nfunc TestEntry(t *testing.T) { _ = Entry() }\n",
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) }); code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}
	for _, args := range [][]string{
		{"path", "Run", "lib/lib.go:resolve"},
		{"path", "main.go:Run", "lib::resolve"},
		{"path", "Run", "example.com/paths/lib::resolve"},
	} {
		out, stderr, code := runCLIParityInDir(t, root, func() int { return Run(args) })
		if code != 0 || !strings.Contains(out, "calls lib.Entry") || !strings.Contains(out, "lib/lib.go:5") {
			t.Errorf("%v must reach lib's resolve through Entry: code=%d out=%s err=%s", args, code, out, stderr)
		}
	}
	out, _, _ := runCLIParityInDir(t, root, func() int { return Run([]string{"path", "TestEntry", "main.go:resolve"}) })
	if !strings.Contains(out, "No call path") {
		t.Errorf("lib's resolve must not stand in for main's: %s", out)
	}
}

func TestSkeletonFileShowsImportBlock(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":     "module example.com/imports\n\ngo 1.22\n",
		"main.go":    "package main\n\nimport (\n\t\"fmt\"\n\tstr \"strings\"\n\n\t\"example.com/imports/lib\"\n)\n\nfunc main() { fmt.Println(str.ToUpper(lib.Name())) }\n",
		"lib/lib.go": "package lib\n\nimport \"os\"\n\nfunc Name() string { return os.Args[0] }\n",
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) }); code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}
	out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"skeleton", "main.go"}) })
	want := "package main\n\nimport (\n\t\"fmt\"\n\tstr \"strings\"\n\n\t\"example.com/imports/lib\"\n)\n\nfunc main()"
	if code != 0 || !strings.HasPrefix(out, want) {
		t.Fatalf("skeleton main.go: code=%d out=%q err=%s", code, out, stderr)
	}
	out, _, _ = runCLIParityInDir(t, root, func() int { return Run([]string{"skeleton", "lib/lib.go"}) })
	if !strings.HasPrefix(out, "package lib\n\nimport \"os\"\n\nfunc Name()") {
		t.Fatalf("skeleton lib/lib.go: %q", out)
	}
}

func TestQueriesUseRecordedBuildTagsForFreshness(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/tagged\n\ngo 1.22\n",
		"a.go":   "package tagged\n\nfunc Plain() {}\n",
		"b.go":   "//go:build integration\n\npackage tagged\n\nfunc Tagged() { Plain() }\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", ".", "--tags=integration"}) }); code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}
	for _, args := range [][]string{
		{"source", "Tagged"},
		{"source", "Tagged", "--tags=integration"},
		{"callers", "Plain", "--tags", "integration"},
	} {
		out, stderr, code := runCLIParityInDir(t, root, func() int { return Run(args) })
		if code != 0 || strings.Contains(stderr, "graph is stale") {
			t.Errorf("%v on a graph built with its tags must be fresh: code=%d out=%s err=%s", args, code, out, stderr)
		}
	}
	_, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"source", "Tagged", "--tags=other"}) })
	if code != 0 || !strings.Contains(stderr, "graph is stale") {
		t.Errorf("different explicit tags must report stale: code=%d err=%s", code, stderr)
	}
	t.Setenv("GOFLAGS", "-tags=other")
	if _, stderr, _ := runCLIParityInDir(t, root, func() int { return Run([]string{"source", "Tagged"}) }); !strings.Contains(stderr, "graph is stale") {
		t.Errorf("GOFLAGS tags that differ from the recorded ones must report stale: %s", stderr)
	}
}

func TestPathWalksThroughMethodsOnASTGraphs(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":              "module example.com/methods\n\ngo 1.22\n",
		"app/manager.go":      "package app\n\ntype Manager struct{}\n\nfunc (m *Manager) Run() { m.recv() }\n\nfunc (m *Manager) recv() {}\n",
		"app/manager_test.go": "package app\n\nimport \"testing\"\n\nfunc TestRun(t *testing.T) { (&Manager{}).Run() }\n",
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) }); code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}
	for _, args := range [][]string{
		{"path", "TestRun", "app/manager.go:Manager.recv"},
		{"path", "TestRun", "recv"},
	} {
		out, stderr, code := runCLIParityInDir(t, root, func() int { return Run(args) })
		if code != 0 || !strings.Contains(out, "(*Manager).Run — calls m.recv") {
			t.Errorf("%v must walk TestRun -> Run -> recv: code=%d out=%s err=%s", args, code, out, stderr)
		}
	}
}
