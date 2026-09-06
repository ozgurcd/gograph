package search

import (
	"math/bits"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ozgurcd/gograph/internal/graph"
)

const (
	IdentitySchemaVersion = "gograph.identity.v1"
	CoverageSchemaVersion = "gograph.coverage.v1"
	TestsSchemaVersion    = "gograph.tests.v1"
)

// SymbolIdentity is the durable, location-independent identity of a graph
// symbol. StableID is module import path + receiver/name identity; File and
// Line are current location metadata and are not part of the identity.
type SymbolIdentity struct {
	StableID  string           `json:"stable_id"`
	Kind      graph.SymbolKind `json:"kind"`
	Name      string           `json:"name"`
	Receiver  string           `json:"receiver,omitempty"`
	Package   string           `json:"package"`
	File      string           `json:"file"`
	Line      int              `json:"line"`
	Signature string           `json:"signature,omitempty"`
}

// IdentityReport resolves a canonical symbol ID or an exact user-facing
// symbol spelling without silently choosing among ambiguous matches.
type IdentityReport struct {
	SchemaVersion string           `json:"schema_version"`
	Query         string           `json:"query"`
	Package       string           `json:"package,omitempty"`
	Status        string           `json:"status"`
	Matches       []SymbolIdentity `json:"matches"`
}

func symbolIdentity(s graph.SymbolNode) SymbolIdentity {
	return SymbolIdentity{
		StableID:  s.ID,
		Kind:      s.Kind,
		Name:      s.Name,
		Receiver:  s.Receiver,
		Package:   s.PackageName,
		File:      s.File,
		Line:      s.Line,
		Signature: s.Signature,
	}
}

// Identity resolves exact canonical IDs case-sensitively. Other accepted
// forms use the same exact package/receiver/name grammar as exact call queries.
func Identity(g *graph.Graph, query string) IdentityReport {
	return IdentityInPackage(g, query, "")
}

// IdentityInPackage applies an optional exact Go package-name qualifier. The
// qualifier disambiguates the uncommon case where an in-package test and an
// external foo_test package produce the same module-rooted graph ID.
func IdentityInPackage(g *graph.Graph, query, packageName string) IdentityReport {
	query = strings.TrimSpace(query)
	packageName = strings.TrimSpace(packageName)
	report := IdentityReport{SchemaVersion: IdentitySchemaVersion, Query: query, Package: packageName, Status: "not_found", Matches: []SymbolIdentity{}}
	if g == nil || query == "" {
		return report
	}
	canonical := isFullyQualifiedID(query)
	for _, s := range g.Symbols {
		if packageName != "" && s.PackageName != packageName {
			continue
		}
		matched := s.ID == query
		if !canonical {
			matched = matchSymbolExact(s, query)
		}
		if matched {
			report.Matches = append(report.Matches, symbolIdentity(s))
		}
	}
	sort.Slice(report.Matches, func(i, j int) bool { return report.Matches[i].StableID < report.Matches[j].StableID })
	switch len(report.Matches) {
	case 0:
		report.Status = "not_found"
	case 1:
		report.Status = "exact"
	default:
		report.Status = "ambiguous"
	}
	return report
}

// CoverageSymbol is one production symbol statically reachable from a test.
// Resolution is exact only when every edge in the representative path is
// statically resolved; any uncertain edge degrades the result to possible.
type CoverageSymbol struct {
	StableID   string           `json:"stable_id"`
	Kind       graph.SymbolKind `json:"kind"`
	Name       string           `json:"name"`
	Receiver   string           `json:"receiver,omitempty"`
	Package    string           `json:"package"`
	File       string           `json:"file"`
	Line       int              `json:"line"`
	Resolution string           `json:"resolution"`
	Depth      int              `json:"depth"`
	Path       []string         `json:"path"`
}

// CoverageReport is reverse test attribution: the transitive product-symbol
// set exercised by one unambiguous test according to the static graph.
type CoverageReport struct {
	SchemaVersion      string                       `json:"schema_version"`
	Query              string                       `json:"query"`
	Package            string                       `json:"package,omitempty"`
	Status             string                       `json:"status"`
	AnalysisPrecision  graph.PrecisionMode          `json:"analysis_precision"`
	TestCallResolution graph.TestCallResolutionMode `json:"test_call_resolution"`
	MatchedTests       []SymbolIdentity             `json:"matched_tests"`
	Symbols            []CoverageSymbol             `json:"symbols"`
	Limitations        []string                     `json:"limitations"`
}

