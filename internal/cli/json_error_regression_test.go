package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

type commandEnvelope struct {
	Command string          `json:"command"`
	Status  string          `json:"status"`
	Count   int             `json:"count"`
	Results json.RawMessage `json:"results"`
	Error   string          `json:"error"`
}

func decodeCommandEnvelope(t *testing.T, stdout, stderr string) commandEnvelope {
	t.Helper()
	if stderr != "" {
		t.Fatalf("JSON command wrote stderr:\n%s", stderr)
	}
	var envelope commandEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("decode JSON envelope: %v\nstdout:\n%s", err, stdout)
	}
	return envelope
}

func TestCLIJSONFailuresUseCommandEnvelope(t *testing.T) {
	validRoot := writeCLIParityGraph(t, &graph.Graph{
		FlowFunctions: []graph.FlowFunction{{ID: "example.com/json::Idle", Name: "Idle", File: "main.go"}},
	})
	invalidConfig := filepath.Join(validRoot, "bad-flow.json")
	if err := os.WriteFile(invalidConfig, []byte(`{"sanitizers":`), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		root    string
		args    []string
		command string
		wantErr string
	}{
		{name: "usage", root: t.TempDir(), args: []string{"focus", "--json"}, command: "focus", wantErr: "usage: gograph focus"},
		{name: "parse", root: t.TempDir(), args: []string{"godobj", "--top", "not-a-number", "--json"}, command: "godobj", wantErr: "invalid --top value"},
		{name: "callers missing depth", root: t.TempDir(), args: []string{"callers", "Target", "--depth", "--json"}, command: "callers", wantErr: "--depth requires a value"},
		{name: "callees invalid depth", root: t.TempDir(), args: []string{"callees", "Target", "--depth", "many", "--json"}, command: "callees", wantErr: "invalid --depth value"},
		{name: "context missing limit", root: t.TempDir(), args: []string{"context", "Target", "--limit", "--json"}, command: "context", wantErr: "--limit requires a value"},
		{name: "endpoint invalid depth", root: t.TempDir(), args: []string{"endpoint", "Target", "--depth", "deep", "--json"}, command: "endpoint", wantErr: "invalid --depth value"},
		{name: "hotspot trailing junk", root: t.TempDir(), args: []string{"hotspot", "--top", "3junk", "--json"}, command: "hotspot", wantErr: "invalid --top value"},
		{name: "path extra positional", root: t.TempDir(), args: []string{"path", "From", "To", "Extra", "--json"}, command: "path", wantErr: "usage: gograph path"},
		{name: "arity missing minimum", root: t.TempDir(), args: []string{"arity", "--min", "--json"}, command: "arity", wantErr: "--min requires a value"},
		{name: "changes missing ref", root: t.TempDir(), args: []string{"changes", "--git", "--json"}, command: "changes", wantErr: "--git requires a value"},
		{name: "boundaries missing config", root: t.TempDir(), args: []string{"boundaries", "--config", "--json"}, command: "boundaries", wantErr: "--config requires a value"},
		{name: "global parse", root: t.TempDir(), args: []string{"query", "anything", "--json", "--intention"}, command: "query", wantErr: "flag requires a value"},
		{name: "conflicting output modes", root: t.TempDir(), args: []string{"query", "anything", "--json", "--files-only"}, command: "query", wantErr: "request only one"},
		{name: "graph load", root: t.TempDir(), args: []string{"focus", "sample", "--json"}, command: "focus", wantErr: "run `gograph build` first"},
		{name: "config", root: validRoot, args: []string{"flow", "--config", invalidConfig, "--json"}, command: "flow", wantErr: "invalid JSON in flow config"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr, code := runCLIParityInDir(t, test.root, func() int {
				return Run(test.args)
			})
			if code != exitError {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, exitError, stdout, stderr)
			}
			envelope := decodeCommandEnvelope(t, stdout, stderr)
			if envelope.Command != test.command || envelope.Status != "error" || !strings.Contains(envelope.Error, test.wantErr) {
				t.Fatalf("unexpected error envelope: %+v", envelope)
			}
		})
	}
}

func TestCLISessionAuditJSONFailureUsesEnvelope(t *testing.T) {
	root := writeCLIParityGraph(t, &graph.Graph{})
	stdout, stderr, code := runCLIParityInDir(t, root, func() int {
		return Run([]string{"session", "audit", "missing-session", "--json"})
	})
	if code != exitError {
		t.Fatalf("exit code = %d, want %d", code, exitError)
	}
	envelope := decodeCommandEnvelope(t, stdout, stderr)
	if envelope.Command != "session audit" || envelope.Status != "error" || !strings.Contains(envelope.Error, "Error opening session log") {
		t.Fatalf("unexpected session audit envelope: %+v", envelope)
	}
}

