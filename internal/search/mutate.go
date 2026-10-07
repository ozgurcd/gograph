package search

import (
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// MutationResolutionDescription names the boundary of indexed mutation evidence.
const MutationResolutionDescription = "Known fields without indexed mutation sites report a mutation-resolution limit: writes through arbitrary pointer arguments (including database Scan), aliases, and reflective code cannot be resolved by this index. Empty mutation evidence is not proof that the field is immutable."

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

	if len(results) == 0 {
		for _, s := range g.Symbols {
			if !mutationTypeMatches(s.Name, typeName) {
				continue
			}
			for _, f := range s.StructFields {
				if strings.ToLower(f.Name) == field {
					results = append(results, Result{Kind: "limit", Name: s.Name + "." + f.Name, File: s.File, Line: s.Line,
						Detail: "mutation-resolution limit: mutation sites for this field cannot be resolved; no indexed assignment or recognized mutating method was found. Writes through arbitrary pointer arguments (including database Scan), aliases, and reflective code are not resolved.", Score: 1})
				}
			}
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
