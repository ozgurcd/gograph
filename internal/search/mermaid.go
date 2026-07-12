package search

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

type mermaidGraph struct {
	nodes  map[string]string // label -> safe alphanumeric ID ("n0", "n1", ...)
	edges  map[string]bool   // set of edges "n0 --> n1"
	nextID int
}

func newMermaidGraph() *mermaidGraph {
	return &mermaidGraph{
		nodes: make(map[string]string),
		edges: make(map[string]bool),
	}
}

func (mg *mermaidGraph) getID(label string) string {
	if id, ok := mg.nodes[label]; ok {
		return id
	}
	id := fmt.Sprintf("n%d", mg.nextID)
	mg.nextID++
	mg.nodes[label] = id
	return id
}

func (mg *mermaidGraph) addEdge(from, to string) {
	if from == to {
		return // skip self-loops to prevent dagre/DAG layout errors in Mermaid rendering
	}
	fromID := mg.getID(from)
	toID := mg.getID(to)
	mg.edges[fmt.Sprintf("    %s --> %s", fromID, toID)] = true
}

func (mg *mermaidGraph) StringWithRenderer(direction, renderer string) string {
	var sb strings.Builder
	sb.WriteString("```mermaid\n")
	if renderer != "" {
		fmt.Fprintf(&sb, "%%%%{init: {'flowchart': {'defaultRenderer': '%s'}}}%%%%\n", renderer)
	}
	sb.WriteString("flowchart ")
	sb.WriteString(direction)
	sb.WriteString("\n")

	type nodeItem struct {
		id    string
		label string
	}
	var sortedNodes []nodeItem
	for label, id := range mg.nodes {
		sortedNodes = append(sortedNodes, nodeItem{id, label})
	}
	sort.Slice(sortedNodes, func(i, j int) bool {
		return sortedNodes[i].id < sortedNodes[j].id
	})
	for _, n := range sortedNodes {
		escapedLabel := strings.ReplaceAll(n.label, "\"", "\\\"")
		fmt.Fprintf(&sb, "    %s[\"%s\"]\n", n.id, escapedLabel)
	}
	var sortedEdges []string
	for edge := range mg.edges {
		sortedEdges = append(sortedEdges, edge)
	}
	sort.Strings(sortedEdges)
	for _, edge := range sortedEdges {
		sb.WriteString(edge)
		sb.WriteString("\n")
	}
	sb.WriteString("```")
	return sb.String()
}

func (mg *mermaidGraph) String(direction string) string {
	var sb strings.Builder
	sb.WriteString("```mermaid\n")
	sb.WriteString("flowchart ")
	sb.WriteString(direction)
	sb.WriteString("\n")

	// Print node definitions with labels
	type nodeItem struct {
		id    string
		label string
	}
	var sortedNodes []nodeItem
	for label, id := range mg.nodes {
		sortedNodes = append(sortedNodes, nodeItem{id, label})
	}
	sort.Slice(sortedNodes, func(i, j int) bool {
		return sortedNodes[i].id < sortedNodes[j].id
	})

	for _, n := range sortedNodes {
		escapedLabel := strings.ReplaceAll(n.label, "\"", "\\\"")
		fmt.Fprintf(&sb, "    %s[\"%s\"]\n", n.id, escapedLabel)
	}

	// Print sorted edges
	var sortedEdges []string
	for edge := range mg.edges {
		sortedEdges = append(sortedEdges, edge)
	}
	sort.Strings(sortedEdges)

	for _, edge := range sortedEdges {
		sb.WriteString(edge)
		sb.WriteString("\n")
	}

	sb.WriteString("```")
	return sb.String()
}

// visibleMermaidCallEdges collapses traversal-only wrapper forwarding into
// the ordinary source edge that reached the wrapper. The resulting graph keeps
// every concrete terminal target while never rendering a synthetic node or an
// invented source call site.
func visibleMermaidCallEdges(g *graph.Graph, includeTests bool) []graph.CallEdge {
	var visible []graph.CallEdge
	for _, call := range g.Calls {
		if call.Synthetic || !includeTests && isTestFile(call.File) {
			continue
		}
		targetIDs := visibleForwardingTargets(g, call.CalleeSymbolID)
		if len(targetIDs) == 0 {
			visible = append(visible, call)
			continue
		}
		for _, targetID := range targetIDs {
			resolved := call
			resolved.CalleeSymbolID = targetID
			visible = append(visible, resolved)
		}
	}
	return visible
}

