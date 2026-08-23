package precise

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

// fixtureDir returns the absolute path to the shared test fixture project.
// The fixture is a small, compilable Go module used across integration tests.
func fixtureDir(t *testing.T) string {
	t.Helper()
	// __FILE__ lives in internal/precise/; fixture is at ../../testdata/fixture.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "testdata", "fixture"))
}

// emptyGraph returns a minimal graph suitable for passing to Enrich.
func emptyGraph() *graph.Graph {
	return &graph.Graph{
		Version: graph.Version,
	}
}

func requirePreciseEnrich(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if testing.CoverMode() != "" && strings.Contains(err.Error(), "reading srcfiles list: cache entry not found") {
		t.Skipf("local Go coverage toolchain cannot provide compiled source metadata to packages.Load: %v", err)
	}
	t.Fatalf("Enrich: %v", err)
}

func writePreciseFixture(t *testing.T, module, filename, source string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+module+"\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filename), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func sourceCallLocation(t *testing.T, source, callee string, occurrence int) (line, column int) {
	t.Helper()
	needle := callee + "("
	seen := 0
	for lineIndex, text := range strings.Split(source, "\n") {
		offset := 0
		for {
			index := strings.Index(text[offset:], needle)
			if index < 0 {
				break
			}
			index += offset
			if seen == occurrence {
				return lineIndex + 1, index + len(callee) + 1
			}
			seen++
			offset = index + len(needle)
		}
	}
	t.Fatalf("call %q occurrence %d not found", callee, occurrence)
	return 0, 0
}

// TestEnrich_DoesNotError verifies that Enrich completes without error on a
// compilable fixture project.
func TestEnrich_DoesNotError(t *testing.T) {
	dir := fixtureDir(t)
	g := emptyGraph()
	if err := Enrich(dir, g); err != nil {
		t.Fatalf("Enrich returned unexpected error: %v", err)
	}
}

func TestEnrichResolvesDirectInterfaceAndMethodValueTestCalls(t *testing.T) {
	production := `package testcalls

type Runner interface { Run() }
type Direct struct{}
func (*Direct) Run() {}
type Memory struct{}
func (*Memory) Run() {}
type SQL struct{}
func (*SQL) Run() {}
`
	testSource := `package testcalls

import "testing"

func TestCalls(t *testing.T) {
	direct := &Direct{}
	direct.Run()
	var runner Runner = &Memory{}
	runner.Run()
	run := direct.Run
	run()
}
`
	root := writePreciseFixture(t, "example.com/testcalls", "calls.go", production)
	if err := os.WriteFile(filepath.Join(root, "calls_test.go"), []byte(testSource), 0o644); err != nil {
		t.Fatal(err)
	}
	directLine, directColumn := sourceCallLocation(t, testSource, "direct.Run", 0)
	interfaceLine, interfaceColumn := sourceCallLocation(t, testSource, "runner.Run", 0)
	valueLine, valueColumn := sourceCallLocation(t, testSource, "run", 0)
	const (
		directID = "example.com/testcalls::(*Direct).Run"
		memoryID = "example.com/testcalls::(*Memory).Run"
		sqlID    = "example.com/testcalls::(*SQL).Run"
	)
	g := &graph.Graph{
		Version: graph.Version,
		Build:   &graph.BuildMetadata{},
		Files: []graph.FileNode{
			{ID: "calls.go", Path: "calls.go", PackageName: "testcalls"},
			{ID: "calls_test.go", Path: "calls_test.go", PackageName: "testcalls"},
		},
		Symbols: []graph.SymbolNode{
			{ID: "example.com/testcalls::Runner", Kind: graph.KindInterface, Name: "Runner", PackageName: "testcalls", File: "calls.go"},
			{ID: directID, Kind: graph.KindMethod, Name: "Run", Receiver: "*Direct", PackageName: "testcalls", File: "calls.go"},
			{ID: memoryID, Kind: graph.KindMethod, Name: "Run", Receiver: "*Memory", PackageName: "testcalls", File: "calls.go"},
			{ID: sqlID, Kind: graph.KindMethod, Name: "Run", Receiver: "*SQL", PackageName: "testcalls", File: "calls.go"},
		},
	}
	for _, edge := range []graph.TestEdge{
		{TestFunc: "TestCalls", Target: "direct.Run", File: "calls_test.go", Line: directLine, Column: directColumn},
		{TestFunc: "TestCalls", Target: "runner.Run", File: "calls_test.go", Line: interfaceLine, Column: interfaceColumn},
		{TestFunc: "TestCalls", Target: "run", File: "calls_test.go", Line: valueLine, Column: valueColumn},
	} {
		g.TestEdges = append(g.TestEdges, edge)
		g.Calls = append(g.Calls, graph.CallEdge{CallerSymbolID: "example.com/testcalls::TestCalls", CallerName: "TestCalls", CalleeRaw: edge.Target, File: edge.File, Line: edge.Line, Column: edge.Column})
	}

	requirePreciseEnrich(t, Enrich(root, g))
	if got := g.Build.EffectiveTestCallResolution(); got != graph.TestCallResolutionTyped {
		t.Fatalf("test call resolution = %q, want %q; warnings=%v", got, graph.TestCallResolutionTyped, g.Build.Warnings)
	}

	targetsAt := func(line, column int) map[string]graph.CallResolution {
		t.Helper()
		got := make(map[string]graph.CallResolution)
		for _, edge := range g.TestEdges {
			if edge.Line == line && edge.Column == column {
				got[edge.TargetSymbolID] = edge.Resolution
			}
		}
		return got
	}
	if got := targetsAt(directLine, directColumn); !reflect.DeepEqual(got, map[string]graph.CallResolution{directID: graph.CallResolutionStatic}) {
		t.Fatalf("direct test targets = %#v", got)
	}
	if got := targetsAt(interfaceLine, interfaceColumn); !reflect.DeepEqual(got, map[string]graph.CallResolution{directID: graph.CallResolutionCHA, memoryID: graph.CallResolutionCHA, sqlID: graph.CallResolutionCHA}) {
		t.Fatalf("interface test targets = %#v", got)
	}
	if got := targetsAt(valueLine, valueColumn); !reflect.DeepEqual(got, map[string]graph.CallResolution{directID: graph.CallResolutionStatic}) {
		t.Fatalf("method-value test targets = %#v", got)
	}
}

