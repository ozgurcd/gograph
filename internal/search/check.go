package search

import (
	"fmt"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

type CheckLevel string

const (
	CheckOff   CheckLevel = "off"
	CheckWarn  CheckLevel = "warn"
	CheckError CheckLevel = "error"
)

type CheckStatus string

const (
	CheckPassed  CheckStatus = "passed"
	CheckWarning CheckStatus = "warning"
	CheckFailed  CheckStatus = "failed"
)

type CheckFinding struct {
	Check    string         `json:"check"`
	Level    string         `json:"level"`
	Message  string         `json:"message"`
	File     string         `json:"file,omitempty"`
	Line     int            `json:"line,omitempty"`
	Symbol   string         `json:"symbol,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type CheckSkipped struct {
	Check  string `json:"check"`
	Reason string `json:"reason"`
}

type CheckSummary struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Skipped  int `json:"skipped"`
}

type CheckReport struct {
	Status      string         `json:"status"`
	Summary     CheckSummary   `json:"summary"`
	Findings    []CheckFinding `json:"findings"`
	Skipped     []CheckSkipped `json:"skipped"`
	Limitations []string       `json:"limitations"`
}

type CheckConfig struct {
	Checks           map[string]any `json:"checks"`
	BoundariesConfig string         `json:"boundaries_config"`
	Baseline         string         `json:"baseline"`
}

type CheckParams struct {
	CurrentGraph  *graph.Graph
	BaselineGraph *graph.Graph // Can be nil
	Config        *CheckConfig
	SinceRef      string
	Uncommitted   bool
	RootDir       string
}

func parseLevel(val any) (CheckLevel, int) {
	switch v := val.(type) {
	case string:
		return CheckLevel(v), 0
	case map[string]any:
		lvl, _ := v["level"].(string)
		valFloat, _ := v["value"].(float64)
		return CheckLevel(lvl), int(valFloat)
	}
	return CheckOff, 0
}

func RunChecks(p *CheckParams) (*CheckReport, error) {
	report := &CheckReport{
		Findings: []CheckFinding{},
		Skipped:  []CheckSkipped{},
		Limitations: []string{
			"gograph check is static analysis only.",
			"It does not execute target repository code.",
			"It does not prove runtime behavior.",
		},
	}

	validChecks := map[string]bool{
		"boundaries":                       true,
		"api_drift":                        true,
		"require_tests_for_changed_routes": true,
		"require_tests_for_changed_exported_symbols": true,
		"test_coverage":  true,
		"no_orphans":     true,
		"max_arity":      true,
		"max_complexity": true,
		"new_globals":    true,
	}

	for checkName := range p.Config.Checks {
		if !validChecks[checkName] {
			return nil, fmt.Errorf("unknown check name in config: %s", checkName)
		}
	}

	addFinding := func(check, msg, file, symbol string, line int, lvl CheckLevel, meta map[string]any) {
		report.Findings = append(report.Findings, CheckFinding{
			Check:    check,
			Level:    string(lvl),
			Message:  msg,
			File:     file,
			Line:     line,
			Symbol:   symbol,
			Metadata: meta,
		})
		switch lvl {
		case CheckError:
			report.Summary.Errors++
		case CheckWarn:
			report.Summary.Warnings++
		}
	}

	addSkipped := func(check, reason string) {
		report.Skipped = append(report.Skipped, CheckSkipped{Check: check, Reason: reason})
		report.Summary.Skipped++
	}

	hasBaselineOrUncommitted := p.BaselineGraph != nil || p.Uncommitted
	needsChangedSymbols := false
	for _, check := range []string{"require_tests_for_changed_routes", "require_tests_for_changed_exported_symbols", "new_globals"} {
		if level, _ := parseLevel(p.Config.Checks[check]); level != CheckOff {
			needsChangedSymbols = true
			break
		}
	}

	// Extract affected symbols once for every change-scoped check. Git-backed
	// baselines use changed files so body-only edits are not lost merely because
	// a declaration's name and signature stayed the same.
	var changedSymbols []string
	if hasBaselineOrUncommitted && needsChangedSymbols {
		var err error
		changedSymbols, err = checkChangedSymbols(p)
		if err != nil {
			return nil, err
		}
	}

	// 1. boundaries
	if lvl, _ := parseLevel(p.Config.Checks["boundaries"]); lvl != CheckOff {
		if p.Config.BoundariesConfig == "" {
			addSkipped("boundaries", "no boundaries config exists")
		} else {
			results, err := Boundaries(p.CurrentGraph, p.Config.BoundariesConfig)
			if err != nil {
				// config file doesn't exist or is invalid
				addSkipped("boundaries", fmt.Sprintf("failed to load boundaries config: %v", err))
			} else {
				for _, r := range results {
					meta := map[string]any{"rule": r.Kind} // r.Kind stores the rule type, e.g., "invalid_import"
					// r.Name is just the layer name (e.g. "internal_cli");
					// r.Detail carries the human-readable explanation
					// ("layer 'X' illegally imports 'Y'"). Prefer Detail
					// for the warning message; fall back to Name if no
					// Detail was set so we never surface an empty string.
					msg := r.Detail
					if msg == "" {
						msg = r.Name
					}
					addFinding("boundaries", msg, r.File, r.Name, r.Line, lvl, meta)
				}
			}
		}
	}

	// 2. api_drift
	if lvl, _ := parseLevel(p.Config.Checks["api_drift"]); lvl != CheckOff {
		if p.BaselineGraph == nil {
			addSkipped("api_drift", "no --since baseline provided")
		} else {
			driftRes := APIDrift(p.BaselineGraph, p.CurrentGraph, p.SinceRef)
			if driftRes.BreakingHTTPAPI == "yes" {
				addFinding("api_drift", "Breaking HTTP API drift detected", "", "", 0, lvl, nil)
			}
			if driftRes.BreakingGoAPI {
				addFinding("api_drift", "Breaking internal contract drift detected", "", "", 0, lvl, nil)
			}
		}
	}

	// 3. require_tests_for_changed_routes
	if lvl, _ := parseLevel(p.Config.Checks["require_tests_for_changed_routes"]); lvl != CheckOff {
		if !hasBaselineOrUncommitted {
			addSkipped("require_tests_for_changed_routes", "no --since or --uncommitted provided")
		} else {
			seenHandlers := make(map[string]bool)
			for _, route := range p.CurrentGraph.Routes {
				if route.DynamicHandler || !containsCheckSymbol(changedSymbols, route.Handler) {
					continue
				}
				handlerKey := normalizeCheckSymbol(route.Handler)
				if seenHandlers[handlerKey] {
					continue
				}
				seenHandlers[handlerKey] = true
				if len(Tests(p.CurrentGraph, route.Handler)) == 0 && len(Tests(p.CurrentGraph, handlerKey)) == 0 {
					addFinding("require_tests_for_changed_routes", fmt.Sprintf("Changed route handler %s has no mapped tests", route.Handler), route.File, route.Handler, route.Line, lvl, map[string]any{"method": route.Method, "path": route.Path})
				}
			}
		}
	}

	// 4. require_tests_for_changed_exported_symbols
	if lvl, _ := parseLevel(p.Config.Checks["require_tests_for_changed_exported_symbols"]); lvl != CheckOff {
		if !hasBaselineOrUncommitted {
			addSkipped("require_tests_for_changed_exported_symbols", "no --since or --uncommitted provided")
		} else {
			for _, sym := range changedSymbols {
				// check if exported
				if len(sym) > 0 && sym[0] >= 'A' && sym[0] <= 'Z' {
					// ensure it's in the graph
					nodes := Node(p.CurrentGraph, sym)
					if len(nodes) > 0 {
						n := nodes[0]
						if n.Kind != "test" && n.Kind != "file" && n.Kind != "interface" {
							tests := Tests(p.CurrentGraph, sym)
							if len(tests) == 0 {
								addFinding("require_tests_for_changed_exported_symbols", fmt.Sprintf("Changed exported symbol %s has no mapped tests", sym), n.File, sym, n.Line, lvl, nil)
							}
						}
					}
				}
			}
		}
	}

	// 5. test_coverage
	if lvl, _ := parseLevel(p.Config.Checks["test_coverage"]); lvl != CheckOff {
		for _, s := range p.CurrentGraph.Symbols {
			if (s.Kind != graph.KindFunction && s.Kind != graph.KindMethod) || isTestFile(s.File) || s.Name == "main" || s.Name == "init" || !isExportedName(s.Name) {
				continue
			}
			if len(Tests(p.CurrentGraph, s.ID)) == 0 && len(Tests(p.CurrentGraph, s.Name)) == 0 {
				addFinding("test_coverage", fmt.Sprintf("Exported symbol %s has no mapped tests", s.Name), s.File, s.Name, s.Line, lvl, nil)
			}
		}
	}

	// 6. no_orphans
	if lvl, _ := parseLevel(p.Config.Checks["no_orphans"]); lvl != CheckOff {
		for _, orphan := range ReachableOrphans(p.CurrentGraph) {
			addFinding("no_orphans", fmt.Sprintf("Unreachable symbol %s", orphan.Name), orphan.File, orphan.Name, orphan.Line, lvl, nil)
		}
	}

	// 7. max_arity
	if lvl, val := parseLevel(p.Config.Checks["max_arity"]); lvl != CheckOff {
		if val <= 0 {
			val = 6 // default
		}
		for _, s := range p.CurrentGraph.Symbols {
			if s.Kind == graph.KindFunction || s.Kind == graph.KindMethod {
				arity := countArgs(s.Signature)
				if arity > val {
					addFinding("max_arity", fmt.Sprintf("Function %s has arity %d", s.Name, arity), s.File, s.Name, s.Line, lvl, map[string]any{"arity": arity, "threshold": val})
				}
			}
		}
	}

	// 8. max_complexity
	if lvl, val := parseLevel(p.Config.Checks["max_complexity"]); lvl != CheckOff {
		if val <= 0 {
			val = 20 // default
		}
		results := Complexity(p.CurrentGraph, "")
		for _, r := range results {
			if r.Score > val {
				addFinding("max_complexity", fmt.Sprintf("Symbol %s has complexity %d", r.Symbol, r.Score), r.File, r.Symbol, r.Line, lvl, map[string]any{"complexity": r.Score, "threshold": val})
			}
		}
	}

	// 9. new_globals
	if lvl, _ := parseLevel(p.Config.Checks["new_globals"]); lvl != CheckOff {
		if !hasBaselineOrUncommitted {
			addSkipped("new_globals", "no --since or --uncommitted provided")
		} else {
			// Find globals in current
			currGlobals := Globals(p.CurrentGraph, "")

			if p.Uncommitted {
				changes := Changes(p.CurrentGraph, p.RootDir)
				for _, g := range currGlobals {
					for _, c := range changes.Symbols {
						if c.Status == ChangeNew && c.Name == g.Name {
							addFinding("new_globals", fmt.Sprintf("New package-level global %s", g.Name), g.File, g.Name, g.Line, lvl, nil)
						}
					}
				}
			} else if p.BaselineGraph != nil {
				baseGlobals := Globals(p.BaselineGraph, "")
				baseMap := make(map[string]bool)
				for _, b := range baseGlobals {
					baseMap[b.Name] = true
				}
				for _, g := range currGlobals {
					if !baseMap[g.Name] {
						// Only warn if the symbol itself changed or is new
						for _, sym := range changedSymbols {
							if sym == g.Name {
								addFinding("new_globals", fmt.Sprintf("New package-level global %s", g.Name), g.File, g.Name, g.Line, lvl, nil)
								break
							}
						}
					}
				}
			}
		}
	}

	if report.Summary.Errors > 0 {
		report.Status = string(CheckFailed)
	} else if report.Summary.Warnings > 0 {
		report.Status = string(CheckWarning)
	} else {
		report.Status = string(CheckPassed)
	}

	return report, nil
}

func countArgs(signature string) int {
	if signature == "" {
		return 0
	}
	start := strings.Index(signature, "(")
	end := strings.LastIndex(signature, ")")
	if start == -1 || end == -1 || start >= end {
		return 0
	}
	argsStr := signature[start+1 : end]
	if strings.TrimSpace(argsStr) == "" {
		return 0
	}
	// Note: this is a simple heuristic and might be slightly off for complex generics or funcs as args,
	// but it matches arity.go's heuristic. Actually, we should reuse arity logic if possible.
	return len(strings.Split(argsStr, ","))
}

func checkChangedSymbols(p *CheckParams) ([]string, error) {
	var changed []string
	if p.Uncommitted {
		ids, err := UncommittedSymbols(p.CurrentGraph)
		if err != nil {
			return nil, fmt.Errorf("identify uncommitted symbols: %w", err)
		}
		for _, id := range ids {
			matches := FindSymbols(p.CurrentGraph, id)
			if len(matches) == 0 {
				changed = append(changed, id)
				continue
			}
			for _, s := range matches {
				changed = append(changed, s.Name)
			}
		}
		return uniqueStrings(changed), nil
	}

	if p.BaselineGraph == nil {
		return nil, nil
	}
	root := p.RootDir
	if root == "" {
		root = p.CurrentGraph.Root
	}
	if p.SinceRef != "" && !strings.HasSuffix(p.SinceRef, ".json") {
		changes, err := ChangesByGitRef(p.CurrentGraph, root, p.SinceRef)
		if err != nil {
			return nil, fmt.Errorf("identify symbols changed since %q: %w", p.SinceRef, err)
		}
		for _, s := range changes.Symbols {
			changed = append(changed, s.Name)
		}
		return uniqueStrings(changed), nil
	}

	baselineSymbols := make(map[string]graph.SymbolNode, len(p.BaselineGraph.Symbols))
	for _, s := range p.BaselineGraph.Symbols {
		baselineSymbols[checkSymbolKey(s)] = s
	}
	for _, s := range p.CurrentGraph.Symbols {
		baseline, ok := baselineSymbols[checkSymbolKey(s)]
		if !ok || baseline.Signature != s.Signature {
			changed = append(changed, s.Name)
		}
	}
	return uniqueStrings(changed), nil
}

func checkSymbolKey(s graph.SymbolNode) string {
	if s.ID != "" {
		return s.ID
	}
	return s.PackageName + "|" + s.Receiver + "|" + s.Name + "|" + s.File
}

func normalizeCheckSymbol(name string) string {
	name = strings.TrimSpace(strings.TrimSuffix(name, "()"))
	if idx := strings.LastIndex(name, "::"); idx >= 0 {
		name = name[idx+2:]
	}
	return normalizeSymbolName(name)
}

func containsCheckSymbol(symbols []string, target string) bool {
	target = normalizeCheckSymbol(target)
	if target == "" {
		return false
	}
	for _, symbol := range symbols {
		if normalizeCheckSymbol(symbol) == target {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func isExportedName(name string) bool {
	return len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z'
}
