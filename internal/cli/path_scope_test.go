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
