package search

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/baseline"
	"github.com/ozgurcd/gograph/internal/graph"
)

// safeGitRef is a positive allowlist for git refs to prevent shell injection.
// Allows alphanumeric characters, dots, slashes, hyphens, tildes, and carets.
var safeGitRef = regexp.MustCompile(`^[A-Za-z0-9._/\-~^]+$`)

// CurrentChangeSelectionContract is shared by CLI help and MCP discovery.
const CurrentChangeSelectionContract = "Uncommitted modes compare declarations against HEAD, including selected untracked Go files. Current-graph consumers refuse incomplete comparisons, deleted declarations requiring historical caller evidence, and missing/ambiguous graph identities. Inspect changes --git REF for the declaration census; rebuild before traversing newly added symbols."

// ChangesByGitRef compares current declarations to a confined historical
// baseline, including additions and deletions inside surviving files. It uses
// the graph's recorded file selection when available, not ambient build tags.
//
// root is the absolute path to the repository root.
func ChangesByGitRef(g *graph.Graph, root, ref string) (*ChangesResult, error) {
	return ChangesByGitRefContext(context.Background(), g, root, ref)
}

// ChangesByGitRefContext compares a safely extracted historical declaration
// baseline without running dependency downloads, package compilation or hooks.
func ChangesByGitRefContext(ctx context.Context, g *graph.Graph, root, ref string) (*ChangesResult, error) {
	if ref == "" || strings.HasPrefix(ref, "-") || !safeGitRef.MatchString(ref) {
		return nil, fmt.Errorf("invalid git ref %q", ref)
	}
	before, err := baseline.Build(ctx, root, ref, func(path string) (*graph.Graph, error) {
		return buildDeclarationBaseline(ctx, path, graphSelection(g))
	})
	if err != nil {
		return nil, fmt.Errorf("build declaration baseline for %s: %w", ref, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := ChangesContext(ctx, before, root)
	result.GraphAge = g.GeneratedAt
	return result, nil
}

// UncommittedSymbols compares declarations to HEAD. It refuses incomplete or
// unrepresentable selections instead of silently losing deleted declarations.
func UncommittedSymbols(g *graph.Graph) ([]string, error) {
	return UncommittedSymbolsContext(context.Background(), g)
}

// UncommittedSymbolsContext uses the same confined declaration comparison as
// changes --git HEAD, including build selection and current-source validation.
func UncommittedSymbolsContext(ctx context.Context, g *graph.Graph) ([]string, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is unavailable")
	}
	root := g.Root
	if root == "" {
		root = "."
	}
	changes, err := ChangesByGitRefContext(ctx, g, root, "HEAD")
	if err != nil {
		return nil, err
	}
	return CurrentChangedSymbolIDs(g, changes)
}

// CurrentChangedSymbolIDs is the gate for consumers that traverse only the
// current graph. A historical declaration census can explain deletions; it does
// not contain historical call edges and cannot prove a deleted symbol's impact.
func CurrentChangedSymbolIDs(g *graph.Graph, changes *ChangesResult) ([]string, error) {
	if g == nil || changes == nil {
		return nil, fmt.Errorf("graph or change evaluation is unavailable")
	}
	if changes.Evaluation != "complete" {
		return nil, fmt.Errorf("cannot traverse incomplete changes: %s", strings.Join(changes.Diagnostics, "; "))
	}
	present := make(map[string][]graph.SymbolNode)
	for _, symbol := range g.Symbols {
		present[symbol.ID] = append(present[symbol.ID], symbol)
	}
	seen := make(map[string]bool)
	var ids []string
	for _, change := range changes.Symbols {
		if change.Status == ChangeDeleted {
			return nil, fmt.Errorf("deleted declaration %q requires historical caller evidence; inspect changes --git HEAD (or the selected ref); current-graph traversal cannot evaluate deletions", change.StableID)
		}
		if change.Status != ChangeNew && change.Status != ChangeModified {
			return nil, fmt.Errorf("cannot traverse %s declaration %q", change.Status, change.StableID)
		}
		matches := present[change.StableID]
		if change.StableID == "" || len(matches) == 0 {
			return nil, fmt.Errorf("changed declaration %q is absent from the graph; rebuild the graph and retry", change.StableID)
		}
		if len(matches) != 1 || change.PackageName != "" && matches[0].PackageName != change.PackageName {
			return nil, fmt.Errorf("changed declaration %q has ambiguous graph identity; inspect changes for file/package details", change.StableID)
		}
		if !seen[change.StableID] {
			seen[change.StableID] = true
			ids = append(ids, change.StableID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}
