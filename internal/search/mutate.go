package search

import (
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// Mutate searches for functions that mutate the given struct field.
// The query can be "Status" or "User.Status".
func Mutate(g *graph.Graph, query string) []Result {
	parts := strings.Split(query, ".")
	field := query
	typeName := ""
	if len(parts) > 1 {
		field = parts[len(parts)-1]
		typeName = strings.Join(parts[:len(parts)-1], ".")
	}
	field = strings.ToLower(field)

	var results []Result
	for _, m := range g.Mutations {
		if strings.ToLower(m.Field) == field && mutationTypeMatches(m.TypeName, typeName) {
			detail := "mutates field " + m.Field
			if m.TypeName != "" {
				detail = "mutates field " + m.TypeName + "." + m.Field
			}
			// Indirect mutations carry Via — the name of the mutating
			// method or "chan<-" for sends. Surface it so the reader can
			// tell `s.field = x` from `s.field.Store(x)` without opening
			// the file.
			if m.Via != "" {
				detail += " via " + m.Via
			}
			results = append(results, Result{
				Kind:   "mutation",
				Name:   m.Function,
				File:   m.File,
				Line:   m.Line,
				Detail: detail,
				Score:  1,
			})
		}
	}

	sortResults(results)
	return results
}

func mutationTypeMatches(actual, requested string) bool {
	if requested == "" {
		return true
	}
	if actual == "" {
		return false
	}
	normalize := func(value string) string {
		value = strings.TrimSpace(value)
		value = strings.TrimPrefix(value, "*")
		value = strings.Trim(value, "()")
		return strings.ToLower(value)
	}
	a := normalize(actual)
	r := normalize(requested)
	return a == r || strings.HasSuffix(a, "."+r) || strings.HasSuffix(r, "."+a)
}