// AttributingTest is one test with a representative static path to a selected
// product symbol. Resolution is exact only when every edge on that path is
// statically resolved.
type AttributingTest struct {
	StableID   string   `json:"stable_id"`
	Name       string   `json:"name"`
	Package    string   `json:"package"`
	File       string   `json:"file"`
	Line       int      `json:"line"`
	Resolution string   `json:"resolution"`
	Depth      int      `json:"depth"`
	Path       []string `json:"path"`
}

// TestsReport is reverse-transitive test attribution for one unambiguous
// product symbol. It complements the legacy direct Tests query without
// changing that command's result contract.
type TestsReport struct {
	SchemaVersion      string                       `json:"schema_version"`
	Query              string                       `json:"query"`
	Package            string                       `json:"package,omitempty"`
	Status             string                       `json:"status"`
	AnalysisPrecision  graph.PrecisionMode          `json:"analysis_precision"`
	TestCallResolution graph.TestCallResolutionMode `json:"test_call_resolution"`
	MatchedSymbols     []SymbolIdentity             `json:"matched_symbols"`
	Tests              []AttributingTest            `json:"tests"`
	Limitations        []string                     `json:"limitations"`
}

type coverageState struct {
	id         string
	resolution string
	depth      int
	parent     string
}

type attributionLink struct {
	from       string
	to         string
	resolution string
}

type reverseAttributionState struct {
	id         string
	resolution string
	depth      int
	next       string
}

type symbolTestReach struct {
	exactCount    int
	possibleCount int
}

type attributionGraphData struct {
	links []attributionLink
	tests map[string]graph.SymbolNode
}

// Coverage attributes transitive static product reachability to one test. A
// short test name that exists in multiple packages is ambiguous and is never
// merged; callers can retry with the stable test symbol ID.
func Coverage(g *graph.Graph, query string, exactOnly bool) CoverageReport {
	return CoverageInPackage(g, query, "", exactOnly)
}

// CoverageInPackage applies an optional exact package-name qualifier to the
// test seed before traversing product calls.
func CoverageInPackage(g *graph.Graph, query, packageName string, exactOnly bool) CoverageReport {
	return NewSnapshot(g).Coverage(query, packageName, exactOnly)
}

