package search

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func coverageGraph() *graph.Graph {
	return &graph.Graph{
		Packages: []graph.PackageNode{{ImportPathBestEffort: "example.com/app/core", Files: []string{"core/core.go", "core/core_test.go"}}},
		Symbols: []graph.SymbolNode{
			{ID: "example.com/app/core::Run", Kind: graph.KindFunction, Name: "Run", PackageName: "core", File: "core/core.go", Line: 3, EndLine: 6},
			{ID: "example.com/app/core::Idle", Kind: graph.KindFunction, Name: "Idle", PackageName: "core", File: "core/core.go", Line: 8, EndLine: 10},
			{ID: "example.com/app/core::Orphan", Kind: graph.KindFunction, Name: "Orphan", PackageName: "core", File: "core/core.go", Line: 12, EndLine: 14},
			{ID: "example.com/app/core::(E).Error", Kind: graph.KindMethod, Name: "Error", Receiver: "E", PackageName: "core", File: "core/core.go", Line: 16, EndLine: 16},
			{ID: "example.com/app/core::(*S).Handle", Kind: graph.KindMethod, Name: "Handle", Receiver: "*S", PackageName: "core", File: "core/core.go", Line: 18, EndLine: 18},
			{ID: "example.com/app/core::S", Kind: graph.KindStruct, Name: "S", PackageName: "core", File: "core/core.go", Line: 17},
			{ID: "example.com/app/core::Handler", Kind: graph.KindInterface, Name: "Handler", PackageName: "core", File: "core/core.go", Line: 20, InterfaceMethods: map[string]string{"Handle": "func()"}},
		},
		Implements: []graph.ImplementsEdge{{ConcreteID: "example.com/app/core::S", InterfaceID: "example.com/app/core::Handler"}},
		Calls: []graph.CallEdge{
			{File: "core/main.go", CalleeSymbolID: "example.com/app/core::Run"},
			{File: "core/main.go", CalleeSymbolID: "example.com/app/core::Idle"},
			{File: "core/main.go", CalleeSymbolID: "example.com/app/core::Idle"},
			{File: "core/main.go", CalleeSymbolID: "example.com/app/core::(E).Error"},
			{File: "core/main.go", CalleeSymbolID: "example.com/app/core::(E).Error"},
			{File: "core/main.go", CalleeSymbolID: "example.com/app/core::(E).Error"},
			{File: "core/main.go", CalleeSymbolID: "example.com/app/core::(*S).Handle"},
		},
		TestEdges: []graph.TestEdge{{TestFunc: "TestX", Target: "Error", TargetSymbolID: "example.com/app/core::(E).Error", Resolution: graph.CallResolutionCHA, File: "core/core_test.go"}},
	}
}

const profileA = `mode: set
example.com/app/core/core.go:3.20,5.2 2 1
example.com/app/core/core.go:5.2,6.2 1 0
example.com/app/core/core.go:8.20,10.2 2 0
example.com/app/core/core.go:12.20,14.2 1 0
example.com/app/core/core.go:16.20,16.40 1 0
example.com/other/x.go:1.1,2.2 1 1
`

// profileB is a second run merged into the same file.
const profileB = `mode: set
example.com/app/core/core.go:8.20,10.2 2 1
`

func TestParseCoverProfileMapsAndMergesRuns(t *testing.T) {
	profile, err := ParseCoverProfile([]byte(profileA+profileB), coverageGraph())
	if err != nil {
		t.Fatal(err)
	}
	if profile.UnmappedFiles != 1 || len(profile.Files["core/core.go"]) != 5 {
		t.Fatalf("profile = %+v", profile)
	}
	if statements, executed, known := profile.statementsIn("core/core.go", 8, 10); !known || statements != 2 || executed != 2 {
		t.Fatalf("merged Idle statements=%d executed=%d known=%v", statements, executed, known)
	}
	for _, bad := range []string{"", "example.com/app/core/core.go:1.1,2.2 1 1\n", "mode: set\nx.go:1.1 1 1\n", "mode: set\nmode: count\n"} {
		if _, err := ParseCoverProfile([]byte(bad), coverageGraph()); err == nil {
			t.Errorf("accepted malformed profile %q", bad)
		}
	}
}

