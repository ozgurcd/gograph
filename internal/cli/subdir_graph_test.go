package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	testBinaryOnce sync.Once
	testBinaryPath string
	testBinaryErr  error
)

// buildTestBinary always compiles the current source once per package test run.
func buildTestBinary(t *testing.T) string {
	t.Helper()
	testBinaryOnce.Do(func() {
		repoRoot, err := filepath.Abs("../..")
		if err != nil {
			testBinaryErr = fmt.Errorf("resolve repo root: %w", err)
			return
		}
		binDir, err := os.MkdirTemp("", "gograph-test-bin-*")
		if err != nil {
			testBinaryErr = fmt.Errorf("create test bin dir: %w", err)
			return
		}
		testBinaryPath = filepath.Join(binDir, "gograph")
		cmd := exec.Command("go", "build", "-o", testBinaryPath, filepath.Join(repoRoot, "cmd", "gograph", "main.go"))
		cmd.Dir = repoRoot
		out, err := cmd.CombinedOutput()
		if err != nil {
			testBinaryErr = fmt.Errorf("build test binary: %w\nOutput: %s", err, out)
		}
	})
	if testBinaryErr != nil {
		t.Fatal(testBinaryErr)
	}
	return testBinaryPath
}

// setupGraphFixture creates a minimal Go project in a temp directory, builds
// the gograph index at the root, and returns (root, binPath).
func setupGraphFixture(t *testing.T) (string, string) {
	t.Helper()
	binPath := buildTestBinary(t)

	root := t.TempDir()
	// Create a simple Go source tree with a subdirectory package.
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/subdir\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	mainGo := filepath.Join(root, "main.go")
	if err := os.WriteFile(mainGo, []byte(`package main

import "fmt"

func main() { fmt.Println("hello") }

func RunAudit() error { return nil }
`), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	// Create a subdirectory with its own package.
	subDir := filepath.Join(root, "internal", "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "sub.go"), []byte(`package sub

func Helper() string { return "ok" }
`), 0o644); err != nil {
		t.Fatalf("write sub.go: %v", err)
	}

	// Build the graph at root.
	cmd := exec.Command(binPath, "build", root)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gograph build: %v\n%s", err, out)
	}

	return root, binPath
}

// --- Regression tests for gograph-root-aware-graph-loading ---

// TestPlanFromRoot verifies plan works when invoked from the repo root.
func TestPlanFromRoot(t *testing.T) {
	root, bin := setupGraphFixture(t)

	cmd := exec.Command(bin, "plan", "RunAudit")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("plan from root failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Change plan for RunAudit") {
		t.Errorf("expected plan output, got:\n%s", out)
	}
}

// TestPlanFromSubdirectory verifies plan works when invoked from a
// subdirectory (the key bug this feature fixes).
func TestPlanFromSubdirectory(t *testing.T) {
	root, bin := setupGraphFixture(t)
	subDir := filepath.Join(root, "internal", "sub")

	cmd := exec.Command(bin, "plan", "RunAudit")
	cmd.Dir = subDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("plan from subdir failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Change plan for RunAudit") {
		t.Errorf("expected plan output from subdirectory, got:\n%s", out)
	}
}

// TestReviewFromSubdirectory verifies review works from a subdirectory.
func TestReviewFromSubdirectory(t *testing.T) {
	root, bin := setupGraphFixture(t)
	subDir := filepath.Join(root, "internal", "sub")

	cmd := exec.Command(bin, "review", "RunAudit")
	cmd.Dir = subDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("review from subdir failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Code Review for RunAudit") {
		t.Errorf("expected review output from subdirectory, got:\n%s", out)
	}
}

func TestFilesystemCommandsFromSubdirectory(t *testing.T) {
	root, bin := setupGraphFixture(t)
	subDir := filepath.Join(root, "internal", "sub")

	for _, args := range [][]string{{"source", "RunAudit"}, {"context", "RunAudit"}} {
		cmd := exec.Command(bin, args...)
		cmd.Dir = subDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("gograph %v from subdirectory: %v\n%s", args, err, out)
		}
		if !strings.Contains(string(out), "func RunAudit() error") {
			t.Fatalf("gograph %v did not read source from the graph root:\n%s", args, out)
		}
	}

	mainGo := filepath.Join(root, "main.go")
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(mainGo, future, future); err != nil {
		t.Fatalf("mark main.go newer: %v", err)
	}
	for _, args := range [][]string{{"stale"}, {"changes"}} {
		cmd := exec.Command(bin, args...)
		cmd.Dir = subDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("gograph %v from subdirectory: %v\n%s", args, err, out)
		}
		if !strings.Contains(string(out), "main.go") {
			t.Fatalf("gograph %v did not inspect the graph root:\n%s", args, out)
		}
	}
}

func TestGitCommandsFromSubdirectory(t *testing.T) {
	root, bin := setupGraphFixture(t)
	subDir := filepath.Join(root, "internal", "sub")

	gitCommands := [][]string{
		{"init", "--quiet"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test User"},
		{"add", "."},
		{"commit", "--quiet", "-m", "baseline"},
	}
	for _, args := range gitCommands {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	mainGo := filepath.Join(root, "main.go")
	data, err := os.ReadFile(mainGo)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "return nil", "return fmt.Errorf(\"changed\")", 1))
	if err := os.WriteFile(mainGo, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "changes", "--git", "HEAD")
	cmd.Dir = subDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gograph changes from subdirectory: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "RunAudit") {
		t.Fatalf("changes did not map root Git changes to symbols:\n%s", out)
	}

	cmd = exec.Command(bin, "impact", "--since", "HEAD")
	cmd.Dir = subDir
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gograph impact from subdirectory: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "No Go symbol changes found") {
		t.Fatalf("impact did not inspect Git changes at the graph root:\n%s", out)
	}
}

// TestSessionAndGraphLoading_SubdirectoryE2E exercises the full lifecycle:
//
//  1. Create a session from the root.
//  2. Chdir into a subdirectory.
//  3. Run plan with -i.
//  4. Run review with -i.
//  5. End session and audit.
//  6. Verify: total_commands >= 2, success_count >= 2, failure_count = 0,
//     plan_run = true, review_run = true, grade != "F".
func TestSessionAndGraphLoading_SubdirectoryE2E(t *testing.T) {
	root, bin := setupGraphFixture(t)
	subDir := filepath.Join(root, "internal", "sub")

	// 1. Create session from root.
	cmd := exec.Command(bin, "session", "create", "subdirgraph")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("session create: %v\n%s", err, out)
	}

	// 2+3. Plan from subdirectory with intention.
	cmd = exec.Command(bin, "plan", "RunAudit", "-i", "subdir e2e plan")
	cmd.Dir = subDir
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("plan from subdir: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Change plan for RunAudit") {
		t.Errorf("plan did not produce expected output:\n%s", out)
	}

	// 4. Review from subdirectory with intention.
	cmd = exec.Command(bin, "review", "RunAudit", "-i", "subdir e2e review")
	cmd.Dir = subDir
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("review from subdir: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Code Review for RunAudit") {
		t.Errorf("review did not produce expected output:\n%s", out)
	}

	// 5. End session.
	cmd = exec.Command(bin, "session", "end")
	cmd.Dir = root
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("session end: %v\n%s", err, out)
	}

	// 6. Audit in JSON mode.
	cmd = exec.Command(bin, "session", "audit", "--json")
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("session audit: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	var report struct {
		TotalCommands int     `json:"total_commands"`
		SuccessCount  int     `json:"success_count"`
		FailureCount  int     `json:"failure_count"`
		PlanRun       bool    `json:"plan_run"`
		ReviewRun     bool    `json:"review_run"`
		Grade         string  `json:"grade"`
		SuccessRate   float64 `json:"success_rate"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("parse audit JSON: %v\nraw: %s", err, stdout.String())
	}

	if report.TotalCommands < 2 {
		t.Errorf("total_commands = %d, want >= 2", report.TotalCommands)
	}
	if report.SuccessCount < 2 {
		t.Errorf("success_count = %d, want >= 2", report.SuccessCount)
	}
	if report.FailureCount != 0 {
		t.Errorf("failure_count = %d, want 0", report.FailureCount)
	}
	if !report.PlanRun {
		t.Error("plan_run = false, want true")
	}
	if !report.ReviewRun {
		t.Error("review_run = false, want true")
	}
	if strings.HasPrefix(report.Grade, "F") {
		t.Errorf("grade = %q, want non-F", report.Grade)
	}
}

func TestSessionExemptions(t *testing.T) {
	root, bin := setupGraphFixture(t)

	// 1. Create session.
	cmd := exec.Command(bin, "session", "create", "exemptions")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("session create: %v\n%s", err, out)
	}

	// Clean up session at the end.
	defer func() {
		cmdEnd := exec.Command(bin, "session", "end")
		cmdEnd.Dir = root
		_ = cmdEnd.Run()
	}()

	// 2. Run capabilities (which is non-analytical) without intention.
	cmdCap := exec.Command(bin, "capabilities")
	cmdCap.Dir = root
	outCap, errCap := cmdCap.CombinedOutput()
	if errCap != nil {
		t.Fatalf("capabilities failed under active session: %v\nOutput: %s", errCap, outCap)
	}
	if !strings.Contains(string(outCap), "gograph: AST-aware Repository Navigation Tool") {
		t.Errorf("unexpected capabilities output: %s", outCap)
	}

	// 3. Run wiki (non-analytical) without intention.
	cmdWiki := exec.Command(bin, "wiki", "--output", filepath.Join(root, "my-llm-wiki"))
	cmdWiki.Dir = root
	outWiki, errWiki := cmdWiki.CombinedOutput()
	if errWiki != nil {
		t.Fatalf("wiki failed under active session: %v\nOutput: %s", errWiki, outWiki)
	}
	if !strings.Contains(string(outWiki), "wrote ") {
		t.Errorf("unexpected wiki output: %s", outWiki)
	}

	// 4. Run doc (non-analytical) without intention.
	cmdDoc := exec.Command(bin, "doc", "fmt")
	cmdDoc.Dir = root
	outDoc, errDoc := cmdDoc.CombinedOutput()
	if errDoc != nil {
		t.Fatalf("doc failed under active session: %v\nOutput: %s", errDoc, outDoc)
	}
	if !strings.Contains(string(outDoc), "package fmt") {
		t.Errorf("unexpected doc output: %s", outDoc)
	}
}

