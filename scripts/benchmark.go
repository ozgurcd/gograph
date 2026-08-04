package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type benchmarkSuite struct {
	SchemaVersion string              `json:"schema_version"`
	Name          string              `json:"name"`
	Description   string              `json:"description"`
	Fixture       string              `json:"fixture"`
	AnalysisMode  string              `json:"analysis_mode"`
	Limitations   []string            `json:"limitations"`
	Scenarios     []benchmarkScenario `json:"scenarios"`
}

type benchmarkScenario struct {
	ID       string              `json:"id"`
	Title    string              `json:"title"`
	Question string              `json:"question"`
	Evidence []benchmarkEvidence `json:"evidence"`
	Gograph  benchmarkWorkflow   `json:"gograph"`
	Baseline benchmarkWorkflow   `json:"baseline"`
	Demo     benchmarkDemo       `json:"demo"`
}

type benchmarkEvidence struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Needle      string `json:"needle"`
}

type benchmarkWorkflow struct {
	Label string          `json:"label"`
	Steps []benchmarkStep `json:"steps"`
}

type benchmarkStep struct {
	Program string   `json:"program"`
	Args    []string `json:"args"`
}

type benchmarkDemo struct {
	Command string `json:"command"`
	Finding string `json:"finding"`
}

type benchmarkReport struct {
	SchemaVersion  string           `json:"schema_version"`
	Suite          string           `json:"suite"`
	Description    string           `json:"description"`
	GeneratedAt    string           `json:"generated_at"`
	SourceRevision string           `json:"source_revision"`
	SourceDirty    bool             `json:"source_dirty"`
	BinaryVersion  string           `json:"binary_version"`
	RunnerDigest   string           `json:"runner_sha256"`
	FixtureDigest  string           `json:"fixture_sha256"`
	SuiteDigest    string           `json:"suite_sha256"`
	AnalysisMode   string           `json:"analysis_mode"`
	Setup          setupResult      `json:"setup"`
	Limitations    []string         `json:"limitations"`
	Scenarios      []scenarioResult `json:"scenarios"`
}

type setupResult struct {
	FixtureTestsPassed bool   `json:"fixture_tests_passed"`
	BuildPassed        bool   `json:"build_passed"`
	Precision          string `json:"precision"`
	BuildStatus        string `json:"build_status"`
}

type scenarioResult struct {
	ID       string              `json:"id"`
	Title    string              `json:"title"`
	Question string              `json:"question"`
	Demo     benchmarkDemo       `json:"demo"`
	Evidence []benchmarkEvidence `json:"evidence"`
	Gograph  workflowResult      `json:"gograph"`
	Baseline workflowResult      `json:"baseline"`
	Passed   bool                `json:"passed"`
}

type workflowResult struct {
	Label            string           `json:"label"`
	ToolCalls        int              `json:"tool_calls"`
	MedianMillis     int64            `json:"median_millis"`
	OutputBytes      int              `json:"output_bytes"`
	OutputLines      int              `json:"output_lines"`
	OutputSHA256     string           `json:"output_sha256"`
	EvidenceFound    int              `json:"evidence_found"`
	EvidenceTotal    int              `json:"evidence_total"`
	EvidenceCoverage float64          `json:"evidence_coverage"`
	Evidence         []evidenceResult `json:"evidence"`
	Steps            []stepResult     `json:"steps"`
}

type evidenceResult struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Found       bool   `json:"found"`
}

type stepResult struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

type commandRun struct {
	steps    []stepResult
	combined string
	duration time.Duration
}

func main() {
	suitePath := flag.String("suite", "benchmarks/suite.json", "Path to the benchmark suite")
	repositoryRoot := flag.String("repository-root", ".", "Path to the gograph repository")
	gographBin := flag.String("gograph-bin", "bin/gograph", "Path to the gograph binary")
	outputPath := flag.String("output", "", "Write the complete JSON report to this path")
	demoOutputPath := flag.String("demo-output", "", "Also write the same verified report to the public demo data path")
	runs := flag.Int("runs", 3, "Number of measured workflow runs")
	flag.Parse()

	if err := runBenchmark(*repositoryRoot, *suitePath, *gographBin, *outputPath, *demoOutputPath, *runs); err != nil {
		fmt.Fprintln(os.Stderr, "benchmark:", err)
		os.Exit(1)
	}
}

