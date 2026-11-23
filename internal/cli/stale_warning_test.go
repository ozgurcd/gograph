package cli

import (
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/search"
)

func TestStaleGraphWarningNamesChangedFilesAndRebuild(t *testing.T) {
	if warning := staleGraphWarning(search.StaleResult{}); warning != "" {
		t.Fatalf("current graph warned: %q", warning)
	}
	warning := staleGraphWarning(search.StaleResult{IsStale: true, ChangedFiles: []string{"a.go", "b.go", "c.go", "d.go"}})
	for _, want := range []string{"4 selected file(s) changed", "a.go, b.go, c.go", "gograph build ."} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning %q lacks %q", warning, want)
		}
	}
	if strings.Contains(warning, "d.go") {
		t.Errorf("warning lists more than three files: %q", warning)
	}
	if warning := staleGraphWarning(search.StaleResult{IsStale: true, BuildContextChanged: true}); !strings.Contains(warning, "build context") {
		t.Errorf("build-context warning = %q", warning)
	}
}

func TestStaleWarningSkipsCommandsThatReportFreshness(t *testing.T) {
	for _, command := range []string{"stale", "stats", "changes", "build", "mcp"} {
		if commandWarnsWhenStale(command) {
			t.Errorf("%s should not add a stale warning", command)
		}
	}
	for _, command := range []string{"query", "strings", "callers", "source", "tests"} {
		if !commandWarnsWhenStale(command) {
			t.Errorf("%s should warn on a stale graph", command)
		}
	}
}

func TestStringsSupportsJSONAndFilesOnly(t *testing.T) {
	support := supportedOutputModes([]string{"strings"})
	if support&outputJSON == 0 || support&outputFilesOnly == 0 {
		t.Fatalf("strings output modes = %v", support)
	}
}
