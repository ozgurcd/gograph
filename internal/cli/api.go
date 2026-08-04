package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/ozgurcd/gograph/internal/search"
)

func runAPI(args []string) int {
	var baselineRef string

	for i := 0; i < len(args); i++ {
		if args[i] == "--since" && i+1 < len(args) {
			baselineRef = args[i+1]
			i++
		}
	}

	if baselineRef == "" {
		if jsonMode {
			return PrintJSON(errEnvelope("api", "missing --since flag. Usage: gograph api --since <ref>"))
		}
		fmt.Fprintln(os.Stderr, "usage: gograph api --since <git-ref|file.json> [--json]")
		return 1
	}

	currentGraph, err := loadGraph(".")
	if err != nil {
		if jsonMode {
			return PrintJSON(errEnvelope("api", err.Error()))
		}
		fmt.Fprintln(os.Stderr, "error loading current graph:", err)
		return 1
	}

	baselineGraph, err := BuildBaselineGraphFromGitRefAtRoot(graphRoot(currentGraph), baselineRef, BuildGraph)
	if err != nil {
		if jsonMode {
			return PrintJSON(errEnvelope("api", err.Error()))
		}
		fmt.Fprintf(os.Stderr, "error building baseline graph: %v\n", err)
		return 1
	}

	res := search.APIDrift(baselineGraph, currentGraph, baselineRef)

	if jsonMode {
		return PrintJSON(okEnvelope("api", baselineRef, res, apiDriftItemCount(res)))
	}

	fmt.Printf("API / contract drift since %s\n", baselineRef)
	if !strings.HasSuffix(baselineRef, ".json") {
		fmt.Printf("(Evaluated using a temporary baseline graph extracted from Git ref %s)\n", baselineRef)
	}
	fmt.Println()

	if len(res.ExportedSymbols.Changed) > 0 || len(res.Interfaces.Removed) > 0 || len(res.ExportedSymbols.Removed) > 0 || len(res.Interfaces.Changed) > 0 {
		fmt.Println("Breaking Go API changes:")
		for _, s := range res.ExportedSymbols.Removed {
			fmt.Printf("  - %s removed\n", s)
		}
		for _, s := range res.Interfaces.Removed {
			fmt.Printf("  - %s interface removed\n", s)
		}
		for _, c := range res.ExportedSymbols.Changed {
			fmt.Printf("  - %s signature changed (%s)\n", c.Name, c.Details)
		}
		for _, c := range res.Interfaces.Changed {
			fmt.Printf("  - %s interface changed (%s)\n", c.Name, c.Details)
		}
		fmt.Println()
	}

	if len(res.Structs.Changed) > 0 || len(res.Structs.Removed) > 0 || len(res.Routes.Removed) > 0 || len(res.Routes.Changed) > 0 {
		fmt.Println("Possible HTTP contract changes:")
		for _, r := range res.Routes.Removed {
			fmt.Printf("  - %s removed\n", r)
		}
		for _, r := range res.Routes.Changed {
			fmt.Printf("  - %s %s (%s)\n", r.Method, r.Path, r.Details)
		}
		for _, s := range res.Structs.Removed {
			fmt.Printf("  - %s struct removed\n", s)
		}
		for _, c := range res.Structs.Changed {
			fmt.Printf("  - %s struct changed (%s)\n", c.Name, c.Details)
		}
		fmt.Println()
	}

	if len(res.ExportedSymbols.Added) > 0 || len(res.Interfaces.Added) > 0 || len(res.Structs.Added) > 0 || len(res.Routes.Added) > 0 {
		fmt.Println("Non-breaking additions:")
		for _, r := range res.Routes.Added {
			fmt.Printf("  - %s added\n", r)
		}
		for _, s := range res.Structs.Added {
			fmt.Printf("  - %s struct added\n", s)
		}
		for _, s := range res.ExportedSymbols.Added {
			fmt.Printf("  - %s added\n", s)
		}
		for _, s := range res.Interfaces.Added {
			fmt.Printf("  - %s interface added\n", s)
		}
		fmt.Println()
	}

	if len(res.AffectedMocks) > 0 || len(res.AffectedTests) > 0 {
		fmt.Println("Likely affected tests / mocks:")
		for _, m := range res.AffectedMocks {
			fmt.Printf("  - %s may be stale\n", m)
		}
		for _, t := range res.AffectedTests {
			fmt.Printf("  - %s tests likely affected\n", t)
		}
		fmt.Println()
	}

	fmt.Println("Risk summary:")
	fmt.Printf("  breaking Go API: %v\n", res.BreakingGoAPI)
	fmt.Printf("  breaking HTTP API: %s\n", res.BreakingHTTPAPI)
	fmt.Printf("  stale mocks: %v\n\n", res.StaleMocksLikely)

	fmt.Println("Note: Contract drift is based on static AST and graph comparison. It identifies likely compatibility risks but does not prove runtime behavior.")

	return 0
}

// apiDriftItemCount counts concrete contract changes. Affected tests and
// mocks are consequences of those changes, not additional drift items.
func apiDriftItemCount(res *search.APIDriftResult) int {
	if res == nil {
		return 0
	}
	return len(res.ExportedSymbols.Added) +
		len(res.ExportedSymbols.Removed) +
		len(res.ExportedSymbols.Changed) +
		len(res.Interfaces.Added) +
		len(res.Interfaces.Removed) +
		len(res.Interfaces.Changed) +
		len(res.Structs.Added) +
		len(res.Structs.Removed) +
		len(res.Structs.Changed) +
		len(res.Routes.Added) +
		len(res.Routes.Removed) +
		len(res.Routes.Changed)
}
