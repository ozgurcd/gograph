package search

import (
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// NotFoundHint suggests declarations for a selector that matched nothing:
// the same name under another receiver or package first (a method selector
// for a plain function, or the reverse), then a different letter case, then
// close spellings. It returns "" when nothing is close.
func NotFoundHint(g *graph.Graph, query string) string {
	suggestions := DeclarationSuggestions(g, query, 5)
	if len(suggestions) == 0 {
		return ""
	}
	return "did you mean: " + strings.Join(suggestions, ", ")
}

// DeclarationKnown reports whether query selects at least one declaration.
func DeclarationKnown(g *graph.Graph, query string) bool {
	return g != nil && len(FindSymbols(g, query)) > 0
}

// DeclarationSuggestions returns up to limit selectors close to query.
func DeclarationSuggestions(g *graph.Graph, query string, limit int) []string {
	if g == nil || limit <= 0 {
		return nil
	}
	bare := suggestionBareName(query)
	if bare == "" {
		return nil
	}
	lowerBare := strings.ToLower(bare)
	maxDistance := 1
	if len(bare) >= 8 {
		maxDistance = 2
	}
	type candidate struct {
		score    int
		selector string
	}
	best := make(map[string]int)
	for _, symbol := range g.Symbols {
		name := symbol.Name
		lower := strings.ToLower(name)
		score := -1
		switch {
		case name == bare:
			score = 0
		case lower == lowerBare:
			score = 1
		case editDistanceAtMost(lower, lowerBare, maxDistance):
			score = 2
		case len(lowerBare) >= 5 && strings.Contains(lower, lowerBare):
			score = 3
		}
		if score < 0 {
			continue
		}
		selector := fileSelector(symbol)
		if previous, ok := best[selector]; !ok || score < previous {
			best[selector] = score
		}
	}
	candidates := make([]candidate, 0, len(best))
	for selector, score := range best {
		candidates = append(candidates, candidate{score: score, selector: selector})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score < candidates[j].score
		}
		return candidates[i].selector < candidates[j].selector
	})
	var out []string
	for _, c := range candidates {
		if len(out) == limit {
			break
		}
		out = append(out, c.selector)
	}
	return out
}

// suggestionBareName is the declaration name a selector ends with:
// "(*Manager).classify", "pkg.Manager.classify", "dir/file.go:classify" and
// "example.com/m/pkg::classify" all give "classify".
func suggestionBareName(query string) string {
	query = strings.TrimSpace(query)
	if _, name, ok := strings.Cut(query, ".go:"); ok {
		query = name
	}
	if _, member, ok := strings.Cut(query, "::"); ok {
		query = member
	}
	if position := strings.LastIndex(query, "/"); position >= 0 {
		query = query[position+1:]
	}
	if position := strings.LastIndex(query, "."); position >= 0 {
		query = query[position+1:]
	}
	return strings.Trim(query, "()* ")
}

// editDistanceAtMost reports whether the Levenshtein distance of a and b is
// at most limit.
func editDistanceAtMost(a, b string, limit int) bool {
	if a == b {
		return true
	}
	if d := len(a) - len(b); d > limit || -d > limit {
		return false
	}
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		rowMin := current[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
			rowMin = min(rowMin, current[j])
		}
		if rowMin > limit {
			return false
		}
		previous, current = current, previous
	}
	return previous[len(b)] <= limit
}
