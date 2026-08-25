package search

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ozgurcd/gograph/internal/buildctx"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/scanner"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

func normalizeSymbolName(name string) string {
	name = strings.TrimPrefix(name, "(")
	if idx := strings.Index(name, ")."); idx >= 0 {
		name = name[idx+2:]
	}
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		name = name[idx+1:]
	}
	return strings.ToLower(name)
}

// isFullyQualifiedID reports whether the user-supplied query looks like a
// full SymbolNode.ID — recognisable by the "::" separator the parser uses
// between an import path and the symbol's bare name
// (e.g. "github.com/foo/bar::(*Service).Validate"). Query commands use this
// to switch from fuzzy substring matching against bare names to exact
// matching against CalleeSymbolID/CallerSymbolID. The short-name UX is
// preserved: "Validate" still works as before; "pkg::(*S).Validate"
// disambiguates between methods that happen to share a short name.
func isFullyQualifiedID(s string) bool {
	return strings.Contains(s, "::")
}

// Path finds the shortest call chain from symbol `from` to symbol `to` using
// BFS over the call graph edges. It returns the chain as a slice of Result
// values ordered from source to destination. An empty slice means no path was
// found. Both names are matched case-insensitively as substrings so partial
// names (e.g. "ValidateUser" instead of "(AuthService).ValidateUser") work.
// Package-qualified names like "cli.Run" are normalized to just "Run".
func Path(g *graph.Graph, from, to string, includeTests bool) []Result {
	fl := normalizeSymbolName(from)
	tl := normalizeSymbolName(to)
	fromFQ := isFullyQualifiedID(from)
	toFQ := isFullyQualifiedID(to)

	// Matchers accept either a CallerName/CalleeRaw (substring) OR a full
	// SymbolNode.ID (exact, when the user gave an FQ query). Path treats
	// from/to symmetrically — both can be FQ to disambiguate same-named
	// methods, or short for the legacy fuzzy UX.
	//
	// IMPORTANT: matchesToName must NOT fire when the user gave an FQ
	// destination. normalizeSymbolName strips an FQ down to its bare name
	// ("pkg::(*A).Validate" → "validate"), and falling back to a substring
	// match on that would terminate on the first node containing "validate"
	// regardless of receiver — defeating the whole point of FQ disambiguation.
	matchesFromName := func(s string) bool {
		if fromFQ {
			return false
		}
		return strings.Contains(strings.ToLower(s), fl)
	}
	matchesToName := func(s string) bool {
		if toFQ {
			return false
		}
		return strings.Contains(strings.ToLower(s), tl)
	}
	matchesToID := func(id string) bool { return toFQ && id != "" && id == to }

	// Build adjacency keyed by caller NAME (legacy) and by CallerSymbolID
	// (precise). The BFS below walks both maps so an edge is reachable
	// whether the node was added by name or by ID.
	adj := make(map[string][]graph.CallEdge)
	adjLower := make(map[string][]graph.CallEdge)
	adjByID := make(map[string][]graph.CallEdge)
	for _, c := range g.Calls {
		if !includeTests && isTestFile(c.File) {
			continue
		}
		adj[c.CallerName] = append(adj[c.CallerName], c)
		adjLower[strings.ToLower(c.CallerName)] = append(adjLower[strings.ToLower(c.CallerName)], c)
		if c.CallerSymbolID != "" {
			adjByID[c.CallerSymbolID] = append(adjByID[c.CallerSymbolID], c)
		}
	}

	// Seed BFS from all nodes matching "from".
	visited := make(map[string]bool)
	type state struct {
		node string
		path []graph.CallEdge
	}
	var queue []state
	for _, c := range g.Calls {
		seedByName := matchesFromName(c.CallerName)
		seedByID := fromFQ && c.CallerSymbolID == from
		if (seedByName || seedByID) && !visited[c.CallerName] {
			visited[c.CallerName] = true
			queue = append(queue, state{node: c.CallerName})
		}
		// When the FROM query is an FQ ID, also seed by that exact ID so
		// the BFS can walk via adjByID without name conflation.
		if seedByID && !visited[c.CallerSymbolID] {
			visited[c.CallerSymbolID] = true
			queue = append(queue, state{node: c.CallerSymbolID})
		}
	}
	for _, s := range g.Symbols {
		node := s.Name
		if strings.HasPrefix(s.ID, "(") {
			if idx := strings.Index(s.ID, ")"); idx > 0 {
				node = s.ID[idx+1:]
			}
		}
		if matchesFromName(node) && !visited[node] {
			visited[node] = true
			queue = append(queue, state{node: node})
		}
		if fromFQ && s.ID == from && !visited[s.ID] {
			visited[s.ID] = true
			queue = append(queue, state{node: s.ID})
		}
	}

	// enqueueEdge appends a follow-on state to the queue for an outgoing
	// edge. It also visits the edge's CalleeSymbolID (when present) so a
	// later iteration can pick the node up via adjByID and walk forward
	// exactly — no name conflation across symbols that share a short name.
	enqueueEdge := func(cur state, edge graph.CallEdge) {
		nextNode := edge.CalleeRaw
		if strings.Contains(nextNode, ".") {
			normalized := normalizeSymbolName(nextNode)
			parts := strings.Split(normalized, ".")
			nextNode = parts[len(parts)-1]
		}
		newPath := make([]graph.CallEdge, len(cur.path)+1)
		copy(newPath, cur.path)
		newPath[len(cur.path)] = edge

		if !visited[nextNode] {
			visited[nextNode] = true
			if _, exists := adj[nextNode]; exists || strings.Contains(nextNode, "(") {
				visited[edge.CalleeRaw] = true
			}
			queue = append(queue, state{node: nextNode, path: newPath})
			if _, exists := adj[nextNode]; !exists {
				queue = append(queue, state{node: edge.CalleeRaw, path: newPath})
			}
		}
		// Also enqueue the precise CalleeSymbolID as a node so the next
		// hop can walk adjByID exactly — this is the Bug 6 fix: a chain
		// reaching (*A).Validate will not accidentally continue into
		// (*B).Validate's callees on the next hop.
		if edge.CalleeSymbolID != "" && !visited[edge.CalleeSymbolID] {
			visited[edge.CalleeSymbolID] = true
			queue = append(queue, state{node: edge.CalleeSymbolID, path: newPath})
		}
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		// Termination: either the current node name matches (legacy fuzzy
		// match) OR the last edge's CalleeSymbolID matches an FQ to-query.
		matched := matchesToName(cur.node)
		if !matched && len(cur.path) > 0 {
			last := cur.path[len(cur.path)-1]
			if matchesToID(last.CalleeSymbolID) {
				matched = true
			}
		}
		if matched && len(cur.path) > 0 {
			var chain []Result
			for _, edge := range cur.path {
				if edge.Synthetic {
					continue
				}
				chain = append(chain, Result{
					Kind:   "path",
					Name:   edge.CallerName,
					File:   edge.File,
					Line:   edge.Line,
					Detail: fmt.Sprintf("calls %s", edge.CalleeRaw),
					Score:  10,
				})
			}
			last := cur.path[len(cur.path)-1]
			destinationFile, destinationLine := last.File, last.Line
			if last.CalleeSymbolID != "" {
				for _, symbol := range g.Symbols {
					if symbol.ID == last.CalleeSymbolID {
						destinationFile, destinationLine = symbol.File, symbol.Line
						break
					}
				}
			}
			chain = append(chain, Result{
				Kind:   "path",
				Name:   last.CalleeRaw,
				File:   destinationFile,
				Line:   destinationLine,
				Detail: "destination",
				Score:  10,
			})
			return chain
		}

		for _, edge := range adj[cur.node] {
			enqueueEdge(cur, edge)
		}
		// ID-keyed adjacency: when cur.node is a SymbolNode.ID (because a
		// previous hop seeded it via edge.CalleeSymbolID), walking via
		// adjByID gives exact-identity expansion — no conflation with
		// other symbols sharing the short name.
		for _, edge := range adjByID[cur.node] {
			enqueueEdge(cur, edge)
		}
		for _, edge := range adjLower[strings.ToLower(cur.node)] {
			nextNode := edge.CalleeRaw
			if strings.Contains(nextNode, ".") {
				normalized := normalizeSymbolName(nextNode)
				parts := strings.Split(normalized, ".")
				nextNode = parts[len(parts)-1]
			}
			if !visited[nextNode] {
				visited[nextNode] = true
				if _, exists := adj[nextNode]; exists || strings.Contains(nextNode, "(") {
					visited[edge.CalleeRaw] = true
				}
				newPath := make([]graph.CallEdge, len(cur.path)+1)
				copy(newPath, cur.path)
				newPath[len(cur.path)] = edge
				queue = append(queue, state{node: nextNode, path: newPath})
				if _, exists := adj[nextNode]; !exists {
					queue = append(queue, state{node: edge.CalleeRaw, path: newPath})
				}
			}
		}
	}
	return nil
}

