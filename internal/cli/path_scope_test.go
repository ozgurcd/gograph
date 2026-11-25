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
