package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestE2E_Commands compiles a temporary binary and runs end-to-end integration tests
// to ensure the newly added commands output the expected text format.
func TestE2E_Commands(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a mock package that has some constructors, globals, etc.
	mainGo := filepath.Join(tmpDir, "main.go")
	content := `package main

import "fmt"

type Worker struct {
	ID int ` + "`" + `json:"id" db:"workers"` + "`" + `
}

var GlobalCounter int

func NewWorker() *Worker {
	GlobalCounter++
	return &Worker{ID: 1}
}

type Service interface {
	DoWork()
}

func ReturnError() error {
	return fmt.Errorf("invalid arguments")
}

func main() {
	fmt.Println("Hello", GlobalCounter)
	if err := ReturnError(); err != nil {
		fmt.Println("caught:", err)
	}
}
`
	if err := os.WriteFile(mainGo, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write dummy source: %v", err)
	}

	binPath := buildTestBinary(t)

	runCmd := func(args ...string) (string, error) {
		cmd := exec.Command(binPath, args...)
		cmd.Dir = tmpDir
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		return out.String(), err
	}

	// 1. Build the graph in the temporary directory
	out, err := runCmd("build", tmpDir)
	if err != nil {
		t.Fatalf("build failed: %v\nOutput:\n%s", err, out)
	}
	if !strings.Contains(out, "packages: 1") {
		t.Errorf("expected 1 package, got: %s", out)
	}

	// 2. Test constructors
	out, err = runCmd("constructors", "Worker")
	if err != nil {
		t.Fatalf("constructors failed: %v", err)
	}
	if !strings.Contains(out, "NewWorker") {
		t.Errorf("expected constructors to find NewWorker, got: %s", out)
	}

	// 3. Test schema
	out, err = runCmd("schema", "workers")
	if err != nil {
		t.Fatalf("schema failed: %v", err)
	}
	if !strings.Contains(out, "Worker") {
		t.Errorf("expected schema to find Worker, got: %s", out)
	}

	// 4. Test globals
	out, err = runCmd("globals", "main")
	if err != nil {
		t.Fatalf("globals failed: %v", err)
	}
	if !strings.Contains(out, "GlobalCounter") {
		t.Errorf("expected globals to find GlobalCounter, got: %s", out)
	}

	// 5. Test plan
	out, err = runCmd("plan", "ReturnError")
	if err != nil {
		t.Fatalf("plan failed: %v\nOutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Change plan for ReturnError") {
		t.Errorf("expected plan output, got: %s", out)
	}

	// 6. Test review
	out, err = runCmd("review", "ReturnError")
	if err != nil {
		t.Fatalf("review failed: %v\nOutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Code Review for ReturnError") {
		t.Errorf("expected review output, got: %s", out)
	}

	// 7. Test errorflow
	out, err = runCmd("errorflow", "invalid arguments")
	if err != nil {
		t.Fatalf("errorflow failed: %v\nOutput:\n%s", err, out)
	}
	if !strings.Contains(out, "ErrorFlow Report for") {
		t.Errorf("expected errorflow output, got: %s", out)
	}

	// 8. Test JSON flags
	outJSON, _ := runCmd("--json", "plan", "ReturnError")
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(outJSON), &env); err != nil {
		t.Fatalf("Failed to parse JSON for plan: %v\nOutput: %s", err, outJSON)
	}
	if env["status"] != "ok" {
		t.Errorf("expected JSON success status, got: %s", outJSON)
	}
}

func TestE2E_FlowAnalysis(t *testing.T) {
	root := t.TempDir()
	source := `package sample

import (
	"net/http"
	"os"
	"os/exec"
)

func cleanPath(path string) string { return path }

func handle(request *http.Request) {
	path := cleanPath(request.URL.Path)
	_ = os.WriteFile(path, nil, 0600)
	_ = exec.Command(os.Getenv("RUN_COMMAND")).Run()
}
`
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/flow\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	binPath := buildTestBinary(t)
	run := func(args ...string) (string, error) {
		cmd := exec.Command(binPath, args...)
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		return string(output), err
	}
	if output, err := run("build", "."); err != nil {
		t.Fatalf("flow fixture build failed: %v\n%s", err, output)
	}

	output, err := run("flow", "--no-tests")
	if err != nil {
		t.Fatalf("flow failed: %v\n%s", err, output)
	}
	for _, want := range []string{"http_request -> filesystem", "environment -> process_execution"} {
		if !strings.Contains(output, want) {
			t.Errorf("flow output missing %q:\n%s", want, output)
		}
	}

	jsonOutput, err := run("--json", "flow", "--sink", "filesystem", "--no-tests")
	if err != nil {
		t.Fatalf("JSON flow failed: %v\n%s", err, jsonOutput)
	}
	var envelope struct {
		Status string `json:"status"`
		Count  int    `json:"count"`
	}
	if err := json.Unmarshal([]byte(jsonOutput), &envelope); err != nil {
		t.Fatalf("decode JSON flow output: %v\n%s", err, jsonOutput)
	}
	if envelope.Status != "ok" || envelope.Count != 1 {
		t.Fatalf("unexpected JSON flow envelope: %+v\n%s", envelope, jsonOutput)
	}

	config := `{"sanitizers":[{"function":"cleanPath","for":["filesystem"]}]}`
	if err := os.WriteFile(filepath.Join(root, ".gograph", "flow.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err = run("flow", "--sink", "filesystem", "--no-tests")
	if err != nil {
		t.Fatalf("sanitized flow failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "No potential untrusted-data flows found.") {
		t.Fatalf("query-time sanitizer config was not applied:\n%s", output)
	}
}

// TestE2E_HTTPCalls verifies the full httpcalls pipeline: build a Go project
// with HTTP client calls, then query them via the CLI command.
func TestE2E_HTTPCalls(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a minimal Go module with HTTP client calls.
	mainGo := filepath.Join(tmpDir, "main.go")
	content := `package main

import (
	"fmt"
	"net/http"
)

func fetchUsers() {
	resp, err := http.Get("https://api.example.com/users")
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	fmt.Println(resp.StatusCode)
}

func createUser() {
	resp, err := http.Post("https://api.example.com/users", "application/json", nil)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
}

func login() {
	http.PostForm("https://api.example.com/login", nil)
}

func healthCheck() {
	http.Head("https://api.example.com/health")
}

func dynamicURL(url string) {
	http.Get(url)
}

func main() {
	fetchUsers()
	createUser()
	login()
	healthCheck()
}
`
	if err := os.WriteFile(mainGo, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write dummy source: %v", err)
	}
	goMod := filepath.Join(tmpDir, "go.mod")
	if err := os.WriteFile(goMod, []byte("module example.com/httpclient\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}

	binPath := buildTestBinary(t)

	runCmd := func(args ...string) (string, error) {
		cmd := exec.Command(binPath, args...)
		cmd.Dir = tmpDir
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		return out.String(), err
	}

	// 1. Build the graph.
	out, err := runCmd("build", tmpDir)
	if err != nil {
		t.Fatalf("build failed: %v\nOutput:\n%s", err, out)
	}

	// 2. Verify graph.json contains http_calls.
	graphPath := filepath.Join(tmpDir, ".gograph", "graph.json")
	data, err := os.ReadFile(graphPath)
	if err != nil {
		t.Fatalf("failed to read graph.json: %v", err)
	}
	var g struct {
		HTTPCalls []map[string]any `json:"http_calls"`
	}
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatalf("invalid graph.json: %v\nContent: %s", err, string(data))
	}
	if len(g.HTTPCalls) == 0 {
		t.Fatal("expected at least one http_call in graph.json, got none")
	}
	t.Logf("graph.json contains %d HTTP call(s)", len(g.HTTPCalls))

	// 3. Run gograph httpcalls (text mode).
	out, err = runCmd("httpcalls")
	if err != nil {
		t.Fatalf("httpcalls failed: %v\nOutput:\n%s", err, out)
	}
	expected := []string{
		"GET https://api.example.com/users",
		"POST https://api.example.com/users",
		"POST https://api.example.com/login",
		"HEAD https://api.example.com/health",
	}
	for _, exp := range expected {
		if !strings.Contains(out, exp) {
			t.Errorf("expected httpcalls output to contain %q\nGot:\n%s", exp, out)
		}
	}
	t.Logf("httpcalls text output:\n%s", out)

	// 4. Test filtering.
	out, err = runCmd("httpcalls", "POST")
	if err != nil {
		t.Fatalf("httpcalls POST failed: %v\nOutput:\n%s", err, out)
	}
	if !strings.Contains(out, "POST https://api.example.com/users") {
		t.Errorf("expected filtered output to contain POST, got:\n%s", out)
	}
	if strings.Contains(out, "GET https://api.example.com/users") {
		t.Errorf("filtered output should not contain GET, got:\n%s", out)
	}
	t.Logf("httpcalls POST filtered output:\n%s", out)

	// 5. Test JSON output.
	out, err = runCmd("httpcalls", "--json")
	if err != nil {
		t.Fatalf("httpcalls --json failed: %v\nOutput:\n%s", err, out)
	}
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("Failed to parse JSON: %v\nOutput: %s", err, out)
	}
	if env["status"] != "ok" {
		t.Errorf("expected JSON status 'ok', got %v", env["status"])
	}
	if env["command"] != "httpcalls" {
		t.Errorf("expected JSON command 'httpcalls', got %v", env["command"])
	}
	results, ok := env["results"].([]interface{})
	if !ok {
		t.Fatalf("expected results array, got %T", env["results"])
	}
	if len(results) == 0 {
		t.Fatal("expected non-empty results array")
	}
	t.Logf("httpcalls --json returned %d results", len(results))

	// 6. Test empty results message.
	out, err = runCmd("httpcalls", "NonExistentTermXYZ")
	if err != nil {
		t.Fatalf("httpcalls with no-match failed: %v\nOutput:\n%s", err, out)
	}
	if !strings.Contains(out, "No HTTP client calls found") {
		t.Errorf("expected empty results message, got:\n%s", out)
	}
}

func TestE2E_ClaudePluginInstall(t *testing.T) {
	binPath := buildTestBinary(t)
	home := t.TempDir()
	project := t.TempDir()
	appData := filepath.Join(home, "AppData", "Roaming")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", appData)

	var desktopConfig string
	switch runtime.GOOS {
	case "darwin":
		desktopConfig = filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "linux":
		desktopConfig = filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	case "windows":
		desktopConfig = filepath.Join(appData, "Claude", "claude_desktop_config.json")
	default:
		t.Skipf("Claude Desktop config path is unsupported on %s", runtime.GOOS)
	}

	cmd := exec.Command(binPath, "add-claude-plugin")
	cmd.Dir = project
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("add-claude-plugin failed: %v\n%s", err, out)
	}
	wantFiles := []string{
		desktopConfig,
		filepath.Join(home, ".claude", "CLAUDE.md"),
		filepath.Join(home, ".claude", "hooks", "gograph-guard.sh"),
		filepath.Join(home, ".claude", "settings.json"),
	}
	for _, path := range wantFiles {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected installer output %s: %v", path, err)
		}
	}
}

func TestE2E_HookGuard(t *testing.T) {
	binPath := buildTestBinary(t)
	cmd := exec.Command(binPath, "hook-guard")
	cmd.Stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"rg UserService --include=*.go"}}`)
	out, err := cmd.CombinedOutput()
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 2 {
		t.Fatalf("hook-guard exit = %v, want 2\n%s", err, out)
	}
	if !strings.Contains(string(out), "blocked grep") || !strings.Contains(string(out), `gograph_context "UserService"`) {
		t.Fatalf("hook-guard did not provide the documented redirect:\n%s", out)
	}

	allow := exec.Command(binPath, "hook-guard")
	allow.Stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"rg TODO --include=*.go"}}`)
	if out, err := allow.CombinedOutput(); err != nil {
		t.Fatalf("hook-guard should allow comment search: %v\n%s", err, out)
	}
}