func visibleForwardingTargets(g *graph.Graph, targetID string) []string {
	if targetID == "" {
		return nil
	}
	expanded := expandForwardingCallees(g, map[string]bool{targetID: true})
	forwarders := make(map[string]bool)
	for _, call := range g.Calls {
		if call.Synthetic && expanded[call.CallerSymbolID] && expanded[call.CalleeSymbolID] {
			forwarders[call.CallerSymbolID] = true
		}
	}
	visible := make([]string, 0, len(expanded))
	for id := range expanded {
		if id != "" && !forwarders[id] {
			visible = append(visible, id)
		}
	}
	if len(visible) == 0 {
		visible = append(visible, targetID)
	}
	sort.Strings(visible)
	return visible
}

func mermaidResolvedFrontier(g *graph.Graph, term string) map[string]bool {
	frontier := make(map[string]bool)
	resolved, interfaceMethodQuery := resolvedCallTargetIDs(g, term)
	for id := range resolved {
		frontier[id] = true
	}
	if interfaceMethodQuery {
		// Match the text/JSON caller contract: an interface-qualified query
		// is resolved only through proven implementers and must never widen
		// to unrelated same-named symbols when that set is empty.
		return expandTransparentCallerTargets(g, frontier)
	}

	tl := strings.ToLower(term)
	for _, symbol := range g.Symbols {
		if MatchSymbol(symbol, term) ||
			(!isFullyQualifiedID(term) && (strings.Contains(strings.ToLower(symbol.Name), tl) ||
				symbol.Receiver != "" && strings.Contains(strings.ToLower(symbol.Receiver), tl))) {
			frontier[symbol.ID] = true
			frontier[strings.ToLower(symbol.Name)] = true
		}
	}
	if len(frontier) == 0 {
		frontier[tl] = true
	}
	return expandTransparentCallerTargets(g, frontier)
}

// DepsToMermaid builds a package dependency diagram in TD style.
func DepsToMermaid(g *graph.Graph, result *DepsResult) string {
	mg := newMermaidGraph()

	// Build short-name → full-import-path mapping first so inScope uses canonical paths.
	shortToFull := make(map[string]string)
	for _, s := range g.Symbols {
		if s.PackageName == "" {
			continue
		}
		if i := strings.Index(s.ID, "::"); i >= 0 {
			shortToFull[s.PackageName] = s.ID[:i]
		}
	}
	canonical := func(pkg string) string {
		if full, ok := shortToFull[pkg]; ok {
			return full
		}
		return pkg
	}

	// Build inScope using canonical paths so the filter matches resolved import edges.
	srcPkg := canonical(result.Package)
	inScope := make(map[string]bool)
	inScope[strings.ToLower(srcPkg)] = true
	for _, d := range result.Direct {
		inScope[strings.ToLower(canonical(d))] = true
	}
	for _, d := range result.Transitive {
		inScope[strings.ToLower(canonical(d))] = true
	}

	// Traverse g.Imports restricted to in-scope packages.
	for _, imp := range g.Imports {
		if imp.FromPackage == "" || imp.ImportPath == "" {
			continue
		}
		fromPkg := canonical(imp.FromPackage)
		toPkg := canonical(imp.ImportPath)
		if inScope[strings.ToLower(fromPkg)] && inScope[strings.ToLower(toPkg)] {
			mg.addEdge(fromPkg, toPkg)
		}
	}

	// Ensure all direct dependencies are drawn using canonical names.
	for _, d := range result.Direct {
		mg.addEdge(srcPkg, canonical(d))
	}

	return mg.String("TD")
}

