package search

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestCheckConfigDefaults(t *testing.T) {
	config := &CheckConfig{
		Checks: map[string]any{
			"max_arity": map[string]any{"level": "warn", "value": 6.0},
		},
	}
	p := &CheckParams{
		CurrentGraph: &graph.Graph{},
		Config:       config,
	}
	report, err := RunChecks(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Status != "passed" {
		t.Errorf("expected passed, got %s", report.Status)
	}
}

func TestCheckConfigUnknown(t *testing.T) {
	config := &CheckConfig{
		Checks: map[string]any{
			"unknown_check": "warn",
		},
	}
	p := &CheckParams{
		CurrentGraph: &graph.Graph{},
		Config:       config,
	}
	_, err := RunChecks(p)
	if err == nil {
		t.Fatalf("expected error for unknown check, got nil")
	}
}

func TestCheckMaxArity(t *testing.T) {
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{Name: "FuncOk", Kind: graph.KindFunction, Signature: "func(a, b int)"},
			{Name: "FuncBad", Kind: graph.KindFunction, Signature: "func(a, b, c, d int)", File: "test.go", Line: 10},
		},
	}
	config := &CheckConfig{
		Checks: map[string]any{
			"max_arity": map[string]any{"level": "error", "value": 3.0},
		},
	}
	p := &CheckParams{CurrentGraph: g, Config: config}
	report, _ := RunChecks(p)

	if report.Summary.Errors != 1 {
		t.Errorf("expected 1 error, got %d", report.Summary.Errors)
	}
	if len(report.Findings) != 1 || report.Findings[0].Symbol != "FuncBad" {
		t.Errorf("expected FuncBad to be reported, got %v", report.Findings)
	}
	if report.Status != "failed" {
		t.Errorf("expected failed status, got %s", report.Status)
	}
}

func TestCheckJSONShape(t *testing.T) {
	g := &graph.Graph{}
	config := &CheckConfig{
		Checks: map[string]any{
			"boundaries": "warn",
		},
		BoundariesConfig: "not_exist.json",
	}
	p := &CheckParams{CurrentGraph: g, Config: config}
	report, _ := RunChecks(p)

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("failed to marshal report: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	for _, k := range []string{"status", "summary", "findings", "skipped", "limitations"} {
		if _, ok := out[k]; !ok {
			t.Errorf("JSON output missing key %s", k)
		}
	}
}

func TestCheckBaselineSkipped(t *testing.T) {
	g := &graph.Graph{}
	config := &CheckConfig{
		Checks: map[string]any{
			"api_drift":   "error",
			"new_globals": "warn",
		},
	}
	p := &CheckParams{CurrentGraph: g, Config: config}
	report, _ := RunChecks(p)

	if report.Summary.Skipped != 2 {
		t.Errorf("expected 2 skipped checks, got %d", report.Summary.Skipped)
	}
	for _, skip := range report.Skipped {
		if skip.Check != "api_drift" && skip.Check != "new_globals" {
			t.Errorf("unexpected skipped check: %s", skip.Check)
		}
	}
}

func TestCheckBoundariesViolation(t *testing.T) {
	// create temporary boundaries config
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "boundaries.json")
	configData := `{
		"layers": [
			{"name": "Domain", "packages": ["domain/**"], "may_import": []}
		]
	}`
	if err := os.WriteFile(configPath, []byte(configData), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	g := &graph.Graph{
		Packages: []graph.PackageNode{
			{ID: "domain/user", Name: "user"},
			{ID: "infra/db", Name: "db"},
		},
		Imports: []graph.ImportEdge{
			{FromFile: "domain/user/user.go", ImportPath: "github.com/example/infra/db"},
		},
	}

	config := &CheckConfig{
		Checks: map[string]any{
			"boundaries": "error",
		},
		BoundariesConfig: configPath,
	}
	p := &CheckParams{CurrentGraph: g, Config: config}
	report, _ := RunChecks(p)

	if report.Summary.Errors != 1 {
		t.Errorf("expected 1 error, got %d", report.Summary.Errors)
	}
	if len(report.Findings) != 1 || report.Findings[0].Check != "boundaries" {
		t.Errorf("expected boundaries finding")
	}
}

