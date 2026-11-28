package search

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// TestSelectionSchemaVersion identifies the runnable test selection document.
const TestSelectionSchemaVersion = "gograph.test_selection.v1"

// TestSelection groups the tests that statically reach a set of root symbols
// into per-package `go test -run` selections.
type TestSelection struct {
	SchemaVersion      string                       `json:"schema_version"`
	Selector           string                       `json:"selector"`
	Status             string                       `json:"status"`
	AnalysisPrecision  graph.PrecisionMode          `json:"analysis_precision"`
	TestCallResolution graph.TestCallResolutionMode `json:"test_call_resolution"`
	ExactOnly          bool                         `json:"exact_only"`
	MaxDepth           int                          `json:"max_depth,omitempty"`
	BuildTags          []string                     `json:"build_tags"`
	Roots              []SelectionRoot              `json:"roots"`
	Unselected         []UnselectedRoot             `json:"unselected"`
	Packages           []TestPackageSelection       `json:"packages"`
	TestCount          int                          `json:"test_count"`
	ExactCount         int                          `json:"exact_count"`
	PossibleCount      int                          `json:"possible_count"`
	Limitations        []string                     `json:"limitations"`
	// PreviouslyPassed lists selected tests left out because they already
	// passed on identical inputs (tests --skip-passed).
	PreviouslyPassed []PreviouslyPassedTest `json:"previously_passed,omitempty"`
}

// SelectionRoot is one changed or requested symbol whose tests were selected.
type SelectionRoot struct {
	StableID string `json:"stable_id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Change   string `json:"change,omitempty"`
	Tests    int    `json:"tests"`
	// Users counts the functions that use a changed type, variable or
	// constant; their tests are its tests.
	Users int `json:"users,omitempty"`
}

// UnselectedRoot is a changed declaration that could not select tests.
type UnselectedRoot struct {
	StableID string `json:"stable_id,omitempty"`
	Name     string `json:"name"`
	File     string `json:"file"`
	Change   string `json:"change,omitempty"`
	Reason   string `json:"reason"`
}

// TestPackageSelection is one `go test` invocation.
type TestPackageSelection struct {
	ImportPath string         `json:"import_path"`
	Dir        string         `json:"dir"`
	Tests      []SelectedTest `json:"tests"`
	// RunAll is true when every test, fuzz and example function of the
	// package is selected; the package then runs without -run.
	RunAll bool `json:"run_all"`
	// RunPatterns holds anchored -run patterns, split so each stays well
	// below the operating system's single-argument length limit.
	RunPatterns  []string `json:"run_patterns"`
	BenchPattern string   `json:"bench_pattern,omitempty"`
	Commands     []string `json:"commands"`
}

// SelectedTest is one top-level test, fuzz, example, or benchmark function.
type SelectedTest struct {
	Name       string `json:"name"`
	StableID   string `json:"stable_id"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Resolution string `json:"resolution"`
	Depth      int    `json:"depth"`
}

// SelectionInput is one root for SelectTests. Change is "modified", "new",
// "deleted", or empty for an explicitly requested symbol.
type SelectionInput struct {
	StableID string
	Name     string
	File     string
	Change   string
}

// SelectTests returns the tests reaching every root, grouped per package.
// Roots that are test entry points select themselves. Roots absent from the
// graph, or that are not callable, are reported as unselected rather than
// silently dropped.
func (snapshot *Snapshot) SelectTests(selector string, roots []SelectionInput, exactOnly bool) TestSelection {
	return snapshot.SelectTestsWithin(selector, roots, exactOnly, 0)
}