// DependentsToMermaid maps who imports a target package.
func DependentsToMermaid(pkg string, results []Result) string {
	mg := newMermaidGraph()
	for _, r := range results {
		mg.addEdge(r.Name, pkg)
	}
	return mg.String("TD")
}

// CouplingToMermaid maps all packages and their imports.
func CouplingToMermaid(g *graph.Graph, term string, opts CouplingOptions) string {
	mg := newMermaidGraph()

	shortToFull := make(map[string]string)
	for _, s := range g.Symbols {
		if s.PackageName == "" {
			continue
		}
		if i := strings.Index(s.ID, "::"); i >= 0 {
			shortToFull[s.PackageName] = s.ID[:i]
		}
	}
	canonical := func(pkg string) string {
		if full, ok := shortToFull[pkg]; ok {
			return full
		}
		return pkg
	}

	moduleLower := strings.ToLower(opts.ModuleOnly)
	includePkg := func(pkg string) bool {
		if pkg == "" {
			return false
		}
		if !opts.IncludeStdlib && isStdlibPackage(pkg) {
			return false
		}
		if moduleLower != "" && !strings.HasPrefix(strings.ToLower(pkg), moduleLower) {
			return false
		}
		return true
	}

	tl := strings.ToLower(term)
	for _, imp := range g.Imports {
		from := canonical(imp.FromPackage)
		to := canonical(imp.ImportPath)
		if from == "" || to == "" {
			continue
		}
		if !includePkg(from) || !includePkg(to) {
			continue
		}
		if tl != "" && !strings.Contains(strings.ToLower(from), tl) && !strings.Contains(strings.ToLower(to), tl) {
			continue
		}
		mg.addEdge(from, to)
	}

	return mg.String("TD")
}

// CallersToMermaid traces caller chains backwards up to maxDepth.
func CallersToMermaid(g *graph.Graph, term string, maxDepth int, includeTests bool) string {
	if maxDepth <= 0 {
		maxDepth = 1
	} else if maxDepth > 10 {
		maxDepth = 10
	}

	mg := newMermaidGraph()
	allCalls := visibleMermaidCallEdges(g, includeTests)
	frontier := mermaidResolvedFrontier(g, term)

	symMap := make(map[string]string)
	for _, s := range g.Symbols {
		symMap[s.ID] = s.ID
		if s.Receiver != "" {
			symMap[s.ID] = "(" + s.Receiver + ")." + s.Name
		} else {
			symMap[s.ID] = s.Name
		}
	}

	visited := make(map[string]bool)
	currentLevel := make(map[string]bool)
	for k := range frontier {
		currentLevel[k] = true
	}

	for depth := 1; depth <= maxDepth; depth++ {
		currentLevel = expandTransparentCallerTargets(g, currentLevel)
		nextLevel := make(map[string]bool)
		for _, c := range allCalls {
			matched := false
			if c.CalleeSymbolID != "" && currentLevel[c.CalleeSymbolID] {
				matched = true
			} else if currentLevel[strings.ToLower(c.CalleeRaw)] {
				matched = true
			}

			if matched {
				callerLabel := c.CallerSymbolID
				if label, ok := symMap[c.CallerSymbolID]; ok {
					callerLabel = label
				} else if c.CallerName != "" {
					callerLabel = c.CallerName
				}

				calleeLabel := c.CalleeSymbolID
				if label, ok := symMap[c.CalleeSymbolID]; ok {
					calleeLabel = label
				} else if c.CalleeRaw != "" {
					calleeLabel = c.CalleeRaw
				}

				if callerLabel != "" && calleeLabel != "" {
					mg.addEdge(callerLabel, calleeLabel)
				}

				if c.CallerSymbolID != "" && !visited[c.CallerSymbolID] {
					visited[c.CallerSymbolID] = true
					nextLevel[c.CallerSymbolID] = true
				}
				if c.CallerName != "" && !visited[strings.ToLower(c.CallerName)] {
					visited[strings.ToLower(c.CallerName)] = true
					nextLevel[strings.ToLower(c.CallerName)] = true
				}
			}
		}
		if len(nextLevel) == 0 {
			break
		}
		currentLevel = nextLevel
	}

	return mg.String("LR")
}

