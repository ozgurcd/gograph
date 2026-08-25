package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/memorylimit"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestBuildStrictReportsPreciseFallbackAndDoctorExplainsIt(t *testing.T) {
	root := t.TempDir()
	writeIssue36File(t, filepath.Join(root, "go.mod"), "module example.com/strict\n\ngo 1.24\n")
	writeIssue36File(t, filepath.Join(root, "main.go"), "package main\n\nvar _ = missingSymbol\n")
	t.Setenv("GOWORK", "off")
	t.Setenv("GO111MODULE", "")

	_, stderr, code := captureIssue36Output(t, func() int { return runBuild([]string{root, "--precise", "--strict"}) })
	if code != 1 || !strings.Contains(stderr, "strict precise build failed") {
		t.Fatalf("strict fallback code/stderr = %d, %q", code, stderr)
	}
	persisted, err := loadGraph(root)
	if err != nil {
		t.Fatalf("load fallback artifact: %v", err)
	}
	if persisted.Build == nil || persisted.Build.EffectivePrecision() != graph.PrecisionFallback {
		t.Fatalf("strict fallback graph build = %+v", persisted.Build)
	}

	_, _, compatibilityCode := captureIssue36Output(t, func() int { return runBuild([]string{root, "--precise"}) })
	if compatibilityCode != 0 {
		t.Fatalf("default precise fallback exit = %d, want preserved exit 0", compatibilityCode)
	}
	repository, findings := inspectDoctorRepository(root, nil)
	if repository == nil || !repository.GraphAvailable || repository.AnalysisMode != string(graph.PrecisionFallback) {
		t.Fatalf("doctor repository = %+v", repository)
	}
	if !hasDoctorFinding(findings, "repository_precise_fallback") {
		t.Fatalf("doctor findings = %+v, want precise fallback", findings)
	}
}

func TestPreciseBuildAndMCPRefreshAllowSameCheckoutWorkspaceAndDataLinks(t *testing.T) {
	repositoryRoot := t.TempDir()
	analysisRoot := filepath.Join(repositoryRoot, "cust", "app")
	siblingRoot := filepath.Join(repositoryRoot, "mesa2", "core")
	if err := os.MkdirAll(filepath.Join(repositoryRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeIssue36File(t, filepath.Join(analysisRoot, "go.mod"), "module example.com/app\n\ngo 1.24\n")
	writeIssue36File(t, filepath.Join(analysisRoot, "go.work"), "go 1.24\n\nuse (\n\t.\n\t../../mesa2/core\n)\n")
	writeIssue36File(t, filepath.Join(analysisRoot, "main.go"), "package main\n\nimport \"example.com/mesa\"\n\nfunc main() { mesa.Use() }\n")
	writeIssue36File(t, filepath.Join(siblingRoot, "go.mod"), "module example.com/mesa\n\ngo 1.24\n")
	writeIssue36File(t, filepath.Join(siblingRoot, "mesa.go"), "package mesa\n\nfunc Use() {}\n")
	sharedConfig := filepath.Join(siblingRoot, "data", "config.yaml")
	writeIssue36File(t, sharedConfig, "enabled: true\n")
	writeIssue36File(t, filepath.Join(analysisRoot, "data", "Materials_all.tsv"), "material\n")
	if err := os.Symlink(sharedConfig, filepath.Join(analysisRoot, "data", "config.yaml")); err != nil {
		t.Skipf("create YAML link: %v", err)
	}
	if err := os.Symlink("Materials_all.tsv", filepath.Join(analysisRoot, "data", "Materials.tsv")); err != nil {
		t.Skipf("create TSV link: %v", err)
	}
	t.Setenv("GOWORK", "auto")
	t.Setenv("GO111MODULE", "")

	_, stderr, code := captureIssue36Output(t, func() int { return runBuild([]string{analysisRoot, "--precise", "--strict"}) })
	if code != 0 {
		t.Fatalf("same-checkout strict precise build = %d\n%s", code, stderr)
	}
	persisted, _, err := prepareMCPGraph(mcpOptions{Root: analysisRoot, Memory: memorylimit.Standard()})
	if err != nil {
		t.Fatalf("prepare MCP graph: %v", err)
	}
	if persisted.Build == nil || persisted.Build.EffectivePrecision() != graph.PrecisionPrecise {
		t.Fatalf("prepared MCP graph precision = %+v", persisted.Build)
	}
	writeIssue36File(t, filepath.Join(repositoryRoot, ".gograph-workspace.yml"), `schema_version: gograph.workspace-manifest.v1
name: issue-36
default_scope: default
repositories:
  - id: app
    path: cust/app
    precision: precise
scopes:
  - id: default
    repositories: [app]
`)
	if err := os.RemoveAll(filepath.Join(analysisRoot, ".gograph")); err != nil {
		t.Fatal(err)
	}
	_, workspaceStderr, workspaceCode := captureIssue36Output(t, func() int {
		return runWorkspaceBuild([]string{repositoryRoot, "--refresh-members"})
	})
	if workspaceCode != 0 {
		t.Fatalf("workspace refresh for same-checkout member = %d\n%s", workspaceCode, workspaceStderr)
	}
	persisted, root, err := prepareMCPGraph(mcpOptions{Root: analysisRoot, Memory: memorylimit.Standard()})
	if err != nil {
		t.Fatalf("prepare MCP graph after workspace refresh: %v", err)
	}
	writeIssue36File(t, filepath.Join(analysisRoot, "main.go"), "package main\n\nimport \"example.com/mesa\"\n\nfunc main() { mesa.Use() }\nfunc Added() { mesa.Use() }\n")
	freshness := func(candidate *graph.Graph, candidateRoot string) search.StaleResult {
		config, _ := resolveBuildConfigWithTags(candidateRoot, nil)
		return search.StaleWithConfig(candidate, candidateRoot, config)
	}
	refresh := graphRefresherWithFreshness(
		persisted,
		root,
		func(candidateRoot string) (*graph.Graph, error) { return buildGraphWithTags(candidateRoot, nil) },
		func(candidateRoot string) (*graph.Graph, error) {
			return buildPreciseGraphWithMemoryAndTags(candidateRoot, memorylimit.Standard(), nil)
		},
		freshness,
	)
	refreshed, err := refresh()
	if err != nil {
		t.Fatalf("MCP precise refresh in same checkout: %v", err)
	}
	if refreshed.Build == nil || refreshed.Build.EffectivePrecision() != graph.PrecisionPrecise {
		t.Fatalf("refreshed MCP graph precision = %+v", refreshed.Build)
	}
}

func writeIssue36File(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasDoctorFinding(findings []doctorFinding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func captureIssue36Output(t *testing.T, run func() int) (stdout, stderr string, exit int) {
	t.Helper()
	oldStdout, oldStderr := os.Stdout, os.Stderr
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = stdoutWrite, stderrWrite
	exit = run()
	if err := stdoutWrite.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrWrite.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = oldStdout, oldStderr
	stdoutBytes, err := io.ReadAll(stdoutRead)
	if err != nil {
		t.Fatal(err)
	}
	stderrBytes, err := io.ReadAll(stderrRead)
	if err != nil {
		t.Fatal(err)
	}
	return string(stdoutBytes), string(stderrBytes), exit
}
