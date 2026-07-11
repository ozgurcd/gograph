package search

import (
	"fmt"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

type RiskDetail struct {
	Symbol          string `json:"symbol"`
	File            string `json:"file"`
	Line            int    `json:"line"`
	Score           int    `json:"score"`
	Verdict         string `json:"verdict"` // "SAFE", "REVIEW", "DANGER"
	Callers         int    `json:"callers"`
	UntestedCallers int    `json:"untested_callers"`
	Complexity      int    `json:"complexity"`
	SQLCount        int    `json:"sql_count"`
	EnvCount        int    `json:"env_count"`
	PublicAPI       bool   `json:"public_api"`
	// Detailed breakdown scores
	BlastRadiusScore int `json:"blast_radius_score"`
	ComplexityScore  int `json:"complexity_score"`
	TestScore        int `json:"test_score"`
	PublicAPIScore   int `json:"public_api_score"`
	SQLScore         int `json:"sql_score"`
	EnvScore         int `json:"env_score"`
}

type RiskReport struct {
	Title   string       `json:"title"`
	Results []RiskDetail `json:"results"`
	Message string       `json:"message,omitempty"`
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit-3] + "..."
}

func (r *RiskReport) String() string {
	if r.Message != "" {
		return r.Message
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Risk Report for %s\n\n", r.Title)
	fmt.Fprintf(&sb, "%-30s  %-8s  %-5s  %s\n", "SYMBOL", "VERDICT", "SCORE", "METRICS")
	fmt.Fprintf(&sb, "%s\n", strings.Repeat("-", 80))
	for _, d := range r.Results {
		metrics := fmt.Sprintf("callers=%d (%d untested)  complexity=%d  sql=%d  env=%d",
			d.Callers, d.UntestedCallers, d.Complexity, d.SQLCount, d.EnvCount)
		fmt.Fprintf(&sb, "%-30s  %-8s  %-5d  %s\n", truncate(d.Symbol, 30), d.Verdict, d.Score, metrics)
	}
	return sb.String()
}

// Risk evaluates the risk profile of target symbols.
func Risk(g *graph.Graph, symbolNames []string, title string) *RiskReport {
	if len(symbolNames) == 0 {
		return &RiskReport{Message: "No symbols found to evaluate risk."}
	}

	report := &RiskReport{
		Title: title,
	}

	// Build lookup tables
	symMapByLine := make(map[string]graph.SymbolNode)
	symMapByID := make(map[string]graph.SymbolNode)
	for i := range g.Symbols {
		s := g.Symbols[i]
		key := fmt.Sprintf("%s:%d", s.File, s.Line)
		symMapByLine[key] = s
		symMapByID[s.ID] = s
		if _, exists := symMapByID[s.Name]; !exists {
			symMapByID[s.Name] = s
		}
	}

	// Build tested sets
	testedIDs := make(map[string]bool)
	testedNames := make(map[string]bool)
	for _, te := range g.TestEdges {
		testedIDs[te.Target] = true
		if idx := strings.LastIndex(te.Target, "::"); idx >= 0 {
			testedNames[te.Target[idx+2:]] = true
		} else {
			testedNames[te.Target] = true
		}
	}

	// Resolve the target symbols to FQ-IDs
	var resolvedIDs []string
	for _, name := range symbolNames {
		matches := FindSymbols(g, name)
		for _, sym := range matches {
			if _, exists := symMapByID[sym.ID]; exists {
				resolvedIDs = append(resolvedIDs, sym.ID)
			}
		}
	}

	for _, symID := range resolvedIDs {
		sym := symMapByID[symID]

		// 1. Blast Radius (Transitive Callers)
		blastResults := ImpactMultiple(g, []string{sym.ID}, "risk", true)
		callersCount := len(blastResults)
		untestedCallers := 0
		for _, r := range blastResults {
			key := fmt.Sprintf("%s:%d", r.File, r.Line)
			callerSym, ok := symMapByLine[key]
			isCallerTested := false
			if ok {
				isCallerTested = testedIDs[callerSym.ID] || testedNames[callerSym.Name]
			} else {
				isCallerTested = testedIDs[r.Name] || testedNames[r.Name]
			}
			if !isCallerTested {
				untestedCallers++
			}
		}

		blastRadiusScore := callersCount * 3
		if blastRadiusScore > 30 {
			blastRadiusScore = 30
		}

		// 2. Cyclomatic Complexity
		compScore := 1
		if sym.Kind == graph.KindFunction || sym.Kind == graph.KindMethod {
			compResults := Complexity(g, sym.ID)
			if len(compResults) > 0 {
				compScore = compResults[0].Score
			}
		}
		complexityScore := (compScore - 1) * 2
		if complexityScore > 25 {
			complexityScore = 25
		}
		if complexityScore < 0 {
			complexityScore = 0
		}

		// 3. Test Coverage
		ts := Tests(g, sym.ID)
		if sym.Name != "" && sym.Name != sym.ID {
			ts2 := Tests(g, sym.Name)
			seen := make(map[string]bool)
			var merged []Result
			for _, t := range ts {
				if !seen[t.Name] {
					seen[t.Name] = true
					merged = append(merged, t)
				}
			}
			for _, t := range ts2 {
				if !seen[t.Name] {
					seen[t.Name] = true
					merged = append(merged, t)
				}
			}
			ts = merged
		}
		testCount := len(ts)
		testScore := 0
		switch testCount {
		case 0:
			testScore = 20
		case 1:
			testScore = 10
		}

		// 4. Public API
		publicAPIScore := 0
		isPublic := false
		if sym.Kind == graph.KindFunction || sym.Kind == graph.KindMethod || sym.Kind == graph.KindStruct || sym.Kind == graph.KindInterface {
			parts := strings.Split(sym.ID, "::")
			short := parts[len(parts)-1]
			parts2 := strings.Split(short, ".")
			name := parts2[len(parts2)-1]
			if len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z' {
				isPublic = true
				publicAPIScore = 10
			}
		}

		// 5. Downstream SQL & Env vars
		downstream := make(map[string]bool)
		queue := []string{sym.ID}
		downstream[sym.ID] = true
		if sym.Name != "" {
			downstream[sym.Name] = true
		}
		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]
			for _, call := range g.Calls {
				if call.CallerSymbolID == curr || call.CallerName == curr {
					callee := call.CalleeSymbolID
					if callee == "" {
						callee = call.CalleeRaw
					}
					if !downstream[callee] {
						downstream[callee] = true
						queue = append(queue, callee)
					}
					if call.CalleeSymbolID != "" {
						downstream[call.CalleeSymbolID] = true
					}
					if call.CalleeRaw != "" {
						downstream[call.CalleeRaw] = true
					}
				}
			}
		}

		sqlCount := 0
		for _, sql := range g.SQLs {
			if downstream[sql.Function] {
				sqlCount++
			}
		}
		sqlScore := 0
		if sqlCount > 0 {
			sqlScore = 10
		}

		envSet := make(map[string]bool)
		for _, env := range g.EnvReads {
			if downstream[env.Function] {
				envSet[env.Key] = true
			}
		}
		envCount := len(envSet)
		envScore := 0
		if envCount > 0 {
			envScore = 5
		}

		totalScore := blastRadiusScore + complexityScore + testScore + publicAPIScore + sqlScore + envScore
		if totalScore > 100 {
			totalScore = 100
		}

		verdict := "SAFE"
		if totalScore > 70 {
			verdict = "DANGER"
		} else if totalScore > 30 {
			verdict = "REVIEW"
		}

		displayName := sym.Name
		if sym.Receiver != "" {
			displayName = "(" + sym.Receiver + ")." + sym.Name
		}

		report.Results = append(report.Results, RiskDetail{
			Symbol:           displayName,
			File:             sym.File,
			Line:             sym.Line,
			Score:            totalScore,
			Verdict:          verdict,
			Callers:          callersCount,
			UntestedCallers:  untestedCallers,
			Complexity:       compScore,
			SQLCount:         sqlCount,
			EnvCount:         envCount,
			PublicAPI:        isPublic,
			BlastRadiusScore: blastRadiusScore,
			ComplexityScore:  complexityScore,
			TestScore:        testScore,
			PublicAPIScore:   publicAPIScore,
			SQLScore:         sqlScore,
			EnvScore:         envScore,
		})
	}

	return report
}