func TestCheckChangedRouteMatchesHandlerIdentity(t *testing.T) {
	current := &graph.Graph{
		Symbols: []graph.SymbolNode{{ID: "example.com/api::HandleUsers", Name: "HandleUsers", Kind: graph.KindFunction, Signature: "func(int)", File: "handler.go", Line: 3}},
		Routes:  []graph.HTTPRoute{{Method: "GET", Path: "/users", Handler: "api.HandleUsers", File: "routes.go", Line: 8}},
	}
	baseline := &graph.Graph{Symbols: []graph.SymbolNode{{ID: "example.com/api::HandleUsers", Name: "HandleUsers", Kind: graph.KindFunction, Signature: "func()"}}}
	report, err := RunChecks(&CheckParams{
		CurrentGraph:  current,
		BaselineGraph: baseline,
		Config: &CheckConfig{Checks: map[string]any{
			"require_tests_for_changed_routes": "error",
		}},
	})
	if err != nil {
		t.Fatalf("RunChecks: %v", err)
	}
	if len(report.Findings) != 1 || report.Findings[0].Symbol != "api.HandleUsers" {
		t.Fatalf("expected missing-test route finding, got %+v", report.Findings)
	}
}

func TestCheckChangedRouteDetectsBodyOnlyGitChange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "handler.go")
	if err := os.WriteFile(path, []byte("package api\n\nfunc HandleUsers() { println(1) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "--quiet"}, {"config", "user.email", "test@example.com"}, {"config", "user.name", "Test User"}, {"add", "."}, {"commit", "--quiet", "-m", "baseline"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(path, []byte("package api\n\nfunc HandleUsers() { println(2) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	symbol := graph.SymbolNode{ID: "example.com/api::HandleUsers", Name: "HandleUsers", Kind: graph.KindFunction, Signature: "func()", File: "handler.go", Line: 3}
	current := &graph.Graph{Root: root, Symbols: []graph.SymbolNode{symbol}, Routes: []graph.HTTPRoute{{Method: "GET", Path: "/users", Handler: "HandleUsers", File: "routes.go", Line: 8}}}
	baseline := &graph.Graph{Symbols: []graph.SymbolNode{symbol}}
	report, err := RunChecks(&CheckParams{
		CurrentGraph:  current,
		BaselineGraph: baseline,
		SinceRef:      "HEAD",
		RootDir:       root,
		Config: &CheckConfig{Checks: map[string]any{
			"require_tests_for_changed_routes": "error",
		}},
	})
	if err != nil {
		t.Fatalf("RunChecks: %v", err)
	}
	if len(report.Findings) != 1 || report.Findings[0].Check != "require_tests_for_changed_routes" {
		t.Fatalf("expected body-only route change finding, got %+v", report.Findings)
	}
}

func TestCheckAdvertisedCoverageAndOrphanChecks(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example.com/api::Public", Name: "Public", Kind: graph.KindFunction, File: "api.go", Line: 3},
		{ID: "example.com/api::dead", Name: "dead", Kind: graph.KindFunction, File: "api.go", Line: 8},
	}}
	report, err := RunChecks(&CheckParams{CurrentGraph: g, Config: &CheckConfig{Checks: map[string]any{
		"test_coverage": "warn",
		"no_orphans":    "error",
	}}})
	if err != nil {
		t.Fatalf("RunChecks: %v", err)
	}
	seen := map[string]bool{}
	for _, finding := range report.Findings {
		seen[finding.Check] = true
	}
	if !seen["test_coverage"] || !seen["no_orphans"] {
		t.Fatalf("expected both advertised checks, got %+v", report.Findings)
	}
}
