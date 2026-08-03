package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestRunStaleJSONContextOnlyUsesNonEmptyEnvelope(t *testing.T) {
	setDeterministicBuildEnvironment(t, "")
	root := t.TempDir()
	writeBuildContextFixture(t, root, "active.go", "package fixture\n")
	if err := os.MkdirAll(filepath.Join(root, outputDir), 0o750); err != nil {
		t.Fatal(err)
	}
	g := &graph.Graph{
		Version:     graph.Version,
		Root:        root,
		GeneratedAt: time.Now().Add(time.Hour),
		Files:       []graph.FileNode{{Path: "active.go"}},
		Build:       &graph.BuildMetadata{BuildContextFingerprint: "different-context"},
	}
	if err := writeJSON(filepath.Join(root, graphFile), g); err != nil {
		t.Fatal(err)
	}

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(originalWD) }()

	originalJSONMode := jsonMode
	jsonMode = true
	defer func() { jsonMode = originalJSONMode }()

	originalStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	defer func() { os.Stdout = originalStdout }()

	if code := runStale(); code != exitStale {
		t.Fatalf("runStale exit code = %d, want %d", code, exitStale)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}

	var envelope struct {
		Status  string             `json:"status"`
		Count   int                `json:"count"`
		Results search.StaleResult `json:"results"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		t.Fatalf("decode stale JSON: %v\n%s", err, output)
	}
	if envelope.Status != "ok" || envelope.Count != 1 {
		t.Fatalf("envelope = status:%q count:%d, want ok/1", envelope.Status, envelope.Count)
	}
	if !envelope.Results.IsStale || !envelope.Results.BuildContextChanged {
		t.Fatalf("context-only stale result = %+v", envelope.Results)
	}
	if envelope.Results.ChangedFiles == nil || len(envelope.Results.ChangedFiles) != 0 {
		t.Fatalf("changed_files = %#v, want []", envelope.Results.ChangedFiles)
	}
}
