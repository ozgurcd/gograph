package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMakeVulnerabilityChecksUseFreshExplicitInputs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("release Makefile uses a POSIX shell")
	}
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is unavailable")
	}
	repositoryRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	native := dryRunMake(t, makePath, repositoryRoot, "vulnerability-check", "GRYPE=grype-contract")
	assertOrderedText(t, native,
		"go build ",
		"grype-contract file:go.mod --fail-on high",
		"grype-contract file:bin/gograph --fail-on high",
	)
	if strings.Contains(native, "grype-contract dir:.") {
		t.Fatalf("native vulnerability gate scans ambient repository artifacts:\n%s", native)
	}

	const output = ".release-work/makefile-contract"
	release := dryRunMake(t, makePath, repositoryRoot,
		"release-artifact-vulnerability-check",
		"MCPB_OUTPUT="+output,
		"GRYPE=grype-contract",
	)
	assertOrderedText(t, release,
		"goreleaser/v2@v2.17.0 release",
		`for artifact in "`+output+`/goreleaser-dist"/gograph_*.tar.gz`,
		`if [ "$count" -ne 6 ]`,
		output+`/goreleaser-dist/gograph_Darwin_arm64.tar.gz`,
		`grype-contract "file:$artifact" --fail-on high`,
	)
	if strings.Contains(release, "grype-contract dir:.") || strings.Contains(release, "file:dist/") {
		t.Fatalf("release vulnerability gate scans ambient repository artifacts:\n%s", release)
	}
}

func TestMakeReleaseArtifactScanRequiresTheExactArchiveSet(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("release Makefile uses a POSIX shell")
	}
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is unavailable")
	}
	repositoryRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dist := t.TempDir()
	names := []string{
		"gograph_Darwin_arm64.tar.gz",
		"gograph_Darwin_x86_64.tar.gz",
		"gograph_Linux_arm64.tar.gz",
		"gograph_Linux_x86_64.tar.gz",
		"gograph_Windows_arm64.zip",
		"gograph_Windows_x86_64.zip",
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dist, name), []byte("fixture"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	args := []string{"--no-print-directory", "scan-release-artifacts", "RELEASE_DIST=" + dist, "GRYPE=true"}
	if output, err := runMake(makePath, repositoryRoot, args...); err != nil {
		t.Fatalf("exact release artifact scan failed: %v\n%s", err, output)
	}
	failingArgs := []string{"--no-print-directory", "scan-release-artifacts", "RELEASE_DIST=" + dist, "GRYPE=false"}
	if output, err := runMake(makePath, repositoryRoot, failingArgs...); err == nil {
		t.Fatalf("release artifact scanner ignored Grype failure:\n%s", output)
	}

	missing := filepath.Join(dist, names[0])
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(dist, "gograph_Unexpected_arm64.tar.gz")
	if err := os.WriteFile(extra, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err := runMake(makePath, repositoryRoot, args...)
	if err == nil || !strings.Contains(string(output), "Missing expected release archive") {
		t.Fatalf("replacement archive was accepted: %v\n%s", err, output)
	}

	if err := os.WriteFile(missing, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err = runMake(makePath, repositoryRoot, args...)
	if err == nil || !strings.Contains(string(output), "Expected 6 freshly generated release archives, found 7") {
		t.Fatalf("extra release archive was accepted: %v\n%s", err, output)
	}
}

func TestGitHubWorkflowsUseTheCurrentInputVulnerabilityGates(t *testing.T) {
	repositoryRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path      string
		required  []string
		forbidden []string
	}{
		{
			path: ".github/workflows/ci.yml",
			required: []string{
				"anchore/scan-action/download-grype@e1165082ffb1fe366ebaf02d8526e7c4989ea9d2",
				"grype-version: v0.116.1",
				"go test -count=1 -v ./...",
				"go test -count=1 -race ./...",
				"make vulnerability-check",
			},
			forbidden: []string{"grype dir:."},
		},
		{
			path: ".github/workflows/release.yml",
			required: []string{
				"anchore/scan-action/download-grype@e1165082ffb1fe366ebaf02d8526e7c4989ea9d2",
				"grype-version: v0.116.1",
				"go test -count=1 -v ./...",
				"go test -count=1 -race ./...",
				"make vulnerability-check",
				"make scan-release-artifacts RELEASE_DIST=dist",
			},
			forbidden: []string{"grype dir:."},
		},
	}
	for _, test := range tests {
		data, err := os.ReadFile(filepath.Join(repositoryRoot, test.path))
		if err != nil {
			t.Fatal(err)
		}
		contents := string(data)
		for _, required := range test.required {
			if !strings.Contains(contents, required) {
				t.Errorf("%s is missing %q", test.path, required)
			}
		}
		for _, forbidden := range test.forbidden {
			if strings.Contains(contents, forbidden) {
				t.Errorf("%s contains ambient scan %q", test.path, forbidden)
			}
		}
	}
}

func TestMakeReleaseTestsDisableGoTestCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("release Makefile uses a POSIX shell")
	}
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is unavailable")
	}
	repositoryRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	output := dryRunMake(t, makePath, repositoryRoot, "test", "GRYPE=grype-contract")
	for _, command := range []string{
		"go test -count=1 -v ./...",
		"go test -count=1 -race ./...",
	} {
		if !strings.Contains(output, command) {
			t.Fatalf("make test is missing %q:\n%s", command, output)
		}
	}
}

func dryRunMake(t *testing.T, makePath, repositoryRoot string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"--dry-run", "--no-print-directory"}, args...)
	command := exec.Command(makePath, commandArgs...)
	command.Dir = repositoryRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("make %s failed: %v\n%s", strings.Join(commandArgs, " "), err, output)
	}
	return string(output)
}

func runMake(makePath, repositoryRoot string, args ...string) ([]byte, error) {
	command := exec.Command(makePath, args...)
	command.Dir = repositoryRoot
	return command.CombinedOutput()
}

func assertOrderedText(t *testing.T, output string, fragments ...string) {
	t.Helper()
	position := 0
	for _, fragment := range fragments {
		index := strings.Index(output[position:], fragment)
		if index < 0 {
			t.Fatalf("output is missing %q after byte %d:\n%s", fragment, position, output)
		}
		position += index + len(fragment)
	}
}
