package cli

import "testing"

func TestCommandTelemetryStatus(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		exitCode int
		want     string
	}{
		{name: "ordinary success", command: "query", exitCode: exitSuccess, want: "success"},
		{name: "stale result", command: "stale", exitCode: exitStale, want: "success"},
		{name: "stale error", command: "stale", exitCode: exitError, want: "failure"},
		{name: "other exit two", command: "query", exitCode: exitStale, want: "failure"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := commandTelemetryStatus(tt.command, tt.exitCode); got != tt.want {
				t.Fatalf("commandTelemetryStatus(%q, %d) = %q, want %q", tt.command, tt.exitCode, got, tt.want)
			}
		})
	}
}

func TestStaleJSONExitCode(t *testing.T) {
	tests := []struct {
		name      string
		printCode int
		isStale   bool
		want      int
	}{
		{name: "current", printCode: exitSuccess, want: exitSuccess},
		{name: "stale", printCode: exitSuccess, isStale: true, want: exitStale},
		{name: "print error current", printCode: exitError, want: exitError},
		{name: "print error stale", printCode: exitError, isStale: true, want: exitError},
		{name: "unexpected print failure", printCode: 99, isStale: true, want: exitError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := staleJSONExitCode(tt.printCode, tt.isStale); got != tt.want {
				t.Fatalf("staleJSONExitCode(%d, %t) = %d, want %d", tt.printCode, tt.isStale, got, tt.want)
			}
		})
	}
}