// CalleesToMermaid traces callee chains forwards down to maxDepth.
func CalleesToMermaid(g *graph.Graph, term string, maxDepth int, includeTests bool) string {
	if maxDepth <= 0 {
		maxDepth = 1
	} else if maxDepth > 10 {
		maxDepth = 10
	}

	mg := newMermaidGraph()
	allCalls := visibleMermaidCallEdges(g, includeTests)
	frontier := mermaidResolvedFrontier(g, term)

	symMap := make(map[string]string)
	for _, s := range g.Symbols {
		symMap[s.ID] = s.ID
		if s.Receiver != "" {
			symMap[s.ID] = "(" + s.Receiver + ")." + s.Name
		} else {
			symMap[s.ID] = s.Name
		}
	}

	visited := make(map[string]bool)
	currentLevel := make(map[string]bool)
	for k := range frontier {
		currentLevel[k] = true
	}

	for depth := 1; depth <= maxDepth; depth++ {
		currentLevel = expandForwardingCallees(g, currentLevel)
		nextLevel := make(map[string]bool)
		for _, c := range allCalls {
			matched := false
			if c.CallerSymbolID != "" && currentLevel[c.CallerSymbolID] {
				matched = true
			} else if currentLevel[strings.ToLower(c.CallerName)] {
				matched = true
			}

			if matched {
				callerLabel := c.CallerSymbolID
				if label, ok := symMap[c.CallerSymbolID]; ok {
					callerLabel = label
				} else if c.CallerName != "" {
					callerLabel = c.CallerName
				}

				calleeLabel := c.CalleeSymbolID
				if label, ok := symMap[c.CalleeSymbolID]; ok {
					calleeLabel = label
				} else if c.CalleeRaw != "" {
					calleeLabel = c.CalleeRaw
				}

				if callerLabel != "" && calleeLabel != "" {
					mg.addEdge(callerLabel, calleeLabel)
				}

				if c.CalleeSymbolID != "" && !visited[c.CalleeSymbolID] {
					visited[c.CalleeSymbolID] = true
					nextLevel[c.CalleeSymbolID] = true
				}
				if c.CalleeRaw != "" && !visited[strings.ToLower(c.CalleeRaw)] {
					visited[strings.ToLower(c.CalleeRaw)] = true
					nextLevel[strings.ToLower(c.CalleeRaw)] = true
				}
			}
		}
		if len(nextLevel) == 0 {
			break
		}
		currentLevel = nextLevel
	}

	return mg.String("LR")
}

// PathToMermaid draws a shortest sequential call path in LR style.
func PathToMermaid(chain []Result) string {
	if len(chain) == 0 {
		return ""
	}
	mg := newMermaidGraph()
	for i := 0; i < len(chain)-1; i++ {
		mg.addEdge(chain[i].Name, chain[i+1].Name)
	}
	return mg.String("LR")
}

// ImpactToMermaid draws downstream blast radius in TD style.
func ImpactToMermaid(g *graph.Graph, term string, includeTests bool) string {
	return CallersToMermaid(g, term, 20, includeTests)
}

