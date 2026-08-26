package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

type jsonContractEnvelope struct {
	SchemaVersion string          `json:"schema_version"`
	Command       string          `json:"command"`
	Query         string          `json:"query"`
	Status        string          `json:"status"`
	Count         int             `json:"count"`
	Results       json.RawMessage `json:"results"`
	Error         string          `json:"error"`
}

func captureJSONContractOutput(t *testing.T, run func() int) (stdout, stderr string, code int) {
	t.Helper()

	originalStdout, originalStderr := os.Stdout, os.Stderr
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = stdoutWriter, stderrWriter

	func() {
		defer func() {
			os.Stdout, os.Stderr = originalStdout, originalStderr
		}()
		code = run()
		_ = stdoutWriter.Close()
		_ = stderrWriter.Close()
	}()

	stdoutBytes, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatal(err)
	}
	stderrBytes, err := io.ReadAll(stderrReader)
	if err != nil {
		t.Fatal(err)
	}
	_ = stdoutReader.Close()
	_ = stderrReader.Close()
	return string(stdoutBytes), string(stderrBytes), code
}

func runJSONContractInDir(t *testing.T, root string, run func() int) (stdout, stderr string, code int) {
	t.Helper()

	originalWorkingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(originalWorkingDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	}()

	originalJSONMode := jsonMode
	jsonMode = true
	defer func() { jsonMode = originalJSONMode }()
	return captureJSONContractOutput(t, run)
}

func writeJSONContractGraph(t *testing.T, root string, g *graph.Graph) {
	t.Helper()

	currentPolicyGraph(g)
	g.Version = graph.Version
	g.Root = root
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o750); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gograph", "graph.json"), data, 0o640); err != nil {
		t.Fatal(err)
	}
}

func writeJSONContractFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o640); err != nil {
		t.Fatal(err)
	}
}

func decodeJSONContractEnvelope(t *testing.T, output string) (jsonContractEnvelope, map[string]json.RawMessage) {
	t.Helper()
	var envelope jsonContractEnvelope
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("decode JSON envelope: %v\n%s", err, output)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &fields); err != nil {
		t.Fatalf("decode JSON envelope fields: %v\n%s", err, output)
	}
	return envelope, fields
}

func requireJSONContractFields(t *testing.T, fields map[string]json.RawMessage, want ...string) {
	t.Helper()
	got := make([]string, 0, len(fields))
	for field := range fields {
		got = append(got, field)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON fields = %v, want %v", got, want)
	}
}

func requireCheckErrorEnvelope(t *testing.T, stdout, stderr string, code int, expectedError string) {
	t.Helper()
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	envelope, fields := decodeJSONContractEnvelope(t, stdout)
	if envelope.SchemaVersion != SchemaVersion || envelope.Command != "check" || envelope.Status != "error" || envelope.Count != 0 {
		t.Fatalf("unexpected error envelope: %+v", envelope)
	}
	if envelope.Error != expectedError {
		t.Fatalf("error = %q, want %q", envelope.Error, expectedError)
	}
	requireJSONContractFields(t, fields, "schema_version", "command", "status", "count", "error")
}

func TestOKEnvelopeAlwaysIncludesCountAndNormalizesNilSlices(t *testing.T) {
	tests := []struct {
		name    string
		results any
	}{
		{name: "nil interface", results: nil},
		{name: "typed nil slice", results: []string(nil)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(okEnvelope("query", "missing", test.results, 0))
			if err != nil {
				t.Fatal(err)
			}
			envelope, fields := decodeJSONContractEnvelope(t, string(data))
			if envelope.Status != "empty" || envelope.Count != 0 {
				t.Fatalf("envelope = status:%q count:%d, want empty/0", envelope.Status, envelope.Count)
			}
			if string(envelope.Results) != "[]" {
				t.Fatalf("results = %s, want []", envelope.Results)
			}
			requireJSONContractFields(t, fields, "schema_version", "command", "query", "status", "count", "results")
		})
	}
}

func TestRunCheckJSONArgumentErrorsUseExactEnvelopes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		err  string
	}{
		{name: "config missing separate value", args: []string{"--config"}, err: "missing value for --config"},
		{name: "config followed by flag", args: []string{"--config", "--uncommitted"}, err: "missing value for --config"},
		{name: "config missing equals value", args: []string{"--config="}, err: "missing value for --config"},
		{name: "since missing separate value", args: []string{"--since"}, err: "missing value for --since"},
		{name: "since followed by flag", args: []string{"--since", "--uncommitted"}, err: "missing value for --since"},
		{name: "since missing equals value", args: []string{"--since="}, err: "missing value for --since"},
		{name: "unknown positional", args: []string{"unexpected"}, err: "unknown argument: unexpected"},
		{name: "unknown flag", args: []string{"--unexpected"}, err: "unknown argument: --unexpected"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			originalJSONMode := jsonMode
			jsonMode = true
			defer func() { jsonMode = originalJSONMode }()
			stdout, stderr, code := captureJSONContractOutput(t, func() int { return runCheck(test.args) })
			requireCheckErrorEnvelope(t, stdout, stderr, code, test.err)
		})
	}
}

