// Package search provides case-insensitive query matching over a Graph.
package search

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

func isSafePathSegment(seg string) bool {
	if seg == "" {
		return false
	}
	if strings.Contains(seg, "..") {
		return false
	}
	return true
}

// Result is a single match returned by any search function.
type Result struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Detail string `json:"detail,omitempty"`
	Score  int    `json:"-"` // internal ranking only, not serialised

	// CallSiteFile and CallSiteLine carry the exact location of the call
	// expression that produced this result (callers/callees). Empty for
	// non-call results. This is the provenance field.
	CallSiteFile string `json:"call_site_file,omitempty"`
	CallSiteLine int    `json:"call_site_line,omitempty"`
}

// String returns a compact human-readable representation.
func (r Result) String() string {
	loc := r.File
	if r.Line > 0 {
		loc = fmt.Sprintf("%s:%d", r.File, r.Line)
	}
	provenance := ""
	if r.CallSiteFile != "" {
		provenance = fmt.Sprintf(" [call @ %s:%d]", r.CallSiteFile, r.CallSiteLine)
	}
	if r.Detail != "" {
		return fmt.Sprintf("[%s] %s — %s  (%s)%s", r.Kind, r.Name, r.Detail, loc, provenance)
	}
	return fmt.Sprintf("[%s] %s  (%s)%s", r.Kind, r.Name, loc, provenance)
}

// Query searches g for all occurrences of any of the given terms (OR logic)
// and returns ranked results. Matching is case-insensitive.
func Query(g *graph.Graph, terms []string) []Result {
	lterms := make([]string, len(terms))
	for i, t := range terms {
		lterms[i] = strings.ToLower(t)
	}
	match := func(s string) int {
		sl := strings.ToLower(s)
		for _, t := range lterms {
			if strings.Contains(sl, t) {
				return 1
			}
		}
		return 0
	}

	seen := make(map[string]bool)
	var results []Result
	add := func(r Result) {
		key := fmt.Sprintf("%s|%s|%d", r.Kind, r.Name, r.Line)
		if !seen[key] {
			seen[key] = true
			results = append(results, r)
		}
	}

	for _, pkg := range g.Packages {
		if match(pkg.Name)+match(pkg.Dir) > 0 {
			add(Result{Kind: "package", Name: pkg.Name, File: pkg.Dir, Score: 1})
		}
	}
	for _, f := range g.Files {
		if match(f.Path) > 0 {
			add(Result{Kind: "file", Name: f.Path, File: f.Path, Score: 1})
		}
	}
	for _, s := range g.Symbols {
		score := match(s.Name) + match(s.Doc) + match(s.Receiver)
		if score > 0 {
			name := s.Name
			if s.Receiver != "" {
				name = fmt.Sprintf("(%s).%s", s.Receiver, s.Name)
			}
			add(Result{Kind: string(s.Kind), Name: name, File: s.File, Line: s.Line, Detail: string(s.Kind), Score: score})
		}
	}
	for _, imp := range g.Imports {
		if match(imp.ImportPath) > 0 {
			add(Result{Kind: "import", Name: imp.ImportPath, File: imp.FromFile, Detail: "imported by " + imp.FromPackage, Score: 1})
		}
	}
	for _, c := range g.Calls {
		if match(c.CalleeRaw) > 0 {
			add(Result{Kind: "call", Name: c.CalleeRaw, File: c.File, Line: c.Line, Detail: "called by " + c.CallerName, Score: 1})
		}
	}

	sortResults(results)
	return results
}

// Node finds symbols/packages/files whose name matches (exact or partial).
func Node(g *graph.Graph, name string) []Result {
	nl := strings.ToLower(name)
	seen := make(map[string]bool)
	var results []Result
	add := func(r Result) {
		key := fmt.Sprintf("%s|%s|%d", r.Kind, r.Name, r.Line)
		if !seen[key] {
			seen[key] = true
			results = append(results, r)
		}
	}

	for _, pkg := range g.Packages {
		if strings.ToLower(pkg.Name) == nl {
			add(Result{Kind: "package", Name: pkg.Name, File: pkg.Dir})
		}
	}
	for _, f := range g.Files {
		fl := strings.ToLower(f.Path)
		if fl == nl || fl == nl+".go" {
			add(Result{Kind: "file", Name: f.Path, File: f.Path})
		}
	}
	for _, s := range g.Symbols {
		sname := strings.ToLower(s.Name)
		full := strings.ToLower(fmt.Sprintf("(%s).%s", s.Receiver, s.Name))
		if MatchSymbol(s, name) || sname == nl || strings.Contains(full, nl) || strings.Contains(sname, nl) {
			n2 := s.Name
			if s.Receiver != "" {
				n2 = fmt.Sprintf("(%s).%s", s.Receiver, s.Name)
			}
			add(Result{Kind: string(s.Kind), Name: n2, File: s.File, Line: s.Line, Detail: s.Signature})
		}
	}

	sortResults(results)
	return results
}

// Callers returns functions/methods that contain a call expression matching name.
// Each result includes call-site provenance (CallSiteFile, CallSiteLine) pointing
// to the exact line of the call expression, not just the enclosing function.
//
// When name is a fully-qualified symbol ID (contains "::"), callees are matched
// exactly against CallEdge.CalleeSymbolID — this disambiguates (*A).Validate
// from (*B).Validate when both exist. Short names (e.g. "Validate") fall back
// to case-insensitive substring matching against CalleeRaw, preserving the
// fuzzy UX users rely on for casual queries.
func Callers(g *graph.Graph, name string, includeTests bool, exactMatch bool) []Result {
	nl := strings.ToLower(name)
	fqQuery := isFullyQualifiedID(name)
	callerSymbols := make(map[string]graph.SymbolNode)
	for _, s := range g.Symbols {
		callerSymbols[s.ID] = s
	}

	targetSymbols := FindSymbols(g, name)
	matchesCallee := func(c graph.CallEdge) bool {
		if fqQuery && c.CalleeSymbolID == name {
			return true
		}
		for _, ts := range targetSymbols {
			if c.CalleeSymbolID == ts.ID {
				return true
			}
		}
		if strings.Contains(name, ".") {
			replaced := strings.ReplaceAll(nl, ".", "::")
			if strings.Contains(strings.ToLower(c.CalleeRaw), replaced) {
				return true
			}
		}
		if exactMatch {
			return strings.ToLower(c.CalleeRaw) == nl
		}
		return strings.Contains(strings.ToLower(c.CalleeRaw), nl)
	}

	// Collect all matching call edges (one per unique call site).
	type siteKey struct {
		id, callFile string
		callLine     int
	}
	seen := make(map[siteKey]bool)
	var results []Result
	for _, c := range g.Calls {
		if !includeTests && isTestFile(c.File) {
			continue
		}
		if matchesCallee(c) {
			k := siteKey{c.CallerSymbolID, c.File, c.Line}
			if seen[k] {
				continue
			}
			seen[k] = true
			if !isSafePathSegment(c.File) || strings.Contains(c.File, "..") || strings.Contains(c.File, "\\") {
				continue
			}
			sym, ok := callerSymbols[c.CallerSymbolID]
			file, line := c.File, 0
			if ok {
				file, line = sym.File, sym.Line
			}

			snippet := ""
			absPath := filepath.Join(g.Root, c.File)
			if data, err := os.ReadFile(absPath); err == nil {
				lines := strings.Split(string(data), "\n")
				if c.Line > 0 && c.Line <= len(lines) {
					snippet = strings.TrimSpace(lines[c.Line-1])
				}
			}

			detail := fmt.Sprintf("calls %s", c.CalleeRaw)
			if snippet != "" {
				detail += fmt.Sprintf("  ->  `%s`", snippet)
			}

			results = append(results, Result{
				Kind:         "caller",
				Name:         c.CallerName,
				File:         file,
				Line:         line,
				Detail:       detail,
				CallSiteFile: c.File,
				CallSiteLine: c.Line,
			})
		}
	}
	sortResults(results)
	return results
}

