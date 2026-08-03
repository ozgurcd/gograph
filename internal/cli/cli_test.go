package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ozgurcd/gograph/internal/cli"
)

func TestBuildGraph(t *testing.T) {
	// Create a temporary directory with a dummy Go file
	tmpDir := t.TempDir()
	dummyGo := filepath.Join(tmpDir, "main.go")
	content := `package main
import "fmt"
func main() {
	fmt.Println("Hello")
}
`
	if err := os.WriteFile(dummyGo, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create dummy file: %v", err)
	}

	g, err := cli.BuildGraph(tmpDir)
	if err != nil {
		t.Fatalf("BuildGraph failed: %v", err)
	}

	if g == nil {
		t.Fatal("expected non-nil graph")
	}
	if g.Root != tmpDir {
		t.Errorf("expected root %s, got %s", tmpDir, g.Root)
	}
	if len(g.Packages) == 0 {
		t.Fatal("expected at least one package")
	}
	if len(g.Files) == 0 {
		t.Fatal("expected at least one file")
	}
	if g.Files[0].PackageName != "main" {
		t.Errorf("expected package main, got %s", g.Files[0].PackageName)
	}

	// Check if the call was captured
	foundCall := false
	for _, call := range g.Calls {
		if call.CalleeRaw == "fmt.Println" {
			foundCall = true
		}
	}
	if !foundCall {
		t.Error("expected to find fmt.Println call in the graph")
	}
}

func TestBuildGraphRejectsAllParseFailures(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "broken.go"), []byte("package broken\nfunc {\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	g, err := cli.BuildGraph(root)
	if err == nil {
		t.Fatalf("expected all-parse-failures error, got graph %+v", g)
	}
	if !strings.Contains(err.Error(), "none of 1 Go files parsed successfully") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildGraphReportsPartialParseFailures(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "valid.go"), []byte("package sample\nfunc Valid() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken.go"), []byte("package sample\nfunc {\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	g, err := cli.BuildGraph(root)
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	if g.Build == nil || g.Build.Complete || g.Build.ScannedFiles != 2 || g.Build.ParsedFiles != 1 || len(g.Build.Failures) != 1 {
		t.Fatalf("unexpected build metadata: %+v", g.Build)
	}
}

func TestBuildGraphFiltersNonCallableArgumentsAndDedupesCallbacks(t *testing.T) {
	root := t.TempDir()
	source := `package sample

import (
	"fmt"
	"os"
)

type handler struct{}

func callback() {}
func (h *handler) serve() {}
func register(...any) {}

func run(h *handler, err error, g any, root string) {
	fmt.Fprintf(os.Stderr, "%v", err)
	register(callback)
	register(h.serve)
	register(g)
	register(root)
}
`
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(source), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	g, err := cli.BuildGraph(root)
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}

	forbidden := map[string]bool{
		"err":       true,
		"g":         true,
		"root":      true,
		"os.Stderr": true,
	}
	seen := make(map[string]int)
	for _, call := range g.Calls {
		if forbidden[call.CalleeRaw] {
			t.Errorf("ordinary argument %q was emitted as a call", call.CalleeRaw)
		}
		key := fmt.Sprintf("%s|%s|%s|%d|%s", call.CallerSymbolID, call.CalleeRaw, call.File, call.Line, call.CalleeSymbolID)
		seen[key]++
		if seen[key] > 1 {
			t.Errorf("duplicate call edge %s", key)
		}
	}

	for _, want := range []string{"fmt.Fprintf", "register", "callback", "h.serve"} {
		found := false
		for _, call := range g.Calls {
			if call.CalleeRaw == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected call edge %q, got %+v", want, g.Calls)
		}
	}
}

func TestBuildCommandRejectsDirectoryWithoutGoFiles(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	binPath := filepath.Join(t.TempDir(), "gograph")
	build := exec.Command("go", "build", "-o", binPath, filepath.Join(repoRoot, "cmd", "gograph", "main.go"))
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build test binary: %v\n%s", err, out)
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/empty\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	emptyGraphDir := filepath.Join(root, ".gograph")
	if err := os.MkdirAll(emptyGraphDir, 0o755); err != nil {
		t.Fatalf("mkdir empty .gograph dir: %v", err)
	}

	cmd := exec.Command(binPath, "build", "--precise", ".")
	cmd.Dir = emptyGraphDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err = cmd.Run()
	if err == nil {
		t.Fatalf("expected build to fail for directory without Go files, got success:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "no Go files") {
		t.Fatalf("expected no-Go-files error, got:\n%s", out.String())
	}

	nestedOutputDir := filepath.Join(emptyGraphDir, ".gograph")
	if _, err := os.Stat(nestedOutputDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no nested output dir at %s, stat err: %v", nestedOutputDir, err)
	}
	if _, err := os.Stat(filepath.Join(emptyGraphDir, ".gitignore")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no .gitignore side effect, stat err: %v", err)
	}
}

func TestBuildCommandWritesGitignoreAtRepositoryRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	binPath := filepath.Join(t.TempDir(), "gograph")
	build := exec.Command("go", "build", "-o", binPath, filepath.Join(repoRoot, "cmd", "gograph", "main.go"))
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build test binary: %v\n%s", err, out)
	}

	root := t.TempDir()
	initGit := exec.Command("git", "init")
	initGit.Dir = root
	if out, err := initGit.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/root\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("write root go.mod: %v", err)
	}

	nested := filepath.Join(root, "e2e")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "go.mod"), []byte("module example.com/e2e\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("write nested go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write nested main.go: %v", err)
	}

	cmd := exec.Command(binPath, "build", nested)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build nested module: %v\n%s", err, out)
	}

	rootGitignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read root .gitignore: %v", err)
	}
	if !strings.Contains(string(rootGitignore), ".gograph/") {
		t.Fatalf("expected root .gitignore to contain .gograph/, got:\n%s", rootGitignore)
	}
	checkIgnore := exec.Command("git", "check-ignore", "--quiet", filepath.Join(nested, ".gograph", "graph.json"))
	checkIgnore.Dir = root
	if out, err := checkIgnore.CombinedOutput(); err != nil {
		t.Fatalf("expected root .gitignore to ignore nested graph output: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(nested, ".gitignore")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no nested .gitignore, stat err: %v", err)
	}
}