func runBenchmark(repositoryRoot, suitePath, gographBin, outputPath, demoOutputPath string, runs int) error {
	if runs < 1 || runs > 20 {
		return fmt.Errorf("runs must be between 1 and 20")
	}

	root, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	suiteFile := rootedPath(root, suitePath)
	suiteBytes, err := os.ReadFile(suiteFile)
	if err != nil {
		return fmt.Errorf("read suite: %w", err)
	}
	var suite benchmarkSuite
	if err := json.Unmarshal(suiteBytes, &suite); err != nil {
		return fmt.Errorf("decode suite: %w", err)
	}
	if err := validateSuite(suite); err != nil {
		return err
	}

	binary := rootedPath(root, gographBin)
	binary, err = filepath.Abs(binary)
	if err != nil {
		return fmt.Errorf("resolve gograph binary: %w", err)
	}
	if info, err := os.Stat(binary); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("gograph binary is not a regular file: %s", binary)
	}

	fixtureSource := rootedPath(root, suite.Fixture)
	fixtureDigest, err := digestDirectory(fixtureSource)
	if err != nil {
		return fmt.Errorf("digest fixture: %w", err)
	}
	runnerBytes, err := os.ReadFile(filepath.Join(root, "scripts", "benchmark.go"))
	if err != nil {
		return fmt.Errorf("digest benchmark runner: %w", err)
	}
	fixtureDir, err := os.MkdirTemp("", "gograph-evidence-")
	if err != nil {
		return fmt.Errorf("create fixture workspace: %w", err)
	}
	defer os.RemoveAll(fixtureDir)
	if err := copyDirectory(fixtureSource, fixtureDir); err != nil {
		return fmt.Errorf("materialize fixture: %w", err)
	}

	setup, err := prepareFixture(fixtureDir, binary, suite.AnalysisMode)
	if err != nil {
		return err
	}
	binaryVersion, err := firstLine(runCommand(root, binary, "version"))
	if err != nil {
		return fmt.Errorf("read gograph version: %w", err)
	}
	revision := commandText(root, "git", "rev-parse", "HEAD")
	dirtyStatus := commandText(root, "git", "status", "--porcelain")
	dirty := dirtyStatus == "unknown" || strings.TrimSpace(dirtyStatus) != ""

	report := benchmarkReport{
		SchemaVersion:  "1",
		Suite:          suite.Name,
		Description:    suite.Description,
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		SourceRevision: revision,
		SourceDirty:    dirty,
		BinaryVersion:  binaryVersion,
		RunnerDigest:   digestBytes(runnerBytes),
		FixtureDigest:  fixtureDigest,
		SuiteDigest:    digestBytes(suiteBytes),
		AnalysisMode:   suite.AnalysisMode,
		Setup:          setup,
		Limitations:    append([]string(nil), suite.Limitations...),
	}

	allPassed := true
	for _, scenario := range suite.Scenarios {
		gographResult, err := measureWorkflow(fixtureDir, binary, scenario.ID, scenario.Gograph, scenario.Evidence, runs)
		if err != nil {
			return fmt.Errorf("scenario %s gograph workflow: %w", scenario.ID, err)
		}
		baselineResult, err := measureWorkflow(fixtureDir, binary, scenario.ID, scenario.Baseline, scenario.Evidence, runs)
		if err != nil {
			return fmt.Errorf("scenario %s baseline workflow: %w", scenario.ID, err)
		}
		passed := gographResult.EvidenceFound == len(scenario.Evidence)
		allPassed = allPassed && passed
		report.Scenarios = append(report.Scenarios, scenarioResult{
			ID: scenario.ID, Title: scenario.Title, Question: scenario.Question,
			Demo: scenario.Demo, Evidence: append([]benchmarkEvidence(nil), scenario.Evidence...),
			Gograph: gographResult, Baseline: baselineResult, Passed: passed,
		})
	}

	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	encoded = append(encoded, '\n')
	for _, path := range []string{outputPath, demoOutputPath} {
		if path == "" {
			continue
		}
		if err := writeFileAtomically(rootedPath(root, path), encoded); err != nil {
			return err
		}
	}

	printSummary(report)
	if outputPath == "" && demoOutputPath == "" {
		_, _ = os.Stdout.Write(encoded)
	}
	if !allPassed {
		return errors.New("one or more gograph workflows missed declared ground-truth evidence")
	}
	return nil
}

