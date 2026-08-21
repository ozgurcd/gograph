package repositoryfingerprint

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ozgurcd/gograph/internal/buildctx"
	"github.com/ozgurcd/gograph/internal/scanner"
)

func TestFingerprintIsDeterministicAndLocationIndependent(t *testing.T) {
	first := fixtureFingerprint(t, "module example.com/fixture\n\ngo 1.25\n", "package fixture\nfunc Present() {}\n")
	second := fixtureFingerprint(t, "module example.com/fixture\n\ngo 1.25\n", "package fixture\nfunc Present() {}\n")
	if first != second || len(first) != 64 {
		t.Fatalf("fingerprints = %q and %q", first, second)
	}
}

func TestFingerprintChangesWithSourceOrMetadata(t *testing.T) {
	base := fixtureFingerprint(t, "module example.com/fixture\n\ngo 1.25\n", "package fixture\nfunc Present() {}\n")
	changedSource := fixtureFingerprint(t, "module example.com/fixture\n\ngo 1.25\n", "package fixture\nfunc Changed() {}\n")
	changedMetadata := fixtureFingerprint(t, "module example.com/fixture\n\n// changed\ngo 1.25\n", "package fixture\nfunc Present() {}\n")
	if base == changedSource || base == changedMetadata {
		t.Fatalf("fingerprint did not change: base=%s source=%s metadata=%s", base, changedSource, changedMetadata)
	}
}

func fixtureFingerprint(t *testing.T, module, source string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := buildctx.Resolve(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	paths, _, errs := scanner.WalkWithConfigAndFingerprint(root, config)
	if len(errs) != 0 {
		t.Fatalf("selection errors: %v", errs)
	}
	result, err := Compute(context.Background(), root, config, paths)
	if err != nil {
		t.Fatal(err)
	}
	return result.Fingerprint
}
