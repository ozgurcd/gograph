package validation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ozgurcd/gograph/internal/buildctx"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/repositoryfingerprint"
	"github.com/ozgurcd/gograph/internal/scanner"
)

func TestRepositoryLoaderFingerprintsAndFreshness(t *testing.T) {
	root, graphBytes := writeRepositoryFixture(t)
	loader := RepositoryLoader{}
	snapshot, err := loader.Load(context.Background(), root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if snapshot.Freshness != "current" || len(snapshot.SourceFingerprint) != 64 || len(snapshot.GraphFingerprint) != 64 {
		t.Fatalf("snapshot fingerprints/freshness = %+v", snapshot)
	}
	wantGraph := sha256.Sum256(graphBytes)
	if snapshot.GraphFingerprint != hex.EncodeToString(wantGraph[:]) {
		t.Fatalf("graph fingerprint = %s", snapshot.GraphFingerprint)
	}
	if err := loader.VerifyCurrent(context.Background(), snapshot); err != nil {
		t.Fatalf("VerifyCurrent() error = %v", err)
	}
}

func TestRepositoryLoaderRejectsOversizedGraphBeforeDecode(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, graphPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(graph.MaxArtifactBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = (RepositoryLoader{}).Load(context.Background(), root)
	var snapshotErr *SnapshotError
	if !errors.As(err, &snapshotErr) || snapshotErr.Reason != ReasonGraphInvalid || !strings.Contains(err.Error(), "exceeds read limit") {
		t.Fatalf("oversized validation graph error = %v, want graph_invalid size rejection", err)
	}
}

func TestRepositoryLoaderRejectsMissingAndStaleGraphs(t *testing.T) {
	t.Run("missing graph", func(t *testing.T) {
		root := writeGoRepository(t)
		_, err := (RepositoryLoader{}).Load(context.Background(), root)
		assertSnapshotReason(t, err, ReasonGraphMissing)
	})

	t.Run("malformed graph", func(t *testing.T) {
		root := writeGoRepository(t)
		if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(root, graphPath), `{`)
		_, err := (RepositoryLoader{}).Load(context.Background(), root)
		assertSnapshotReason(t, err, ReasonGraphInvalid)
	})

	t.Run("duplicate graph key", func(t *testing.T) {
		root := writeGoRepository(t)
		if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(root, graphPath), `{"version":"2","version":"2"}`)
		_, err := (RepositoryLoader{}).Load(context.Background(), root)
		assertSnapshotReason(t, err, ReasonGraphInvalid)
	})

	t.Run("source changed", func(t *testing.T) {
		root, _ := writeRepositoryFixture(t)
		writeFile(t, filepath.Join(root, "pkg.go"), "package fixture\nfunc Changed() {}\n")
		_, err := (RepositoryLoader{}).Load(context.Background(), root)
		assertSnapshotReason(t, err, ReasonGraphStale)
	})

	t.Run("metadata changed", func(t *testing.T) {
		root, _ := writeRepositoryFixture(t)
		writeFile(t, filepath.Join(root, "go.mod"), "module example.com/fixture\n\n// freshness change\ngo 1.25\n")
		_, err := (RepositoryLoader{}).Load(context.Background(), root)
		assertSnapshotReason(t, err, ReasonGraphStale)
	})

	t.Run("build context mismatch", func(t *testing.T) {
		root, _ := writeRepositoryFixture(t)
		graphFile := filepath.Join(root, graphPath)
		data, err := os.ReadFile(graphFile)
		if err != nil {
			t.Fatal(err)
		}
		var persisted graph.Graph
		if err := json.Unmarshal(data, &persisted); err != nil {
			t.Fatal(err)
		}
		persisted.Build.BuildContextFingerprint = "different"
		writeGraph(t, root, &persisted)
		_, err = (RepositoryLoader{}).Load(context.Background(), root)
		assertSnapshotReason(t, err, ReasonGraphStale)
	})

	t.Run("missing persisted source fingerprint", func(t *testing.T) {
		root, _ := writeRepositoryFixture(t)
		graphFile := filepath.Join(root, graphPath)
		data, err := os.ReadFile(graphFile)
		if err != nil {
			t.Fatal(err)
		}
		var persisted graph.Graph
		if err := json.Unmarshal(data, &persisted); err != nil {
			t.Fatal(err)
		}
		persisted.Build.SourceFingerprint = ""
		writeGraph(t, root, &persisted)
		_, err = (RepositoryLoader{}).Load(context.Background(), root)
		assertSnapshotReason(t, err, ReasonGraphStale)
	})

	t.Run("unsupported graph schema", func(t *testing.T) {
		root, _ := writeRepositoryFixture(t)
		persisted := readFixtureGraph(t, root)
		persisted.Version = "999"
		writeGraph(t, root, persisted)
		_, err := (RepositoryLoader{}).Load(context.Background(), root)
		assertSnapshotReason(t, err, ReasonGraphSchemaUnsupported)
	})

	t.Run("unsupported source policy", func(t *testing.T) {
		root, _ := writeRepositoryFixture(t)
		persisted := readFixtureGraph(t, root)
		persisted.Build.SourcePolicyVersion = 0
		writeGraph(t, root, persisted)
		_, err := (RepositoryLoader{}).Load(context.Background(), root)
		assertSnapshotReason(t, err, ReasonSourcePolicyUnsupported)
	})
}