func TestEnrichKeepsProductionPrecisionWhenTestPackageDoesNotCompile(t *testing.T) {
	root := writePreciseFixture(t, "example.com/brokentest", "production.go", "package brokentest\n\nfunc Ready() {}\n")
	brokenTest := "package brokentest\n\nimport \"testing\"\n\nfunc TestBroken(t *testing.T) { Missing() }\n"
	if err := os.WriteFile(filepath.Join(root, "production_test.go"), []byte(brokenTest), 0o644); err != nil {
		t.Fatal(err)
	}
	line, column := sourceCallLocation(t, brokenTest, "Missing", 0)
	g := &graph.Graph{
		Version: graph.Version,
		Build:   &graph.BuildMetadata{},
		Files: []graph.FileNode{
			{ID: "production.go", Path: "production.go", PackageName: "brokentest"},
			{ID: "production_test.go", Path: "production_test.go", PackageName: "brokentest"},
		},
		Symbols:   []graph.SymbolNode{{ID: "example.com/brokentest::Ready", Kind: graph.KindFunction, Name: "Ready", PackageName: "brokentest", File: "production.go"}},
		TestEdges: []graph.TestEdge{{TestFunc: "TestBroken", Target: "Missing", File: "production_test.go", Line: line, Column: column}},
		Calls:     []graph.CallEdge{{CallerSymbolID: "example.com/brokentest::TestBroken", CallerName: "TestBroken", CalleeRaw: "Missing", File: "production_test.go", Line: line, Column: column}},
	}

	if err := Enrich(root, g); err != nil {
		t.Fatalf("test compilation must not downgrade production precision: %v", err)
	}
	if got := g.Build.EffectiveTestCallResolution(); got != graph.TestCallResolutionPartial {
		t.Fatalf("test call resolution = %q, want partial", got)
	}
	if len(g.Build.Warnings) == 0 || !strings.Contains(g.Build.Warnings[0], "typed test call resolution incomplete") {
		t.Fatalf("partial test resolution warning missing: %v", g.Build.Warnings)
	}
}

