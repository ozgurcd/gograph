package cli_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runCmd executes the current package-scoped CLI binary against an isolated
// fixture copy. It returns stdout or fails the test.
func runCmd(t *testing.T, fixtureDir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(buildTestBinary(t), args...)
	cmd.Dir = fixtureDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		// allow empty results to return a non-zero exit code sometimes depending on command,
		// but standard errors shouldn't happen
		if !strings.Contains(string(out), "schema_version") {
			t.Fatalf("command failed: %v\nOutput: %s", err, string(out))
		}
	}

	return out
}

func copyJSONFixture(t *testing.T) string {
	t.Helper()
	source := filepath.Join(testRepositoryRoot, "testdata", "fixture")
	destination := t.TempDir()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".gograph" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(destination, rel), 0o755)
		}
		if !entry.Type().IsRegular() {
			return &fs.PathError{Op: "copy fixture", Path: path, Err: fs.ErrInvalid}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(destination, rel), data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy JSON fixture: %v", err)
	}
	return destination
}

func TestJSONSchema(t *testing.T) {
	fixtureDir := copyJSONFixture(t)
	// 1. Build the graph for the fixture repository
	runCmd(t, fixtureDir, "build", ".")

	t.Run("callers schema", func(t *testing.T) {
		out := runCmd(t, fixtureDir, "callers", "GetUser", "--json")

		var env map[string]interface{}
		if err := json.Unmarshal(out, &env); err != nil {
			t.Fatalf("invalid json: %v\nOutput: %s", err, string(out))
		}

		if env["schema_version"] != "1" {
			t.Errorf("expected schema_version '1', got %v", env["schema_version"])
		}
		if env["status"] != "ok" {
			t.Errorf("expected status 'ok', got %v", env["status"])
		}
		if env["command"] != "callers" {
			t.Errorf("expected command 'callers', got %v", env["command"])
		}
		state, ok := env["graph_state"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected graph_state object, got %T", env["graph_state"])
		}
		if state["schema_version"] != "gograph.graph-state.v1" || state["source"] != "persisted" || state["freshness"] != "current" || state["completeness"] != "complete" || state["precision"] != "ast" {
			t.Fatalf("unexpected graph_state: %+v", state)
		}

		results, ok := env["results"].([]interface{})
		if !ok {
			t.Fatalf("expected results array, got %T", env["results"])
		}

		if len(results) == 0 {
			t.Fatal("expected callers for GetUser, got none")
		}

		// Verify result structure matches the Result JSON tags
		first := results[0].(map[string]interface{})
		requiredFields := []string{"name", "file", "line", "kind", "call_site_file", "call_site_line"}
		for _, field := range requiredFields {
			if _, ok := first[field]; !ok {
				t.Errorf("missing field %q in result JSON", field)
			}
		}
	})

	t.Run("hotspot schema", func(t *testing.T) {
		out := runCmd(t, fixtureDir, "hotspot", "--json")

		var env map[string]interface{}
		if err := json.Unmarshal(out, &env); err != nil {
			t.Fatalf("invalid json: %v\nOutput: %s", err, string(out))
		}

		results, ok := env["results"].([]interface{})
		if !ok || len(results) == 0 {
			t.Fatalf("expected hotspot results array, got %v", env["results"])
		}

		first := results[0].(map[string]interface{})
		requiredFields := []string{"name", "file", "line", "kind", "incoming_calls"}
		for _, field := range requiredFields {
			if _, ok := first[field]; !ok {
				t.Errorf("missing field %q in hotspot result JSON", field)
			}
		}
	})

	t.Run("deps schema", func(t *testing.T) {
		out := runCmd(t, fixtureDir, "deps", "auth", "--json")

		var env map[string]interface{}
		if err := json.Unmarshal(out, &env); err != nil {
			t.Fatalf("invalid json: %v\nOutput: %s", err, string(out))
		}

		result, ok := env["results"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected deps result object, got %T", env["results"])
		}

		if result["package"] != "auth" {
			t.Errorf("expected package 'auth', got %v", result["package"])
		}
		if _, ok := result["direct"]; !ok {
			t.Errorf("missing 'direct' dependencies field")
		}
	})

	t.Run("empty results schema", func(t *testing.T) {
		out := runCmd(t, fixtureDir, "query", "NonExistentFunctionXYZ123", "--json")

		var env map[string]interface{}
		if err := json.Unmarshal(out, &env); err != nil {
			t.Fatalf("invalid json: %v\nOutput: %s", err, string(out))
		}

		if env["status"] != "empty" {
			t.Errorf("expected status 'empty', got %v", env["status"])
		}
		if countVal, ok := env["count"]; ok {
			if countVal.(float64) != 0 {
				t.Errorf("expected count 0 or omitted, got %v", countVal)
			}
		}
	})
}