func isInternal(path string) bool {
	parts := strings.Split(path, "/")
	for _, p := range parts {
		if p == "internal" {
			return true
		}
	}
	return false
}

// ReachableOrphans returns symbols that are truly unreachable from any program
// entry point. Entry points are: main() functions, HTTP route handlers, and
// exported functions (which may be called by external consumers).
//
// This is stricter than the simple "0 incoming edges" orphan check — a
// function called only by dead code is itself flagged as dead.
func ReachableOrphans(g *graph.Graph) []Result {
	rootIDs := make(map[string]bool)
	// Package-level initializers are emitted with CallerName == "init" and
	// may not have a corresponding SymbolNode. This is a genuine identity-free
	// runtime root, so retain the legacy name key for it.
	fallbackRoots := map[string]bool{"init": true}
	symbolIDsByName := make(map[string][]string)
	for _, s := range g.Symbols {
		if (s.Kind == graph.KindFunction || s.Kind == graph.KindMethod) && s.ID != "" {
			name := normalizeSymbolName(s.Name)
			symbolIDsByName[name] = append(symbolIDsByName[name], s.ID)
		}
	}
	addRoot := func(s graph.SymbolNode) {
		if s.ID != "" {
			rootIDs[s.ID] = true
			return
		}
		fallbackRoots[normalizeSymbolName(s.Name)] = true
	}

	for _, s := range g.Symbols {
		if s.Kind != graph.KindFunction && s.Kind != graph.KindMethod {
			continue
		}
		// Entry points the Go runtime always invokes:
		//   - main()  — program entry point
		//   - init()  — runs at package load time, every package, every binary
		if s.Name == "main" || s.Name == "init" {
			addRoot(s)
			continue
		}
		if isTestFile(s.File) {
			if strings.HasPrefix(s.Name, "Test") || strings.HasPrefix(s.Name, "Benchmark") || strings.HasPrefix(s.Name, "Fuzz") {
				addRoot(s)
			}
			continue
		}
		if isInternal(s.File) || isInternal(s.ID) {
			// Exported symbols inside internal packages are NOT roots.
			continue
		}
		if len(s.Name) > 0 && s.Name[0] >= 'A' && s.Name[0] <= 'Z' {
			addRoot(s)
		}
	}

	for _, route := range g.Routes {
		matched := false
		for _, s := range g.Symbols {
			if s.Kind != graph.KindFunction && s.Kind != graph.KindMethod {
				continue
			}
			if s.ID != "" && MatchSymbol(s, route.Handler) {
				rootIDs[s.ID] = true
				matched = true
			}
		}
		if matched {
			continue
		}
		handlerName := normalizeSymbolName(route.Handler)
		if ids := symbolIDsByName[handlerName]; len(ids) > 0 {
			for _, id := range ids {
				rootIDs[id] = true
			}
			continue
		}
		fallbackRoots[handlerName] = true
	}

	// Exact symbol IDs remain isolated. Name keys exist only for roots or
	// call edges whose identity was unavailable in the graph.
	reachable := make(map[string]bool)
	for id := range rootIDs {
		reachable[id] = true
	}
	for name := range fallbackRoots {
		reachable[name] = true
	}

	adj := make(map[string][]string)
	for _, call := range g.Calls {
		callerKey := call.CallerSymbolID
		if callerKey == "" {
			callerKey = normalizeSymbolName(call.CallerName)
		}

		if call.CalleeSymbolID != "" {
			adj[callerKey] = append(adj[callerKey], call.CalleeSymbolID)
			continue
		}

		// With no resolved callee identity, preserve the legacy conservative
		// fallback: walk the name key and every symbol sharing that name.
		calleeName := normalizeSymbolName(call.CalleeRaw)
		adj[callerKey] = append(adj[callerKey], calleeName)
		adj[callerKey] = append(adj[callerKey], symbolIDsByName[calleeName]...)
	}

	bfsQueue := make([]string, 0, len(reachable))
	for root := range reachable {
		bfsQueue = append(bfsQueue, root)
	}
	for len(bfsQueue) > 0 {
		cur := bfsQueue[0]
		bfsQueue = bfsQueue[1:]
		for _, callee := range adj[cur] {
			if !reachable[callee] {
				reachable[callee] = true
				bfsQueue = append(bfsQueue, callee)
			}
		}
	}

	incomingCount := make(map[string]int)
	for _, call := range sourceCallSites(g.Calls) {
		incomingCount[normalizeSymbolName(call.CalleeRaw)]++
	}

	var results []Result
	for _, s := range g.Symbols {
		if s.Kind != graph.KindFunction && s.Kind != graph.KindMethod {
			continue
		}
		if isTestFile(s.File) {
			continue
		}
		if s.ID != "" && reachable[s.ID] {
			continue
		}
		if s.ID == "" && reachable[normalizeSymbolName(s.Name)] {
			continue
		}
		results = append(results, Result{
			Kind:   "orphan",
			Name:   s.ID,
			File:   s.File,
			Line:   s.Line,
			Detail: fmt.Sprintf("unreachable from any entry point (incoming calls: %d)", incomingCount[normalizeSymbolName(s.Name)]),
			Score:  10,
		})
	}
	sortResults(results)
	return results
}

