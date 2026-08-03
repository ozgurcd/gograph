package cli_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestPreciseInterfaceCallerFormsAndBuildDeterminism(t *testing.T) {
	repositoryRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "gograph")
	buildBinary := exec.Command("go", "build", "-o", binary, "./cmd/gograph")
	buildBinary.Dir = repositoryRoot
	if output, err := buildBinary.CombinedOutput(); err != nil {
		t.Fatalf("build gograph: %v\n%s", err, output)
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/interfacecall\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const source = `package main

type Deleter interface {
	Delete(string) error
}

type StateRepository interface {
	Deleter
}

type MemoryStateRepository struct{}

func (*MemoryStateRepository) Delete(string) error { return nil }

type SQLStateRepository struct{}

func (*SQLStateRepository) Delete(string) error { return nil }

func purge(states StateRepository) error {
	return states.Delete("state")
}

func main() {
	_ = purge(&MemoryStateRepository{})
}
`
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) []byte {
		t.Helper()
		command := exec.Command(binary, args...)
		command.Dir = root
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("gograph %v: %v\n%s", args, err, output)
		}
		return output
	}
	readTargets := func() []string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, ".gograph", "graph.json"))
		if err != nil {
			t.Fatal(err)
		}
		var indexed graph.Graph
		if err := json.Unmarshal(data, &indexed); err != nil {
			t.Fatal(err)
		}
		if testing.CoverMode() != "" && indexed.Build.EffectivePrecision() == graph.PrecisionFallback {
			for _, warning := range indexed.Build.Warnings {
				if strings.Contains(warning, "reading srcfiles list: cache entry not found") {
					t.Skipf("local Go coverage toolchain cannot provide compiled source metadata to packages.Load: %s", warning)
				}
			}
		}
		if got := indexed.Build.EffectivePrecision(); got != graph.PrecisionPrecise {
			t.Fatalf("precision = %q, want %q", got, graph.PrecisionPrecise)
		}
		var targets []string
		for _, call := range indexed.Calls {
			if call.CallerName == "purge" && call.CalleeRaw == "states.Delete" {
				targets = append(targets, call.CalleeSymbolID)
			}
		}
		return targets
	}

	run("build", ".", "--precise")
	firstTargets := readTargets()
	wantTargets := []string{
		"example.com/interfacecall::(*MemoryStateRepository).Delete",
		"example.com/interfacecall::(*SQLStateRepository).Delete",
	}
	if !reflect.DeepEqual(firstTargets, wantTargets) {
		t.Fatalf("precise interface targets = %#v, want %#v", firstTargets, wantTargets)
	}
	readStatsPrecision := func() graph.PrecisionMode {
		t.Helper()
		var stats struct {
			Results struct {
				Precision graph.PrecisionMode `json:"precision"`
			} `json:"results"`
		}
		if err := json.Unmarshal(run("stats", "--json"), &stats); err != nil {
			t.Fatalf("decode stats: %v", err)
		}
		return stats.Results.Precision
	}
	if got := readStatsPrecision(); got != graph.PrecisionPrecise {
		t.Fatalf("stats precision = %q, want %q", got, graph.PrecisionPrecise)
	}

	type envelope struct {
		Count   int `json:"count"`
		Results []struct {
			Name           string `json:"name"`
			CallSiteFile   string `json:"call_site_file"`
			CallSiteLine   int    `json:"call_site_line"`
			CallSiteColumn int    `json:"call_site_column"`
		} `json:"results"`
	}
	callOffset := strings.Index(source, "states.Delete(")
	if callOffset < 0 {
		t.Fatal("fixture Delete call not found")
	}
	wantCallLine := strings.Count(source[:callOffset], "\n") + 1
	query := func(term string, exact bool) envelope {
		t.Helper()
		args := []string{"callers", term, "--json"}
		if exact {
			args = append(args, "--exact")
		}
		var result envelope
		if err := json.Unmarshal(run(args...), &result); err != nil {
			t.Fatalf("decode callers %q: %v", term, err)
		}
		if result.Count != 1 || len(result.Results) != 1 {
			t.Fatalf("callers %q returned count=%d results=%#v", term, result.Count, result.Results)
		}
		if result.Results[0].Name != "purge" || result.Results[0].CallSiteFile != "main.go" || result.Results[0].CallSiteLine != wantCallLine || result.Results[0].CallSiteColumn <= 0 {
			t.Fatalf("callers %q returned wrong site: %#v", term, result.Results[0])
		}
		return result
	}

	type queryCase struct {
		term  string
		exact bool
	}
	queries := []queryCase{
		{term: "Delete"},
		{term: "Delete", exact: true},
		{term: "MemoryStateRepository.Delete", exact: true},
		{term: "SQLStateRepository.Delete", exact: true},
		{term: wantTargets[0], exact: true},
		{term: wantTargets[1], exact: true},
		{term: "StateRepository.Delete"},
		{term: "StateRepository.Delete", exact: true},
		{term: "example.com/interfacecall::StateRepository.Delete", exact: true},
	}
	firstResults := make(map[queryCase]envelope, len(queries))
	var formBaseline envelope
	for _, test := range queries {
		result := query(test.term, test.exact)
		firstResults[test] = result
		if len(formBaseline.Results) == 0 {
			formBaseline = result
		} else if !reflect.DeepEqual(result, formBaseline) {
			t.Fatalf("caller form %q disagrees with the baseline: baseline=%#v actual=%#v", test.term, formBaseline, result)
		}
	}

	run("build", ".", "--precise")
	secondTargets := readTargets()
	if !reflect.DeepEqual(firstTargets, secondTargets) {
		t.Fatalf("repeated precise build changed targets:\nfirst:  %#v\nsecond: %#v", firstTargets, secondTargets)
	}
	for _, test := range queries {
		if got := query(test.term, test.exact); !reflect.DeepEqual(got, firstResults[test]) {
			t.Fatalf("repeated precise build changed callers %q: first=%#v second=%#v", test.term, firstResults[test], got)
		}
	}

	brokenSource := source + "\nfunc broken() { missingSymbol() }\n"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(brokenSource), 0o644); err != nil {
		t.Fatal(err)
	}
	run("build", ".", "--precise")
	if got := readStatsPrecision(); got != graph.PrecisionFallback {
		t.Fatalf("fallback stats precision = %q, want %q", got, graph.PrecisionFallback)
	}
	run("build", ".")
	if got := readStatsPrecision(); got != graph.PrecisionAST {
		t.Fatalf("AST stats precision = %q, want %q", got, graph.PrecisionAST)
	}
}
