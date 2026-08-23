package cli_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func setupAttributionFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/attribution\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "core.go"), []byte(`package attribution

func Start() { Leaf() }
func Leaf() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "core_test.go"), []byte(`package attribution

import "testing"

func TestStart(t *testing.T) { Start() }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildTestBinary(t)
	cmd := exec.Command(bin, "build", root)
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build attribution fixture: %v\n%s", err, output)
	}
	return root, bin
}

func runJSONCommand(t *testing.T, root, bin string, arguments ...string) map[string]any {
	t.Helper()
	cmd := exec.Command(bin, arguments...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gograph %v: %v\n%s", arguments, err, output)
	}
	var document map[string]any
	if err := json.Unmarshal(output, &document); err != nil {
		t.Fatalf("gograph %v returned invalid JSON: %v\n%s", arguments, err, output)
	}
	return document
}

func TestAttributionCommandsAndCensusQueriesHaveJSONContracts(t *testing.T) {
	root, bin := setupAttributionFixture(t)

	identity := runJSONCommand(t, root, bin, "identity", "Start", "--package", "attribution", "--json")
	identityReport, ok := identity["results"].(map[string]any)
	if !ok || identityReport["schema_version"] != "gograph.identity.v1" || identityReport["status"] != "exact" {
		t.Fatalf("identity JSON = %#v", identity)
	}
	matches, _ := identityReport["matches"].([]any)
	if len(matches) != 1 || matches[0].(map[string]any)["stable_id"] != "example.com/attribution::Start" {
		t.Fatalf("identity matches = %#v", matches)
	}

	coverage := runJSONCommand(t, root, bin, "coverage", "TestStart", "--package", "attribution", "--json")
	coverageReport, ok := coverage["results"].(map[string]any)
	if !ok || coverageReport["schema_version"] != "gograph.coverage.v1" || coverageReport["status"] != "exact" {
		t.Fatalf("coverage JSON = %#v", coverage)
	}
	symbols, _ := coverageReport["symbols"].([]any)
	if len(symbols) != 2 {
		t.Fatalf("coverage symbols = %#v", symbols)
	}

	for _, query := range [][]string{
		{"callers", "Leaf", "--json"},
		{"tests", "Start", "--json"},
		{"untested", "--json"},
	} {
		document := runJSONCommand(t, root, bin, query...)
		if document["command"] != query[0] {
			t.Fatalf("%v JSON command = %#v", query, document)
		}
		if _, ok := document["results"].([]any); !ok {
			t.Fatalf("%v results are not an array: %#v", query, document["results"])
		}
	}

	excluded := runJSONCommand(t, root, bin, "untested", "--exclude", "core.go", "--json")
	if excluded["status"] != "empty" || excluded["count"] != float64(0) {
		t.Fatalf("excluded untested JSON = %#v", excluded)
	}
}
