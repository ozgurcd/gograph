package search

import (
	"fmt"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

type PlanResult struct {
	Title      string   `json:"title"`
	ReadFirst  []Result `json:"read_first"`
	Tests      []string `json:"tests"`
	PublicAPI  string   `json:"public_api"` // "yes", "no"
	Routes     []string `json:"routes"`
	Envs       []string `json:"envs"`
	TouchesSQL string   `json:"touches_sql"` // "yes", "no"
}

// ContextForPlanResult resolves a location-bearing ReadFirst row back to its
// canonical symbol before building context. Plan rows intentionally use the
// compact Result shape, so re-querying row.Name alone can conflate unrelated
// same-named symbols from different packages. The file and declaration line
// retain enough identity to recover the stable SymbolNode ID.
//
// The returned string is the canonical ID when resolution succeeds, or the
// original display name when a legacy/incomplete graph has no matching symbol.
func ContextForPlanResult(g *graph.Graph, rootDir string, row Result) (string, *ContextResult) {
	if symbol := symbolForPlanResult(g, row); symbol != nil && symbol.ID != "" {
		if result := Context(g, rootDir, symbol.ID, true); result != nil {
			return symbol.ID, result
		}
	}
	return row.Name, Context(g, rootDir, row.Name, false)
}

func symbolForPlanResult(g *graph.Graph, row Result) *graph.SymbolNode {
	var locationMatches []*graph.SymbolNode
	for i := range g.Symbols {
		symbol := &g.Symbols[i]
		if symbol.File != row.File || (row.Line > 0 && symbol.Line != row.Line) {
			continue
		}
		locationMatches = append(locationMatches, symbol)
	}
	for _, symbol := range locationMatches {
		if matchSymbolExact(*symbol, row.Name) {
			return symbol
		}
	}

	// Older call edges can retain the caller's file but only the call-site
	// line. Fall back to exact display-name matching within that file.
	var fileMatch *graph.SymbolNode
	for i := range g.Symbols {
		symbol := &g.Symbols[i]
		if symbol.File != row.File || !matchSymbolExact(*symbol, row.Name) {
			continue
		}
		if fileMatch != nil {
			return nil
		}
		fileMatch = symbol
	}
	return fileMatch
}

func (r *PlanResult) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Change plan for %s\n\n", r.Title)

	// 1. Read first
	sb.WriteString("1. Read first:\n")
	if len(r.ReadFirst) > 0 {
		limit := len(r.ReadFirst)
		if limit > 15 {
			limit = 15
		}
		for i := 0; i < limit; i++ {
			c := r.ReadFirst[i]
			fmt.Fprintf(&sb, "   - %s:%d %s\n", c.File, c.Line, c.Name)
		}
		if len(r.ReadFirst) > 15 {
			fmt.Fprintf(&sb, "   ... and %d more\n", len(r.ReadFirst)-15)
		}
	} else {
		sb.WriteString("   - (No source or callers found)\n")
	}
	sb.WriteString("\n")

	// 2. Update likely affected tests
	sb.WriteString("2. Update likely affected tests:\n")
	if len(r.Tests) > 0 {
		for _, tf := range r.Tests {
			fmt.Fprintf(&sb, "   - %s\n", tf)
		}
	} else {
		sb.WriteString("   - (No direct tests found)\n")
	}
	sb.WriteString("\n")

	// 3. Risk Profile
	sb.WriteString("3. Risk:\n")
	fmt.Fprintf(&sb, "   - Public API: %s\n", r.PublicAPI)

	// Check Routes
	if len(r.Routes) > 0 {
		if len(r.Routes) > 3 {
			fmt.Fprintf(&sb, "   - Called by HTTP route: %s, and %d more\n", strings.Join(r.Routes[:3], ", "), len(r.Routes)-3)
		} else {
			fmt.Fprintf(&sb, "   - Called by HTTP route: %s\n", strings.Join(r.Routes, ", "))
		}
	} else {
		sb.WriteString("   - Called by HTTP route: no\n")
	}

	// Check Envs
	if len(r.Envs) > 0 {
		if len(r.Envs) > 3 {
			fmt.Fprintf(&sb, "   - Reads env: %s, and %d more\n", strings.Join(r.Envs[:3], ", "), len(r.Envs)-3)
		} else {
			fmt.Fprintf(&sb, "   - Reads env: %s\n", strings.Join(r.Envs, ", "))
		}
	} else {
		sb.WriteString("   - Reads env: no\n")
	}

	fmt.Fprintf(&sb, "   - Touches SQL: %s\n", r.TouchesSQL)

	return sb.String()
}