// Callees returns call expressions found inside the given function/method name.
//
// When name is a fully-qualified symbol ID (contains "::"), the caller seed
// is matched exactly against SymbolNode.ID — disambiguates same-named
// functions/methods across types or packages. Short names fall back to
// fuzzy substring matching (preserves the casual-query UX).
func Callees(g *graph.Graph, name string, includeTests bool, exactMatch bool) []Result {
	nl := strings.ToLower(name)
	fqQuery := isFullyQualifiedID(name)
	matchedIDs := make(map[string]bool)
	for _, s := range g.Symbols {
		if fqQuery && s.ID == name {
			matchedIDs[s.ID] = true
			continue
		}
		sname := strings.ToLower(s.Name)
		full := strings.ToLower(fmt.Sprintf("(%s).%s", s.Receiver, s.Name))
		if exactMatch {
			// Exact: only seed when the name matches precisely.
			if sname == nl || strings.ToLower(s.ID) == nl {
				matchedIDs[s.ID] = true
			}
		} else if MatchSymbol(s, name) || sname == nl || strings.Contains(full, nl) || strings.Contains(sname, nl) {
			matchedIDs[s.ID] = true
		}
	}

	var results []Result
	for _, c := range g.Calls {
		if !includeTests && isTestFile(c.File) {
			continue
		}
		if matchedIDs[c.CallerSymbolID] {
			if !isSafePathSegment(c.File) || strings.Contains(c.File, "..") || strings.Contains(c.File, "\\") {
				continue
			}
			snippet := ""
			absPath := filepath.Join(g.Root, c.File)
			if data, err := os.ReadFile(absPath); err == nil {
				lines := strings.Split(string(data), "\n")
				if c.Line > 0 && c.Line <= len(lines) {
					snippet = strings.TrimSpace(lines[c.Line-1])
				}
			}

			detail := fmt.Sprintf("called by %s", c.CallerName)
			if snippet != "" {
				detail += fmt.Sprintf("  ->  `%s`", snippet)
			}

			results = append(results, Result{
				Kind:         "callee",
				Name:         c.CalleeRaw,
				File:         c.File,
				Line:         c.Line,
				Detail:       detail,
				CallSiteFile: c.File,
				CallSiteLine: c.Line,
			})
		}
	}
	sortResults(results)
	return results
}

// CallersDepth traverses the caller graph up to depth hops above name.
// depth=1 is equivalent to Callers. Results carry a "depth N" prefix in Detail
// so callers can group output by level. maxDepth is clamped to [1, 10].
//
// Frontier tracking is done by symbol identity (both full ID and normalised
// short name) rather than name alone. When precise/CHA edges carry
// CalleeSymbolID, the BFS uses exact-ID matching to step backwards from
// (*A).Validate without leaking into (*B).Validate's caller tree (Bug 6).
// Name-keyed entries are kept in the frontier as a fallback for legacy
// edges that lack CalleeSymbolID.
func CallersDepth(g *graph.Graph, name string, maxDepth int, includeTests bool, exactMatch bool) []Result {
	if maxDepth <= 1 {
		return Callers(g, name, includeTests, exactMatch)
	}
	if maxDepth > 10 {
		maxDepth = 10
	}

	type edge struct {
		callerID       string
		callerName     string
		file           string
		line           int
		calleeNameLow  string
		calleeSymbolID string
	}
	var allEdges []edge
	for _, c := range g.Calls {
		if !includeTests && isTestFile(c.File) {
			continue
		}
		allEdges = append(allEdges, edge{
			c.CallerSymbolID,
			c.CallerName,
			c.File,
			c.Line,
			strings.ToLower(c.CalleeRaw),
			c.CalleeSymbolID,
		})
	}

	symByID := make(map[string]graph.SymbolNode)
	for _, s := range g.Symbols {
		symByID[s.ID] = s
	}

	// Frontier is a multi-key set: full SymbolNode.ID entries match exact
	// CalleeSymbolID hits; lowercase short-name entries match CalleeRaw
	// substring as fallback. Seeding both forms so an FQ query
	// ("pkg::(*S).Validate") seeds the ID, and a short query ("Validate")
	// seeds the name.
	frontier := make(map[string]bool)
	nl := strings.ToLower(name)
	frontier[nl] = true
	if isFullyQualifiedID(name) {
		frontier[name] = true
	}
	seen := make(map[string]bool) // seen caller symbol IDs across all depths
	var results []Result

	for depth := 1; depth <= maxDepth; depth++ {
		nextFrontier := make(map[string]bool)
		for _, e := range allEdges {
			// Match priority: exact ID > short-name substring.
			matched := false
			if e.calleeSymbolID != "" && frontier[e.calleeSymbolID] {
				matched = true
			} else if frontier[e.calleeNameLow] {
				matched = true
			}
			if !matched {
				continue
			}
			if seen[e.callerID] {
				continue
			}
			seen[e.callerID] = true
			sym, ok := symByID[e.callerID]
			file, line := e.file, e.line
			if ok {
				file, line = sym.File, sym.Line
			}
			results = append(results, Result{
				Kind:         "caller",
				Name:         e.callerName,
				File:         file,
				Line:         line,
				Detail:       fmt.Sprintf("depth %d — calls %s", depth, e.calleeNameLow),
				CallSiteFile: e.file,
				CallSiteLine: e.line,
				Score:        10 - depth,
			})
			// Push BOTH forms of the caller into the next frontier so the
			// next hop matches edges keyed by either CalleeSymbolID (exact)
			// or CalleeRaw (name). Prefer the full ID for exactness; the
			// short name keeps the fuzzy fallback intact.
			if e.callerID != "" {
				nextFrontier[e.callerID] = true
			}
			nextFrontier[strings.ToLower(e.callerName)] = true
		}
		if len(nextFrontier) == 0 {
			break
		}
		frontier = nextFrontier
	}

	sortResults(results)
	return results
}