func TestRiskCommand(t *testing.T) {
	root, bin := setupGraphFixture(t)

	// Initialize git repo so git diff command doesn't fail
	cmdGitInit := exec.Command("git", "init")
	cmdGitInit.Dir = root
	if out, err := cmdGitInit.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, out)
	}
	if err := exec.Command("git", "-C", root, "config", "user.email", "test@example.com").Run(); err != nil {
		t.Fatalf("git config email failed: %v", err)
	}
	if err := exec.Command("git", "-C", root, "config", "user.name", "Test User").Run(); err != nil {
		t.Fatalf("git config name failed: %v", err)
	}
	if err := exec.Command("git", "-C", root, "add", ".").Run(); err != nil {
		t.Fatalf("git add failed: %v", err)
	}
	if err := exec.Command("git", "-C", root, "commit", "-m", "initial commit").Run(); err != nil {
		t.Fatalf("git commit failed: %v", err)
	}

	// 1. Run risk command for a specific symbol.
	cmd := exec.Command(bin, "risk", "RunAudit")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gograph risk RunAudit failed: %v\nOutput: %s", err, out)
	}

	outStr := string(out)
	if !strings.Contains(outStr, "Risk Report for RunAudit") {
		t.Errorf("unexpected output structure: %s", outStr)
	}
	if !strings.Contains(outStr, "RunAudit") || !strings.Contains(outStr, "SAFE") {
		t.Errorf("expected RunAudit in SAFE verdict, got: %s", outStr)
	}

	// 2. Run risk command with JSON mode.
	cmdJSON := exec.Command(bin, "--json", "risk", "RunAudit")
	cmdJSON.Dir = root
	outJSON, errJSON := cmdJSON.CombinedOutput()
	if errJSON != nil {
		t.Fatalf("gograph risk RunAudit JSON failed: %v\nOutput: %s", errJSON, outJSON)
	}
	if !strings.Contains(string(outJSON), `"status": "ok"`) || !strings.Contains(string(outJSON), `"verdict": "SAFE"`) {
		t.Errorf("unexpected JSON output: %s", outJSON)
	}

	// 3. Run risk command with --uncommitted.
	cmdUncommitted := exec.Command(bin, "risk", "--uncommitted")
	cmdUncommitted.Dir = root
	outUnc, errUnc := cmdUncommitted.CombinedOutput()
	if errUnc != nil {
		t.Fatalf("gograph risk --uncommitted failed: %v\nOutput: %s", errUnc, outUnc)
	}
	if !strings.Contains(string(outUnc), "No uncommitted modified symbols") {
		t.Errorf("expected no uncommitted changes message, got: %s", outUnc)
	}
}
