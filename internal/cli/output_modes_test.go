package cli

import (
	"strings"
	"testing"
)

func TestValidateOutputModes(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		json      bool
		files     bool
		mermaid   bool
		wantError string
	}{
		{name: "query json", args: []string{"query", "x"}, json: true},
		{name: "query files", args: []string{"query", "x"}, files: true},
		{name: "query mermaid", args: []string{"query", "x"}, mermaid: true, wantError: "query does not support --mermaid"},
		{name: "callers mermaid", args: []string{"callers", "x"}, mermaid: true},
		{name: "bare mermaid", mermaid: true},
		{name: "bare json", json: true, wantError: "bare command does not support --json"},
		{name: "version json", args: []string{"version"}, json: true},
		{name: "validate json", args: []string{"validate"}, json: true},
		{name: "session audit json", args: []string{"session", "audit"}, json: true},
		{name: "session create json", args: []string{"session", "create"}, json: true, wantError: "session create does not support --json"},
		{name: "boundaries create json", args: []string{"boundaries", "--create"}, json: true, wantError: "boundaries does not support --json"},
		{name: "conflicting modes", args: []string{"callers", "x"}, json: true, files: true, wantError: "request only one"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oldJSON, oldFiles, oldMermaid := jsonMode, filesOnlyMode, mermaidMode
			jsonMode, filesOnlyMode, mermaidMode = test.json, test.files, test.mermaid
			defer func() {
				jsonMode, filesOnlyMode, mermaidMode = oldJSON, oldFiles, oldMermaid
			}()

			err := validateOutputModes(test.args)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("validateOutputModes() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("validateOutputModes() error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}