func validateSuite(suite benchmarkSuite) error {
	if suite.SchemaVersion != "1" {
		return fmt.Errorf("unsupported suite schema %q", suite.SchemaVersion)
	}
	if strings.TrimSpace(suite.Name) == "" || strings.TrimSpace(suite.Fixture) == "" {
		return errors.New("suite name and fixture are required")
	}
	if suite.AnalysisMode != "ast" && suite.AnalysisMode != "precise" {
		return fmt.Errorf("analysis_mode must be ast or precise")
	}
	if len(suite.Scenarios) == 0 {
		return errors.New("suite must contain at least one scenario")
	}
	seen := make(map[string]bool)
	for _, scenario := range suite.Scenarios {
		if scenario.ID == "" || seen[scenario.ID] {
			return fmt.Errorf("scenario IDs must be non-empty and unique: %q", scenario.ID)
		}
		seen[scenario.ID] = true
		if len(scenario.Evidence) == 0 || len(scenario.Gograph.Steps) == 0 || len(scenario.Baseline.Steps) == 0 {
			return fmt.Errorf("scenario %s requires evidence and both workflows", scenario.ID)
		}
		for _, evidence := range scenario.Evidence {
			if evidence.ID == "" || evidence.Needle == "" {
				return fmt.Errorf("scenario %s has incomplete evidence", scenario.ID)
			}
		}
	}
	return nil
}

