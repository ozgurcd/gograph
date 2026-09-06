package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ozgurcd/gograph/internal/search"
)

var listLimit = search.DefaultResultsLimit
var listCursor string

func stripListPagination(args []string) ([]string, error) {
	listLimit, listCursor = search.DefaultResultsLimit, ""
	if len(args) == 0 || !search.SupportsListPagination(args[0]) {
		return args, nil
	}
	filtered := []string{args[0]}
	seen := make(map[string]bool)
	for i := 1; i < len(args); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		if name != "--limit" && name != "--cursor" {
			filtered = append(filtered, args[i])
			continue
		}
		if seen[name] {
			return nil, fmt.Errorf("%s may only be supplied once", name)
		}
		seen[name] = true
		if !hasValue {
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("%s requires a value", name)
			}
			value = args[i]
		}
		if filesOnlyMode {
			return nil, fmt.Errorf("--files-only returns the full file census; do not combine it with %s", name)
		}
		if mermaidMode {
			return nil, fmt.Errorf("%s pages result rows, not Mermaid diagrams; use --json or text output", name)
		}
		if name == "--cursor" {
			if len(value) > 128 {
				return nil, fmt.Errorf("invalid result cursor; restart without cursor")
			}
			listCursor = value
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > search.MaxResultsLimit {
			return nil, fmt.Errorf("limit must be between 1 and %d", search.MaxResultsLimit)
		}
		listLimit = parsed
	}
	return filtered, nil
}

func listPage(command string, results []search.Result) (search.ResultPage, error) {
	outputGraph.RLock()
	g := outputGraph.graph
	outputGraph.RUnlock()
	fingerprint, err := search.ResultSnapshotFingerprint(g)
	if err != nil {
		return search.ResultPage{}, err
	}
	return search.PageResults(fingerprint, command, results, listLimit, listCursor)
}

func printListPaginationHelp(command string) {
	if command != "" && !search.SupportsListPagination(command) {
		return
	}
	fmt.Printf("\nRESULT PAGINATION\n  --limit N       Maximum rows per page (default %d, range 1-%d).\n  --cursor TOKEN  Continue using next_cursor and the same graph snapshot/results.\n", search.DefaultResultsLimit, search.MaxResultsLimit)
	fmt.Printf("  Pages use a %d KiB native-JSON budget; the byte budget may return fewer rows.\n", search.MaxResultsBytes/1024)
	fmt.Println("  JSON exposes total, returned, truncated, next_cursor, limit, and offset.")
	fmt.Println("  Changed graph content or result selection invalidates the cursor; restart without it.")
	fmt.Println("  One oversized row is refused with guidance, never silently cut off.")
	fmt.Println("  --files-only keeps the full file census; do not combine pagination with it or --mermaid.")
	if command == "" {
		fmt.Println("  Commands: " + strings.Join(search.ListPaginationCommands, ", "))
	}
}