func TestCLIJSONMissingSessionIntentionUsesCommandEnvelope(t *testing.T) {
	root := writeCLIParityGraph(t, &graph.Graph{})
	pointer := []byte(`{"active_session_id":"json-errors-test"}`)
	if err := os.WriteFile(filepath.Join(root, ".gograph", "active_session.json"), pointer, 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLIParityInDir(t, root, func() int {
		return Run([]string{"query", "anything", "--json"})
	})
	if code != exitError {
		t.Fatalf("exit code = %d, want %d", code, exitError)
	}
	envelope := decodeCommandEnvelope(t, stdout, stderr)
	if envelope.Command != "query" || envelope.Status != "error" || !strings.Contains(envelope.Error, "requires an intention") {
		t.Fatalf("unexpected missing-intention envelope: %+v", envelope)
	}
}

func TestCLIFilesOnlyEmptyResultsWriteNothing(t *testing.T) {
	tests := []struct {
		name  string
		graph *graph.Graph
		args  []string
	}{
		{name: "result list", graph: &graph.Graph{}, args: []string{"query", "absent", "--files-only"}},
		{
			name:  "flow",
			graph: &graph.Graph{FlowFunctions: []graph.FlowFunction{{ID: "example.com/json::Idle", Name: "Idle", File: "main.go"}}},
			args:  []string{"flow", "--files-only"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeCLIParityGraph(t, test.graph)
			stdout, stderr, code := runCLIParityInDir(t, root, func() int {
				return Run(test.args)
			})
			if code != exitSuccess || stdout != "" || stderr != "" {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}

func TestCLIEndpointNotFoundIsAnEmptySuccess(t *testing.T) {
	root := writeCLIParityGraph(t, &graph.Graph{})

	stdout, stderr, code := runCLIParityInDir(t, root, func() int {
		return Run([]string{"endpoint", "MissingHandler", "--json"})
	})
	if code != exitSuccess {
		t.Fatalf("JSON endpoint exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	envelope := decodeCommandEnvelope(t, stdout, stderr)
	if envelope.Command != "endpoint" || envelope.Status != "empty" || envelope.Count != 0 || string(envelope.Results) != "[]" {
		t.Fatalf("unexpected endpoint envelope: %+v (results=%s)", envelope, envelope.Results)
	}

	_, _, code = runCLIParityInDir(t, root, func() int {
		return Run([]string{"endpoint", "MissingHandler"})
	})
	if code != exitSuccess {
		t.Fatalf("text endpoint exit=%d, want 0", code)
	}
}

func TestCLISingleTargetCommandsRejectSurplusPositionals(t *testing.T) {
	commands := []string{
		"callers", "callees", "focus", "source", "public", "fields", "embeds", "imports",
		"impact", "implementers", "mocks", "envs", "interfaces", "concurrency", "tests",
		"complexity", "context", "dependents", "sql", "errors", "httpcalls", "mutate",
		"constructors", "usages", "returnusage", "literals", "schema", "globals", "fixtures",
		"plan", "review", "risk", "explain", "doc",
	}
	root := t.TempDir()
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			stdout, stderr, code := runCLIParityInDir(t, root, func() int {
				return Run([]string{command, "First", "Second", "--json"})
			})
			if code != exitError {
				t.Fatalf("%s accepted a surplus positional argument: exit=%d stdout=%s stderr=%s", command, code, stdout, stderr)
			}
			envelope := decodeCommandEnvelope(t, stdout, stderr)
			if envelope.Command != command || envelope.Status != "error" {
				t.Fatalf("unexpected %s surplus-argument envelope: %+v", command, envelope)
			}
		})
	}
}

func TestCLISingleTargetCommandsRejectUnknownFlags(t *testing.T) {
	for _, command := range []string{"focus", "envs", "source", "complexity", "dependents", "sql", "httpcalls", "doc"} {
		t.Run(command, func(t *testing.T) {
			stdout, stderr, code := runCLIParityInDir(t, t.TempDir(), func() int {
				return Run([]string{command, "--unknown", "--json"})
			})
			if code != exitError {
				t.Fatalf("%s accepted an unknown flag: exit=%d stdout=%s stderr=%s", command, code, stdout, stderr)
			}
			envelope := decodeCommandEnvelope(t, stdout, stderr)
			if envelope.Command != command || envelope.Status != "error" {
				t.Fatalf("unexpected %s unknown-flag envelope: %+v", command, envelope)
			}
		})
	}
}

func TestCLICleanUncommittedPlanAndReviewAreEmptyJSONSuccesses(t *testing.T) {
	root := writeCLIParityGraph(t, &graph.Graph{})
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package sample\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitForJSONTest(t, root, "init")
	runGitForJSONTest(t, root, "add", "main.go")
	runGitForJSONTest(t, root, "-c", "user.name=gograph test", "-c", "user.email=gograph@example.invalid", "-c", "commit.gpgSign=false", "commit", "-m", "initial")

	for _, command := range []string{"plan", "review"} {
		t.Run(command, func(t *testing.T) {
			stdout, stderr, code := runCLIParityInDir(t, root, func() int {
				return Run([]string{command, "--uncommitted", "--json"})
			})
			if code != exitSuccess {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			envelope := decodeCommandEnvelope(t, stdout, stderr)
			if envelope.Command != command || envelope.Status != "empty" || envelope.Count != 0 || string(envelope.Results) != "[]" {
				t.Fatalf("unexpected clean %s envelope: %+v (results=%s)", command, envelope, envelope.Results)
			}
		})
	}
}

func runGitForJSONTest(t *testing.T, root string, args ...string) {
	t.Helper()
	commandArgs := append([]string{"-C", root}, args...)
	if output, err := exec.Command("git", commandArgs...).CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