// CalleesDepth traverses the callee graph up to depth hops below name.
// depth=1 is equivalent to Callees. maxDepth is clamped to [1, 10].
//
// When the precise/CHA pass has populated CalleeSymbolID on an edge, the
// next frontier uses that ID directly — exact identity, no name conflation.
// For legacy edges without CalleeSymbolID, the BFS falls back to the
// previous behaviour: resolve callee name to all SymbolNode.ID values that
// share that short name. The fallback is intentionally over-approximating
// (false positives over false negatives) for unresolved dynamic dispatch.
func CalleesDepth(g *graph.Graph, name string, maxDepth int, includeTests bool) []Result {
	if maxDepth <= 1 {
		return Callees(g, name, includeTests, false)
	}
	if maxDepth > 10 {
		maxDepth = 10
	}

	symByID := make(map[string]graph.SymbolNode)
	// Multiple symbols can share the same short name (e.g., two unrelated
	// types both have a Validate method). Tracking by lowercase name was
	// last-write-wins, silently dropping all but one when resolving a
	// callee — the BFS then expanded into the wrong receiver's callees.
	// Store every matching ID; expansion below uses all of them so the
	// over-approximation goes in the safe direction (show what *could*
	// be reached) instead of the wrong direction (show one arbitrary path).
	symByName := make(map[string][]string)
	for _, s := range g.Symbols {
		symByID[s.ID] = s
		key := strings.ToLower(s.Name)
		symByName[key] = append(symByName[key], s.ID)
	}

	// Find the seed symbol IDs for name. FQ-ID queries match exactly;
	// short names use the fuzzy substring path (existing UX).
	nl := strings.ToLower(name)
	fqQuery := isFullyQualifiedID(name)
	seedIDs := make(map[string]bool)
	for _, s := range g.Symbols {
		if fqQuery && s.ID == name {
			seedIDs[s.ID] = true
			continue
		}
		sname := strings.ToLower(s.Name)
		full := strings.ToLower(fmt.Sprintf("(%s).%s", s.Receiver, s.Name))
		if sname == nl || strings.Contains(full, nl) || strings.Contains(sname, nl) {
			seedIDs[s.ID] = true
		}
	}

	frontier := make(map[string]bool) // current caller symbol IDs to expand
	for id := range seedIDs {
		frontier[id] = true
	}

	seen := make(map[string]bool) // seen callee raw names
	var results []Result

	for depth := 1; depth <= maxDepth; depth++ {
		nextFrontier := make(map[string]bool)
		for _, c := range g.Calls {
			if !includeTests && isTestFile(c.File) {
				continue
			}
			if !frontier[c.CallerSymbolID] {
				continue
			}
			calleeKey := strings.ToLower(c.CalleeRaw) + "|" + c.File + fmt.Sprintf("|%d", c.Line)
			if seen[calleeKey] {
				continue
			}
			seen[calleeKey] = true
			results = append(results, Result{
				Kind:         "callee",
				Name:         c.CalleeRaw,
				File:         c.File,
				Line:         c.Line,
				Detail:       fmt.Sprintf("depth %d — called by %s", depth, c.CallerName),
				CallSiteFile: c.File,
				CallSiteLine: c.Line,
				Score:        10 - depth,
			})
			// Resolve callee to symbol IDs for the next frontier.
			// Prefer the precise CalleeSymbolID (exact identity, no
			// conflation). Fall back to name-resolution for legacy edges:
			// same-named symbols across different receivers/packages all
			// expand — see the symByName comment above.
			if c.CalleeSymbolID != "" {
				if _, isKnown := symByID[c.CalleeSymbolID]; isKnown {
					nextFrontier[c.CalleeSymbolID] = true
					continue
				}
				// CalleeSymbolID points at a symbol we don't have in the
				// table (stdlib, third-party). Treat as a leaf — no
				// expansion needed; the edge itself is already in results.
				continue
			}
			calleeLower := strings.ToLower(c.CalleeRaw)
			parts := strings.Split(calleeLower, ".")
			shortName := parts[len(parts)-1]
			if ids, ok := symByName[calleeLower]; ok {
				for _, id := range ids {
					nextFrontier[id] = true
				}
			} else if ids, ok := symByName[shortName]; ok {
				for _, id := range ids {
					nextFrontier[id] = true
				}
			}
		}
		if len(nextFrontier) == 0 {
			break
		}
		frontier = nextFrontier
	}

	sortResults(results)
	return results
}

func sortResults(results []Result) {
	for i := 1; i < len(results); i++ {
		for j := i; j > 0; j-- {
			a, b := results[j-1], results[j]
			swap := a.Score < b.Score || (a.Score == b.Score && a.Name > b.Name)
			if swap {
				results[j-1], results[j] = results[j], results[j-1]
			} else {
				break
			}
		}
	}
}

// Focus returns all files, symbols, imports, and calls associated with a specific package.
func Focus(g *graph.Graph, pkgName string) []Result {
	nl := strings.ToLower(pkgName)
	var results []Result

	// Find the package(s)
	var targetPkgs []graph.PackageNode
	for _, pkg := range g.Packages {
		if strings.ToLower(pkg.Name) == nl || strings.ToLower(pkg.Dir) == nl {
			targetPkgs = append(targetPkgs, pkg)
			results = append(results, Result{Kind: "package", Name: pkg.Name, File: pkg.Dir, Score: 10})
		}
	}

	if len(targetPkgs) == 0 {
		return results
	}

	// Make a set of files belonging to this package
	pkgFiles := make(map[string]bool)
	for _, pkg := range targetPkgs {
		for _, f := range pkg.Files {
			pkgFiles[f] = true
		}
	}

	// Add files and symbols
	for _, f := range g.Files {
		if pkgFiles[f.Path] {
			results = append(results, Result{Kind: "file", Name: f.Path, File: f.Path, Score: 9})
		}
	}

	for _, s := range g.Symbols {
		if pkgFiles[s.File] {
			name := s.Name
			if s.Receiver != "" {
				name = fmt.Sprintf("(%s).%s", s.Receiver, s.Name)
			}
			results = append(results, Result{Kind: string(s.Kind), Name: name, File: s.File, Line: s.Line, Score: 8})
		}
	}

	// Add things this package imports
	for _, imp := range g.Imports {
		if pkgFiles[imp.FromFile] {
			results = append(results, Result{Kind: "imports", Name: imp.ImportPath, File: imp.FromFile, Detail: "dependency", Score: 7})
		}
	}

	// Collect outgoing calls (callees) — deduplicated by name, filtered to real call expressions.
	// A real call expression either contains a dot (pkg.Func or method call) or starts with an
	// uppercase letter (exported symbol) or is a known Go builtin. Raw expressions that are pure
	// lowercase identifiers without a dot are almost always local variable names appearing as
	// arguments, not actual callee targets — skip them to avoid noise.
	goBuiltins := map[string]bool{
		"append": true, "len": true, "cap": true, "make": true, "new": true,
		"delete": true, "copy": true, "close": true, "panic": true, "recover": true,
		"print": true, "println": true, "error": true, "string": true,
	}
	isLikelyCallee := func(raw string) bool {
		if raw == "" {
			return false
		}
		if strings.Contains(raw, ".") {
			return true // pkg.Func or receiver.Method
		}
		if goBuiltins[raw] {
			return true
		}
		// Unexported single-word names without a dot are almost always local variables.
		if raw[0] >= 'a' && raw[0] <= 'z' {
			return false
		}
		return true // Uppercase = exported function
	}
	type calleeKey struct{ name, caller string }
	calleeCounts := make(map[string]int)   // callee name → total call count
	calleeSites := make(map[string]string) // callee name → representative caller
	callerCounts := make(map[calleeKey]bool)
	for _, c := range g.Calls {
		if pkgFiles[c.File] {
			if !isLikelyCallee(c.CalleeRaw) {
				continue
			}
			calleeCounts[c.CalleeRaw]++
			calleeSites[c.CalleeRaw] = c.CallerName
		} else {
			for _, pkg := range targetPkgs {
				prefix := pkg.Name + "."
				if strings.HasPrefix(c.CalleeRaw, prefix) {
					callerCounts[calleeKey{c.CallerName, c.File}] = true
					break
				}
			}
		}
	}
	for name, count := range calleeCounts {
		detail := "called by " + calleeSites[name]
		if count > 1 {
			detail = fmt.Sprintf("%d calls, e.g. by %s", count, calleeSites[name])
		}
		results = append(results, Result{Kind: "callee", Name: name, Detail: detail, Score: 6})
	}
	for key := range callerCounts {
		results = append(results, Result{Kind: "caller", Name: key.name, File: key.caller, Score: 5})
	}

	sortResults(results)
	return results
}