// Plan generates an operational change plan for one or more symbols.
func Plan(g *graph.Graph, symbolNames []string, title string) *PlanResult {
	res := &PlanResult{
		Title:      title,
		PublicAPI:  "no",
		TouchesSQL: "no",
	}

	readSet := make(map[string]bool)

	for _, symName := range symbolNames {
		// Find symbol
		matches := FindSymbols(g, symName)
		for _, s := range matches {
			line := fmt.Sprintf("%s:%d %s", s.File, s.Line, s.Name)
			if !readSet[line] {
				readSet[line] = true
				res.ReadFirst = append(res.ReadFirst, Result{File: s.File, Line: s.Line, Name: s.Name})
			}
		}
		// Find immediate callers
		for _, s := range matches {
			callers := Callers(g, s.ID, true, false)
			for _, c := range callers {
				line := fmt.Sprintf("%s:%d %s", c.File, c.Line, c.Name)
				if !readSet[line] {
					readSet[line] = true
					res.ReadFirst = append(res.ReadFirst, c)
				}
			}
		}
	}

	testFiles := make(map[string]bool)
	for _, symName := range symbolNames {
		ts := Tests(g, symName)
		for _, t := range ts {
			testFiles[t.File] = true
		}
	}
	for tf := range testFiles {
		res.Tests = append(res.Tests, tf)
	}

	// Check Public API
	for _, symName := range symbolNames {
		if len(symName) > 0 {
			parts := strings.Split(symName, "::")
			short := parts[len(parts)-1]
			parts2 := strings.Split(short, ".")
			name := parts2[len(parts2)-1]
			if len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z' {
				res.PublicAPI = "yes"
				break
			}
		}
	}

	// Check Routes
	routeSet := make(map[string]bool)
	blastRadius := ImpactMultiple(g, symbolNames, "plan", true)
	blastMap := make(map[string]bool)
	for _, b := range blastRadius {
		blastMap[b.Name] = true
	}
	for _, s := range symbolNames {
		blastMap[s] = true
	}

	for _, route := range g.Routes {
		if blastMap[route.Handler] {
			rt := fmt.Sprintf("%s %s", route.Method, route.Path)
			if !routeSet[rt] {
				routeSet[rt] = true
				res.Routes = append(res.Routes, rt)
			}
		}
	}

	// Check Envs and SQL via downstream BFS
	downstream := make(map[string]bool)
	queue := make([]string, len(symbolNames))
	copy(queue, symbolNames)
	for _, s := range symbolNames {
		downstream[s] = true
	}
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		for _, call := range g.Calls {
			if call.CallerName == curr {
				if !downstream[call.CalleeRaw] {
					downstream[call.CalleeRaw] = true
					queue = append(queue, call.CalleeRaw)
				}
			}
		}
	}

	envSet := make(map[string]bool)
	for _, env := range g.EnvReads {
		if downstream[env.Function] {
			if !envSet[env.Key] {
				envSet[env.Key] = true
				res.Envs = append(res.Envs, env.Key)
			}
		}
	}

	for _, sql := range g.SQLs {
		if downstream[sql.Function] {
			res.TouchesSQL = "yes"
			break
		}
	}

	return res
}