// StaleResult reports the freshness of graph.json relative to source files.
type StaleResult struct {
	IsStale             bool     `json:"is_stale"`
	GraphAge            string   `json:"graph_age"`
	NewestSourceMtime   string   `json:"newest_source_mtime,omitempty"`
	NewestSourceFile    string   `json:"newest_source_file,omitempty"`
	ChangedFiles        []string `json:"changed_files"`
	BuildContextChanged bool     `json:"build_context_changed"`
}

// ChangeCount returns the number of independent stale signals represented by
// the result. It keeps machine-readable envelopes non-empty for a context-only
// transition while retaining one count per changed selected file.
func (r StaleResult) ChangeCount() int {
	count := len(r.ChangedFiles)
	if r.BuildContextChanged {
		count++
	}
	return count
}

// GodObjectCandidate is a struct that exceeded at least one threshold.
type GodObjectCandidate struct {
	Name          string `json:"name"`
	File          string `json:"file"`
	Line          int    `json:"line"`
	MethodCount   int    `json:"method_count"`
	FieldCount    int    `json:"field_count"`
	OutgoingCalls int    `json:"outgoing_calls"`
	Severity      string `json:"severity"`
	Score         int    `json:"score"`
}

// Stale compares graph.json's selected-file inventory, effective build
// context, and persisted content digests with the current repository state.
// File modification times remain diagnostic only. Pass the absolute
// repository root path.
//
// Returns:
//   - is_stale:            true when selected files or their build context differ.
//   - graph_age:           UTC timestamp of the graph build.
//   - newest_source_mtime: UTC mtime of the newest .go file found (populated
//     regardless of staleness — useful for diagnosis).
//   - newest_source_file:  repo-relative path of that newest file.
//   - changed_files:       files whose bytes changed or that were added/removed
//     from the active build selection (stale case only).
//   - build_context_changed: true when source-selection inputs changed.
func Stale(g *graph.Graph, root string) StaleResult {
	files, buildContextFingerprint, _ := scanner.WalkWithFingerprint(root)
	return staleWithSelection(g, root, files, buildContextFingerprint)
}