// SelectTestsWithin is SelectTests keeping only tests whose shortest path to
// a root is at most maxDepth calls; 0 means unlimited.
func (snapshot *Snapshot) SelectTestsWithin(selector string, roots []SelectionInput, exactOnly bool, maxDepth int) TestSelection {
	g := snapshot.g
	selection := TestSelection{
		SchemaVersion:      TestSelectionSchemaVersion,
		Selector:           selector,
		Status:             "complete",
		AnalysisPrecision:  graph.PrecisionAST,
		TestCallResolution: graph.TestCallResolutionAST,
		ExactOnly:          exactOnly,
		MaxDepth:           maxDepth,
		BuildTags:          []string{},
		Roots:              []SelectionRoot{},
		Unselected:         []UnselectedRoot{},
		Packages:           []TestPackageSelection{},
		Limitations: []string{
			"Static test attribution is not runtime or branch coverage proof.",
			"Possible results may over-approximate dynamic dispatch or parser-only call targets.",
			"Attribution follows calls. A changed type selects the tests of its methods, of functions whose signatures name it, of composite literals of it, and (precise graphs) of its field uses; a changed variable or constant selects the tests of its uses only on precise graphs.",
			"Tests that start a process (exec.Command) in a main package are possible matches when the change reaches that package's main function; tests in other packages that run a built binary are not linked.",
		},
	}
	if g == nil {
		selection.Status = "not_found"
		return selection
	}
	selection.AnalysisPrecision = g.Build.EffectivePrecision()
	selection.TestCallResolution = g.Build.EffectiveTestCallResolution()
	if g.Build != nil && g.Build.Selection != nil && len(g.Build.Selection.BuildTags) > 0 {
		selection.BuildTags = append([]string(nil), g.Build.Selection.BuildTags...)
		sort.Strings(selection.BuildTags)
	}

	symbols := make(map[string][]graph.SymbolNode, len(g.Symbols))
	for _, symbol := range g.Symbols {
		symbols[symbol.ID] = append(symbols[symbol.ID], symbol)
	}

	type chosen struct {
		symbol graph.SymbolNode
		state  reverseAttributionState
	}
	// A test deleted by the change no longer exists and cannot be run.
	deleted := make(map[string]bool)
	for _, root := range roots {
		if root.Change == string(ChangeDeleted) {
			deleted[root.StableID+"\x00"+path.Clean(root.File)] = true
		}
	}
	tests := make(map[string]chosen)
	consider := func(symbol graph.SymbolNode, state reverseAttributionState) bool {
		if exactOnly && state.resolution != "exact" {
			return false
		}
		if maxDepth > 0 && state.depth > maxDepth {
			return false
		}
		if deleted[symbol.ID+"\x00"+path.Clean(symbol.File)] {
			return false
		}
		key := symbol.ID + "\x00" + path.Clean(symbol.File)
		if previous, ok := tests[key]; !ok || betterAttributionPath(state.resolution, state.depth, previous.state.resolution, previous.state.depth) {
			tests[key] = chosen{symbol: symbol, state: state}
		}
		return true
	}

	var processTests map[string][]graph.SymbolNode
	considerProcessTests := func(best map[string]reverseAttributionState) int {
		if processTests == nil {
			processTests = snapshot.processTests()
		}
		count := 0
		for _, match := range processTestStates(processTests, best) {
			if consider(match.test, match.state) {
				count++
			}
		}
		return count
	}

	seenRoot := make(map[string]bool)
	for _, root := range roots {
		key := root.StableID + "\x00" + root.Change
		if seenRoot[key] {
			continue
		}
		seenRoot[key] = true
		if root.Change == string(ChangeDeleted) && isTestFile(root.File) {
			// A deleted test or test helper cannot run; its callers changed too.
			continue
		}
		matches := symbols[root.StableID]
		if root.StableID == "" || len(matches) == 0 {
			reason := "absent from the graph; run gograph build and retry"
			if root.Change == string(ChangeDeleted) {
				reason = "deleted, and the graph was built after the deletion, so its former callers are unknown; callers changed to stop using it select their own tests"
			}
			selection.Unselected = append(selection.Unselected, UnselectedRoot{StableID: root.StableID, Name: root.Name, File: root.File, Change: root.Change, Reason: reason})
			continue
		}
		symbol := matches[0]
		if symbol.Kind != graph.KindFunction && symbol.Kind != graph.KindMethod {
			users := snapshot.declarationUsers(symbol)
			if len(users) == 0 {
				reason := "no recorded uses of this declaration"
				if (symbol.Kind == graph.KindVar || symbol.Kind == graph.KindConst) && (g.Build == nil || !g.Build.PreciseRequested()) {
					reason += "; AST graphs record no variable or constant uses, run gograph build --precise"
				}
				selection.Unselected = append(selection.Unselected, UnselectedRoot{StableID: root.StableID, Name: symbol.Name, File: symbol.File, Change: root.Change, Reason: reason})
				continue
			}
			ids := make([]string, 0, len(users))
			for id := range users {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			count := 0
			for _, id := range ids {
				attribution, best := snapshot.reverseTestReach(id)
				for testID, state := range best {
					test, ok := attribution.tests[testID]
					if !ok {
						for _, match := range symbols[testID] {
							if testID == id && isTestEntrySymbol(match) {
								test, ok = match, true
							}
						}
					}
					if !ok {
						continue
					}
					state.depth++
					if users[id] == "possible" {
						state.resolution = "possible"
					}
					if consider(test, state) {
						count++
					}
				}
				count += considerProcessTests(best)
			}
			selection.Roots = append(selection.Roots, SelectionRoot{
				StableID: symbol.ID, Name: symbol.Name, Kind: string(symbol.Kind), File: symbol.File, Line: symbol.Line,
				Change: root.Change, Tests: count, Users: len(users),
			})
			continue
		}
		count := 0
		for _, match := range matches {
			if isTestEntrySymbol(match) && consider(match, reverseAttributionState{id: match.ID, resolution: "exact"}) {
				count++
			}
		}
		attribution, best := snapshot.reverseTestReach(root.StableID)
		for id, state := range best {
			test, ok := attribution.tests[id]
			if !ok {
				continue
			}
			if consider(test, state) {
				count++
			}
		}
		count += considerProcessTests(best)
		selection.Roots = append(selection.Roots, SelectionRoot{
			StableID: symbol.ID, Name: symbol.Name, Kind: string(symbol.Kind), File: symbol.File, Line: symbol.Line,
			Change: root.Change, Tests: count,
		})
	}

	runnableByPackage := make(map[string]map[string]struct{})
	for _, symbol := range g.Symbols {
		if isTestEntrySymbol(symbol) && !isGoTestEntryName(symbol.Name, "Benchmark") {
			importPath := stableIDPackagePath(symbol.ID)
			if runnableByPackage[importPath] == nil {
				runnableByPackage[importPath] = make(map[string]struct{})
			}
			runnableByPackage[importPath][symbol.Name] = struct{}{}
		}
	}

	byPackage := make(map[string]*TestPackageSelection)
	for _, entry := range tests {
		importPath := stableIDPackagePath(entry.symbol.ID)
		pkg := byPackage[importPath]
		if pkg == nil {
			pkg = &TestPackageSelection{ImportPath: importPath, Dir: path.Dir(path.Clean(entry.symbol.File))}
			byPackage[importPath] = pkg
		}
		pkg.Tests = append(pkg.Tests, SelectedTest{
			Name: entry.symbol.Name, StableID: entry.symbol.ID, File: entry.symbol.File, Line: entry.symbol.Line,
			Resolution: entry.state.resolution, Depth: entry.state.depth,
		})
		selection.TestCount++
		if entry.state.resolution == "exact" {
			selection.ExactCount++
		} else {
			selection.PossibleCount++
		}
	}
	importPaths := make([]string, 0, len(byPackage))
	for importPath := range byPackage {
		importPaths = append(importPaths, importPath)
	}
	sort.Strings(importPaths)
	for _, importPath := range importPaths {
		pkg := byPackage[importPath]
		sort.Slice(pkg.Tests, func(i, j int) bool {
			if pkg.Tests[i].Name != pkg.Tests[j].Name {
				return pkg.Tests[i].Name < pkg.Tests[j].Name
			}
			return pkg.Tests[i].File < pkg.Tests[j].File
		})
		var runNames, benchNames []string
		for _, test := range pkg.Tests {
			if isGoTestEntryName(test.Name, "Benchmark") {
				benchNames = append(benchNames, test.Name)
			} else {
				runNames = append(runNames, test.Name)
			}
		}
		pkg.RunAll = len(runNames) > 0 && coversAll(runNames, runnableByPackage[importPath])
		pkg.RunPatterns = []string{}
		if !pkg.RunAll {
			pkg.RunPatterns = anchoredTestPatterns(runNames, maxRunPatternBytes)
		}
		pkg.BenchPattern = anchoredTestPattern(benchNames)
		pkg.Commands = goTestCommands(importPath, selection.BuildTags, pkg.RunAll, pkg.RunPatterns, pkg.BenchPattern)
		selection.Packages = append(selection.Packages, *pkg)
	}

	switch {
	case len(selection.Roots) == 0 && len(selection.Unselected) == 0:
		selection.Status = "empty"
	case len(selection.Unselected) > 0:
		selection.Status = "partial"
	}
	return selection
}

// stableIDPackagePath returns the Go import path part of a stable symbol ID.
func stableIDPackagePath(id string) string {
	if before, _, ok := strings.Cut(id, "::"); ok {
		return before
	}
	return id
}

// anchoredTestPattern builds a `go test -run` pattern that matches exactly the
// given top-level names and nothing else. Names are de-duplicated, sorted and
// regex-escaped.
func anchoredTestPattern(names []string) string {
	if len(names) == 0 {
		return ""
	}
	unique := make(map[string]struct{}, len(names))
	for _, name := range names {
		unique[name] = struct{}{}
	}
	quoted := make([]string, 0, len(unique))
	for name := range unique {
		quoted = append(quoted, regexp.QuoteMeta(name))
	}
	sort.Strings(quoted)
	return "^(" + strings.Join(quoted, "|") + ")$"
}

// maxRunPatternBytes keeps each -run argument far below Linux's 128 KiB
// single-argument limit and Windows' 32 KiB command-line limit.
const maxRunPatternBytes = 16 * 1024

func coversAll(names []string, all map[string]struct{}) bool {
	if len(all) == 0 {
		return false
	}
	selected := make(map[string]struct{}, len(names))
	for _, name := range names {
		selected[name] = struct{}{}
	}
	for name := range all {
		if _, ok := selected[name]; !ok {
			return false
		}
	}
	return true
}

// anchoredTestPatterns splits names into anchored patterns of at most
// maxBytes each (a single over-long name still gets its own pattern).
func anchoredTestPatterns(names []string, maxBytes int) []string {
	unique := make(map[string]struct{}, len(names))
	for _, name := range names {
		unique[name] = struct{}{}
	}
	sorted := make([]string, 0, len(unique))
	for name := range unique {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	var patterns []string
	var chunk []string
	size := 0
	for _, name := range sorted {
		quoted := regexp.QuoteMeta(name)
		if len(chunk) > 0 && size+len(quoted)+1+len("^()$") > maxBytes {
			patterns = append(patterns, anchoredTestPattern(chunk))
			chunk, size = nil, 0
		}
		chunk = append(chunk, name)
		size += len(quoted) + 1
	}
	if len(chunk) > 0 {
		patterns = append(patterns, anchoredTestPattern(chunk))
	}
	if patterns == nil {
		patterns = []string{}
	}
	return patterns
}

func goTestCommands(importPath string, tags []string, runAll bool, runPatterns []string, benchPattern string) []string {
	prefix := "go test"
	if len(tags) > 0 {
		prefix += " -tags=" + strings.Join(tags, ",")
	}
	commands := []string{}
	if runAll {
		commands = append(commands, prefix+" "+importPath)
	}
	for _, pattern := range runPatterns {
		commands = append(commands, prefix+" "+importPath+" -run '"+pattern+"'")
	}
	if benchPattern != "" {
		commands = append(commands, prefix+" "+importPath+" -run '^$' -bench '"+benchPattern+"'")
	}
	return commands
}

// SelectionInputsFromChanges converts a complete declaration comparison into
// test-selection roots. Blank identifiers are skipped; excluded and unknown
// declarations are kept so SelectTests reports them as unselected.
func SelectionInputsFromChanges(changes *ChangesResult) []SelectionInput {
	if changes == nil {
		return nil
	}
	inputs := make([]SelectionInput, 0, len(changes.Symbols))
	for _, change := range changes.Symbols {
		if change.Name == "_" {
			continue
		}
		inputs = append(inputs, SelectionInput{StableID: change.StableID, Name: change.Name, File: change.File, Change: string(change.Status)})
	}
	sort.SliceStable(inputs, func(i, j int) bool {
		if inputs[i].StableID != inputs[j].StableID {
			return inputs[i].StableID < inputs[j].StableID
		}
		return inputs[i].Change < inputs[j].Change
	})
	return inputs
}
