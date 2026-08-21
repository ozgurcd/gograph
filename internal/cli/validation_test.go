package cli

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/validation"
)

func TestVersionJSON(t *testing.T) {
	oldJSON := jsonMode
	jsonMode = true
	defer func() { jsonMode = oldJSON }()

	exit, output := captureMachineOutput(t, runVersion)
	if exit != 0 {
		t.Fatalf("exit = %d", exit)
	}
	var document validation.VersionDocument
	decodeOneJSON(t, output, &document)
	if document.SchemaVersion != validation.VersionSchemaVersion || document.Version != Version {
		t.Fatalf("document = %+v", document)
	}
}

func TestValidateInvalidInvocationIsJSON(t *testing.T) {
	oldJSON := jsonMode
	jsonMode = true
	defer func() { jsonMode = oldJSON }()

	exit, output := captureMachineOutput(t, func() int { return runValidate([]string{"--unknown"}) })
	if exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	var result validation.Result
	decodeOneJSON(t, output, &result)
	if result.SchemaVersion != validation.ResultSchemaVersion || result.Evaluation.Outcome != validation.OutcomeCannotEvaluate || result.Evaluation.Reason != validation.ReasonInvalidRequest {
		t.Fatalf("result = %+v", result)
	}
}

func TestValidateRejectsOtherOutputModesWithValidationJSON(t *testing.T) {
	oldJSON, oldFiles, oldMermaid := jsonMode, filesOnlyMode, mermaidMode
	defer func() { jsonMode, filesOnlyMode, mermaidMode = oldJSON, oldFiles, oldMermaid }()

	exit, output := captureMachineOutput(t, func() int {
		return Run([]string{"validate", "--repo", "/repo", "--binding-json", `{}`, "--files-only"})
	})
	if exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	var result validation.Result
	decodeOneJSON(t, output, &result)
	if result.Evaluation.Reason != validation.ReasonInvalidRequest {
		t.Fatalf("result = %+v", result)
	}
}

func TestValidationOutcomeExitCodesAndJSON(t *testing.T) {
	tests := []struct {
		name    string
		outcome validation.Outcome
		exit    int
	}{
		{name: "pass", outcome: validation.OutcomePass, exit: 0},
		{name: "fail", outcome: validation.OutcomeFail, exit: 1},
		{name: "cannot evaluate", outcome: validation.OutcomeCannotEvaluate, exit: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := validation.InvalidRequestResult("test", "/repo", "fixture")
			result.Evaluation.Outcome = test.outcome
			exit, output := captureMachineOutput(t, func() int { return writeValidationResult(result) })
			if exit != test.exit {
				t.Fatalf("exit = %d, want %d", exit, test.exit)
			}
			var decoded validation.Result
			decodeOneJSON(t, output, &decoded)
		})
	}
}

func TestParseValidationArgsIsClosed(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "valid", args: []string{"--repo", "/repo", "--binding-json", `{}`}, want: true},
		{name: "arbitrary flag", args: []string{"--repo", "/repo", "--binding-json", `{}`, "--exec", "sh"}},
		{name: "duplicate repo", args: []string{"--repo", "/repo", "--repo", "/other", "--binding-json", `{}`}},
		{name: "missing repo", args: []string{"--binding-json", `{}`}},
		{name: "missing binding", args: []string{"--repo", "/repo"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := parseValidationArgs(test.args)
			if test.want && err != nil {
				t.Fatalf("parseValidationArgs() error = %v", err)
			}
			if !test.want && err == nil {
				t.Fatal("parseValidationArgs() unexpectedly succeeded")
			}
		})
	}
}

func captureMachineOutput(t *testing.T, run func() int) (int, string) {
	t.Helper()
	oldStdout := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = write
	exit := run()
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = oldStdout
	data, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	return exit, string(data)
}

func decodeOneJSON(t *testing.T, output string, target any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(output))
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode JSON output %q: %v", output, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("stdout contained more than one JSON document: %q", output)
	}
}
