package cli_test

import (
	"bytes"
	"fmt"
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
	testBinaryOnce     sync.Once
	testBinaryPath     string
	testBinaryDir      string
	testBinaryVersion  string
	testBinaryErr      error
	testRepositoryRoot string
)

func TestMain(m *testing.M) {
	root, err := findTestRepositoryRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "prepare CLI tests: %v\n", err)
		os.Exit(1)
	}
	testRepositoryRoot = root

	code := m.Run()
	if testBinaryDir != "" {
		if err := os.RemoveAll(testBinaryDir); err != nil {
			fmt.Fprintf(os.Stderr, "clean CLI test binary directory: %v\n", err)
			if code == 0 {
				code = 1
			}
		}
	}
	os.Exit(code)
}

func findTestRepositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get package working directory: %w", err)
	}
	for {
		modPath := filepath.Join(dir, "go.mod")
		data, readErr := os.ReadFile(modPath)
		if readErr == nil && bytes.Contains(data, []byte("module github.com/ozgurcd/gograph")) {
			return filepath.Clean(dir), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("find repository root from %s", dir)
		}
		dir = parent
	}
}

// buildTestBinary compiles the current checkout exactly once per package test
// execution. The binary lives outside the repository so ignored historical
// artifacts can neither be reused by tests nor discovered by repository scans.
func buildTestBinary(t *testing.T) string {
	t.Helper()
	testBinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "gograph-cli-test-bin-*")
		if err != nil {
			testBinaryErr = fmt.Errorf("create CLI test binary directory: %w", err)
			return
		}
		testBinaryDir = dir

		name := "gograph"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		testBinaryPath = filepath.Join(dir, name)
		testBinaryVersion = fmt.Sprintf("0.0.0-test.%d.%d", os.Getpid(), time.Now().UnixNano())
		marker := "gograph-release-version=/" + testBinaryVersion + "/"
		ldflags := "-X main.version=" + testBinaryVersion + " -X main.releaseVersionMarker=" + marker

		cmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", testBinaryPath, "./cmd/gograph")
		cmd.Dir = testRepositoryRoot
		output, err := cmd.CombinedOutput()
		if err != nil {
			testBinaryErr = fmt.Errorf("build current CLI test binary: %w\nOutput: %s", err, output)
		}
	})
	if testBinaryErr != nil {
		t.Fatal(testBinaryErr)
	}
	return testBinaryPath
}

func TestCLIExecutableTestsUseCurrentEphemeralBinary(t *testing.T) {
	bin := buildTestBinary(t)
	rel, err := filepath.Rel(testRepositoryRoot, bin)
	if err != nil {
		t.Fatalf("compare test binary with repository root: %v", err)
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("test binary must be outside the repository, got %s", bin)
	}
	for _, stalePath := range []string{
		filepath.Join(testRepositoryRoot, "bin", "gograph"),
		filepath.Join(testRepositoryRoot, "bin", "gograph-test"),
	} {
		if filepath.Clean(bin) == filepath.Clean(stalePath) {
			t.Fatalf("test binary reused repository artifact %s", stalePath)
		}
	}

	cmd := exec.Command(bin, "version")
	cmd.Dir = testRepositoryRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run current CLI test binary: %v\n%s", err, output)
	}
	want := "gograph version v" + testBinaryVersion
	if strings.TrimSpace(string(output)) != want {
		t.Fatalf("test binary version = %q, want %q", strings.TrimSpace(string(output)), want)
	}
}
