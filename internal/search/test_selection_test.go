package search_test

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func selectionGraph() *graph.Graph {
	return &graph.Graph{
		Build: &graph.BuildMetadata{
			Precision: graph.PrecisionPrecise, TestCallResolution: graph.TestCallResolutionTyped,
			Selection: &graph.BuildSelection{BuildTags: []string{"integration"}},
		},
		Symbols: []graph.SymbolNode{
			{ID: "example.com/app/calc::Mul", Kind: graph.KindFunction, Name: "Mul", PackageName: "calc", File: "calc/calc.go", Line: 3},
			{ID: "example.com/app/calc::Add", Kind: graph.KindFunction, Name: "Add", PackageName: "calc", File: "calc/calc.go", Line: 4},
			{ID: "example.com/app/calc::(Sq).Area", Kind: graph.KindMethod, Name: "Area", Receiver: "Sq", PackageName: "calc", File: "calc/calc.go", Line: 5},
			{ID: "example.com/app/calc::Total", Kind: graph.KindFunction, Name: "Total", PackageName: "calc", File: "calc/calc.go", Line: 6},
			{ID: "example.com/app/calc::Sq", Kind: graph.KindStruct, Name: "Sq", PackageName: "calc", File: "calc/calc.go", Line: 2},
			{ID: "example.com/app/calc::TestMul", Kind: graph.KindFunction, Name: "TestMul", PackageName: "calc", File: "calc/calc_test.go", Line: 5},
			{ID: "example.com/app/calc::TestMulIntegration", Kind: graph.KindFunction, Name: "TestMulIntegration", PackageName: "calc", File: "calc/integ_test.go", Line: 5},
			{ID: "example.com/app/calc::TestTotal", Kind: graph.KindFunction, Name: "TestTotal", PackageName: "calc", File: "calc/calc_test.go", Line: 6},
			{ID: "example.com/app/calc::TestAdd", Kind: graph.KindFunction, Name: "TestAdd", PackageName: "calc", File: "calc/calc_test.go", Line: 7},
			{ID: "example.com/app/calc::BenchmarkMul", Kind: graph.KindFunction, Name: "BenchmarkMul", PackageName: "calc", File: "calc/calc_test.go", Line: 8},
			{ID: "example.com/app/app::Run", Kind: graph.KindFunction, Name: "Run", PackageName: "app", File: "app/app.go", Line: 5},
			{ID: "example.com/app/app::TestRun", Kind: graph.KindFunction, Name: "TestRun", PackageName: "app_test", File: "app/app_test.go", Line: 9},
		},
		TestEdges: []graph.TestEdge{
			{TestFunc: "TestMul", Target: "Mul", TargetSymbolID: "example.com/app/calc::Mul", Resolution: graph.CallResolutionStatic, File: "calc/calc_test.go", Line: 5},
			{TestFunc: "TestMulIntegration", Target: "Mul", TargetSymbolID: "example.com/app/calc::Mul", Resolution: graph.CallResolutionStatic, File: "calc/integ_test.go", Line: 5},
			{TestFunc: "TestTotal", Target: "Total", TargetSymbolID: "example.com/app/calc::Total", Resolution: graph.CallResolutionStatic, File: "calc/calc_test.go", Line: 6},
			{TestFunc: "TestAdd", Target: "Add", TargetSymbolID: "example.com/app/calc::Add", Resolution: graph.CallResolutionStatic, File: "calc/calc_test.go", Line: 7},
			{TestFunc: "BenchmarkMul", Target: "Mul", TargetSymbolID: "example.com/app/calc::Mul", Resolution: graph.CallResolutionStatic, File: "calc/calc_test.go", Line: 8},
			{TestFunc: "TestRun", Target: "app.Run", TargetSymbolID: "example.com/app/app::Run", Resolution: graph.CallResolutionStatic, File: "app/app_test.go", Line: 9},
		},
		Calls: []graph.CallEdge{
			{CallerSymbolID: "example.com/app/calc::(Sq).Area", CalleeSymbolID: "example.com/app/calc::Mul", Resolution: graph.CallResolutionStatic},
			{CallerSymbolID: "example.com/app/calc::Total", CalleeSymbolID: "example.com/app/calc::(Sq).Area", Resolution: graph.CallResolutionCHA},
			{CallerSymbolID: "example.com/app/app::Run", CalleeSymbolID: "example.com/app/calc::Mul", Resolution: graph.CallResolutionStatic},
		},
	}
}