// TestAllCommandsRegistered parses the Run() switch statement in cli.go via
// go/ast and asserts every canonical CLI command has a registered case.
// This prevents the class of bug where a command is documented but never wired.
func TestAllCommandsRegistered(t *testing.T) {
	// Canonical list: every user-facing command that must be in the Run() switch.
	// Maintenance rule: when you add a command to help/capabilities, add it here too.
	want := []string{
		"build",
		"query",
		"focus",
		"node",
		"source",
		"public",
		"fields",
		"embeds",
		"imports",
		"callers",
		"callees",
		"impact",
		"implementers",
		"interfaces",
		"path",
		"stale",
		"stats",
		"orphans",
		"godobj",
		"complexity",
		"diagram",
		"coupling",
		"context",
		"hotspot",
		"httpcalls",
		"deps",
		"dependents",
		"changes",
		"capabilities",
		"mcp",
		"routes",
		"sql",
		"errors",
		"envs",
		"concurrency",
		"tests",
		"constructors",
		"literals",
		"usages",
		"returnusage",
		"schema",
		"globals",
		"mocks",
		"trace",
		"arity",
		"mutate",
		"skeleton",
		"api",
		"contract",
		"errorflow",
		"flow",
		"fixtures",
		"plan",
		"review",
		"risk",
		"boundaries",
		"endpoint",
		"explain",
		"gate",
		"snapshot",
		"check",
		"add-claude-plugin",
		"hook-guard",
		"--json",
		"--files-only",
		"--mermaid",
		"session",
		"--session",
		"wiki",
		"summary",
		"untested",
		"doc",
		"-i",
		"--intention",
		// aliases
		"help",
		"--help",
		"-h",
		"version",
		"--version",
		"-v",
	}

	// Locate cli.go relative to this test file.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	cliPath := filepath.Join(filepath.Dir(thisFile), "cli.go")

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, cliPath, nil, 0)
	if err != nil {
		t.Fatalf("failed to parse cli.go: %v", err)
	}

	// Walk the AST and collect all case clause string literals inside Run() and dispatch().
	registered := make(map[string]bool)
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		if fn.Name.Name != "Run" && fn.Name.Name != "dispatch" {
			return true
		}
		// Found Run or dispatch — now collect all CaseClause string values within it.
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			cc, ok := inner.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range cc.List {
				if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					// Strip surrounding quotes.
					val := lit.Value[1 : len(lit.Value)-1]
					registered[val] = true
				}
			}
			return true
		})
		return false
	})

	sort.Strings(want)
	var missing []string
	for _, cmd := range want {
		if !registered[cmd] {
			missing = append(missing, cmd)
		}
	}
	if len(missing) > 0 {
		t.Errorf("the following commands are documented but NOT registered in Run():\n  %v\nAdd a case for each in the Run() switch in cli.go.", missing)
	}

	// Inverse check: warn about cases in Run() not in the canonical list.
	wantSet := make(map[string]bool, len(want))
	for _, w := range want {
		wantSet[w] = true
	}
	var extra []string
	for cmd := range registered {
		if !wantSet[cmd] {
			extra = append(extra, cmd)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		t.Errorf("the following cases exist in Run() but are NOT in the canonical want list in this test:\n  %v\nAdd them to the want slice above.", extra)
	}
}

