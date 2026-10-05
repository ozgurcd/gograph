package search

import (
	"fmt"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

type ReviewResult struct {
	Title      string   `json:"title"`
	Changes    []Result `json:"changes"`
	Tests      []string `json:"tests"`
	PublicAPI  string   `json:"public_api"` // "yes", "no"
	Routes     []string `json:"routes"`
	Envs       []string `json:"envs"`
	TouchesSQL string   `json:"touches_sql"` // "yes", "no"
	Errors     []string `json:"errors"`
	Message    string   `json:"message,omitempty"` // For "No modified symbols found"
}

func (r *ReviewResult) String() string {
	if r.Message != "" {
		return r.Message
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Code Review for %s\n\n", r.Title)

	sb.WriteString("1. What changed?\n")
	for _, c := range r.Changes {
		fmt.Fprintf(&sb, "   - %s:%d %s (%s)\n", c.File, c.Line, c.Name, string(c.Kind))
	}
	sb.WriteString("\n")

	sb.WriteString("2. Tests likely needing review:\n")
	if len(r.Tests) > 0 {
		for _, tf := range r.Tests {
			fmt.Fprintf(&sb, "   - %s\n", tf)
		}
	} else {
		sb.WriteString("   - (No direct tests found)\n")
	}
	sb.WriteString("\n")

	sb.WriteString("3. Surface Area:\n")
	fmt.Fprintf(&sb, "   - Public API: %s\n", r.PublicAPI)

	if len(r.Routes) > 0 {
		if len(r.Routes) > 3 {
			fmt.Fprintf(&sb, "   - HTTP routes: %s, and %d more\n", strings.Join(r.Routes[:3], ", "), len(r.Routes)-3)
		} else {
			fmt.Fprintf(&sb, "   - HTTP routes: %s\n", strings.Join(r.Routes, ", "))
		}
	} else {
		sb.WriteString("   - HTTP routes: no\n")
	}

	if len(r.Envs) > 0 {
		if len(r.Envs) > 3 {
			fmt.Fprintf(&sb, "   - Env reads: %s, and %d more\n", strings.Join(r.Envs[:3], ", "), len(r.Envs)-3)
		} else {
			fmt.Fprintf(&sb, "   - Env reads: %s\n", strings.Join(r.Envs, ", "))
		}
	} else {
		sb.WriteString("   - Env reads: no\n")
	}

	fmt.Fprintf(&sb, "   - SQL touches: %s\n", r.TouchesSQL)

	if len(r.Errors) > 0 {
		if len(r.Errors) > 3 {
			fmt.Fprintf(&sb, "   - Error returns: %s, and %d more\n", strings.Join(r.Errors[:3], ", "), len(r.Errors)-3)
		} else {
			fmt.Fprintf(&sb, "   - Error returns: %s\n", strings.Join(r.Errors, ", "))
		}
	} else {
		sb.WriteString("   - Error returns: none detected\n")
	}

	return sb.String()
}

// Review generates a post-edit review report for modified symbols.
func Review(g *graph.Graph, symbolNames []string, title string) *ReviewResult {
	return NewSnapshot(g).Review(symbolNames, title)
}

// Review composes a change review using this immutable snapshot's derived indexes.
func (snapshot *Snapshot) Review(symbolNames []string, title string) *ReviewResult {
	g := snapshot.g
	if len(symbolNames) == 0 {
		return &ReviewResult{Message: "No modified symbols found to review."}
	}

	res := &ReviewResult{
		Title:      title,
		PublicAPI:  "no",
		TouchesSQL: "no",
	}

	var validSymbols []string
	symbolMap := make(map[string]*graph.SymbolNode)
	for i := range g.Symbols {
		s := &g.Symbols[i]
		symbolMap[s.ID] = s
		if _, exists := symbolMap[s.Name]; !exists {
			symbolMap[s.Name] = s
		}
	}

	for _, name := range symbolNames {
		matches := FindSymbols(g, name)
		for _, sym := range matches {
			if _, exists := symbolMap[sym.ID]; exists {
				validSymbols = append(validSymbols, sym.ID)
			}
		}
	}

	for _, name := range validSymbols {
		sym := symbolMap[name]
		res.Changes = append(res.Changes, Result{File: sym.File, Line: sym.Line, Name: sym.Name, Kind: string(sym.Kind)})
	}

	testFiles := make(map[string]bool)
	for _, symName := range validSymbols {
		ts := Tests(g, symName)
		for _, t := range ts {
			testFiles[t.File] = true
		}
	}
	for tf := range testFiles {
		res.Tests = append(res.Tests, tf)
	}

	for _, symName := range validSymbols {
		sym := symbolMap[symName]
		if sym.Kind == graph.KindFunction || sym.Kind == graph.KindMethod || sym.Kind == graph.KindStruct || sym.Kind == graph.KindInterface {
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
	}

	routeSet := make(map[string]bool)
	blastRadius := snapshot.Impact(validSymbols, "review", ImpactOptions{IncludeTests: true})
	blastMap := make(map[string]bool)
	for _, b := range blastRadius {
		blastMap[b.Name] = true
	}
	for _, s := range validSymbols {
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

	downstream := snapshot.downstream(validSymbols)

	envSet := make(map[string]bool)
	for _, env := range g.EnvReads {
		if downstream[env.Function] {
			if !envSet[env.Key] {
				envSet[env.Key] = true
				res.Envs = append(res.Envs, env.Key)
			}
		}
	}

	for _, key := range bodyEnvironment(g, validSymbols) {
		if !envSet[key] {
			envSet[key] = true
			res.Envs = append(res.Envs, key)
		}
	}

	for _, sql := range g.SQLs {
		if downstream[sql.Function] {
			res.TouchesSQL = "yes"
			break
		}
	}

	errSet := make(map[string]bool)
	for _, errEdge := range g.Errors {
		if downstream[errEdge.Function] {
			eStr := errEdge.Message
			if !errSet[eStr] {
				errSet[eStr] = true
				res.Errors = append(res.Errors, eStr)
			}
		}
	}

	return res
}