// Implementers finds all structs that implement the given interface.
func Implementers(g *graph.Graph, interfaceName string) []Result {
	nl := strings.ToLower(interfaceName)
	var iface *graph.SymbolNode
	var results []Result

	for i, s := range g.Symbols {
		if s.Kind == graph.KindInterface && MatchSymbol(s, interfaceName) {
			iface = &g.Symbols[i]
			break
		}
	}

	if iface == nil || len(iface.InterfaceMethods) == 0 {
		return results
	}

	// 1. Precise Fast-Path (if --precise was used)
	if len(g.Implements) > 0 {
		for _, edge := range g.Implements {
			if strings.ToLower(edge.Interface) == nl {
				// Find the concrete symbol
				for _, s := range g.Symbols {
					if s.Kind == graph.KindStruct && s.Name == edge.Concrete {
						results = append(results, Result{
							Kind:   "struct",
							Name:   s.Name,
							File:   s.File,
							Line:   s.Line,
							Detail: "implements " + iface.Name + " (type-checked)",
							Score:  10,
						})
					}
				}
			}
		}
		if len(results) > 0 {
			sortResults(results)
			return results
		}
	}

	// 2. AST Heuristic Fallback (duck-typing)
	var structs []graph.SymbolNode
	for _, s := range g.Symbols {
		if s.Kind == graph.KindStruct {
			structs = append(structs, s)
		}
	}

	methodsByReceiver := make(map[string][]graph.SymbolNode)
	for _, s := range g.Symbols {
		if s.Kind == graph.KindMethod && s.Receiver != "" {
			recv := strings.TrimPrefix(s.Receiver, "*")
			methodsByReceiver[recv] = append(methodsByReceiver[recv], s)
		}
	}

	for _, str := range structs {
		methods := methodsByReceiver[str.Name]
		if len(methods) < len(iface.InterfaceMethods) {
			continue
		}

		implemented := 0
		for reqName, reqSig := range iface.InterfaceMethods {
			for _, m := range methods {
				if m.Name == reqName && m.MethodSignature == reqSig {
					implemented++
					break
				}
			}
		}

		if implemented == len(iface.InterfaceMethods) {
			results = append(results, Result{
				Kind:   "struct",
				Name:   str.Name,
				File:   str.File,
				Line:   str.Line,
				Detail: "implements " + iface.Name,
				Score:  10,
			})
		}
	}

	sortResults(results)
	return results
}

// MatchSymbol checks if a SymbolNode matches a user query string (case-insensitively).
// It supports bare names ("Graph"), fully-qualified IDs ("pkg::Graph"), and package/receiver-qualified
// names ("graph.Graph", "(*graph.Graph).SomeMethod", "graph.Graph.SomeMethod", "(*Graph).SomeMethod").
func MatchSymbol(s graph.SymbolNode, query string) bool {
	ql := strings.ToLower(query)

	// 1. Exact ID or Name match
	if strings.ToLower(s.ID) == ql || strings.ToLower(s.Name) == ql {
		return true
	}

	// 2. Exact Receiver+Method name matches
	if s.Receiver != "" {
		recName := strings.ToLower(fmt.Sprintf("%s.%s", strings.TrimPrefix(strings.TrimPrefix(s.Receiver, "*"), "("), s.Name))
		fullRecName := strings.ToLower(fmt.Sprintf("(%s).%s", s.Receiver, s.Name))
		if recName == ql || fullRecName == ql {
			return true
		}
	}

	// 3. Substring match for fully qualified ID suffix (e.g. replacing "." with "::" for "pkg.Symbol")
	if strings.Contains(query, ".") {
		// Try converting "pkg.Symbol" to "pkg::Symbol"
		replaced := strings.ReplaceAll(ql, ".", "::")
		if strings.Contains(strings.ToLower(s.ID), replaced) {
			return true
		}

		// Also try structured dot-separated matching
		norm := strings.ReplaceAll(ql, "(", "")
		norm = strings.ReplaceAll(norm, ")", "")
		norm = strings.ReplaceAll(norm, "*", "")
		parts := strings.Split(norm, ".")

		if len(parts) == 2 {
			// Try package + name match
			if strings.ToLower(s.PackageName) == parts[0] && strings.ToLower(s.Name) == parts[1] {
				return true
			}
			// Try receiver + method match
			if s.Receiver != "" {
				rec := strings.ReplaceAll(strings.ToLower(s.Receiver), "*", "")
				if rec == parts[0] && strings.ToLower(s.Name) == parts[1] {
					return true
				}
			}
		} else if len(parts) == 3 {
			// Try package + receiver + method match
			if s.Receiver != "" {
				rec := strings.ReplaceAll(strings.ToLower(s.Receiver), "*", "")
				if strings.ToLower(s.PackageName) == parts[0] && rec == parts[1] && strings.ToLower(s.Name) == parts[2] {
					return true
				}
			}
		}
	}

	return false
}

// FindSymbols returns all symbols matching the given query term.
func FindSymbols(g *graph.Graph, term string) []graph.SymbolNode {
	var matches []graph.SymbolNode
	for i := range g.Symbols {
		if MatchSymbol(g.Symbols[i], term) {
			matches = append(matches, g.Symbols[i])
		}
	}
	return matches
}