// StaleWithConfig compares a graph with source selected by an already-resolved
// build configuration. Servers that were started with explicit build tags use
// this variant so refreshes remain in the selected context instead of falling
// back to the process's ambient GOFLAGS.
func StaleWithConfig(g *graph.Graph, root string, config buildctx.Config) StaleResult {
	files, buildContextFingerprint, _ := scanner.WalkWithConfigAndFingerprint(root, config)
	return staleWithSelection(g, root, files, buildContextFingerprint)
}

func staleWithSelection(g *graph.Graph, root string, files []string, buildContextFingerprint string) StaleResult {
	graphTime := g.GeneratedAt
	staleFiles := make(map[string]struct{})
	var newestMtime time.Time
	var newestPath string

	currentFiles := make(map[string]struct{}, len(files))
	previousDigests := make(map[string]string, len(g.Files))
	for _, file := range g.Files {
		previousDigests[graphFileRelative(root, file.Path)] = file.ContentDigest
	}
	sourceReader, sourceErr := sourcefs.Open(root)
	if sourceReader != nil {
		defer func() { _ = sourceReader.Close() }()
	}
	for _, path := range files {
		rel := graphFileRelative(root, path)
		currentFiles[rel] = struct{}{}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if newestMtime.IsZero() || info.ModTime().After(newestMtime) {
			newestMtime = info.ModTime()
			newestPath = path
		}
		oldDigest := previousDigests[rel]
		if oldDigest == "" {
			// Legacy graphs did not persist digests. Preserve their historical
			// mtime fallback until the next successful build upgrades them.
			if info.ModTime().After(graphTime) {
				staleFiles[rel] = struct{}{}
			}
			continue
		}
		if sourceErr != nil {
			staleFiles[rel] = struct{}{}
			continue
		}
		source, err := sourceReader.ReadFile(rel)
		if err != nil || graph.SourceDigest(source) != oldDigest {
			staleFiles[rel] = struct{}{}
		}
	}

	previousFiles := make(map[string]struct{})
	for _, file := range g.Files {
		previousFiles[graphFileRelative(root, file.Path)] = struct{}{}
	}
	if g.Build != nil {
		for _, failure := range g.Build.Failures {
			previousFiles[graphFileRelative(root, failure.File)] = struct{}{}
		}
	}
	if len(previousFiles) > 0 {
		for path := range previousFiles {
			if _, ok := currentFiles[path]; !ok {
				staleFiles[path] = struct{}{}
			}
		}
		for path := range currentFiles {
			if _, ok := previousFiles[path]; !ok {
				staleFiles[path] = struct{}{}
			}
		}
	}

	buildContextChanged := g.Build != nil &&
		g.Build.BuildContextFingerprint != "" &&
		g.Build.BuildContextFingerprint != buildContextFingerprint
	changedFiles := make([]string, 0, len(staleFiles))
	for path := range staleFiles {
		changedFiles = append(changedFiles, path)
	}
	sortStrings(changedFiles)

	sr := StaleResult{
		IsStale:             len(changedFiles) > 0 || buildContextChanged,
		GraphAge:            graphTime.Format("2006-01-02 15:04:05 UTC"),
		ChangedFiles:        changedFiles,
		BuildContextChanged: buildContextChanged,
	}
	if !newestMtime.IsZero() {
		sr.NewestSourceMtime = newestMtime.UTC().Format("2006-01-02 15:04:05 UTC")
		if rel, err := filepath.Rel(root, newestPath); err == nil {
			sr.NewestSourceFile = rel
		} else {
			sr.NewestSourceFile = newestPath
		}
	}
	return sr
}