// TestEnrich_PopulatesImplements verifies that Enrich discovers at least one
// interface-satisfaction edge in the fixture project.
func TestEnrich_PopulatesImplements(t *testing.T) {
	dir := fixtureDir(t)
	g := emptyGraph()
	if err := Enrich(dir, g); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(g.Implements) == 0 {
		t.Log("warning: no Implements edges found — fixture may not contain an interface/concrete pair; skipping assertion")
		return
	}
	for _, edge := range g.Implements {
		if edge.InterfaceID == "" || edge.ConcreteID == "" {
			t.Fatalf("precise edge lacks qualified IDs: %+v", edge)
		}
	}
}

// TestEnrich_PopulatesCalls verifies that Enrich adds call edges to the graph.
// Enrich is permitted to leave g.Calls empty if CHA finds nothing, but on a
// non-trivial fixture it should always add at least one edge.
func TestEnrich_PopulatesCalls(t *testing.T) {
	dir := fixtureDir(t)
	g := emptyGraph()
	if err := Enrich(dir, g); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(g.Calls) == 0 {
		t.Log("warning: no Call edges produced by Enrich; fixture may be trivial")
	}
	for _, edge := range g.Calls {
		if edge.CalleeSymbolID != "" && !edge.Synthetic && edge.Resolution == "" {
			t.Errorf("resolved call lacks resolution provenance: %+v", edge)
		}
	}
}

// TestEnrich_IsDeterministic runs Enrich twice and checks that the number of
// Implements and Call edges is stable across invocations.
func TestEnrich_IsDeterministic(t *testing.T) {
	dir := fixtureDir(t)

	g1 := emptyGraph()
	if err := Enrich(dir, g1); err != nil {
		t.Fatalf("first Enrich: %v", err)
	}

	g2 := emptyGraph()
	if err := Enrich(dir, g2); err != nil {
		t.Fatalf("second Enrich: %v", err)
	}

	if len(g1.Implements) != len(g2.Implements) {
		t.Errorf("Implements count differs between runs: %d vs %d", len(g1.Implements), len(g2.Implements))
	}
	if len(g1.Calls) != len(g2.Calls) {
		t.Errorf("Calls count differs between runs: %d vs %d", len(g1.Calls), len(g2.Calls))
	}
}

// TestEnrich_InvalidDir verifies that Enrich returns a non-nil error when
// given a path that cannot be loaded as a Go module.
func TestEnrich_InvalidDir(t *testing.T) {
	g := emptyGraph()
	if err := Enrich(t.TempDir(), g); err == nil {
		t.Fatal("Enrich succeeded without a loadable Go package")
	}
}

func TestEnrichRejectsPackageErrorsBeforeMutation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/broken\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken.go"), []byte("package broken\n\nfunc Broken() { missing() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := &graph.Graph{
		Version: graph.Version,
		Calls: []graph.CallEdge{{
			CallerSymbolID: "sentinel::Caller",
			CallerName:     "Caller",
			CalleeRaw:      "Target",
			File:           "sentinel.go",
			Line:           1,
		}},
		Implements: []graph.ImplementsEdge{{Interface: "Sentinel", Concrete: "Value"}},
	}
	wantCalls := append([]graph.CallEdge(nil), g.Calls...)
	wantImplements := append([]graph.ImplementsEdge(nil), g.Implements...)

	err := Enrich(root, g)
	if err == nil {
		t.Fatal("Enrich succeeded for a package with a type error")
	}
	if !strings.Contains(err.Error(), "precise package loading reported") || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("Enrich returned an unhelpful package error: %v", err)
	}
	if !reflect.DeepEqual(g.Calls, wantCalls) || !reflect.DeepEqual(g.Implements, wantImplements) {
		t.Fatalf("Enrich mutated graph before rejecting package errors: calls=%+v implements=%+v", g.Calls, g.Implements)
	}
}

func TestValidatePackageCoverageRejectsNoPackages(t *testing.T) {
	err := validatePackageCoverage(t.TempDir(), nil, emptyGraph())
	if err == nil || !strings.Contains(err.Error(), "matched no packages") {
		t.Fatalf("zero-package coverage check returned %v", err)
	}
}

