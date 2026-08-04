package projectfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadConfigConfinesRelativePaths(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside.json")
	const sentinel = "BENIGN-OUTSIDE-CONFIG-SENTINEL"
	if err := os.WriteFile(outside, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".gograph", "checks.json")); err != nil {
		t.Skipf("create config symlink: %v", err)
	}

	data, _, found, err := ReadConfig(root, "", ".gograph/checks.json")
	if err == nil || found || strings.Contains(string(data), sentinel) || strings.Contains(err.Error(), sentinel) {
		t.Fatalf("linked default result = found %v, data %q, err %v", found, data, err)
	}
	if _, _, _, err := ReadConfig(root, "../outside.json", ""); err == nil {
		t.Fatal("relative traversal was accepted")
	}
}

func TestReadConfigAllowsRegularRelativeAndExplicitAbsoluteFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, ".gograph", "checks.json")
	if err := os.WriteFile(inside, []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, resolved, found, err := ReadConfig(root, "", ".gograph/checks.json")
	if err != nil || !found || string(data) != "inside" || resolved != inside {
		t.Fatalf("relative result = %q, %q, %v, %v", data, resolved, found, err)
	}

	abs := filepath.Join(t.TempDir(), "checks.json")
	if err := os.WriteFile(abs, []byte("absolute"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, resolved, found, err = ReadConfig(root, abs, "")
	if err != nil || !found || string(data) != "absolute" || resolved != abs {
		t.Fatalf("absolute result = %q, %q, %v, %v", data, resolved, found, err)
	}
}

func TestReadConfigTreatsMissingDefaultAsAbsent(t *testing.T) {
	root := t.TempDir()
	data, _, found, err := ReadConfig(root, "", ".gograph/checks.json")
	if err != nil || found || data != nil {
		t.Fatalf("missing default = %q, %v, %v", data, found, err)
	}
	if _, _, _, err := ReadConfig(root, "missing.json", ""); err == nil {
		t.Fatal("missing explicit config was accepted")
	}
}
