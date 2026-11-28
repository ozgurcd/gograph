package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
	"github.com/ozgurcd/gograph/internal/testmemory"
)

// testMemoryRequest holds the tests flags that read or write test memory.
type testMemoryRequest struct {
	skipPassed bool
	record     string // go test -json file, or "-" for stdin
	goFlags    string
	inputs     []string
	trustEnv   bool
	graph      *graph.Graph // the graph the selection was made from
}

// testMemory is set while a tests selection should leave out tests that
// already passed on identical inputs.
var testMemory *testMemoryRequest

// parse consumes one test-memory flag at args[*index], if it is one.
func (r *testMemoryRequest) parse(args []string, index *int) (bool, error) {
	value := func(name string) (string, error) {
		if *index+1 >= len(args) {
			return "", fmt.Errorf("%s requires a value", name)
		}
		*index++
		return args[*index], nil
	}
	argument := args[*index]
	name, inline, hasInline := strings.Cut(argument, "=")
	switch name {
	case "--skip-passed", "--trust-env":
		if hasInline {
			return true, fmt.Errorf("%s takes no value", name)
		}
		if name == "--trust-env" {
			r.trustEnv = true
		} else {
			r.skipPassed = true
		}
	case "--record", "--go-flags", "--input":
		v := inline
		if !hasInline {
			var err error
			if v, err = value(name); err != nil {
				return true, err
			}
		}
		switch name {
		case "--record":
			if v == "" {
				return true, fmt.Errorf("--record requires a go test -json file or -")
			}
			r.record = v
		case "--go-flags":
			r.goFlags = v
		case "--input":
			if strings.TrimSpace(v) == "" {
				return true, fmt.Errorf("--input requires a repository-relative path")
			}
			r.inputs = append(r.inputs, v)
		}
	default:
		return false, nil
	}
	return true, nil
}

// openTestMemory opens test memory for a current graph, or explains why it
// cannot be used; then nothing is skipped or recorded.
func openTestMemory(g *graph.Graph, request testMemoryRequest) (*testmemory.Memory, string) {
	if stale := loadedGraphStale(g); stale.IsStale {
		return nil, "the graph is stale, so the import closure of each test may be out of date; run gograph build . first"
	}
	memory, ok, err := testmemory.Open(graphRoot(g), g, testmemory.Options{
		GoFlags: request.goFlags, Inputs: request.inputs, TrustEnv: request.trustEnv,
		BuildTags: append([]string(nil), queryBuildTags...),
	})
	if !ok {
		return nil, testmemory.DirEnv + "=off disables test memory"
	}
	if err != nil {
		return nil, err.Error()
	}
	return memory, ""
}

// skipPassedTests removes tests that already passed on identical inputs from
// selection and reports them on stderr (text) or in previously_passed (JSON).
func skipPassedTests(selection *search.TestSelection, request testMemoryRequest) int {
	g := request.graph
	if g == nil {
		return 0
	}
	memory, reason := openTestMemory(g, request)
	if memory == nil {
		fmt.Fprintf(os.Stderr, "gograph tests: test memory unavailable (%s); no test is skipped\n", reason)
		return 0
	}
	keys := map[string]string{}
	selection.SkipPassed(func(importPath, dir, name string) bool {
		key, ok := keys[importPath]
		if !ok {
			var err error
			if key, err = memory.Key(importPath, dir); err != nil {
				fmt.Fprintf(os.Stderr, "gograph tests: test memory unavailable for %s (%v); its tests run\n", importPath, err)
			}
			keys[importPath] = key
		}
		return key != "" && memory.Passed(key, name)
	})
	if !jsonMode && len(selection.PreviouslyPassed) > 0 {
		names := make([]string, 0, len(selection.PreviouslyPassed))
		for _, test := range selection.PreviouslyPassed {
			names = append(names, test.Package+"."+test.Name)
		}
		fmt.Fprintf(os.Stderr, "gograph tests: skipped %d test(s) that already passed on identical inputs: %s\n", len(names), strings.Join(names, " "))
	}
	return 0
}

// goTestEvent is one line of go test -json output.
type goTestEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
}

// runTestRecord reads go test -json output and records each top-level test
// that passed, and forgets each that failed, under its package's input key.
func runTestRecord(g *graph.Graph, request testMemoryRequest) int {
	memory, reason := openTestMemory(g, request)
	if memory == nil {
		return failCommandf("tests", "test memory unavailable: %s", reason)
	}
	var input io.Reader = os.Stdin
	if request.record != "-" {
		file, err := os.Open(request.record)
		if err != nil {
			return failCommandf("tests", "open go test -json output: %v", err)
		}
		defer func() { _ = file.Close() }()
		input = file
	}
	type outcome struct{ passed, failed []string }
	outcomes := map[string]*outcome{}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var event goTestEvent
		if json.Unmarshal([]byte(line), &event) != nil || event.Test == "" || strings.Contains(event.Test, "/") {
			continue
		}
		if event.Action != "pass" && event.Action != "fail" {
			continue
		}
		o := outcomes[event.Package]
		if o == nil {
			o = &outcome{}
			outcomes[event.Package] = o
		}
		if event.Action == "pass" {
			o.passed = append(o.passed, event.Test)
		} else {
			o.failed = append(o.failed, event.Test)
		}
	}
	if err := scanner.Err(); err != nil {
		return failCommandf("tests", "read go test -json output: %v", err)
	}
	packages := make([]string, 0, len(outcomes))
	for importPath := range outcomes {
		packages = append(packages, importPath)
	}
	sort.Strings(packages)
	passed, failed := 0, 0
	var skipped []string
	for _, importPath := range packages {
		dir, ok := memory.PackageDir(importPath)
		if !ok {
			skipped = append(skipped, importPath+" (not in the graph)")
			continue
		}
		key, err := memory.Key(importPath, dir)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s (%v)", importPath, err))
			continue
		}
		o := outcomes[importPath]
		if err := memory.Update(key, importPath, o.passed, o.failed); err != nil {
			return failCommandf("tests", "record %s: %v", importPath, err)
		}
		passed += len(o.passed)
		failed += len(o.failed)
	}
	fmt.Printf("recorded %d passed and forgot %d failed test(s) in %d package(s) (%s)\n", passed, failed, len(packages)-len(skipped), memory.GoVersion())
	for _, s := range skipped {
		fmt.Fprintf(os.Stderr, "not recorded: %s\n", s)
	}
	return 0
}
