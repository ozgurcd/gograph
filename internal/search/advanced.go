package search

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
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
			chain = append(chain, Result{
				Kind:   "path",
				Name:   last.CalleeRaw,
				File:   last.File,
				Line:   last.Line,
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
	roots := make(map[string]bool)

	for _, s := range g.Symbols {
		if s.Kind != graph.KindFunction && s.Kind != graph.KindMethod {
			continue
		}
		// Entry points the Go runtime always invokes:
		//   - main()  — program entry point
		//   - init()  — runs at package load time, every package, every binary
		//               (including test binaries; can appear multiple times per
		//               package — Go runs them all)
		if s.Name == "main" || s.Name == "init" {
			roots[normalizeSymbolName(s.ID)] = true
			roots[normalizeSymbolName(s.Name)] = true
			continue
		}
		if isTestFile(s.File) {
			// Test/Benchmark/Fuzz functions in test files are roots
			if strings.HasPrefix(s.Name, "Test") || strings.HasPrefix(s.Name, "Benchmark") || strings.HasPrefix(s.Name, "Fuzz") {
				roots[normalizeSymbolName(s.ID)] = true
				roots[normalizeSymbolName(s.Name)] = true
			}
			continue
		}
		if isInternal(s.File) || isInternal(s.ID) {
			// Exported symbols inside internal packages are NOT roots
			continue
		}
		if len(s.Name) > 0 && s.Name[0] >= 'A' && s.Name[0] <= 'Z' {
			roots[normalizeSymbolName(s.ID)] = true
			roots[normalizeSymbolName(s.Name)] = true
		}
	}
	// Package-level var/const initializer expressions are emitted by the
	// parser as call edges with CallerName == "init" (the natural sibling
	// to actual init() function bodies — both run at package load time).
	// The "init" name above seeds those edges as roots.
	for _, r := range g.Routes {
		roots[normalizeSymbolName(r.Handler)] = true
	}

	// Seed reachable set with all roots — both the normalised-name form
	// (legacy fallback for edges that lack CalleeSymbolID) AND the full
	// symbol ID. The ID-keyed entry is what lets the BFS step through
	// CalleeSymbolID without name conflation (Bug 6).
	reachable := make(map[string]bool)
	for r := range roots {
		reachable[r] = true
	}
	for _, s := range g.Symbols {
		if s.Kind != graph.KindFunction && s.Kind != graph.KindMethod {
			continue
		}
		if !reachable[normalizeSymbolName(s.Name)] && !reachable[normalizeSymbolName(s.ID)] {
			continue
		}
		// Symbol is a root by name; also seed its ID so BFS via
		// CalleeSymbolID can reach things only it specifically calls.
		reachable[s.ID] = true
	}

	// Adjacency from caller-key → list of callee-keys. We index by two
	// keys per side so the BFS works whether edges have CalleeSymbolID
	// (precise mode) or only CalleeRaw (basic mode). On the caller side
	// CallerSymbolID is preferred; CalleeSymbolID is preferred on the
	// callee side. Empty fields fall back to the normalised name —
	// matches the legacy behaviour for unresolved edges.
	adj := make(map[string][]string)
	for _, c := range g.Calls {
		var callerKeys []string
		if c.CallerSymbolID != "" {
			callerKeys = append(callerKeys, c.CallerSymbolID)
		}
		callerKeys = append(callerKeys, normalizeSymbolName(c.CallerName))

		var calleeKey string
		if c.CalleeSymbolID != "" {
			// Exact resolution: no conflation risk. (*A).Validate stays
			// distinct from (*B).Validate — the whole point of Bug 6.
			calleeKey = c.CalleeSymbolID
		} else {
			calleeKey = normalizeSymbolName(c.CalleeRaw)
		}
		for _, ck := range callerKeys {
			adj[ck] = append(adj[ck], calleeKey)
		}
	}

	bfsQueue := make([]string, 0, len(reachable))
	for r := range reachable {
		bfsQueue = append(bfsQueue, r)
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
	for _, c := range g.Calls {
		incomingCount[normalizeSymbolName(c.CalleeRaw)]++
	}

	var results []Result
	for _, s := range g.Symbols {
		if s.Kind != graph.KindFunction && s.Kind != graph.KindMethod {
			continue
		}
		// Exclude symbols declared in test files from being reported as orphans.
		if isTestFile(s.File) {
			continue
		}
		// Three reachability checks per symbol:
		//   - exact ID (matches CalleeSymbolID-based BFS hits)
		//   - normalised ID (matches name-based roots/edges)
		//   - normalised name (legacy short-name fallback)
		if reachable[s.ID] || reachable[normalizeSymbolName(s.ID)] || reachable[normalizeSymbolName(s.Name)] {
			continue
		}
		results = append(results, Result{
			Kind:   "orphan",
			Name:   s.ID,
			File:   s.File,
			Line:   s.Line,
			Detail: fmt.Sprintf("unreachable from any entry point (incoming calls: %d)", incomingCount[normalizeSymbolName(s.ID)]+incomingCount[normalizeSymbolName(s.Name)]),
			Score:  10,
		})
	}
	sortResults(results)
	return results
}

// StaleResult reports the freshness of graph.json relative to source files.
type StaleResult struct {
	IsStale      bool     `json:"is_stale"`
	GraphAge     string   `json:"graph_age"`
	ChangedFiles []string `json:"changed_files,omitempty"`
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

// Stale compares graph.json's GeneratedAt timestamp with the mtime of every
// .go file under root. Pass the absolute repository root path.
func Stale(g *graph.Graph, root string) StaleResult {
	graphTime := g.GeneratedAt
	var staleFiles []string

	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			base := info.Name()
			if base == ".gograph" || base == "vendor" || base == ".git" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if info.ModTime().After(graphTime) {
			if rel, relErr := filepath.Rel(root, path); relErr == nil {
				staleFiles = append(staleFiles, rel)
			} else {
				staleFiles = append(staleFiles, path)
			}
		}
		return nil
	})

	return StaleResult{
		IsStale:      len(staleFiles) > 0,
		GraphAge:     graphTime.Format("2006-01-02 15:04:05 UTC"),
		ChangedFiles: staleFiles,
	}
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
	for _, c := range g.Calls {
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