func TestStaleExitCodes(t *testing.T) {
	root, bin := setupGraphFixture(t)

	cmd := exec.Command(bin, "stale")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gograph stale (up to date): %v\n%s", err, out)
	}

	mainGo := filepath.Join(root, "main.go")
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(mainGo, future, future); err != nil {
		t.Fatalf("mark main.go newer: %v", err)
	}

	cmd = exec.Command(bin, "stale")
	cmd.Dir = root
	out, err = cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		t.Fatalf("gograph stale (stale graph): expected exit 2, got %v\n%s", err, out)
	}
	cmd = exec.Command(bin, "stale", "--json")
	cmd.Dir = root
	out, err = cmd.CombinedOutput()
	exitErr = nil
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		t.Fatalf("gograph stale --json (stale graph): expected exit 2, got %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "\"schema_version\"") || !strings.Contains(string(out), "\"command\": \"stale\"") {
		t.Fatalf("gograph stale --json: expected JSON output, got:\n%s", out)
	}

	noGraphDir := t.TempDir()
	cmd = exec.Command(bin, "stale")
	cmd.Dir = noGraphDir
	out, err = cmd.CombinedOutput()
	exitErr = nil
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("gograph stale (missing graph): expected exit 1, got %v\n%s", err, out)
	}
}

func TestStaleExitCodeIsSuccessfulSessionTelemetry(t *testing.T) {
	root, bin := setupGraphFixture(t)

	cmd := exec.Command(bin, "session", "create", "staleexit")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}

	mainGo := filepath.Join(root, "main.go")
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(mainGo, future, future); err != nil {
		t.Fatalf("mark main.go newer: %v", err)
	}

	cmd = exec.Command(bin, "stale")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		t.Fatalf("gograph stale: expected exit 2, got %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "session", "end")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("end session: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "--json", "session", "audit")
	cmd.Dir = root
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("audit session: %v\n%s", err, out)
	}
	var report struct {
		TotalCommands int     `json:"total_commands"`
		SuccessCount  int     `json:"success_count"`
		FailureCount  int     `json:"failure_count"`
		SuccessRate   float64 `json:"success_rate"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("decode session audit: %v\n%s", err, out)
	}
	if report.TotalCommands != 1 || report.SuccessCount != 1 || report.FailureCount != 0 || report.SuccessRate != 100 {
		t.Fatalf("unexpected stale telemetry audit: %+v", report)
	}
}

func TestHelpDocumentsEveryCanonicalCommand(t *testing.T) {
	cmd := exec.Command(buildTestBinary(t), "--help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gograph --help: %v\n%s", err, out)
	}
	documented := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name := strings.TrimSuffix(fields[0], ",")
		documented[name] = true
	}
	want := []string{
		"build", "stale", "stats", "query", "focus", "node", "source", "public",
		"fields", "embeds", "imports", "mutate", "arity", "skeleton", "callers",
		"callees", "impact", "path", "trace", "orphans", "implementers", "interfaces",
		"constructors", "literals", "returnusage", "usages", "schema", "globals", "mocks",
		"fixtures", "check", "gate", "snapshot", "boundaries", "complexity", "diagram",
		"coupling", "context", "explain", "hotspot", "summary", "untested", "endpoint",
		"deps", "dependents", "changes", "godobj", "plan", "review", "risk", "api",
		"routes", "sql", "httpcalls", "errorflow", "flow", "errors", "envs", "concurrency", "tests",
		"capabilities", "wiki", "doc", "mcp", "session", "add-claude-plugin", "hook-guard",
		"version", "help",
	}
	for _, name := range want {
		if !documented[name] {
			t.Errorf("gograph --help does not document %q", name)
		}
	}
}

func TestHelpDocumentsImplementedModes(t *testing.T) {
	cmd := exec.Command(buildTestBinary(t), "--help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gograph --help: %v\n%s", err, out)
	}

	help := string(out)
	for _, mode := range []string{
		"callers <function> [--no-tests] [--depth N] [--exact]",
		"coupling [package] [--include-stdlib] [--internal-only]",
		"context <symbol> [--limit N] [--exact]",
		"hotspot [--top N] [--include-tests]",
		"plan <symbol> [--with-context]",
		"plan --uncommitted [--with-context]",
		"sql [term]",
		"flow [term] [--source kind] [--sink kind] [--config path] [--no-tests]",
		"Sources: http_request, decoded_json, environment.",
		"Sinks: sql_query, process_execution, filesystem, outbound_http.",
		"Tests are included by default; --no-tests excludes them.",
	} {
		if !strings.Contains(help, mode) {
			t.Errorf("gograph --help does not document implemented mode %q", mode)
		}
	}
}

func TestCapabilitiesDocumentsImplementedModes(t *testing.T) {
	cmd := exec.Command(buildTestBinary(t), "capabilities")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gograph capabilities: %v\n%s", err, out)
	}

	capabilities := string(out)
	for _, want := range []string{
		"build . --precise  then  review --uncommitted",
		"callers <fn> [--no-tests] [--depth N] [--exact]",
		"coupling [pkg] [--include-stdlib] [--internal-only]",
		"context <sym> [--limit N] [--exact]",
		"hotspot [--top N] [--include-tests]",
		"plan <sym> [--with-context]",
		"plan --uncommitted [--with-context]",
		"sql [term]",
		"flow [term] [--source kind] [--sink kind] [--config path] [--no-tests]",
		"source: http_request | decoded_json | environment",
		"sink: sql_query | process_execution | filesystem | outbound_http",
		"tests are included by default; sanitizer policy is evaluated at query time",
	} {
		if !strings.Contains(capabilities, want) {
			t.Errorf("gograph capabilities does not document implemented mode %q", want)
		}
	}
}