func TestRunCheckJSONOperationalErrorsUseEnvelopes(t *testing.T) {
	t.Run("config read", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o750); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, code := runJSONContractInDir(t, root, func() int { return runCheck([]string{"--config", "missing.json"}) })
		envelope, _ := decodeJSONContractEnvelope(t, stdout)
		if !strings.HasPrefix(envelope.Error, "failed to read config: ") {
			t.Fatalf("error = %q", envelope.Error)
		}
		requireCheckErrorEnvelope(t, stdout, stderr, code, envelope.Error)
	})

	t.Run("config parse", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o750); err != nil {
			t.Fatal(err)
		}
		writeJSONContractFile(t, filepath.Join(root, "invalid.json"), []byte("{"))
		stdout, stderr, code := runJSONContractInDir(t, root, func() int { return runCheck([]string{"--config", "invalid.json"}) })
		envelope, _ := decodeJSONContractEnvelope(t, stdout)
		if !strings.HasPrefix(envelope.Error, "failed to parse config: ") {
			t.Fatalf("error = %q", envelope.Error)
		}
		requireCheckErrorEnvelope(t, stdout, stderr, code, envelope.Error)
	})

	t.Run("graph load", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o750); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, code := runJSONContractInDir(t, root, func() int { return runCheck(nil) })
		envelope, _ := decodeJSONContractEnvelope(t, stdout)
		if !strings.HasPrefix(envelope.Error, "failed to load graph: ") {
			t.Fatalf("error = %q", envelope.Error)
		}
		requireCheckErrorEnvelope(t, stdout, stderr, code, envelope.Error)
	})

	t.Run("baseline build", func(t *testing.T) {
		root := t.TempDir()
		writeJSONContractGraph(t, root, &graph.Graph{})
		stdout, stderr, code := runJSONContractInDir(t, root, func() int { return runCheck([]string{"--since", "missing.json"}) })
		envelope, _ := decodeJSONContractEnvelope(t, stdout)
		if !strings.HasPrefix(envelope.Error, "failed to build baseline graph: ") {
			t.Fatalf("error = %q", envelope.Error)
		}
		requireCheckErrorEnvelope(t, stdout, stderr, code, envelope.Error)
	})

	t.Run("check run", func(t *testing.T) {
		root := t.TempDir()
		writeJSONContractGraph(t, root, &graph.Graph{})
		writeJSONContractFile(t, filepath.Join(root, "checks.json"), []byte(`{"checks":{"made_up":"error"}}`))
		stdout, stderr, code := runJSONContractInDir(t, root, func() int { return runCheck([]string{"--config", "checks.json"}) })
		requireCheckErrorEnvelope(t, stdout, stderr, code, "check failed: unknown check name in config: made_up")
	})
}

func TestRunCheckJSONSuccessAndPolicyFailureUseEnvelope(t *testing.T) {
	t.Run("passed with zero findings", func(t *testing.T) {
		root := t.TempDir()
		writeJSONContractGraph(t, root, &graph.Graph{})
		stdout, stderr, code := runJSONContractInDir(t, root, func() int { return runCheck(nil) })
		if code != 0 || stderr != "" {
			t.Fatalf("exit=%d stderr=%q", code, stderr)
		}
		envelope, fields := decodeJSONContractEnvelope(t, stdout)
		if envelope.Command != "check" || envelope.Status != "empty" || envelope.Count != 0 {
			t.Fatalf("unexpected envelope: %+v", envelope)
		}
		var report search.CheckReport
		if err := json.Unmarshal(envelope.Results, &report); err != nil {
			t.Fatalf("decode check report: %v", err)
		}
		if report.Status != string(search.CheckPassed) || len(report.Findings) != 0 {
			t.Fatalf("report = %+v", report)
		}
		requireJSONContractFields(t, fields, "schema_version", "command", "status", "count", "results", "graph_state")
	})

	t.Run("failed policy preserves exit one", func(t *testing.T) {
		root := t.TempDir()
		writeJSONContractGraph(t, root, &graph.Graph{Symbols: []graph.SymbolNode{{
			ID:        "example.com/check::TooMany",
			Name:      "TooMany",
			Kind:      graph.KindFunction,
			Signature: "func(a int, b int)",
		}}})
		writeJSONContractFile(t, filepath.Join(root, "checks.json"), []byte(`{"checks":{"max_arity":{"level":"error","value":1}}}`))
		stdout, stderr, code := runJSONContractInDir(t, root, func() int { return runCheck([]string{"--config", "checks.json"}) })
		if code != 1 || stderr != "" {
			t.Fatalf("exit=%d stderr=%q", code, stderr)
		}
		envelope, fields := decodeJSONContractEnvelope(t, stdout)
		if envelope.Command != "check" || envelope.Status != "ok" || envelope.Count != 1 || envelope.Error != "" {
			t.Fatalf("unexpected envelope: %+v", envelope)
		}
		var report search.CheckReport
		if err := json.Unmarshal(envelope.Results, &report); err != nil {
			t.Fatalf("decode check report: %v", err)
		}
		if report.Status != string(search.CheckFailed) || len(report.Findings) != 1 {
			t.Fatalf("report = %+v", report)
		}
		requireJSONContractFields(t, fields, "schema_version", "command", "status", "count", "results", "graph_state")
	})
}