func packageRuns(selection search.TestSelection) map[string]string {
	runs := make(map[string]string)
	for _, pkg := range selection.Packages {
		if pkg.RunAll {
			runs[pkg.ImportPath] = "ALL"
			continue
		}
		runs[pkg.ImportPath] = strings.Join(pkg.RunPatterns, " ")
	}
	return runs
}

func TestSelectTestsExactAndPossiblePaths(t *testing.T) {
	snapshot := search.NewSnapshot(selectionGraph())
	roots := []search.SelectionInput{{StableID: "example.com/app/calc::Mul"}}
	all := snapshot.SelectTests("Mul", roots, false)
	wantAll := map[string]string{
		"example.com/app/app":  "ALL",
		"example.com/app/calc": "^(TestMul|TestMulIntegration|TestTotal)$",
	}
	if got := packageRuns(all); !reflect.DeepEqual(got, wantAll) {
		t.Fatalf("runs = %#v, want %#v", got, wantAll)
	}
	if all.ExactCount != 4 || all.PossibleCount != 1 || all.TestCount != 5 {
		t.Fatalf("counts exact=%d possible=%d total=%d", all.ExactCount, all.PossibleCount, all.TestCount)
	}
	calc := all.Packages[1]
	if calc.BenchPattern != "^(BenchmarkMul)$" {
		t.Fatalf("bench pattern = %q", calc.BenchPattern)
	}
	wantCommands := []string{
		"go test -tags=integration example.com/app/calc -run '^(TestMul|TestMulIntegration|TestTotal)$'",
		"go test -tags=integration example.com/app/calc -run '^$' -bench '^(BenchmarkMul)$'",
	}
	if !reflect.DeepEqual(calc.Commands, wantCommands) {
		t.Fatalf("commands = %#v", calc.Commands)
	}

	exact := snapshot.SelectTests("Mul", roots, true)
	if got := packageRuns(exact)["example.com/app/calc"]; got != "^(TestMul|TestMulIntegration)$" {
		t.Fatalf("exact-only calc run = %q", got)
	}
	if exact.PossibleCount != 0 {
		t.Fatalf("exact-only kept possible tests: %d", exact.PossibleCount)
	}
}

func TestSelectTestsPatternsMatchOnlySelectedNames(t *testing.T) {
	selection := search.NewSnapshot(selectionGraph()).SelectTests("Add", []search.SelectionInput{{StableID: "example.com/app/calc::Add"}}, false)
	pattern := packageRuns(selection)["example.com/app/calc"]
	re := regexp.MustCompile(pattern)
	for name, want := range map[string]bool{"TestAdd": true, "TestAddMore": false, "XTestAdd": false, "TestMul": false} {
		if got := re.MatchString(name); got != want {
			t.Errorf("pattern %q matches %q = %v, want %v", pattern, name, got, want)
		}
	}
}