func (snapshot *Snapshot) Coverage(query, packageName string, exactOnly bool) CoverageReport {
	g := snapshot.g
	report := CoverageReport{
		SchemaVersion:      CoverageSchemaVersion,
		Query:              strings.TrimSpace(query),
		Package:            strings.TrimSpace(packageName),
		Status:             "not_found",
		AnalysisPrecision:  graph.PrecisionAST,
		TestCallResolution: graph.TestCallResolutionAST,
		MatchedTests:       []SymbolIdentity{},
		Symbols:            []CoverageSymbol{},
		Limitations: []string{
			"Static call attribution is not runtime or branch coverage proof.",
			"Possible results may over-approximate dynamic dispatch or parser-only call targets.",
		},
	}
	if g == nil {
		return report
	}
	report.AnalysisPrecision = g.Build.EffectivePrecision()
	report.TestCallResolution = g.Build.EffectiveTestCallResolution()

	identity := IdentityInPackage(g, query, packageName)
	for _, match := range identity.Matches {
		if isTestFile(match.File) && match.Kind == graph.KindFunction && strings.HasPrefix(match.Name, "Test") {
			report.MatchedTests = append(report.MatchedTests, match)
		}
	}
	if len(report.MatchedTests) == 0 {
		return report
	}
	if len(report.MatchedTests) > 1 {
		report.Status = "ambiguous"
		return report
	}
	report.Status = "exact"
	test := report.MatchedTests[0]

	symbols, adjacency := snapshot.callIndex()

	best := make(map[string]coverageState)
	queue := make([]coverageState, 0)
	enqueue := func(state coverageState) {
		if state.id == "" || state.id == test.StableID {
			return
		}
		previous, exists := best[state.id]
		if exists {
			if previous.resolution == "exact" && state.resolution == "possible" {
				return
			}
			if previous.resolution == state.resolution && previous.depth <= state.depth {
				return
			}
		}
		best[state.id] = state
		queue = append(queue, state)
	}

	for _, edge := range g.TestEdges {
		if edge.TestFunc != test.Name || edge.File != test.File {
			continue
		}
		resolution := callResolution(edge.Resolution)
		if edge.TargetSymbolID != "" {
			enqueue(coverageState{id: edge.TargetSymbolID, resolution: resolution, depth: 1, parent: test.StableID})
			continue
		}
		for _, id := range unresolvedTargetIDs(g, edge.Target, test.Package) {
			enqueue(coverageState{id: id, resolution: "possible", depth: 1, parent: test.StableID})
		}
	}

	for head := 0; head < len(queue); head++ {
		state := queue[head]
		// Skip superseded possible work when an exact path was found later.
		if current := best[state.id]; current.resolution != state.resolution || current.depth != state.depth {
			continue
		}
		for _, edge := range adjacency[state.id] {
			edgeResolution := callResolution(edge.Resolution)
			resolution := state.resolution
			if edgeResolution == "possible" {
				resolution = "possible"
			}
			if edge.CalleeSymbolID != "" {
				enqueue(coverageState{id: edge.CalleeSymbolID, resolution: resolution, depth: state.depth + 1, parent: state.id})
				continue
			}
			for _, id := range unresolvedTargetIDs(g, edge.CalleeRaw, symbols[state.id].PackageName) {
				enqueue(coverageState{id: id, resolution: "possible", depth: state.depth + 1, parent: state.id})
			}
		}
	}

	for id, state := range best {
		symbol, ok := symbols[id]
		if !ok || isTestFile(symbol.File) || symbol.Kind != graph.KindFunction && symbol.Kind != graph.KindMethod {
			continue
		}
		if exactOnly && state.resolution != "exact" {
			continue
		}
		report.Symbols = append(report.Symbols, CoverageSymbol{
			StableID: id, Kind: symbol.Kind, Name: symbol.Name, Receiver: symbol.Receiver,
			Package: symbol.PackageName, File: symbol.File, Line: symbol.Line,
			Resolution: state.resolution, Depth: state.depth, Path: coveragePath(best, test.StableID, state),
		})
	}
	sort.Slice(report.Symbols, func(i, j int) bool {
		if report.Symbols[i].Resolution != report.Symbols[j].Resolution {
			return report.Symbols[i].Resolution == "exact"
		}
		if report.Symbols[i].Depth != report.Symbols[j].Depth {
			return report.Symbols[i].Depth < report.Symbols[j].Depth
		}
		return report.Symbols[i].StableID < report.Symbols[j].StableID
	})
	return report
}

// TransitiveTests returns every test with a static path to one unambiguous
// symbol. Direct Tests results remain available separately for compatibility.
func TransitiveTests(g *graph.Graph, query string, exactOnly bool) TestsReport {
	return TransitiveTestsInPackage(g, query, "", exactOnly)
}

// TransitiveTestsInPackage applies an optional exact package-name qualifier to
// the selected product symbol before traversing the reverse call graph.
func TransitiveTestsInPackage(g *graph.Graph, query, packageName string, exactOnly bool) TestsReport {
	return NewSnapshot(g).TransitiveTests(query, packageName, exactOnly)
}