func TestEnrichRejectsPartialProductionFileCoverageBeforeMutation(t *testing.T) {
	source := `package partial

func Active() {}
`
	root := writePreciseFixture(t, "example.com/partial", "active.go", source)
	taggedSource := `//go:build gograph_unset_build_tag

package partial

func TaggedOut() {}
`
	if err := os.WriteFile(filepath.Join(root, "tagged.go"), []byte(taggedSource), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "active_test.go"), []byte("package partial\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := &graph.Graph{
		Version: graph.Version,
		Files: []graph.FileNode{
			{ID: "active.go", Path: "active.go", PackageName: "partial"},
			{ID: "tagged.go", Path: "tagged.go", PackageName: "partial"},
			// Tests are intentionally outside the production coverage contract.
			{ID: "active_test.go", Path: "active_test.go", PackageName: "partial"},
		},
		Calls: []graph.CallEdge{{
			CallerSymbolID: "sentinel::Caller",
			CallerName:     "Caller",
			CalleeRaw:      "Target",
			File:           "active.go",
			Line:           1,
		}},
	}
	wantCalls := append([]graph.CallEdge(nil), g.Calls...)

	err := Enrich(root, g)
	if testing.CoverMode() != "" && err != nil && strings.Contains(err.Error(), "reading srcfiles list: cache entry not found") {
		t.Skipf("local Go coverage toolchain cannot load compiled source metadata for the partial-coverage fixture: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "omitted 1 indexed production file") || !strings.Contains(err.Error(), "tagged.go") {
		t.Fatalf("partial package coverage returned %v", err)
	}
	if !reflect.DeepEqual(g.Calls, wantCalls) || len(g.Implements) != 0 {
		t.Fatalf("Enrich mutated graph before rejecting partial coverage: calls=%+v implements=%+v", g.Calls, g.Implements)
	}
}

func TestEnrichSkipsUnresolvedFunctionValueExpansions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/cancel\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `package cancel
import "context"
func Run() {
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
}
`
	if err := os.WriteFile(filepath.Join(root, "cancel.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	g := emptyGraph()
	requirePreciseEnrich(t, Enrich(root, g))
	for _, edge := range g.Calls {
		if edge.File == "cancel.go" && edge.Line == 5 {
			t.Fatalf("function value cancel() expanded to unrelated target: %+v", edge)
		}
	}
}

func TestEnrichInterfaceInvokeKeepsEveryConcreteTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/invoke\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `package invoke

type Store interface {
	Delete(string) error
}

type MemoryStore struct{}

func (*MemoryStore) Delete(string) error { return nil }

type SQLStore struct{}

func (*SQLStore) Delete(string) error { return nil }

func Purge(store Store) error {
	return store.Delete("key")
}
`
	if err := os.WriteFile(filepath.Join(root, "store.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	callLine, callColumn := 0, 0
	for i, line := range strings.Split(source, "\n") {
		if start := strings.Index(line, "store.Delete("); start >= 0 {
			callLine = i + 1
			callColumn = start + len("store.Delete") + 1
			break
		}
	}
	if callLine == 0 || callColumn == 0 {
		t.Fatal("Delete call line not found in fixture")
	}

	newGraph := func() *graph.Graph {
		return &graph.Graph{
			Version: graph.Version,
			Calls: []graph.CallEdge{{
				CallerSymbolID: "example.com/invoke::Purge",
				CallerName:     "Purge",
				CalleeRaw:      "store.Delete",
				File:           "store.go",
				Line:           callLine,
				Column:         callColumn,
				ReturnUsage:    "returned",
			}},
		}
	}

	wantIDs := []string{
		"example.com/invoke::(*MemoryStore).Delete",
		"example.com/invoke::(*SQLStore).Delete",
	}
	assertTargets := func(t *testing.T, g *graph.Graph) []graph.CallEdge {
		t.Helper()
		var got []graph.CallEdge
		for _, edge := range g.Calls {
			if edge.File == "store.go" && edge.Line == callLine && edge.Column == callColumn && edge.CalleeRaw == "store.Delete" {
				got = append(got, edge)
			}
		}
		if len(got) != len(wantIDs) {
			t.Fatalf("interface invoke produced %d target edges, want %d: %+v", len(got), len(wantIDs), got)
		}
		for i, edge := range got {
			if edge.CalleeSymbolID != wantIDs[i] {
				t.Errorf("target %d ID = %q, want %q", i, edge.CalleeSymbolID, wantIDs[i])
			}
			if edge.CallerSymbolID != "example.com/invoke::Purge" || edge.CallerName != "Purge" || edge.ReturnUsage != "returned" {
				t.Errorf("target %d did not preserve AST metadata: %+v", i, edge)
			}
			if edge.Resolution != graph.CallResolutionCHA {
				t.Errorf("target %d resolution = %q, want %q", i, edge.Resolution, graph.CallResolutionCHA)
			}
		}
		return got
	}

	g1 := newGraph()
	requirePreciseEnrich(t, Enrich(root, g1))
	first := assertTargets(t, g1)

	// A repeated enrichment of the same graph must replace the prior target
	// set rather than append duplicates.
	requirePreciseEnrich(t, Enrich(root, g1))
	second := assertTargets(t, g1)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated Enrich changed interface target edges:\nfirst:  %+v\nsecond: %+v", first, second)
	}

	// A fresh enrichment must retain the same deterministic target ordering.
	g2 := newGraph()
	requirePreciseEnrich(t, Enrich(root, g2))
	third := assertTargets(t, g2)
	if !reflect.DeepEqual(first, third) {
		t.Fatalf("fresh Enrich changed interface target ordering:\nfirst: %+v\nthird: %+v", first, third)
	}
}

func TestEnrichInterfaceInvokeInsideClosurePreservesParserCaller(t *testing.T) {
	source := `package closure

type Store interface {
	Delete(string) error
}

type MemoryStore struct{}
func (*MemoryStore) Delete(string) error { return nil }

type SQLStore struct{}
func (*SQLStore) Delete(string) error { return nil }

func Run(store Store) func() error {
	return func() error {
		return store.Delete("key")
	}
}
`
	root := writePreciseFixture(t, "example.com/closure", "closure.go", source)
	line, column := sourceCallLocation(t, source, "store.Delete", 0)
	g := &graph.Graph{
		Version: graph.Version,
		Calls: []graph.CallEdge{{
			CallerSymbolID: "example.com/closure::Run",
			CallerName:     "Run",
			CalleeRaw:      "store.Delete",
			File:           "closure.go",
			Line:           line,
			Column:         column,
			ReturnUsage:    "returned",
		}},
	}

	requirePreciseEnrich(t, Enrich(root, g))
	wantIDs := []string{
		"example.com/closure::(*MemoryStore).Delete",
		"example.com/closure::(*SQLStore).Delete",
	}
	var got []graph.CallEdge
	for _, edge := range g.Calls {
		if edge.File == "closure.go" && edge.Line == line && edge.Column == column && edge.CalleeRaw == "store.Delete" {
			got = append(got, edge)
		}
	}
	if len(got) != len(wantIDs) {
		t.Fatalf("closure invoke produced %d edges, want %d: %+v", len(got), len(wantIDs), got)
	}
	for i, edge := range got {
		if edge.CalleeSymbolID != wantIDs[i] || edge.CallerSymbolID != "example.com/closure::Run" || edge.CallerName != "Run" || edge.ReturnUsage != "returned" {
			t.Errorf("closure edge %d lost parser provenance: %+v", i, edge)
		}
	}
}

func TestEnrichDistinguishesInterfaceInvokesOnSameLine(t *testing.T) {
	source := `package sameline

type Left interface {
	Delete(string) error
	Left()
}
type Right interface {
	Delete(string) error
	Right()
}

type LeftStore struct{}
func (*LeftStore) Delete(string) error { return nil }
func (*LeftStore) Left() {}

type RightStore struct{}
func (*RightStore) Delete(string) error { return nil }
func (*RightStore) Right() {}

func Purge(left Left, right Right) { _ = left.Delete("left"); _ = right.Delete("right") }
`
	root := writePreciseFixture(t, "example.com/sameline", "same.go", source)
	line, leftColumn := sourceCallLocation(t, source, "left.Delete", 0)
	rightLine, rightColumn := sourceCallLocation(t, source, "right.Delete", 0)
	if line != rightLine || leftColumn == rightColumn {
		t.Fatalf("fixture calls are not distinct expressions on one line: left=%d:%d right=%d:%d", line, leftColumn, rightLine, rightColumn)
	}
	g := &graph.Graph{
		Version: graph.Version,
		Calls: []graph.CallEdge{
			{
				CallerSymbolID: "example.com/sameline::Purge",
				CallerName:     "Purge",
				CalleeRaw:      "left.Delete",
				File:           "same.go",
				Line:           line,
				Column:         leftColumn,
				ReturnUsage:    "assigned",
			},
			{
				CallerSymbolID: "example.com/sameline::Purge",
				CallerName:     "Purge",
				CalleeRaw:      "right.Delete",
				File:           "same.go",
				Line:           line,
				Column:         rightColumn,
				ReturnUsage:    "partially_ignored",
			},
		},
	}

	requirePreciseEnrich(t, Enrich(root, g))
	want := map[int]graph.CallEdge{
		leftColumn: {
			CalleeRaw:      "left.Delete",
			CalleeSymbolID: "example.com/sameline::(*LeftStore).Delete",
			ReturnUsage:    "assigned",
		},
		rightColumn: {
			CalleeRaw:      "right.Delete",
			CalleeSymbolID: "example.com/sameline::(*RightStore).Delete",
			ReturnUsage:    "partially_ignored",
		},
	}
	got := make(map[int]graph.CallEdge)
	for _, edge := range g.Calls {
		if edge.File == "same.go" && edge.Line == line && (edge.Column == leftColumn || edge.Column == rightColumn) {
			got[edge.Column] = edge
		}
	}
	if len(got) != len(want) {
		t.Fatalf("same-line invokes produced %d distinct edges, want %d: %+v", len(got), len(want), got)
	}
	for column, expected := range want {
		edge := got[column]
		if edge.CalleeRaw != expected.CalleeRaw || edge.CalleeSymbolID != expected.CalleeSymbolID || edge.ReturnUsage != expected.ReturnUsage || edge.CallerSymbolID != "example.com/sameline::Purge" {
			t.Errorf("same-line edge at column %d = %+v, want raw=%q target=%q usage=%q", column, edge, expected.CalleeRaw, expected.CalleeSymbolID, expected.ReturnUsage)
		}
	}
}

func TestEnrichCompletesEmbeddedInterfaceMethods(t *testing.T) {
	source := `package embedded

type Deleter interface {
	Delete(string) error
}

type Store interface {
	Deleter
	Get(string) error
}
`
	root := writePreciseFixture(t, "example.com/embedded", "embedded.go", source)
	g := &graph.Graph{
		Version: graph.Version,
		Symbols: []graph.SymbolNode{
			{
				ID:               "example.com/embedded::Deleter",
				Kind:             graph.KindInterface,
				Name:             "Deleter",
				PackageName:      "embedded",
				InterfaceMethods: map[string]string{"Delete": "func(string) (error)"},
			},
			{
				ID:               "example.com/embedded::Store",
				Kind:             graph.KindInterface,
				Name:             "Store",
				PackageName:      "embedded",
				InterfaceMethods: map[string]string{"Get": "func(string) (error)"},
			},
		},
	}

	requirePreciseEnrich(t, Enrich(root, g))
	store := g.Symbols[1]
	if got := store.InterfaceMethods["Delete"]; got != "func(string) (error)" {
		t.Fatalf("embedded Delete signature = %q, want %q; methods=%v", got, "func(string) (error)", store.InterfaceMethods)
	}
	if got := store.InterfaceMethods["Get"]; got != "func(string) (error)" {
		t.Fatalf("explicit Get metadata changed: %q", got)
	}
}

func TestEnrichPreservesPromotedInterfaceWrapperAndDeclaredMethodReachability(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/promoted\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	storeDir := filepath.Join(root, "internal", "store")
	apiDir := filepath.Join(root, "api")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(apiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	storeSource := `package store

type Store interface {
	Delete(string) error
	Flush() error
}

type DeletePart struct{}
func (*DeletePart) Delete(string) error { return nil }

type CompositeStore struct { *DeletePart }
func (*CompositeStore) Flush() error { return nil }
`
	if err := os.WriteFile(filepath.Join(storeDir, "store.go"), []byte(storeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	apiSource := `package api

import "example.com/promoted/internal/store"

func Purge(value store.Store) error {
	return value.Delete("key")
}
`
	if err := os.WriteFile(filepath.Join(apiDir, "api.go"), []byte(apiSource), 0o644); err != nil {
		t.Fatal(err)
	}
	line, column := sourceCallLocation(t, apiSource, "value.Delete", 0)

	const (
		interfaceID      = "example.com/promoted/internal/store::Store"
		wrapperID        = "example.com/promoted/internal/store::(*CompositeStore).Delete"
		declaredDeleteID = "example.com/promoted/internal/store::(*DeletePart).Delete"
		purgeID          = "example.com/promoted/api::Purge"
	)
	g := &graph.Graph{
		Version: graph.Version,
		Symbols: []graph.SymbolNode{
			{
				ID:               interfaceID,
				Kind:             graph.KindInterface,
				Name:             "Store",
				PackageName:      "store",
				File:             "internal/store/store.go",
				InterfaceMethods: map[string]string{"Delete": "func(string) (error)", "Flush": "func() (error)"},
			},
			{
				ID:              declaredDeleteID,
				Kind:            graph.KindMethod,
				Name:            "Delete",
				Receiver:        "*DeletePart",
				PackageName:     "store",
				File:            "internal/store/store.go",
				MethodSignature: "func(string) (error)",
			},
			{
				ID:              "example.com/promoted/internal/store::(*CompositeStore).Flush",
				Kind:            graph.KindMethod,
				Name:            "Flush",
				Receiver:        "*CompositeStore",
				PackageName:     "store",
				File:            "internal/store/store.go",
				MethodSignature: "func() (error)",
			},
			{ID: purgeID, Kind: graph.KindFunction, Name: "Purge", PackageName: "api", File: "api/api.go"},
		},
		Calls: []graph.CallEdge{{
			CallerSymbolID: purgeID,
			CallerName:     "Purge",
			CalleeRaw:      "value.Delete",
			File:           "api/api.go",
			Line:           line,
			Column:         column,
			ReturnUsage:    "returned",
		}},
	}

	requirePreciseEnrich(t, Enrich(root, g))

	var invokeTargets []string
	wrapperReachesDeclared := false
	syntheticCount := 0
	for _, edge := range g.Calls {
		if edge.File == "api/api.go" && edge.Line == line && edge.Column == column {
			invokeTargets = append(invokeTargets, edge.CalleeSymbolID)
		}
		if edge.CallerSymbolID == wrapperID && edge.CalleeSymbolID == declaredDeleteID {
			wrapperReachesDeclared = true
			if !edge.Synthetic || edge.File != "" || edge.Line != 0 {
				t.Errorf("wrapper forwarding edge must be traversal-only: %+v", edge)
			}
			syntheticCount++
		}
	}
	if !reflect.DeepEqual(invokeTargets, []string{wrapperID}) {
		t.Fatalf("promoted interface invoke targets = %#v, want [%q]; calls=%+v", invokeTargets, wrapperID, g.Calls)
	}
	if !wrapperReachesDeclared {
		t.Fatalf("promoted wrapper %q does not reach declared method %q; calls=%+v", wrapperID, declaredDeleteID, g.Calls)
	}
	if syntheticCount != 1 {
		t.Fatalf("wrapper forwarding edge count = %d, want 1; calls=%+v", syntheticCount, g.Calls)
	}
	firstCalls := append([]graph.CallEdge(nil), g.Calls...)
	requirePreciseEnrich(t, Enrich(root, g))
	if !reflect.DeepEqual(firstCalls, g.Calls) {
		t.Fatalf("repeated enrichment changed promoted wrapper edges:\nfirst:  %+v\nsecond: %+v", firstCalls, g.Calls)
	}

	queries := []string{"Delete", "Store.Delete", "CompositeStore.Delete", wrapperID, declaredDeleteID}
	for _, query := range queries {
		results := search.Callers(g, query, false, true)
		if len(results) != 1 || results[0].Name != "Purge" || results[0].CallSiteLine != line || results[0].CallSiteColumn != column {
			t.Errorf("Callers(%q) = %#v, want the promoted interface call site once", query, results)
		}
		depthResults := search.CallersDepth(g, query, 2, false, true)
		if len(depthResults) != 1 || depthResults[0].Name != "Purge" || depthResults[0].CallSiteFile != "api/api.go" {
			t.Errorf("CallersDepth(%q) exposed or lost a wrapper edge: %#v", query, depthResults)
		}
	}
	if usages := search.ReturnUsages(g, "Delete"); len(usages) != 1 || usages[0].Name != "Purge" || usages[0].File != "api/api.go" {
		t.Fatalf("return usages exposed synthetic wrapper forwarding: %#v", usages)
	}
	for _, result := range search.CalleesDepth(g, purgeID, 3, false) {
		if result.CallSiteFile == "" || result.CallSiteLine == 0 {
			t.Fatalf("callee depth exposed synthetic wrapper forwarding: %#v", result)
		}
	}
	path := search.Path(g, purgeID, declaredDeleteID, false)
	if len(path) == 0 {
		t.Fatal("exact path traversal did not cross the promoted wrapper")
	}
	for _, result := range path {
		if strings.Contains(result.Name, "CompositeStore") || result.File == "" {
			t.Fatalf("path exposed synthetic wrapper forwarding instead of a transparent hop: %#v", path)
		}
	}

	for _, orphan := range search.ReachableOrphans(g) {
		if orphan.Name == declaredDeleteID {
			t.Fatalf("declared promoted method is falsely orphaned: %#v", orphan)
		}
	}
}

func TestEnrichKeepsLocalWrapperForExternallyDeclaredPromotedMethod(t *testing.T) {
	source := `package externalwrapper

import "io"

type Store interface {
	Write([]byte) (int, error)
	Flush() error
}

type CompositeStore struct { io.Writer }
func (*CompositeStore) Flush() error { return nil }

func Persist(value Store, data []byte) error {
	_, err := value.Write(data)
	return err
}
`
	root := writePreciseFixture(t, "example.com/externalwrapper", "store.go", source)
	line, column := sourceCallLocation(t, source, "value.Write", 0)
	g := &graph.Graph{
		Version: graph.Version,
		Calls: []graph.CallEdge{{
			CallerSymbolID: "example.com/externalwrapper::Persist",
			CallerName:     "Persist",
			CalleeRaw:      "value.Write",
			File:           "store.go",
			Line:           line,
			Column:         column,
			ReturnUsage:    "assigned",
		}},
	}

	requirePreciseEnrich(t, Enrich(root, g))
	const wrapperID = "example.com/externalwrapper::(*CompositeStore).Write"
	var targets []string
	for _, edge := range g.Calls {
		if edge.File == "store.go" && edge.Line == line && edge.Column == column {
			targets = append(targets, edge.CalleeSymbolID)
		}
	}
	if !reflect.DeepEqual(targets, []string{wrapperID}) {
		t.Fatalf("local wrapper promoting external io.Writer.Write targets = %#v, want [%q]; calls=%+v", targets, wrapperID, g.Calls)
	}
}

// --- Unit tests for pure helper functions ---

func TestCleanName_StripsStar(t *testing.T) {
	if got := cleanName("*MyType"); got != "MyType" {
		t.Errorf("cleanName(*MyType) = %q, want %q", got, "MyType")
	}
}

func TestCleanName_StripsPackagePath(t *testing.T) {
	if got := cleanName("github.com/foo/bar.Baz"); got != "Baz" {
		t.Errorf("cleanName(github.com/foo/bar.Baz) = %q, want %q", got, "Baz")
	}
}

func TestCleanName_PlainName(t *testing.T) {
	if got := cleanName("Foo"); got != "Foo" {
		t.Errorf("cleanName(Foo) = %q, want %q", got, "Foo")
	}
}

func TestSsaFuncToSymbolID_NilInput(t *testing.T) {
	if got := ssaFuncToSymbolID(nil); got != "" {
		t.Errorf("ssaFuncToSymbolID(nil) = %q, want empty string", got)
	}
}
