package mcpbundle

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	nativeBinaryOnce sync.Once
	nativeBinaryData []byte
	nativeBinaryErr  error
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func nativeTarget(t *testing.T) Target {
	t.Helper()
	target, ok := TargetFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		t.Skipf("unsupported native target %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return target
}

func nativeBinary(t *testing.T) []byte {
	t.Helper()
	target := nativeTarget(t)
	nativeBinaryOnce.Do(func() {
		directory, err := os.MkdirTemp("", "gograph-mcpbundle-test-")
		if err != nil {
			nativeBinaryErr = err
			return
		}
		defer func() { _ = os.RemoveAll(directory) }()
		output := filepath.Join(directory, target.ExecutableName())
		command := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-ldflags=-s -w -X main.version="+testVersion+" -X main.releaseVersionMarker=gograph-release-version=/"+testVersion+"/", "-o", output, "./cmd/gograph")
		command.Dir = repositoryRoot(t)
		command.Env = targetBuildEnvironment(target)
		combined, err := command.CombinedOutput()
		if err != nil {
			nativeBinaryErr = fmt.Errorf("build native test binary: %w\n%s", err, combined)
			return
		}
		nativeBinaryData, nativeBinaryErr = os.ReadFile(output)
	})
	if nativeBinaryErr != nil {
		t.Fatal(nativeBinaryErr)
	}
	return append([]byte(nil), nativeBinaryData...)
}

func repositoryLicense(t *testing.T) []byte {
	t.Helper()
	license, err := os.ReadFile(filepath.Join(repositoryRoot(t), "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	return license
}

func TestBuildBundleIsDeterministicAndVerifiable(t *testing.T) {
	target := nativeTarget(t)
	manifest, err := NewManifest(testVersion, target)
	if err != nil {
		t.Fatal(err)
	}
	first, firstHash, err := BuildBundle(manifest, nativeBinary(t), repositoryLicense(t))
	if err != nil {
		t.Fatal(err)
	}
	second, secondHash, err := BuildBundle(manifest, nativeBinary(t), repositoryLicense(t))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || firstHash != secondHash {
		t.Fatal("identical inputs produced different MCPB bytes or hashes")
	}
	verified, err := VerifyBundle(first, target, testVersion, firstHash)
	if err != nil {
		t.Fatal(err)
	}
	if verified.SHA256 != firstHash || verified.Target != target || verified.Manifest.Version != testVersion || verified.BinarySHA256 == "" {
		t.Fatalf("unexpected verification: %+v", verified)
	}
}

func TestValidateBinaryRejectsDependencyVersionCollision(t *testing.T) {
	target := nativeTarget(t)
	// github.com/spf13/cast v1.7.1 is embedded in current module build metadata;
	// a plain substring check would incorrectly accept this stale binary.
	if err := ValidateBinary(nativeBinary(t), target, "1.7.1"); err == nil {
		t.Fatal("binary was accepted using a dependency version instead of the linked release marker")
	}
}

func TestValidateBinaryRejectsReleaseVersionPrefix(t *testing.T) {
	target := nativeTarget(t)
	const linkedVersion = "1.5.0-rc.1"
	output := filepath.Join(t.TempDir(), target.ExecutableName())
	command := exec.Command(
		"go", "build", "-buildvcs=false", "-trimpath",
		"-ldflags=-s -w -X main.version="+linkedVersion+" -X main.releaseVersionMarker=gograph-release-version=/"+linkedVersion+"/",
		"-o", output, "./cmd/gograph",
	)
	command.Dir = repositoryRoot(t)
	command.Env = targetBuildEnvironment(target)
	if combined, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build prefix-collision binary: %v\n%s", err, combined)
	}
	binary, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateBinary(binary, target, "1.5.0"); err == nil {
		t.Fatal("prerelease binary was accepted for its release-version prefix")
	}
}

func TestVerifyBundleRejectsMalformedOrInconsistentArtifacts(t *testing.T) {
	target := nativeTarget(t)
	manifest, err := NewManifest(testVersion, target)
	if err != nil {
		t.Fatal(err)
	}
	valid, validHash, err := BuildBundle(manifest, nativeBinary(t), repositoryLicense(t))
	if err != nil {
		t.Fatal(err)
	}
	entries := readTestZIP(t, valid)

	var otherTarget Target
	if target.GOARCH == "amd64" {
		otherTarget, _ = TargetFor(target.GOOS, "arm64")
	} else {
		otherTarget, _ = TargetFor(target.GOOS, "amd64")
	}
	badArgs := manifest
	badArgs.Server.MCPConfig.Args = []string{"mcp ${user_config.project_directory}"}
	badArgsJSON, err := json.MarshalIndent(badArgs, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	badArgsJSON = append(badArgsJSON, '\n')

	tests := []struct {
		name         string
		bundle       []byte
		verifyTarget Target
		version      string
		hash         string
	}{
		{name: "not zip", bundle: []byte("not a zip"), verifyTarget: target, version: testVersion},
		{name: "wrong digest", bundle: valid, verifyTarget: target, version: testVersion, hash: strings.Repeat("0", 64)},
		{name: "uppercase digest", bundle: valid, verifyTarget: target, version: testVersion, hash: strings.ToUpper(validHash)},
		{name: "wrong target", bundle: valid, verifyTarget: otherTarget, version: testVersion},
		{name: "wrong expected version", bundle: valid, verifyTarget: target, version: "1.5.1"},
		{name: "missing license", bundle: writeTestZIP(t, withoutEntry(entries, "LICENSE")), verifyTarget: target, version: testVersion},
		{name: "duplicate manifest", bundle: writeTestZIP(t, append(cloneEntries(entries), entries[0])), verifyTarget: target, version: testVersion},
		{name: "unsafe traversal", bundle: writeTestZIP(t, append(cloneEntries(entries), testZIPEntry{name: "../escape", data: []byte("x"), mode: 0o644, method: zip.Deflate, modified: zipEpoch})), verifyTarget: target, version: testVersion},
		{name: "unexpected entry", bundle: writeTestZIP(t, append(cloneEntries(entries), testZIPEntry{name: "README.md", data: []byte("x"), mode: 0o644, method: zip.Deflate, modified: zipEpoch})), verifyTarget: target, version: testVersion},
		{name: "non executable binary", bundle: writeTestZIP(t, mutateEntry(entries, target.ServerPath(), func(entry *testZIPEntry) { entry.mode = 0o644 })), verifyTarget: target, version: testVersion},
		{name: "combined argv", bundle: writeTestZIP(t, mutateEntry(entries, "manifest.json", func(entry *testZIPEntry) { entry.data = badArgsJSON })), verifyTarget: target, version: testVersion},
		{name: "corrupt binary", bundle: writeTestZIP(t, mutateEntry(entries, target.ServerPath(), func(entry *testZIPEntry) { entry.data = []byte("not gograph") })), verifyTarget: target, version: testVersion},
		{name: "stored entry", bundle: writeTestZIP(t, mutateEntry(entries, "LICENSE", func(entry *testZIPEntry) { entry.method = zip.Store })), verifyTarget: target, version: testVersion},
		{name: "variable timestamp", bundle: writeTestZIP(t, mutateEntry(entries, "LICENSE", func(entry *testZIPEntry) { entry.modified = zipEpoch.Add(2 * time.Second) })), verifyTarget: target, version: testVersion},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := VerifyBundle(test.bundle, test.verifyTarget, test.version, test.hash); err == nil {
				t.Fatal("invalid MCPB was accepted")
			}
		})
	}
}

func TestBuildAllVerifyAllAndSmokeNative(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-compiles all release targets")
	}
	// Cold CI runners cross-compile all six targets while other package tests
	// may be doing the same work. Keep a hard bound without making 3 minutes a
	// hidden performance requirement for release-bundle verification.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	output := t.TempDir()
	artifacts, err := BuildAll(ctx, repositoryRoot(t), output, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != len(Targets) {
		t.Fatalf("built %d artifacts, want %d", len(artifacts), len(Targets))
	}
	hashes := make(map[string]string, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.Name != artifact.Target.ArtifactName(testVersion) || artifact.SHA256 == "" || artifact.Size == 0 {
			t.Fatalf("invalid artifact metadata: %+v", artifact)
		}
		hashes[artifact.Name] = artifact.SHA256
	}
	if _, err := VerifyAllHashes(output, testVersion, hashes); err != nil {
		t.Fatal(err)
	}

	// A repeated build must produce identical bytes and safely reuse them.
	rebuilt, err := BuildAll(ctx, repositoryRoot(t), output, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range rebuilt {
		if hashes[artifact.Name] != artifact.SHA256 {
			t.Fatalf("non-deterministic hash for %s: %s then %s", artifact.Name, hashes[artifact.Name], artifact.SHA256)
		}
	}

	project := filepath.Join(t.TempDir(), "project with spaces;no-shell")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module example.com/mcpbsmoke\n\ngo 1.27.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	smoke, err := SmokeNative(ctx, output, project, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	if smoke.ServerName != ServerName || smoke.ServerVersion != testVersion || len(smoke.ToolNames) == 0 {
		t.Fatalf("unexpected smoke result: %+v", smoke)
	}

	first := artifacts[0]
	firstData, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(first.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAll(output, testVersion); err == nil {
		t.Fatal("missing asset was accepted")
	}
	if err := os.WriteFile(first.Path, firstData, 0o644); err != nil {
		t.Fatal(err)
	}
	badHashes := make(map[string]string, len(hashes))
	for name, hash := range hashes {
		badHashes[name] = hash
	}
	badHashes[first.Name] = strings.Repeat("0", 64)
	if _, err := VerifyAllHashes(output, testVersion, badHashes); err == nil {
		t.Fatal("mismatched asset hash was accepted")
	}
	if err := os.WriteFile(filepath.Join(output, "unexpected.mcpb"), firstData, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAll(output, testVersion); err == nil {
		t.Fatal("unexpected MCPB asset was accepted")
	}
}

type testZIPEntry struct {
	name     string
	data     []byte
	mode     os.FileMode
	method   uint16
	modified time.Time
}

func readTestZIP(t *testing.T, data []byte) []testZIPEntry {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]testZIPEntry, 0, len(reader.File))
	for _, file := range reader.File {
		opened, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(opened)
		_ = opened.Close()
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, testZIPEntry{name: file.Name, data: content, mode: file.Mode(), method: file.Method, modified: file.Modified})
	}
	return entries
}

func writeTestZIP(t *testing.T, entries []testZIPEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: entry.method, Modified: entry.modified}
		header.SetMode(entry.mode)
		part, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func cloneEntries(entries []testZIPEntry) []testZIPEntry {
	cloned := make([]testZIPEntry, len(entries))
	for index, entry := range entries {
		cloned[index] = entry
		cloned[index].data = append([]byte(nil), entry.data...)
	}
	return cloned
}

func withoutEntry(entries []testZIPEntry, name string) []testZIPEntry {
	result := make([]testZIPEntry, 0, len(entries)-1)
	for _, entry := range cloneEntries(entries) {
		if entry.name != name {
			result = append(result, entry)
		}
	}
	return result
}

func mutateEntry(entries []testZIPEntry, name string, mutate func(*testZIPEntry)) []testZIPEntry {
	result := cloneEntries(entries)
	for index := range result {
		if result[index].name == name {
			mutate(&result[index])
			return result
		}
	}
	panic("test entry not found: " + name)
}
