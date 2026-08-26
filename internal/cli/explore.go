package cli

import (
	"fmt"
	"strings"

	"github.com/ozgurcd/gograph/internal/search"
)

func runExplore(args []string) int {
	if len(args) == 0 {
		return failCommand("explore", "usage: gograph explore <term...> [--compact|--deep] [--limit N] [--exact]")
	}

	limit := 0
	exact := false
	mode := search.ExploreModeStandard
	var termParts []string
	for i := 0; i < len(args); i++ {
		switch argument := args[i]; argument {
		case "--exact":
			exact = true
		case "--compact":
			if mode == search.ExploreModeDeep {
				return failCommand("explore", "--compact and --deep are mutually exclusive")
			}
			mode = search.ExploreModeCompact
		case "--deep":
			if mode == search.ExploreModeCompact {
				return failCommand("explore", "--compact and --deep are mutually exclusive")
			}
			mode = search.ExploreModeDeep
		case "--limit", "-n":
			value, err := parseIntegerFlag(args, &i)
			if err != nil {
				return failCommand("explore", err.Error())
			}
			if value < 1 {
				value = 1
			}
			limit = search.NormalizeExploreLimit(value)
		default:
			if strings.HasPrefix(argument, "-") {
				return failCommandf("explore", "explore: unknown flag: %s", argument)
			}
			termParts = append(termParts, argument)
		}
	}
	if len(termParts) == 0 {
		return failCommand("explore", "usage: gograph explore <term...> [--compact|--deep] [--limit N] [--exact]")
	}

	g, err := loadGraph(".")
	if err != nil {
		return failCommand("explore", err.Error())
	}
	query := strings.Join(termParts, " ")
	result := search.Explore(g, graphRoot(g), query, search.ExploreOptions{
		Limit: limit,
		Exact: exact,
		Mode:  mode,
	})
	if jsonMode {
		count := 0
		if result.Count > 0 || result.Context != nil {
			count = 1
		}
		return PrintJSON(okEnvelope("explore", query, result, count))
	}
	return printExploreResult(result)
}

func printExploreResult(result search.ExploreResult) int {
	if result.Count == 0 && result.Context == nil {
		fmt.Printf("No repository evidence found matching %q.\n", result.Query)
		return 0
	}

	fmt.Printf("=== EXPLORE: %s ===\n\n", result.Query)
	if result.Mode != search.ExploreModeStandard {
		fmt.Printf("mode: %s (limit %d per section)\n\n", result.Mode, result.Limit)
	}
	if len(result.Matches) > 0 {
		fmt.Printf("--- RANKED MATCHES (showing %d of %d) ---\n", len(result.Matches), result.Totals.Matches)
		for _, match := range result.Matches {
			fmt.Println(match.String())
		}
		fmt.Println()
	}

	if result.Context != nil {
		fmt.Printf("--- SELECTED SYMBOL: %s (%s) ---\n", result.SelectedSymbol, result.SelectionBasis)
		if result.Ambiguous {
			fmt.Printf("warning: selector resolves to %d nodes; use --exact or a fully-qualified ID to narrow it.\n", result.Totals.Nodes)
		}
		for _, node := range result.Context.Nodes {
			fmt.Println(node.String())
		}
		if result.Context.Role != "" {
			fmt.Printf("role: %s\n", result.Context.Role)
		}
		if result.Mode == search.ExploreModeCompact {
			fmt.Printf("available evidence: callers=%d callees=%d tests=%d exact-impact=%d\n",
				result.Totals.Callers, result.Totals.Callees, result.Totals.Tests, result.Totals.Impact)
		}
		fmt.Println()

		if result.Context.Source != "" {
			fmt.Println("--- SOURCE ---")
			fmt.Println(result.Context.Source)
			fmt.Println()
		} else if result.Context.SourceError != "" {
			fmt.Printf("source unavailable: %s\n\n", result.Context.SourceError)
		}

		printExploreSection("CALLERS", result.Context.Callers, result.Totals.Callers)
		printExploreSection("CALLEES", result.Context.Callees, result.Totals.Callees)
		printExploreSection("TESTS", result.Context.TestResults, result.Totals.Tests)
	}
	printExploreSection("TRANSITIVE IMPACT", result.Impact, result.Totals.Impact)
	printExploreDeep(result.Deep)

	if len(result.TruncatedSections) > 0 {
		fmt.Printf("Bounded by --limit=%d; truncated sections: %s. Use the focused command for a complete section.\n",
			result.Limit, strings.Join(result.TruncatedSections, ", "))
	}
	if len(result.OmittedSections) > 0 {
		fmt.Printf("Mode %s omitted sections: %s. Use standard/deep mode or the focused command when needed.\n",
			result.Mode, strings.Join(result.OmittedSections, ", "))
	}
	return 0
}

func printExploreDeep(deep *search.ExploreDeepPayload) {
	if deep == nil {
		return
	}
	if deep.Explanation != nil {
		fmt.Printf("--- DEEP EXPLANATION ---\n%s\n\n", deep.Explanation.Narrative)
	}
	printExploreSection("DEEP CALLERS", deep.Callers, deep.Totals.Callers)
	printExploreSection("DEEP CALLEES", deep.Callees, deep.Totals.Callees)
	printExploreSection("PACKAGE CONTEXT", deep.PackageContext, deep.Totals.PackageContext)
}

func printExploreSection(title string, results []search.Result, total int) {
	if total == 0 {
		return
	}
	fmt.Printf("--- %s (showing %d of %d) ---\n", title, len(results), total)
	for _, result := range results {
		fmt.Println(result.String())
	}
	fmt.Println()
}