// ImpactMultipleToMermaid draws the combined blast radius for multiple starting symbols.
func ImpactMultipleToMermaid(g *graph.Graph, terms []string, includeTests bool) string {
	if len(terms) == 0 {
		return ""
	}
	mg := newMermaidGraph()
	allCalls := visibleMermaidCallEdges(g, includeTests)

	symMap := make(map[string]string)
	for _, s := range g.Symbols {
		if s.Receiver != "" {
			symMap[s.ID] = "(" + s.Receiver + ")." + s.Name
		} else {
			symMap[s.ID] = s.Name
		}
	}

	// Seed frontier from all terms.
	frontier := make(map[string]bool)
	for _, term := range terms {
		for key := range mermaidResolvedFrontier(g, term) {
			frontier[key] = true
		}
	}

	visited := make(map[string]bool)
	currentLevel := make(map[string]bool)
	for k := range frontier {
		currentLevel[k] = true
	}

	for depth := 1; depth <= 20; depth++ {
		currentLevel = expandTransparentCallerTargets(g, currentLevel)
		nextLevel := make(map[string]bool)
		for _, c := range allCalls {
			matched := (c.CalleeSymbolID != "" && currentLevel[c.CalleeSymbolID]) || currentLevel[strings.ToLower(c.CalleeRaw)]
			if !matched {
				continue
			}
			callerLabel := c.CallerSymbolID
			if label, ok := symMap[c.CallerSymbolID]; ok {
				callerLabel = label
			} else if c.CallerName != "" {
				callerLabel = c.CallerName
			}
			calleeLabel := c.CalleeSymbolID
			if label, ok := symMap[c.CalleeSymbolID]; ok {
				calleeLabel = label
			} else if c.CalleeRaw != "" {
				calleeLabel = c.CalleeRaw
			}
			if callerLabel != "" && calleeLabel != "" {
				mg.addEdge(callerLabel, calleeLabel)
			}
			if c.CallerSymbolID != "" && !visited[c.CallerSymbolID] {
				visited[c.CallerSymbolID] = true
				nextLevel[c.CallerSymbolID] = true
			}
			if c.CallerName != "" && !visited[strings.ToLower(c.CallerName)] {
				visited[strings.ToLower(c.CallerName)] = true
				nextLevel[strings.ToLower(c.CallerName)] = true
			}
		}
		if len(nextLevel) == 0 {
			break
		}
		currentLevel = nextLevel
	}

	return mg.String("LR")
}

// inferModulePathFromGraph derives the Go module path from the common path-segment
// prefix of all package paths recorded in the graph. Used as a fallback when
// ReadModulePath cannot find go.mod (e.g., when running from a subdirectory).
func inferModulePathFromGraph(g *graph.Graph) string {
	var paths []string
	for _, s := range g.Symbols {
		if i := strings.Index(s.ID, "::"); i >= 0 {
			paths = append(paths, s.ID[:i])
		}
	}
	if len(paths) == 0 {
		return ""
	}
	prefix := paths[0]
	for _, p := range paths[1:] {
		aParts := strings.Split(prefix, "/")
		bParts := strings.Split(p, "/")
		min := len(aParts)
		if len(bParts) < min {
			min = len(bParts)
		}
		i := 0
		for i < min && aParts[i] == bParts[i] {
			i++
		}
		prefix = strings.Join(aParts[:i], "/")
		if prefix == "" {
			return ""
		}
	}
	return prefix
}