func TestSelectTestsChangesDeletedAndUnselected(t *testing.T) {
	snapshot := search.NewSnapshot(selectionGraph())
	changes := &search.ChangesResult{Evaluation: "complete", Symbols: []search.ChangedSymbol{
		{Name: "Mul", StableID: "example.com/app/calc::Mul", File: "calc/calc.go", Status: search.ChangeDeleted},
		{Name: "TestMul", StableID: "example.com/app/calc::TestMul", File: "calc/calc_test.go", Status: search.ChangeDeleted},
		{Name: "Sq", StableID: "example.com/app/calc::Sq", File: "calc/calc.go", Status: search.ChangeModified},
		{Name: "NewF", StableID: "example.com/app/calc::NewF", File: "calc/new.go", Status: search.ChangeNew},
		{Name: "Gone", StableID: "example.com/app/calc::Gone", File: "calc/gone.go", Status: search.ChangeDeleted},
		{Name: "_", StableID: "example.com/app/calc::_", File: "calc/calc.go", Status: search.ChangeModified},
	}}
	selection := snapshot.SelectTests("--uncommitted", search.SelectionInputsFromChanges(changes), false)
	// The deleted Mul is still in the pre-change graph: its callers' tests are
	// selected, except TestMul, which the change deleted.
	if got := packageRuns(selection)["example.com/app/calc"]; got != "^(TestMulIntegration|TestTotal)$" {
		t.Fatalf("calc run = %q", got)
	}
	if selection.Status != "partial" {
		t.Fatalf("status = %q, want partial", selection.Status)
	}
	var reasons []string
	for _, unselected := range selection.Unselected {
		reasons = append(reasons, unselected.Name)
	}
	if want := []string{"Gone", "NewF"}; !reflect.DeepEqual(reasons, want) {
		t.Fatalf("unselected = %v, want %v", reasons, want)
	}
	// The changed struct Sq selects the tests of its method Area.
	var sq *search.SelectionRoot
	for i := range selection.Roots {
		if selection.Roots[i].Name == "Sq" {
			sq = &selection.Roots[i]
		}
	}
	if sq == nil || sq.Users != 1 || sq.Tests == 0 {
		t.Fatalf("Sq root = %+v, want one user with tests", sq)
	}
}

func TestAnchoredTestPatternsSplitLongSelections(t *testing.T) {
	var names []string
	for i := 0; i < 3000; i++ {
		names = append(names, "TestCase"+strings.Repeat("x", i%7)+itoa(i))
	}
	patterns := search.AnchoredTestPatternsForTest(names, 4096)
	if len(patterns) < 2 {
		t.Fatalf("expected several patterns, got %d", len(patterns))
	}
	matched := 0
	for _, pattern := range patterns {
		if len(pattern) > 4096 {
			t.Fatalf("pattern length %d exceeds limit", len(pattern))
		}
		re := regexp.MustCompile(pattern)
		for _, name := range names {
			if re.MatchString(name) {
				matched++
			}
		}
		if re.MatchString("TestNotSelected") {
			t.Fatalf("pattern matches an unselected name")
		}
	}
	if matched != len(names) {
		t.Fatalf("patterns matched %d of %d names", matched, len(names))
	}
}

func itoa(value int) string {
	digits := ""
	for {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
		if value == 0 {
			return digits
		}
	}
}

func TestSelectTestsChangedTestSelectsItselfAndOutputIsDeterministic(t *testing.T) {
	snapshot := search.NewSnapshot(selectionGraph())
	roots := []search.SelectionInput{
		{StableID: "example.com/app/calc::TestAdd", Change: "modified"},
		{StableID: "example.com/app/app::Run", Change: "modified"},
	}
	first := snapshot.SelectTests("--since main", roots, false)
	reversed := snapshot.SelectTests("--since main", []search.SelectionInput{roots[1], roots[0]}, false)
	if !reflect.DeepEqual(first.Packages, reversed.Packages) {
		t.Fatalf("selection depends on root order:\n%#v\n%#v", first.Packages, reversed.Packages)
	}
	want := map[string]string{"example.com/app/app": "ALL", "example.com/app/calc": "^(TestAdd)$"}
	if got := first.Packages[0].Commands; !reflect.DeepEqual(got, []string{"go test -tags=integration example.com/app/app"}) {
		t.Fatalf("run-all commands = %#v", got)
	}
	if got := packageRuns(first); !reflect.DeepEqual(got, want) {
		t.Fatalf("runs = %#v, want %#v", got, want)
	}
}