func TestUntestedRanksNoneFirstAndExcludesInterfaceMethods(t *testing.T) {
	snapshot := NewSnapshot(coverageGraph())
	results, summary := snapshot.UntestedWith(UntestedOptions{})
	var order []string
	for _, result := range results {
		order = append(order, result.Name+":"+result.TestResolution)
	}
	// Error has the most callers but only a possible (dynamic) test path.
	if want := []string{"Idle:none", "Handle:none", "Run:none", "Error:possible"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	results, summary = snapshot.UntestedWith(UntestedOptions{ExcludeInterfaceMethods: true})
	order = nil
	for _, result := range results {
		order = append(order, result.Name)
	}
	if want := []string{"Idle", "Run"}; !reflect.DeepEqual(order, want) || summary.SkippedInterfaceMethods != 2 {
		t.Fatalf("excluded order = %v skipped=%d", order, summary.SkippedInterfaceMethods)
	}
}

func TestUntestedWithCoverageNeverListsExecutedFunctions(t *testing.T) {
	g := coverageGraph()
	profile, err := ParseCoverProfile([]byte(profileA), g)
	if err != nil {
		t.Fatal(err)
	}
	results, summary := NewSnapshot(g).UntestedWith(UntestedOptions{Coverage: profile})
	var names []string
	for _, result := range results {
		names = append(names, result.Name)
		if result.ExecutedStatements == nil || *result.ExecutedStatements != 0 || result.Statements == 0 {
			t.Fatalf("%s statements=%d executed=%v", result.Name, result.Statements, result.ExecutedStatements)
		}
	}
	// Run executed (partly) and is never listed; Orphan has no callers but
	// never ran; Handle's file has data but no statements in its range.
	if want := []string{"Idle", "Orphan", "Error"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	if summary.ExecutedFunctions != 1 || summary.UnmappedProfileFiles != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	merged, _ := ParseCoverProfile([]byte(profileA+profileB), g)
	results, _ = NewSnapshot(g).UntestedWith(UntestedOptions{Coverage: merged})
	for _, result := range results {
		if result.Name == "Idle" {
			t.Fatal("a function executed in a merged run was listed")
		}
	}
}

func TestUncoveredReportsExactChangedRanges(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/core/core.go b/core/core.go",
		"--- a/core/core.go",
		"+++ b/core/core.go",
		"@@ -4,0 +5,2 @@ func Run() {",
		"@@ -9 +11 @@",
		"@@ -20,3 +21,0 @@",
		"diff --git a/gone.go b/gone.go",
		"+++ /dev/null",
		"@@ -1,3 +0,0 @@",
	}, "\n")
	changed := parseUnifiedDiffAdditions([]byte(diff))
	if want := map[string][]int{"core/core.go": {5, 6, 11}}; !reflect.DeepEqual(changed, want) {
		t.Fatalf("changed = %v", changed)
	}
	profile, err := ParseCoverProfile([]byte(profileA), coverageGraph())
	if err != nil {
		t.Fatal(err)
	}
	changed["core/core.go"] = []int{4, 5, 6, 9, 10, 13, 30}
	changed["core/none.go"] = []int{1}
	report := Uncovered("--since main", "", changed, profile)
	if report.ChangedLines != 8 || report.UncoveredLines != 4 {
		t.Fatalf("report counts = %+v", report)
	}
	// 5 is in an executed block (3.20-5.2) and not reported; 6 only in the
	// unexecuted 5.2-6.2 block; 9-10 in Idle; 13 in Orphan; 30 in no block.
	want := []LineRange{{Start: 6, End: 6}, {Start: 9, End: 10}, {Start: 13, End: 13}}
	if len(report.Files) != 1 || !reflect.DeepEqual(report.Files[0].Ranges, want) {
		t.Fatalf("files = %+v", report.Files)
	}
	if !reflect.DeepEqual(report.FilesWithoutProfileData, []string{"core/none.go"}) {
		t.Fatalf("without data = %v", report.FilesWithoutProfileData)
	}
}

func TestExecutableLinesDropsBracesCommentsAndBlanks(t *testing.T) {
	source := []byte("func f() {\n\tif x {\n\t\treturn 1\n\t}\n\n\t// note\n\t})\n\treturn 2\n}\n")
	if got := executableLines(source, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 40}); !reflect.DeepEqual(got, []int{1, 2, 3, 8}) {
		t.Fatalf("executable lines = %v", got)
	}
}