// DiagramToMermaid generates a high-level architecture overview of the repository.
//
// groupBy controls the abstraction level:
//
//	"package" (default) — one node per import path
//	"module"            — collapse internal packages to their top-level dir group;
//	                      external deps → module root (first 3 path segments)
//	"service"           — two-segment groups within the module (internal/auth,
//	                      cmd/server…); finer than module, coarser than package
//	"file"              — file → imported package edges (most granular)
//
// maxDepth: 0 = unlimited; N > 0 = BFS N levels out from entry packages
// (packages that nothing else imports within the set).
func DiagramToMermaid(g *graph.Graph, groupBy string, maxDepth int, includeStdlib bool) string {
	// Build short-name → full-import-path mapping.
	shortToFull := make(map[string]string)
	for _, s := range g.Symbols {
		if s.PackageName == "" {
			continue
		}
		if i := strings.Index(s.ID, "::"); i >= 0 {
			shortToFull[s.PackageName] = s.ID[:i]
		}
	}
	canonical := func(pkg string) string {
		if full, ok := shortToFull[pkg]; ok {
			return full
		}
		return pkg
	}

	// Determine groupFn based on groupBy.
	modulePath := ReadModulePath(".")
	if modulePath == "" {
		// Fallback: derive module path from the common path-segment prefix of all symbol IDs.
		modulePath = inferModulePathFromGraph(g)
	}

	// externalModuleRoot returns the first 3 path segments of an external import path.
	externalModuleRoot := func(pkg string) string {
		parts := strings.Split(pkg, "/")
		if len(parts) > 3 {
			return strings.Join(parts[:3], "/")
		}
		return pkg
	}

	groupFn := func(pkg string) string {
		switch groupBy {
		case "module":
			if modulePath != "" && strings.HasPrefix(pkg, modulePath+"/") {
				suffix := strings.TrimPrefix(pkg, modulePath+"/")
				if idx := strings.Index(suffix, "/"); idx >= 0 {
					return suffix[:idx]
				}
				return suffix
			}
			return externalModuleRoot(pkg)
		case "service":
			// Two-segment path within the module (e.g. internal/auth, cmd/server).
			if modulePath != "" && strings.HasPrefix(pkg, modulePath+"/") {
				suffix := strings.TrimPrefix(pkg, modulePath+"/")
				parts := strings.SplitN(suffix, "/", 3)
				if len(parts) >= 2 {
					return parts[0] + "/" + parts[1]
				}
				return suffix
			}
			return externalModuleRoot(pkg)
		default: // "package"
			return pkg
		}
	}

	// Collect edges.
	type pkgEdge struct{ from, to string }
	edgeSet := make(map[pkgEdge]bool)
	nodeSet := make(map[string]bool)

	for _, imp := range g.Imports {
		if !includeStdlib && isStdlibPackage(imp.ImportPath) {
			continue
		}
		if groupBy == "file" {
			// File-level: FromFile → imported package path.
			// Restrict to intra-module imports by default to keep the diagram readable;
			// external deps produce far too many nodes for a repo-level overview.
			if imp.FromFile == "" || imp.ImportPath == "" {
				continue
			}
			if modulePath != "" && !strings.HasPrefix(imp.ImportPath, modulePath+"/") {
				continue
			}
			from := imp.FromFile
			to := imp.ImportPath
			if from == to {
				continue
			}
			edgeSet[pkgEdge{from, to}] = true
			nodeSet[from] = true
			nodeSet[to] = true
			continue
		}
		if imp.FromPackage == "" || imp.ImportPath == "" {
			continue
		}
		from := groupFn(canonical(imp.FromPackage))
		to := groupFn(canonical(imp.ImportPath))
		if from == to {
			continue
		}
		edgeSet[pkgEdge{from, to}] = true
		nodeSet[from] = true
		nodeSet[to] = true
	}

	// Apply max-depth BFS from entry packages if requested.
	if maxDepth > 0 && len(edgeSet) > 0 {
		// Entry packages: appear as "from" but never as "to".
		inbound := make(map[string]bool)
		for e := range edgeSet {
			inbound[e.to] = true
		}
		allowed := make(map[string]bool)
		current := make(map[string]bool)
		for node := range nodeSet {
			if !inbound[node] {
				allowed[node] = true
				current[node] = true
			}
		}
		// If every node has an inbound edge (cycle or fully connected), seed all nodes.
		if len(current) == 0 {
			for node := range nodeSet {
				allowed[node] = true
				current[node] = true
			}
		}
		for depth := 0; depth < maxDepth; depth++ {
			next := make(map[string]bool)
			for e := range edgeSet {
				if current[e.from] && !allowed[e.to] {
					allowed[e.to] = true
					next[e.to] = true
				}
			}
			current = next
			if len(current) == 0 {
				break
			}
		}
		// Filter edge set to allowed nodes only.
		filtered := make(map[pkgEdge]bool)
		for e := range edgeSet {
			if allowed[e.from] && allowed[e.to] {
				filtered[e] = true
			}
		}
		edgeSet = filtered
	}

	mg := newMermaidGraph()
	for e := range edgeSet {
		mg.addEdge(e.from, e.to)
	}
	return mg.StringWithRenderer("LR", "elk")
}

// EndpointToMermaid draws matched HTTP routes down to service slices.
func EndpointToMermaid(slices []EndpointSlice) string {
	if len(slices) == 0 {
		return ""
	}
	mg := newMermaidGraph()
	for _, slice := range slices {
		mg.addEdge(slice.Route, slice.Handler)

		for _, step := range slice.CallChain {
			for _, callee := range step.Callees {
				mg.addEdge(step.Symbol, callee)
			}
		}
	}
	return mg.String("TD")
}