func prepareFixture(dir, binary, mode string) (setupResult, error) {
	if out, err := runCommand(dir, "go", "test", "./..."); err != nil {
		return setupResult{}, fmt.Errorf("fixture tests failed: %w\n%s", err, out)
	}
	args := []string{"build", "."}
	if mode == "precise" {
		args = append(args, "--precise")
	}
	if out, err := runCommand(dir, binary, args...); err != nil {
		return setupResult{}, fmt.Errorf("fixture graph build failed: %w\n%s", err, out)
	}
	statsOut, err := runCommand(dir, binary, "stats", "--json")
	if err != nil {
		return setupResult{}, fmt.Errorf("fixture stats failed: %w\n%s", err, statsOut)
	}
	var envelope struct {
		Results struct {
			Precision   string `json:"precision"`
			BuildStatus string `json:"build_status"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(statsOut), &envelope); err != nil {
		return setupResult{}, fmt.Errorf("decode fixture stats: %w", err)
	}
	result := setupResult{FixtureTestsPassed: true, BuildPassed: true, Precision: envelope.Results.Precision, BuildStatus: envelope.Results.BuildStatus}
	if mode == "precise" && result.Precision != "precise" {
		return setupResult{}, fmt.Errorf("fixture requested precise analysis but reported %q", result.Precision)
	}
	if result.BuildStatus != "complete" {
		return setupResult{}, fmt.Errorf("fixture build status is %q, want complete", result.BuildStatus)
	}
	return result, nil
}

func measureWorkflow(dir, binary, scenarioID string, workflow benchmarkWorkflow, evidence []benchmarkEvidence, runs int) (workflowResult, error) {
	measurements := make([]time.Duration, 0, runs)
	var sample commandRun
	for i := 0; i < runs; i++ {
		run, err := executeWorkflow(dir, binary, scenarioID, workflow)
		if err != nil {
			return workflowResult{}, err
		}
		measurements = append(measurements, run.duration)
		if i == 0 {
			sample = run
		} else if sample.combined != run.combined {
			return workflowResult{}, fmt.Errorf("workflow output changed between measured runs:\n--- first ---\n%s\n--- later ---\n%s", sample.combined, run.combined)
		}
	}

	checks := evaluateEvidence(sample.combined, evidence)
	found := 0
	for _, check := range checks {
		if check.Found {
			found++
		}
	}
	coverage := float64(found) / float64(len(evidence))
	return workflowResult{
		Label: workflow.Label, ToolCalls: len(workflow.Steps), MedianMillis: medianMillis(measurements),
		OutputBytes: len(sample.combined), OutputLines: lineCount(sample.combined), OutputSHA256: digestBytes([]byte(sample.combined)),
		EvidenceFound: found, EvidenceTotal: len(evidence), EvidenceCoverage: coverage,
		Evidence: checks, Steps: sample.steps,
	}, nil
}

func executeWorkflow(dir, binary, scenarioID string, workflow benchmarkWorkflow) (commandRun, error) {
	started := time.Now()
	var result commandRun
	var combined strings.Builder
	for _, step := range workflow.Steps {
		program := step.Program
		args := append([]string(nil), step.Args...)
		if program == "{gograph}" {
			program = binary
			args = append([]string{"--intention", "benchmark " + scenarioID}, args...)
		}
		out, err := runCommand(dir, program, args...)
		if err != nil {
			return commandRun{}, fmt.Errorf("%s %s: %w\n%s", program, strings.Join(args, " "), err, out)
		}
		normalized := normalizeOutput(out, dir)
		command := displayCommand(step.Program, step.Args)
		result.steps = append(result.steps, stepResult{Command: command, ExitCode: 0, Output: normalized})
		combined.WriteString(normalized)
		if !strings.HasSuffix(normalized, "\n") {
			combined.WriteByte('\n')
		}
	}
	result.duration = time.Since(started)
	result.combined = combined.String()
	return result, nil
}

func evaluateEvidence(output string, evidence []benchmarkEvidence) []evidenceResult {
	results := make([]evidenceResult, 0, len(evidence))
	for _, item := range evidence {
		results = append(results, evidenceResult{ID: item.ID, Description: item.Description, Found: strings.Contains(output, item.Needle)})
	}
	return results
}

func prepareOutputPath(path string) error {
	if path == "" {
		return nil
	}
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

func writeFileAtomically(path string, data []byte) error {
	if err := prepareOutputPath(path); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".benchmark-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary output: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write temporary output: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary output: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("publish output: %w", err)
	}
	return nil
}

func copyDirectory(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("fixture contains non-regular entry %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func digestDirectory(root string) (string, error) {
	type fileDigest struct{ path, hash string }
	var files []fileDigest
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular fixture entry %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, fileDigest{path: filepath.ToSlash(rel), hash: digestBytes(data)})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	var combined strings.Builder
	for _, file := range files {
		combined.WriteString(file.path)
		combined.WriteByte(0)
		combined.WriteString(file.hash)
		combined.WriteByte('\n')
	}
	return digestBytes([]byte(combined.String())), nil
}

func normalizeOutput(output, fixtureDir string) string {
	return strings.ReplaceAll(output, fixtureDir, "<fixture>")
}

func displayCommand(program string, args []string) string {
	name := program
	if program == "{gograph}" {
		name = "gograph"
	}
	return strings.TrimSpace(name + " " + strings.Join(args, " "))
}

func medianMillis(values []time.Duration) int64 {
	copied := append([]time.Duration(nil), values...)
	sort.Slice(copied, func(i, j int) bool { return copied[i] < copied[j] })
	return copied[len(copied)/2].Milliseconds()
}

func lineCount(value string) int {
	if value == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(value, "\n"), "\n") + 1
}

func rootedPath(root, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(root, path)
}

func runCommand(dir, program string, args ...string) (string, error) {
	cmd := exec.Command(program, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func commandText(dir, program string, args ...string) string {
	out, err := runCommand(dir, program, args...)
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(out)
}

func firstLine(output string, err error) (string, error) {
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	return line, nil
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func printSummary(report benchmarkReport) {
	fmt.Printf("Benchmark suite: %s\n", report.Suite)
	fmt.Printf("Fixture: %s, precision: %s, build: %s\n\n", report.FixtureDigest[:12], report.Setup.Precision, report.Setup.BuildStatus)
	fmt.Printf("%-34s  %-22s  %6s  %8s  %8s\n", "SCENARIO", "WORKFLOW", "CALLS", "EVIDENCE", "MEDIAN")
	for _, scenario := range report.Scenarios {
		fmt.Printf("%-34s  %-22s  %6d  %3d/%-4d  %6dms\n", scenario.ID, "gograph", scenario.Gograph.ToolCalls, scenario.Gograph.EvidenceFound, scenario.Gograph.EvidenceTotal, scenario.Gograph.MedianMillis)
		fmt.Printf("%-34s  %-22s  %6d  %3d/%-4d  %6dms\n", "", "text-search baseline", scenario.Baseline.ToolCalls, scenario.Baseline.EvidenceFound, scenario.Baseline.EvidenceTotal, scenario.Baseline.MedianMillis)
	}
	fmt.Println("\nEvidence coverage is checked against the suite's manually reviewable fixture ground truth; see limitations in the JSON report.")
}