func matchTestTarget(target, testFunc, term string) bool {
	if term == "" {
		return true
	}
	tl := strings.ToLower(term)
	targetLower := strings.ToLower(target)
	testFuncLower := strings.ToLower(testFunc)

	if strings.Contains(targetLower, tl) || strings.Contains(testFuncLower, tl) {
		return true
	}

	// Support dot-separated package prefix
	if strings.Contains(term, ".") {
		replaced := strings.ReplaceAll(tl, ".", "::")
		if strings.Contains(targetLower, replaced) || strings.Contains(testFuncLower, replaced) {
			return true
		}

		// Also support matching on normalized parts
		norm := strings.ReplaceAll(tl, "(", "")
		norm = strings.ReplaceAll(norm, ")", "")
		norm = strings.ReplaceAll(norm, "*", "")
		parts := strings.Split(norm, ".")

		if len(parts) == 2 {
			targetSuffix := strings.ToLower(target)
			if idx := strings.Index(targetSuffix, "::"); idx >= 0 {
				pkgPart := targetSuffix[:idx]
				symPart := targetSuffix[idx+2:]
				if (strings.HasSuffix(pkgPart, parts[0]) || strings.Contains(pkgPart, "/"+parts[0])) && strings.Contains(symPart, parts[1]) {
					return true
				}
			}
		}
	}
	return false
}

// Source extracts the exact source code lines for a given symbol.
func Source(g *graph.Graph, rootDir, symbolName string) (string, error) {
	targets := FindSymbols(g, symbolName)

	if len(targets) == 0 {
		return "", fmt.Errorf("symbol '%s' not found", symbolName)
	}

	var results []string
	limit := 5
	for i, target := range targets {
		if i >= limit {
			results = append(results, fmt.Sprintf("// WARNING: %d other implementations of '%s' were found but omitted to save tokens. Please be more specific (e.g., Receiver.Method).", len(targets)-limit, symbolName))
			break
		}

		if !isSafePathSegment(target.File) || strings.Contains(target.File, "..") || strings.Contains(target.File, "\\") {
			results = append(results, fmt.Sprintf("// WARNING: skipping unsafe path in file %s", target.File))
			continue
		}

		absPath := filepath.Join(rootDir, target.File)
		data, err := os.ReadFile(absPath)
		if err != nil {
			results = append(results, fmt.Sprintf("// Error reading file %s: %v", target.File, err))
			continue
		}

		lines := strings.Split(string(data), "\n")
		start := target.Line - 1
		end := target.EndLine

		if start < 0 {
			start = 0
		}
		if end > len(lines) {
			end = len(lines)
		}
		if start >= end {
			results = append(results, fmt.Sprintf("// Error: invalid line range %d to %d for %s", target.Line, target.EndLine, target.ID))
			continue
		}

		extracted := strings.Join(lines[start:end], "\n")
		results = append(results, fmt.Sprintf("// %s (%s:%d-%d)\n%s", target.ID, target.File, target.Line, target.EndLine, extracted))
	}

	return strings.Join(results, "\n\n---\n\n"), nil
}

// Orphans finds functions and methods that are never explicitly called in the codebase.
func Orphans(g *graph.Graph) []Result {
	var results []Result
	calledNames := make(map[string]bool)

	for _, c := range g.Calls {
		parts := strings.Split(c.CalleeRaw, ".")
		calledNames[parts[len(parts)-1]] = true
		calledNames[c.CalleeRaw] = true
	}

	for _, s := range g.Symbols {
		if s.Kind != graph.KindFunction && s.Kind != graph.KindMethod {
			continue
		}
		if s.Name == "main" || s.Name == "init" {
			continue
		}

		// Check if the name is explicitly called
		if !calledNames[s.Name] {
			results = append(results, Result{
				Kind:   string(s.Kind),
				Name:   s.Name,
				File:   s.File,
				Line:   s.Line,
				Detail: "0 explicit calls found",
				Score:  5,
			})
		}
	}

	sortResults(results)
	return results
}

// Fields extracts all fields for a given struct.
func Fields(g *graph.Graph, structName string) []Result {
	var results []Result

	targets := FindSymbols(g, structName)
	for _, s := range targets {
		if s.Kind == graph.KindStruct {
			for _, f := range s.StructFields {
				detail := f.Type
				if f.Tag != "" {
					detail += " " + f.Tag
				}
				results = append(results, Result{
					Kind:   "field",
					Name:   f.Name,
					File:   s.File,
					Line:   s.Line,
					Detail: detail,
					Score:  10,
				})
			}
			break
		}
	}

	return results
}

// Impact traverses the call graph backwards to find all symbols that eventually call the target symbol.
func Impact(g *graph.Graph, name string, includeTests bool) []Result {
	return ImpactMultiple(g, []string{name}, "downstream impact of "+name, includeTests)
}

// ImpactMultiple calculates blast radius for multiple root symbols simultaneously.
//
// The BFS frontier is a mixed set of full SymbolNode.ID values (exact-identity
// match against CalleeSymbolID — no name conflation) and lowercase short
// names (substring match against CalleeRaw — legacy fallback for edges
// without CalleeSymbolID). Each newly-discovered caller is queued by its
// own SymbolNode.ID so its uplink search is performed once per symbol, even
// when many symbols share the same short name.
func ImpactMultiple(g *graph.Graph, names []string, reason string, includeTests bool) []Result {
	callerSymbols := make(map[string]graph.SymbolNode)
	for _, s := range g.Symbols {
		callerSymbols[s.ID] = s
	}

	// idFrontier holds SymbolNode.ID values to match exactly against
	// CalleeSymbolID. termFrontier holds lowercase substrings to match
	// against CalleeRaw. Both are checked on every edge — order doesn't
	// matter because we dedup on caller symbol identity.
	type frontierItem struct {
		id   string // empty for short-name items
		term string // lowercase substring for short-name items
	}
	var queue []frontierItem
	visitedIDs := make(map[string]bool)     // FQ-ID terms we've already searched
	visitedTerms := make(map[string]bool)   // short-name terms we've already searched
	visitedSymbols := make(map[string]bool) // caller symbols we've already requeued

	for _, name := range names {
		if isFullyQualifiedID(name) {
			if !visitedIDs[name] {
				visitedIDs[name] = true
				queue = append(queue, frontierItem{id: name})
			}
		} else {
			nl := strings.ToLower(name)
			if !visitedTerms[nl] {
				visitedTerms[nl] = true
				queue = append(queue, frontierItem{term: nl})
			}
		}
	}

	var results []Result
	seenIDs := make(map[string]bool)

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		for _, c := range g.Calls {
			if !includeTests && isTestFile(c.File) {
				continue
			}
			matched := false
			if item.id != "" {
				if c.CalleeSymbolID == item.id {
					matched = true
				}
			} else if strings.Contains(strings.ToLower(c.CalleeRaw), item.term) {
				matched = true
			}
			if !matched {
				continue
			}
			callerID := c.CallerSymbolID
			if seenIDs[callerID] {
				continue
			}
			seenIDs[callerID] = true

			sym, ok := callerSymbols[callerID]
			if !ok {
				continue
			}
			results = append(results, Result{
				Kind:   "impact",
				Name:   sym.Name,
				File:   sym.File,
				Line:   sym.Line,
				Detail: reason,
				Score:  8,
			})

			// Enqueue this caller's identity (preferred) AND short name
			// (fallback for callers of THIS symbol that recorded only the
			// name on their edges). Each enqueue is gated by its own visited
			// set so two same-named symbols don't suppress one another.
			if !visitedSymbols[sym.ID] {
				visitedSymbols[sym.ID] = true
				if !visitedIDs[sym.ID] {
					visitedIDs[sym.ID] = true
					queue = append(queue, frontierItem{id: sym.ID})
				}
				nextTerm := strings.ToLower(sym.Name)
				if !visitedTerms[nextTerm] {
					visitedTerms[nextTerm] = true
					queue = append(queue, frontierItem{term: nextTerm})
				}
			}
		}
	}

	sortResults(results)
	return results
}