// GodObjectParams holds the configurable thresholds for god-object detection.
// All thresholds are minimums: a struct is flagged when it exceeds any one of them.
type GodObjectParams struct {
	// MinMethods is the minimum number of methods on a struct to flag it.
	MinMethods int
	// MinFields is the minimum number of struct fields to flag it.
	MinFields int
	// MinCalls is the minimum number of total outgoing calls from a struct's
	// methods combined to flag it.
	MinCalls int
	// Top limits output to the N highest-scoring results. 0 means show all.
	Top int
}

// DefaultGodObjectParams returns conservative defaults suitable for most Go
// projects. Users can override any threshold via CLI flags.
func DefaultGodObjectParams() GodObjectParams {
	return GodObjectParams{
		MinMethods: 5,
		MinFields:  8,
		MinCalls:   15,
		Top:        10,
	}
}

// severity determines a label based on how far the candidate exceeds thresholds.
func severity(methodCount, fieldCount, outgoingCalls int, p GodObjectParams) (string, int) {
	score := 0
	if p.MinMethods > 0 && methodCount > p.MinMethods {
		score += methodCount - p.MinMethods
	}
	if p.MinFields > 0 && fieldCount > p.MinFields {
		score += fieldCount - p.MinFields
	}
	if p.MinCalls > 0 && outgoingCalls > p.MinCalls {
		score += (outgoingCalls - p.MinCalls) / 2
	}
	label := "LOW"
	switch {
	case score >= 40:
		label = "CRITICAL"
	case score >= 20:
		label = "HIGH"
	case score >= 8:
		label = "MEDIUM"
	}
	return label, score
}