func TestRepositoryLoaderRejectsUnsafeMetadata(t *testing.T) {
	root := writeGoRepository(t)
	outside := filepath.Join(t.TempDir(), "outside.sum")
	writeFile(t, outside, "outside")
	if err := os.Symlink(outside, filepath.Join(root, "go.sum")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := (RepositoryLoader{}).Load(context.Background(), root)
	if err == nil {
		t.Fatal("Load() unexpectedly accepted linked build metadata")
	}
}

func TestCaptureSourceStateKeepsExplicitValidationBoundary(t *testing.T) {
	base := t.TempDir()
	repository := filepath.Join(base, "repository")
	root := filepath.Join(repository, "app")
	sibling := filepath.Join(repository, "shared")
	for _, directory := range []string{filepath.Join(repository, ".git"), root, sibling} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{
		filepath.Join(root, "go.mod"):       "module example.com/app\n\ngo 1.24\n",
		filepath.Join(root, "main.go"):      "package app\n",
		filepath.Join(root, "go.work"):      "go 1.24\n\nuse (\n\t.\n\t../shared\n)\n",
		filepath.Join(sibling, "go.mod"):    "module example.com/shared\n\ngo 1.24\n",
		filepath.Join(sibling, "shared.go"): "package shared\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOWORK", "auto")
	t.Setenv("GO111MODULE", "")

	if _, err := captureSourceState(context.Background(), root, nil, false); err == nil {
		t.Fatal("captureSourceState explicit validation authority accepted sibling module")
	} else {
		var snapshotErr *SnapshotError
		if !errors.As(err, &snapshotErr) || snapshotErr.Diagnostic.Code != "build_context_escape" {
			t.Fatalf("captureSourceState strict error = %v", err)
		}
	}
	if _, err := captureSourceState(context.Background(), root, nil, true); err != nil {
		t.Fatalf("captureSourceState checkout authority = %v", err)
	}
}

func writeRepositoryFixture(t *testing.T) (string, []byte) {
	t.Helper()
	root := writeGoRepository(t)
	config, err := buildctx.Resolve(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	paths, selectionFingerprint, errs := scanner.WalkWithConfigAndFingerprint(root, config)
	if len(errs) != 0 {
		t.Fatalf("source selection errors: %v", errs)
	}
	identity, err := repositoryfingerprint.Compute(context.Background(), root, config, paths)
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join(root, "pkg.go"))
	if err != nil {
		t.Fatal(err)
	}
	persisted := &graph.Graph{
		Version: graph.Version, GeneratedAt: time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC), Root: root,
		Build: &graph.BuildMetadata{
			ScannedFiles: 1, ParsedFiles: 1, Complete: true, Precision: graph.PrecisionAST,
			SourcePolicyVersion:     graph.CurrentSourcePolicyVersion,
			BuildContextFingerprint: selectionFingerprint,
			SourceFingerprint:       identity.Fingerprint,
		},
		Files: []graph.FileNode{{Path: "pkg.go", PackageName: "fixture", ContentDigest: graph.SourceDigest(source)}},
	}
	return root, writeGraph(t, root, persisted)
}

func writeGoRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/fixture\n\ngo 1.25\n")
	writeFile(t, filepath.Join(root, "pkg.go"), "package fixture\nfunc Present() {}\n")
	return root
}

func writeGraph(t *testing.T, root string, persisted *graph.Graph) []byte {
	t.Helper()
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileBytes(t, filepath.Join(root, graphPath), data)
	return data
}

func readFixtureGraph(t *testing.T, root string) *graph.Graph {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, graphPath))
	if err != nil {
		t.Fatal(err)
	}
	var persisted graph.Graph
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	return &persisted
}

func writeFile(t *testing.T, name, contents string) {
	t.Helper()
	writeFileBytes(t, name, []byte(contents))
}

func writeFileBytes(t *testing.T, name string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(name, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertSnapshotReason(t *testing.T, err error, want Reason) {
	t.Helper()
	var typed *SnapshotError
	if !errors.As(err, &typed) || typed.Reason != want {
		t.Fatalf("error = %v, want SnapshotError reason %s", err, want)
	}
}