func (snapshot *Snapshot) TransitiveTests(query, packageName string, exactOnly bool) TestsReport {
	g := snapshot.g
	report := TestsReport{
		SchemaVersion:      TestsSchemaVersion,
		Query:              strings.TrimSpace(query),
		Package:            strings.TrimSpace(packageName),
		Status:             "not_found",
		AnalysisPrecision:  graph.PrecisionAST,
		TestCallResolution: graph.TestCallResolutionAST,
		MatchedSymbols:     []SymbolIdentity{},
		Tests:              []AttributingTest{},
		Limitations: []string{
			"Static test attribution is not runtime or branch coverage proof.",
			"Possible results may over-approximate dynamic dispatch or parser-only call targets.",
		},
	}
	if g == nil {
		return report
	}
	report.AnalysisPrecision = g.Build.EffectivePrecision()
	report.TestCallResolution = g.Build.EffectiveTestCallResolution()

	identity := IdentityInPackage(g, query, packageName)
	for _, match := range identity.Matches {
		if !isTestFile(match.File) && (match.Kind == graph.KindFunction || match.Kind == graph.KindMethod) {
			report.MatchedSymbols = append(report.MatchedSymbols, match)
		}
	}
	if len(report.MatchedSymbols) == 0 {
		return report
	}
	if len(report.MatchedSymbols) > 1 {
		report.Status = "ambiguous"
		return report
	}
	report.Status = "exact"
	target := report.MatchedSymbols[0]

	attribution, reverse := snapshot.attributionIndex()

	best := map[string]reverseAttributionState{
		target.StableID: {id: target.StableID, resolution: "exact"},
	}
	queue := []reverseAttributionState{best[target.StableID]}
	enqueue := func(state reverseAttributionState) {
		previous, exists := best[state.id]
		if exists && !betterAttributionPath(state.resolution, state.depth, previous.resolution, previous.depth) {
			return
		}
		best[state.id] = state
		queue = append(queue, state)
	}

	for head := 0; head < len(queue); head++ {
		state := queue[head]
		current := best[state.id]
		if current.resolution != state.resolution || current.depth != state.depth || current.next != state.next {
			continue
		}
		if _, isTest := attribution.tests[state.id]; state.id != target.StableID && isTest {
			continue
		}
		for _, link := range reverse[state.id] {
			resolution := state.resolution
			if link.resolution == "possible" {
				resolution = "possible"
			}
			enqueue(reverseAttributionState{id: link.from, resolution: resolution, depth: state.depth + 1, next: state.id})
		}
	}

	for id, state := range best {
		symbol, ok := attribution.tests[id]
		if !ok || exactOnly && state.resolution != "exact" {
			continue
		}
		report.Tests = append(report.Tests, AttributingTest{
			StableID: symbol.ID, Name: symbol.Name, Package: symbol.PackageName, File: symbol.File, Line: symbol.Line,
			Resolution: state.resolution, Depth: state.depth, Path: reverseAttributionPath(best, attribution.tests, target.StableID, state),
		})
	}
	sort.Slice(report.Tests, func(i, j int) bool {
		if report.Tests[i].Resolution != report.Tests[j].Resolution {
			return report.Tests[i].Resolution == "exact"
		}
		if report.Tests[i].Depth != report.Tests[j].Depth {
			return report.Tests[i].Depth < report.Tests[j].Depth
		}
		if report.Tests[i].StableID != report.Tests[j].StableID {
			return report.Tests[i].StableID < report.Tests[j].StableID
		}
		if report.Tests[i].Package != report.Tests[j].Package {
			return report.Tests[i].Package < report.Tests[j].Package
		}
		return report.Tests[i].File < report.Tests[j].File
	})
	return report
}

func betterAttributionPath(resolution string, depth int, previousResolution string, previousDepth int) bool {
	if resolution != previousResolution {
		return resolution == "exact"
	}
	return depth < previousDepth
}

func reverseAttributionPath(best map[string]reverseAttributionState, tests map[string]graph.SymbolNode, targetID string, state reverseAttributionState) []string {
	path := make([]string, 0, state.depth+1)
	seen := make(map[string]bool, state.depth+1)
	for {
		pathID := state.id
		if test, ok := tests[state.id]; ok {
			pathID = test.ID
		}
		path = append(path, pathID)
		if state.id == targetID || state.next == "" || seen[state.next] {
			break
		}
		seen[state.id] = true
		next, ok := best[state.next]
		if !ok {
			break
		}
		state = next
	}
	return path
}

func isTestEntrySymbol(symbol graph.SymbolNode) bool {
	if symbol.Kind != graph.KindFunction || !isTestFile(symbol.File) {
		return false
	}
	return isGoTestEntryName(symbol.Name, "Test") ||
		isGoTestEntryName(symbol.Name, "Benchmark") ||
		isGoTestEntryName(symbol.Name, "Fuzz") ||
		isGoTestEntryName(symbol.Name, "Example")
}

func isGoTestEntryName(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(name, prefix)
	if suffix == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(suffix)
	return !unicode.IsLower(r)
}

func testAttributionNode(symbol graph.SymbolNode) string {
	return symbol.ID + "\x00test\x00" + symbol.PackageName + "\x00" + path.Clean(symbol.File)
}