// Routes extracts all HTTP REST API routes found in the codebase.
func Routes(g *graph.Graph) []Result {
	var results []Result
	for _, r := range g.Routes {
		results = append(results, Result{
			Kind:   "route",
			Name:   fmt.Sprintf("%s %s", r.Method, r.Path),
			File:   r.File,
			Line:   r.Line,
			Detail: "handled by " + r.Handler,
			Score:  10,
		})
	}
	sortResults(results)
	return results
}

// ExternalImports tracks which files import a specific external package.
func ExternalImports(g *graph.Graph, pkg string) []Result {
	var results []Result
	nl := strings.ToLower(pkg)
	for _, i := range g.Imports {
		if strings.Contains(strings.ToLower(i.ImportPath), nl) {
			results = append(results, Result{
				Kind:   "import",
				Name:   i.ImportPath,
				File:   i.FromFile,
				Line:   0,
				Detail: "imported by " + i.FromPackage,
				Score:  10,
			})
		}
	}
	sortResults(results)
	return results
}

// SQL extracts all database SQL queries found in the codebase.
func SQL(g *graph.Graph, term string) []Result {
	var results []Result
	nl := strings.ToLower(term)
	for _, sql := range g.SQLs {
		if term == "" || strings.Contains(strings.ToLower(sql.Query), nl) {
			results = append(results, Result{
				Kind:   "sql",
				Name:   sql.Query,
				File:   sql.File,
				Line:   sql.Line,
				Detail: "executed by " + sql.Function,
				Score:  10,
			})
		}
	}
	sortResults(results)
	return results
}

// Errors extracts all custom error messages and panics.
// Set includeTests to false to exclude errors from test files.
func Errors(g *graph.Graph, term string, includeTests bool) []Result {
	var results []Result
	nl := strings.ToLower(term)
	for _, err := range g.Errors {
		if !includeTests && isTestFile(err.File) {
			continue
		}
		if term == "" || strings.Contains(strings.ToLower(err.Message), nl) {
			results = append(results, Result{
				Kind:   "error",
				Name:   err.Message,
				File:   err.File,
				Line:   err.Line,
				Detail: "thrown by " + err.Function,
				Score:  10,
			})
		}
	}
	sortResults(results)
	return results
}

func isTestFile(path string) bool {
	if strings.HasSuffix(path, "_test.go") {
		return true
	}
	fl := strings.ToLower(path)
	if strings.Contains(fl, "mock") || strings.Contains(fl, "fake") {
		return true
	}
	parts := strings.Split(path, "/")
	for _, p := range parts {
		if p == "testdata" || p == "test" || p == "tests" {
			return true
		}
	}
	return false
}

// Embeds shows what structs embed the target struct.
func Embeds(g *graph.Graph, structName string) []Result {
	var results []Result
	for _, s := range g.Symbols {
		for _, e := range s.EmbeddedStructs {
			if e == structName || e == "*"+structName {
				results = append(results, Result{
					Kind:   "embed",
					Name:   s.Name,
					File:   s.File,
					Line:   s.Line,
					Detail: "embeds " + structName,
					Score:  10,
				})
			}
		}
	}
	sortResults(results)
	return results
}

// Public shows only the exported symbols of a package.
func Public(g *graph.Graph, pkgQuery string) []Result {
	// pkgQuery can be any of:
	//   - basename:       "service"
	//   - relative path:  "./internal/service"  or  "internal/service"
	//   - import path:    "identuum.ai/internal/service"
	// Previously only the basename matched; the other three forms returned
	// "No exported symbols found" even when the package was present in the
	// graph. Normalise the query and match against every form a symbol can
	// reasonably be identified by.
	q := strings.TrimPrefix(pkgQuery, "./")
	q = strings.TrimSuffix(q, "/")
	qLower := strings.ToLower(q)
	qBasename := qLower
	if i := strings.LastIndex(qLower, "/"); i >= 0 {
		qBasename = qLower[i+1:]
	}

	matchesPackage := func(s graph.SymbolNode) bool {
		pkgLower := strings.ToLower(s.PackageName)
		if pkgLower == qLower {
			return true // basename match: query was "service" and pkg name is "service"
		}
		if pkgLower != qBasename {
			return false
		}
		// Query is a path of some kind ("internal/service",
		// "identuum.ai/internal/service", "./internal/service"). The
		// package's short name matches the query's last segment; confirm
		// via the file's containing directory so we don't conflate two
		// same-named packages in different directories.
		//
		// File paths in graph.json are repo-relative (e.g.
		// "internal/service/admin.go"). The query may carry a module
		// prefix (e.g. "identuum.ai/internal/...") or not. Treating each
		// as a suffix of the other handles both cases:
		//   - query "internal/service" vs fileDir "internal/service"      → equal
		//   - query "identuum.ai/internal/service" vs "internal/service"  → query suffix
		//   - query "service" already handled above (basename match)
		fileDir := strings.ToLower(strings.ReplaceAll(s.File, "\\", "/"))
		if i := strings.LastIndex(fileDir, "/"); i >= 0 {
			fileDir = fileDir[:i]
		}
		if fileDir == qLower {
			return true
		}
		if strings.HasSuffix(qLower, "/"+fileDir) || strings.HasSuffix(fileDir, "/"+qLower) {
			return true
		}
		return false
	}

	var results []Result
	for _, s := range g.Symbols {
		if !matchesPackage(s) {
			continue
		}
		if len(s.Name) == 0 || s.Name[0] < 'A' || s.Name[0] > 'Z' {
			continue
		}
		results = append(results, Result{
			Kind:   string(s.Kind),
			Name:   s.Name,
			File:   s.File,
			Line:   s.Line,
			Detail: s.Signature,
			Score:  10,
		})
	}
	sortResults(results)
	return results
}

