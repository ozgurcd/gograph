package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeToolFaultsRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/faults\n\ngo 1.22\n",
		"main.go": `package main

import (
	"net/http"
	"os"
)

func handle(w http.ResponseWriter, r *http.Request) {
	data, _ := os.ReadFile(os.Getenv("FAULTS_PATH"))
	_, _ = w.Write(data)
}

func main() {
	http.HandleFunc("/api/setup/license", handle)
	_ = http.ListenAndServe(":8080", nil)
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) }); code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}
	return root
}

func TestQueryAndStringsAcceptRepositoryPath(t *testing.T) {
	root := writeToolFaultsRepository(t)
	elsewhere := t.TempDir()

	out, stderr, code := runCLIParityInDir(t, elsewhere, func() int { return Run([]string{"query", "/api/setup/license", root}) })
	if code != 0 || !strings.Contains(out, "[route] HandleFunc /api/setup/license") {
		t.Fatalf("query with a trailing repository path must search that graph and match routes: code=%d out=%s err=%s", code, out, stderr)
	}
	out, stderr, code = runCLIParityInDir(t, elsewhere, func() int { return Run([]string{"query", "--path", root, "handle"}) })
	if code != 0 || !strings.Contains(out, "handle") {
		t.Fatalf("query --path must search that graph: code=%d out=%s err=%s", code, out, stderr)
	}
	out, _, code = runCLIParityInDir(t, root, func() int { return Run([]string{"query", "handle", "."}) })
	if code != 0 || !strings.Contains(out, "handle") {
		t.Fatalf("query with a trailing . must search the current repository: code=%d out=%s", code, out)
	}
	if _, stderr, code = runCLIParityInDir(t, elsewhere, func() int { return Run([]string{"query", "--path", filepath.Join(root, "missing"), "handle"}) }); code == 0 || !strings.Contains(stderr+out, "not a directory") {
		t.Fatalf("a missing --path must be refused, code=%d", code)
	}

	out, stderr, code = runCLIParityInDir(t, elsewhere, func() int { return Run([]string{"strings", "/api/setup", "--path", root}) })
	if code != 0 || !strings.Contains(out, `"/api/setup/license" — string literal in main`) {
		t.Fatalf("strings must find the raw route literal with its function: code=%d out=%s err=%s", code, out, stderr)
	}
}

func TestFlowReportsExhaustedBudget(t *testing.T) {
	root := writeToolFaultsRepository(t)
	out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"flow", "--max-work", "1", "--json"}) })
	if code != exitStale || !strings.Contains(out, `"complete": false`) || !strings.Contains(out, `"max_work": 1`) {
		t.Fatalf("an exhausted flow budget must exit 2 with completeness: code=%d out=%s err=%s", code, out, stderr)
	}
	out, _, code = runCLIParityInDir(t, root, func() int { return Run([]string{"flow", "--json"}) })
	if code != 0 || !strings.Contains(out, `"complete": true`) || !strings.Contains(out, "filesystem") {
		t.Fatalf("the default budget must complete and find the env-to-file flow: code=%d out=%s", code, out)
	}
	if _, _, code = runCLIParityInDir(t, root, func() int { return Run([]string{"flow", "--context-depth", "0"}) }); code == 0 {
		t.Fatal("a non-positive context depth must be refused")
	}
}