func buildAttributionGraph(g *graph.Graph) attributionGraphData {
	if g == nil {
		return attributionGraphData{tests: make(map[string]graph.SymbolNode)}
	}
	symbols := make(map[string]graph.SymbolNode, len(g.Symbols))
	testsBySite := make(map[string][]string)
	tests := make(map[string]graph.SymbolNode)
	for _, symbol := range g.Symbols {
		symbols[symbol.ID] = symbol
		if isTestEntrySymbol(symbol) {
			node := testAttributionNode(symbol)
			tests[node] = symbol
			key := symbol.Name + "\x00" + path.Clean(symbol.File)
			testsBySite[key] = append(testsBySite[key], node)
		}
	}
	for key := range testsBySite {
		sort.Strings(testsBySite[key])
	}

	seen := make(map[string]struct{})
	resolvedTargets := make(map[string][]string)
	resolveTargets := func(raw, callerPackage string) []string {
		key := raw + "\x00" + callerPackage
		if ids, ok := resolvedTargets[key]; ok {
			return ids
		}
		ids := unresolvedTargetIDs(g, raw, callerPackage)
		resolvedTargets[key] = ids
		return ids
	}
	links := make([]attributionLink, 0, len(g.Calls)+len(g.TestEdges))
	add := func(from, to, resolution string) {
		if from == "" || to == "" || from == to {
			return
		}
		key := from + "\x00" + to + "\x00" + resolution
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		links = append(links, attributionLink{from: from, to: to, resolution: resolution})
	}

	for _, call := range g.Calls {
		if call.CallerSymbolID == "" {
			continue
		}
		fromIDs := []string{call.CallerSymbolID}
		if testIDs := testsBySite[call.CallerName+"\x00"+path.Clean(call.File)]; len(testIDs) > 0 {
			fromIDs = testIDs
		}
		resolution := callResolution(call.Resolution)
		if call.CalleeSymbolID != "" {
			for _, from := range fromIDs {
				add(from, call.CalleeSymbolID, resolution)
			}
			continue
		}
		for _, from := range fromIDs {
			callerPackage := symbols[call.CallerSymbolID].PackageName
			if test, ok := tests[from]; ok {
				callerPackage = test.PackageName
			}
			for _, id := range resolveTargets(call.CalleeRaw, callerPackage) {
				add(from, id, "possible")
			}
		}
	}
	for _, edge := range g.TestEdges {
		fromIDs := testsBySite[edge.TestFunc+"\x00"+path.Clean(edge.File)]
		if len(fromIDs) == 0 {
			continue
		}
		resolution := callResolution(edge.Resolution)
		for _, from := range fromIDs {
			if edge.TargetSymbolID != "" {
				add(from, edge.TargetSymbolID, resolution)
				continue
			}
			for _, id := range resolveTargets(edge.Target, tests[from].PackageName) {
				add(from, id, "possible")
			}
		}
	}
	sort.Slice(links, func(i, j int) bool {
		if links[i].from != links[j].from {
			return links[i].from < links[j].from
		}
		if links[i].to != links[j].to {
			return links[i].to < links[j].to
		}
		return links[i].resolution < links[j].resolution
	})
	return attributionGraphData{links: links, tests: tests}
}

func (snapshot *Snapshot) computeTestReachability() map[string]symbolTestReach {
	g := snapshot.g
	if g == nil {
		return map[string]symbolTestReach{}
	}
	attribution, _ := snapshot.attributionIndex()
	adjacency := make(map[string][]attributionLink)
	for _, link := range attribution.links {
		adjacency[link.from] = append(adjacency[link.from], link)
	}
	symbols := make(map[string]graph.SymbolNode, len(g.Symbols))
	for _, symbol := range g.Symbols {
		symbols[symbol.ID] = symbol
	}

	testIDs := make([]string, 0, len(attribution.tests))
	for testID := range attribution.tests {
		testIDs = append(testIDs, testID)
	}
	sort.Strings(testIDs)
	wordCount := (len(testIDs) + 63) / 64
	type reachBits struct {
		exact    []uint64
		possible []uint64
	}
	byNode := make(map[string]*reachBits)
	ensure := func(id string) *reachBits {
		entry := byNode[id]
		if entry == nil {
			entry = &reachBits{exact: make([]uint64, wordCount), possible: make([]uint64, wordCount)}
			byNode[id] = entry
		}
		return entry
	}
	queue := make([]string, 0, len(testIDs))
	queued := make(map[string]bool)
	enqueue := func(id string) {
		if id != "" && !queued[id] {
			queued[id] = true
			queue = append(queue, id)
		}
	}
	for index, testID := range testIDs {
		entry := ensure(testID)
		entry.exact[index/64] |= uint64(1) << uint(index%64)
		enqueue(testID)
	}
	for head := 0; head < len(queue); head++ {
		id := queue[head]
		queued[id] = false
		source := ensure(id)
		for _, link := range adjacency[id] {
			destination := ensure(link.to)
			changed := false
			for word := 0; word < wordCount; word++ {
				if link.resolution == "exact" {
					nextExact := destination.exact[word] | source.exact[word]
					nextPossible := destination.possible[word] | source.possible[word]
					changed = changed || nextExact != destination.exact[word] || nextPossible != destination.possible[word]
					destination.exact[word] = nextExact
					destination.possible[word] = nextPossible
				} else {
					nextPossible := destination.possible[word] | source.exact[word] | source.possible[word]
					changed = changed || nextPossible != destination.possible[word]
					destination.possible[word] = nextPossible
				}
			}
			if changed {
				enqueue(link.to)
			}
		}
	}

	reach := make(map[string]symbolTestReach, len(byNode))
	for id, entry := range byNode {
		if _, ok := symbols[id]; !ok {
			continue
		}
		counts := symbolTestReach{}
		for word := 0; word < wordCount; word++ {
			counts.exactCount += bits.OnesCount64(entry.exact[word])
			counts.possibleCount += bits.OnesCount64(entry.possible[word] &^ entry.exact[word])
		}
		reach[id] = counts
	}
	return reach
}