// Envs returns all environment variable reads in the graph,
// optionally filtered by a keyword term.
func Envs(g *graph.Graph, term string) []Result {
	nl := strings.ToLower(term)
	var results []Result
	for _, ev := range g.EnvReads {
		if term == "" || strings.Contains(strings.ToLower(ev.Key), nl) || strings.Contains(strings.ToLower(ev.Accessor), nl) {
			detail := fmt.Sprintf("read via %s", ev.Accessor)
			if ev.Function != "" {
				detail += " in " + ev.Function
			}
			results = append(results, Result{
				Kind:   "env",
				Name:   ev.Key,
				File:   ev.File,
				Line:   ev.Line,
				Detail: detail,
				Score:  10,
			})
		}
	}
	sortResults(results)
	return results
}

// HTTPCalls returns all detected HTTP client calls in the graph,
// optionally filtered by a keyword term (matches method, URL, or function name).
func HTTPCalls(g *graph.Graph, term string) []Result {
	nl := strings.ToLower(term)
	var results []Result
	for _, h := range g.HTTPCalls {
		if term == "" ||
			strings.Contains(strings.ToLower(h.Method), nl) ||
			strings.Contains(strings.ToLower(h.URL), nl) ||
			strings.Contains(strings.ToLower(h.FunctionName), nl) {
			detail := fmt.Sprintf("%s %s", h.Method, h.URL)
			if h.HasDynamic {
				detail += " [dynamic]"
			}
			detail += " in " + h.FunctionName
			results = append(results, Result{
				Kind:   "httpcall",
				Name:   fmt.Sprintf("%s %s", h.Method, h.URL),
				File:   h.SourceFile,
				Line:   h.SourceLine,
				Detail: detail,
				Score:  10,
			})
		}
	}
	sortResults(results)
	return results
}

// Interfaces returns all interfaces that the named struct satisfies,
// using duck-typing: a struct satisfies an interface if it has all methods
// listed in InterfaceMethods (by name). Only interfaces defined in the graph
// are checked.
func Interfaces(g *graph.Graph, structName string) []Result {
	var results []Result

	// 1. Precise Fast-Path (if --precise was used)
	if len(g.Implements) > 0 {
		for _, edge := range g.Implements {
			if strings.EqualFold(edge.Concrete, structName) {
				// Find the interface symbol
				for _, s := range g.Symbols {
					if s.Kind == graph.KindInterface && s.Name == edge.Interface {
						results = append(results, Result{
							Kind:   "interface",
							Name:   s.Name,
							File:   s.File,
							Line:   s.Line,
							Detail: fmt.Sprintf("%s satisfies %s (type-checked)", structName, s.Name),
							Score:  10,
						})
					}
				}
			}
		}
		if len(results) > 0 {
			sortResults(results)
			return results
		}
	}

	// 2. AST Heuristic Fallback (duck-typing)
	// Build a map of method names owned by the struct (via method receivers).
	structMethods := make(map[string]bool)
	for _, s := range g.Symbols {
		if s.Kind != graph.KindMethod {
			continue
		}
		// Receiver can be "Foo" or "*Foo".
		recv := strings.TrimPrefix(s.Receiver, "*")
		if recv == structName {
			structMethods[s.Name] = true
		}
	}

	for _, iface := range g.Symbols {
		if iface.Kind != graph.KindInterface || len(iface.InterfaceMethods) == 0 {
			continue
		}
		// Check that the struct implements every method on the interface.
		satisfied := true
		for methodName := range iface.InterfaceMethods {
			if !structMethods[methodName] {
				satisfied = false
				break
			}
		}
		if satisfied {
			results = append(results, Result{
				Kind:   "interface",
				Name:   iface.Name,
				File:   iface.File,
				Line:   iface.Line,
				Detail: fmt.Sprintf("%s satisfies %s (%d methods)", structName, iface.Name, len(iface.InterfaceMethods)),
				Score:  10,
			})
		}
	}
	sortResults(results)
	return results
}

// Concurrency returns all concurrency primitives in the graph,
// optionally filtered by a kind keyword (e.g. "goroutine", "mutex").
func Concurrency(g *graph.Graph, term string) []Result {
	nl := strings.ToLower(term)
	var results []Result
	for _, c := range g.Concurrency {
		if term == "" || strings.Contains(strings.ToLower(c.Kind), nl) || strings.Contains(strings.ToLower(c.Function), nl) {
			results = append(results, Result{
				Kind:   "concurrency",
				Name:   c.Kind,
				File:   c.File,
				Line:   c.Line,
				Detail: fmt.Sprintf("%s — %s", c.Function, c.Detail),
				Score:  10,
			})
		}
	}
	sortResults(results)
	return results
}

// Tests returns all test functions that exercise the named symbol.
// Pass an empty term to list all test edges.
func Tests(g *graph.Graph, term string) []Result {
	var results []Result
	seen := make(map[string]bool)
	for _, te := range g.TestEdges {
		if matchTestTarget(te.Target, te.TestFunc, term) {
			key := fmt.Sprintf("%s|%s", te.TestFunc, te.Target)
			if seen[key] {
				continue
			}
			seen[key] = true
			results = append(results, Result{
				Kind:   "test",
				Name:   te.TestFunc,
				File:   te.File,
				Line:   te.Line,
				Detail: fmt.Sprintf("exercises %s", te.Target),
				Score:  10,
			})
		}
	}
	sortResults(results)
	return results
}

// Constructors finds functions whose return signature includes the target struct type,
// indicating they are constructors or factory functions. Matches both pointer and value returns.
func Constructors(g *graph.Graph, structName string) []Result {
	var results []Result
	// Match structName preceded by *, space, or dot, and followed by comma, space, paren, or end of string.
	pattern := `(?:[* \.])` + regexp.QuoteMeta(structName) + `(?:[,) ]|$)`
	re, err := regexp.Compile(pattern)
	if err != nil {
		return results
	}

	// Unqualified pattern for same-package constructors
	var samePkgPat *regexp.Regexp
	var samePkgName string
	if parts := strings.Split(structName, "."); len(parts) == 2 {
		samePkgName = strings.ToLower(parts[0])
		patternSame := `(?:[* ])` + regexp.QuoteMeta(parts[1]) + `(?:[,) ]|$)`
		samePkgPat, _ = regexp.Compile(patternSame)
	}

	for _, s := range g.Symbols {
		if s.Kind != graph.KindFunction && s.Kind != graph.KindMethod {
			continue
		}
		sig := s.Signature
		// Find where parameters end: the last ") " separates params from return type.
		// A void function like "func Foo(g *Graph)" has no ") " (ends with ")").
		idx := strings.LastIndex(sig, ") ")
		if idx == -1 {
			// No return type — skip entirely to avoid false positives on params.
			continue
		}
		returnSig := sig[idx:]

		matched := re.MatchString(returnSig)
		if !matched && samePkgPat != nil && strings.ToLower(s.PackageName) == samePkgName {
			matched = samePkgPat.MatchString(returnSig)
		}

		if matched {
			results = append(results, Result{
				Kind:   "constructor",
				Name:   s.Name,
				File:   s.File,
				Line:   s.Line,
				Detail: "returns " + structName,
				Score:  10,
			})
		}
	}
	sortResults(results)
	return results
}

