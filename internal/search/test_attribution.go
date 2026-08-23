package search

import (
	"path"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

const (
	IdentitySchemaVersion = "gograph.identity.v1"
	CoverageSchemaVersion = "gograph.coverage.v1"
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

type coverageState struct {
	id         string
	resolution string
	depth      int
	parent     string
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

	symbols := make(map[string]graph.SymbolNode, len(g.Symbols))
	for _, s := range g.Symbols {
		symbols[s.ID] = s
	}
	adjacency := make(map[string][]graph.CallEdge)
	for _, call := range g.Calls {
		adjacency[call.CallerSymbolID] = append(adjacency[call.CallerSymbolID], call)
	}

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