func coveragePath(best map[string]coverageState, testID string, state coverageState) []string {
	reversed := make([]string, 0, state.depth+1)
	seen := make(map[string]bool, state.depth)
	for {
		reversed = append(reversed, state.id)
		if state.parent == testID {
			reversed = append(reversed, testID)
			break
		}
		if state.parent == "" || seen[state.parent] {
			// Defensive fallback for malformed legacy graphs. Coverage traversal
			// is finite independently, and the incomplete path remains explicit.
			break
		}
		seen[state.parent] = true
		parent, ok := best[state.parent]
		if !ok {
			break
		}
		state = parent
	}
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed
}

func callResolution(resolution graph.CallResolution) string {
	if resolution == graph.CallResolutionStatic || resolution == graph.CallResolutionSynthetic {
		return "exact"
	}
	return "possible"
}

func unresolvedTargetIDs(g *graph.Graph, raw, callerPackage string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	name := raw
	if index := strings.LastIndex(name, "."); index >= 0 {
		name = name[index+1:]
	}
	if index := strings.Index(name, "["); index >= 0 {
		name = name[:index]
	}
	seen := make(map[string]bool)
	var ids []string
	for _, symbol := range g.Symbols {
		if symbol.Kind != graph.KindFunction && symbol.Kind != graph.KindMethod {
			continue
		}
		matched := matchSymbolExact(symbol, raw)
		if !matched && symbol.Name == name {
			matched = strings.Contains(raw, ".") || symbol.PackageName == callerPackage
		}
		if matched && !seen[symbol.ID] {
			seen[symbol.ID] = true
			ids = append(ids, symbol.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// FilterUntested excludes repository-relative source paths using path.Match
// syntax. A trailing /** matches every descendant. Matching is lexical and
// never reads the filesystem.
func FilterUntested(results []UntestedResult, excludes []string) ([]UntestedResult, error) {
	normalized := make([]string, 0, len(excludes))
	for _, pattern := range excludes {
		pattern = strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(pattern), "\\", "/"), "./")
		if pattern == "" {
			continue
		}
		probe := pattern
		if strings.HasSuffix(probe, "/**") {
			probe = strings.TrimSuffix(probe, "/**") + "/*"
		}
		if _, err := path.Match(probe, "probe"); err != nil {
			return nil, err
		}
		normalized = append(normalized, pattern)
	}
	filtered := make([]UntestedResult, 0, len(results))
	for _, result := range results {
		file := strings.TrimPrefix(strings.ReplaceAll(result.File, "\\", "/"), "./")
		excluded := false
		for _, pattern := range normalized {
			if prefix := strings.TrimSuffix(pattern, "/**"); prefix != pattern && (file == prefix || strings.HasPrefix(file, prefix+"/")) {
				excluded = true
				break
			}
			if matched, _ := path.Match(pattern, file); matched {
				excluded = true
				break
			}
		}
		if !excluded {
			filtered = append(filtered, result)
		}
	}
	return filtered, nil
}
