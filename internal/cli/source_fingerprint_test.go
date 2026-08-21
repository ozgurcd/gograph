package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ozgurcd/gograph/internal/validation"
)

func TestRunBuildPersistsCurrentSourceFingerprint(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/fingerprint\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package fingerprint\nfunc Present() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if exit := runBuild([]string{root}); exit != 0 {
		t.Fatalf("runBuild() exit = %d", exit)
	}
	persisted, err := loadGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Build == nil || len(persisted.Build.SourceFingerprint) != 64 {
		t.Fatalf("source fingerprint missing: %+v", persisted.Build)
	}
	snapshot, err := (validation.RepositoryLoader{}).Load(context.Background(), root)
	if err != nil {
		t.Fatalf("newly built graph is not validation-current: %v", err)
	}
	if snapshot.SourceFingerprint != persisted.Build.SourceFingerprint {
		t.Fatalf("source fingerprint = %s, persisted = %s", snapshot.SourceFingerprint, persisted.Build.SourceFingerprint)
	}
}