// Schema finds the Go struct that maps to a specific database table or schema name via struct tags.
func Schema(g *graph.Graph, tableName string) []Result {
	var results []Result
	nl := strings.ToLower(tableName)

	// Pre-compute the search targets to avoid string concatenation inside the loop
	targets := []string{
		`"` + nl + `"`,
		`'` + nl + `'`,
		`:` + nl + `"`,
		`:` + nl + `'`,
		`"` + nl + `,`,
		`'` + nl + `,`,
	}

	for _, s := range g.Symbols {
		if s.Kind == graph.KindStruct {
			for _, f := range s.StructFields {
				t := strings.ToLower(f.Tag)
				matched := false
				for _, target := range targets {
					if strings.Contains(t, target) {
						matched = true
						break
					}
				}
				if matched {
					results = append(results, Result{
						Kind:   "schema",
						Name:   s.Name,
						File:   s.File,
						Line:   s.Line,
						Detail: fmt.Sprintf("field %s mapped to %s", f.Name, f.Tag),
						Score:  10,
					})
					break // only report the struct once
				}
			}
		}
	}
	sortResults(results)
	return results
}

// Globals finds package-level variables, constants, and functions mutating variables.
func Globals(g *graph.Graph, pkgName string) []Result {
	var results []Result
	nl := strings.ToLower(pkgName)
	var globalVars []graph.SymbolNode

	// Find global variables and constants in the given package
	for _, s := range g.Symbols {
		if (s.Kind == graph.KindVar || s.Kind == graph.KindConst) && strings.Contains(strings.ToLower(s.PackageName), nl) {
			if s.Kind == graph.KindVar {
				globalVars = append(globalVars, s)
			}
			results = append(results, Result{
				Kind:   string(s.Kind),
				Name:   s.Name,
				File:   s.File,
				Line:   s.Line,
				Detail: "package-level " + string(s.Kind) + " in " + s.PackageName,
				Score:  10,
			})
		}
	}

	// Find mutators for those variables
	for _, v := range globalVars {
		for _, m := range g.Mutations {
			// This is a heuristic: if an Ident was mutated with the same name, we list it.
			// It might have false positives with local variables shadowing the global.
			if m.Field == v.Name {
				results = append(results, Result{
					Kind:   "mutator",
					Name:   m.Function,
					File:   m.File,
					Line:   m.Line,
					Detail: "assigns to global var " + v.Name,
					Score:  8,
				})
			}
		}
	}

	sortResults(results)
	return results
}

// Mocks finds structs that implement the given interface, filtered to test or mock files.
func Mocks(g *graph.Graph, interfaceName string) []Result {
	implementers := Implementers(g, interfaceName)
	var results []Result
	for _, res := range implementers {
		if isTestFile(res.File) {
			res.Kind = "mock"
			res.Detail = "mock " + res.Detail
			results = append(results, res)
		}
	}
	return results
}

// Fixtures finds test helper types and factory functions in test files for a package.
func Fixtures(g *graph.Graph, pkgName string) []Result {
	var results []Result
	nl := strings.ToLower(pkgName)

	for _, s := range g.Symbols {
		if !isTestFile(s.File) {
			continue
		}
		if !strings.Contains(strings.ToLower(s.PackageName), nl) {
			continue
		}
		if strings.HasPrefix(s.Name, "Test") || strings.HasPrefix(s.Name, "Benchmark") || strings.HasPrefix(s.Name, "Example") {
			continue
		}

		results = append(results, Result{
			Kind:   "fixture",
			Name:   s.Name,
			File:   s.File,
			Line:   s.Line,
			Detail: string(s.Kind) + " in test file",
			Score:  10,
		})
	}

	sortResults(results)
	return results
}

// UntestedResult describes a function that has callers but no test coverage.
type UntestedResult struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	CallerCount int    `json:"caller_count"`
	PackageName string `json:"package"`
}

// Untested returns functions and methods that have at least one non-test caller
// but zero attributed test edges. This is distinct from Orphans (zero callers):
// untested symbols ARE used in production code but have no test coverage.
//
// One full graph sweep — replaces N sequential 'gograph tests <sym>' calls.
func Untested(g *graph.Graph) []UntestedResult {
	// Build set of symbol names/IDs that appear as test targets.
	testedIDs := make(map[string]bool)
	testedNames := make(map[string]bool)
	for _, te := range g.TestEdges {
		testedIDs[te.Target] = true
		// Target may be a fully-qualified ID or a short name — track both.
		if idx := strings.LastIndex(te.Target, "::"); idx >= 0 {
			testedNames[te.Target[idx+2:]] = true
		} else {
			testedNames[te.Target] = true
		}
	}

	// Build caller count per symbol ID (non-test call sites only).
	callerCount := make(map[string]int)
	for _, c := range g.Calls {
		if isTestFile(c.File) {
			continue
		}
		if c.CalleeSymbolID != "" {
			callerCount[c.CalleeSymbolID]++
		} else {
			callerCount[c.CalleeRaw]++
		}
	}

	// Build a name→packageName lookup for display.
	pkgByID := make(map[string]string)
	for _, s := range g.Symbols {
		pkgByID[s.ID] = s.PackageName
	}

	var results []UntestedResult
	for _, s := range g.Symbols {
		if s.Kind != graph.KindFunction && s.Kind != graph.KindMethod {
			continue
		}
		// Skip test files — we only care about production symbols.
		if isTestFile(s.File) {
			continue
		}
		// Skip init/main — convention; always entry points.
		if s.Name == "main" || s.Name == "init" {
			continue
		}

		// Must have at least one non-test caller.
		count := callerCount[s.ID]
		if count == 0 {
			count = callerCount[s.Name]
		}
		if count == 0 {
			continue
		}

		// Must NOT appear in test edges.
		if testedIDs[s.ID] || testedNames[s.Name] {
			continue
		}

		results = append(results, UntestedResult{
			Name:        s.Name,
			Kind:        string(s.Kind),
			File:        s.File,
			Line:        s.Line,
			CallerCount: count,
			PackageName: s.PackageName,
		})
	}

	// Sort: highest caller count first (most-used untested functions are
	// the highest risk), then alphabetically within the same count.
	for i := 1; i < len(results); i++ {
		for j := i; j > 0; j-- {
			a, b := results[j-1], results[j]
			if a.CallerCount < b.CallerCount || (a.CallerCount == b.CallerCount && a.Name > b.Name) {
				results[j-1], results[j] = results[j], results[j-1]
			} else {
				break
			}
		}
	}
	if results == nil {
		results = []UntestedResult{}
	}
	return results
}