// GodObjects scans the graph for struct types that exceed the given thresholds
// and returns them sorted by severity score descending.
// Results are best-effort: only structs visible in the AST are considered.
func GodObjects(g *graph.Graph, p GodObjectParams) []GodObjectCandidate {
	// 1. Count methods per receiver name.
	methodCount := make(map[string]int)
	for _, s := range g.Symbols {
		if s.Kind == graph.KindMethod && s.Receiver != "" {
			methodCount[s.Receiver]++
		}
	}

	// 2. Count total outgoing calls per receiver (sum across all its methods).
	//    CallerName for methods is typically "(ReceiverType).MethodName".
	outgoingCalls := make(map[string]int)
	for _, c := range sourceCallSites(g.Calls) {
		// Strip "(ReceiverType)." prefix to get receiver name.
		if strings.HasPrefix(c.CallerName, "(") {
			end := strings.Index(c.CallerName, ")")
			if end > 1 {
				receiver := c.CallerName[1:end]
				outgoingCalls[receiver]++
			}
		}
	}

	// 3. Collect struct nodes.
	var candidates []GodObjectCandidate
	for _, s := range g.Symbols {
		if s.Kind != graph.KindStruct {
			continue
		}
		mc := methodCount[s.Name]
		fc := len(s.StructFields)
		oc := outgoingCalls[s.Name]

		// Must exceed at least one threshold to be considered.
		exceeds := (p.MinMethods > 0 && mc > p.MinMethods) ||
			(p.MinFields > 0 && fc > p.MinFields) ||
			(p.MinCalls > 0 && oc > p.MinCalls)
		if !exceeds {
			continue
		}

		sev, score := severity(mc, fc, oc, p)
		candidates = append(candidates, GodObjectCandidate{
			Name:          s.Name,
			File:          s.File,
			Line:          s.Line,
			MethodCount:   mc,
			FieldCount:    fc,
			OutgoingCalls: oc,
			Severity:      sev,
			Score:         score,
		})
	}

	// Sort by score descending (worst first).
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && candidates[j].Score > candidates[j-1].Score; j-- {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
		}
	}

	if p.Top > 0 && len(candidates) > p.Top {
		candidates = candidates[:p.Top]
	}
	return candidates
}