func TestAPIDriftItemCountIncludesEveryChangeCategory(t *testing.T) {
	result := &search.APIDriftResult{}
	result.ExportedSymbols.Added = []string{"a"}
	result.ExportedSymbols.Removed = []string{"b"}
	result.ExportedSymbols.Changed = []search.APISymbolDrift{{Name: "c"}}
	result.Interfaces.Added = []string{"d"}
	result.Interfaces.Removed = []string{"e"}
	result.Interfaces.Changed = []search.APISymbolDrift{{Name: "f"}}
	result.Structs.Added = []string{"g"}
	result.Structs.Removed = []string{"h"}
	result.Structs.Changed = []search.APISymbolDrift{{Name: "i"}}
	result.Routes.Added = []string{"j"}
	result.Routes.Removed = []string{"k"}
	result.Routes.Changed = []search.APIRouteDrift{{Path: "l"}}
	result.AffectedTests = []string{"consequence"}
	result.AffectedMocks = []string{"consequence"}

	if got := apiDriftItemCount(result); got != 12 {
		t.Fatalf("apiDriftItemCount() = %d, want 12", got)
	}
}

func TestRunAPIJSONCountReflectsDriftItems(t *testing.T) {
	root := t.TempDir()
	current := &graph.Graph{
		Symbols: []graph.SymbolNode{{ID: "example.com/api::New", Name: "New", Kind: graph.KindFunction}},
		Routes:  []graph.HTTPRoute{{Method: "GET", Path: "/new", Handler: "New"}},
	}
	writeJSONContractGraph(t, root, current)
	baseline := &graph.Graph{Version: graph.Version, Root: root, Symbols: []graph.SymbolNode{{
		ID:   "example.com/api::Old",
		Name: "Old",
		Kind: graph.KindFunction,
	}}}
	currentPolicyGraph(baseline)
	baselineData, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	writeJSONContractFile(t, filepath.Join(root, "baseline.json"), baselineData)

	stdout, stderr, code := runJSONContractInDir(t, root, func() int { return runAPI([]string{"--since", "baseline.json"}) })
	if code != 0 || stderr != "" {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	envelope, fields := decodeJSONContractEnvelope(t, stdout)
	if envelope.SchemaVersion != SchemaVersion || envelope.Command != "api" || envelope.Query != "baseline.json" || envelope.Status != "ok" {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
	if envelope.Count != 3 {
		t.Fatalf("count = %d, want 3 (one added, one removed, one route added)", envelope.Count)
	}
	requireJSONContractFields(t, fields, "schema_version", "command", "query", "status", "count", "results", "graph_state")
}

func TestRunCheckRejectsLinkedDefaultConfig(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
		t.Fatal(err)
	}
	const sentinel = "BENIGN-LINKED-CHECK-CONFIG-SENTINEL"
	out := filepath.Join(base, "outside.json")
	if err := os.WriteFile(out, []byte(`{"checks":{"`+sentinel+`":"error"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, filepath.Join(root, ".gograph", "checks.json")); err != nil {
		t.Skipf("create check config symlink: %v", err)
	}

	stdout, stderr, code := runJSONContractInDir(t, root, func() int { return runCheck(nil) })
	if code != 1 || strings.Contains(stdout+stderr, sentinel) || !strings.Contains(stdout, "unsafe repository source path") {
		t.Fatalf("linked config result code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
