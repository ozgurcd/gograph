package search

import (
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// Usages finds every place a named type is referenced in the codebase:
// function/method parameter types, return types, struct fields, and interface
// method signatures. Case-insensitive. Run before changing an interface or
// type — the results show the full blast radius beyond just the implementers.
func Usages(g *graph.Graph, typeName string) []Result {
	nl := strings.ToLower(typeName)
	var results []Result

	parts := strings.Split(nl, ".")
	hasDot := len(parts) == 2

	for _, sym := range g.Symbols {
		targetType := nl
		if hasDot {
			if strings.ToLower(sym.PackageName) == parts[0] {
				targetType = parts[1]
			} else {
				targetType = parts[0] + "." + parts[1]
			}
		}

		switch sym.Kind {
		case graph.KindFunction, graph.KindMethod:
			if sym.Signature == "" {
				continue
			}
			if !containsTypeName(strings.ToLower(sym.Signature), targetType) {
				continue
			}
			// Skip the definition of the type itself if it appears in its own sig.
			if strings.EqualFold(sym.Name, typeName) {
				continue
			}
			detail := classifySignatureUsage(sym.Signature, typeName)
			results = append(results, Result{
				Kind:   string(sym.Kind),
				Name:   sym.ID,
				File:   sym.File,
				Line:   sym.Line,
				Detail: detail,
				Score:  10,
			})

		case graph.KindStruct:
			for _, field := range sym.StructFields {
				if containsTypeName(strings.ToLower(field.Type), targetType) {
					results = append(results, Result{
						Kind:   "field",
						Name:   sym.Name + "." + field.Name,
						File:   sym.File,
						Line:   sym.Line,
						Detail: "field type: " + field.Type,
						Score:  10,
					})
				}
			}

		case graph.KindInterface:
			for methodName, methodSig := range sym.InterfaceMethods {
				if containsTypeName(strings.ToLower(methodSig), targetType) {
					results = append(results, Result{
						Kind:   "iface_method",
						Name:   sym.Name + "." + methodName,
						File:   sym.File,
						Line:   sym.Line,
						Detail: "interface method: " + methodSig,
						Score:  10,
					})
				}
			}
		}
	}

	// Composite construction is a type usage too. Keep the focused literals
	// command, but merge its rows here so a broad usages query does not omit
	// dependency-injection/config structs initialized as Foo{...}.
	results = append(results, Literals(g, typeName)...)

	sortResults(results)
	return results
}

// containsTypeName reports whether s contains typeName as a standalone type
// identifier — not as a substring of a longer identifier like TypeNameImpl.
func containsTypeName(s, typeName string) bool {
	for len(s) >= len(typeName) {
		idx := strings.Index(s, typeName)
		if idx < 0 {
			return false
		}
		end := idx + len(typeName)
		okBefore := idx == 0 || !isIdentRune(s[idx-1])
		okAfter := end >= len(s) || !isIdentRune(s[end])
		if okBefore && okAfter {
			return true
		}
		s = s[idx+1:]
	}
	return false
}

func isIdentRune(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// classifySignatureUsage returns a human-readable label describing where
// typeName appears within a function signature string.
func classifySignatureUsage(sig, typeName string) string {
	nl := strings.ToLower(typeName)

	// Find the opening paren of the parameter list. For methods the signature
	// is "func (Recv).Name(params) returns"; for functions "func Name(params)".
	// We want the paren that opens the named parameter list, not the receiver.
	// Strategy: find the last '(' before we start counting returns.
	paramStart := strings.LastIndex(sig[:max(strings.Index(sig, ")")+1, 1)], "(")

	inParams := false
	inReturn := false

	if paramStart >= 0 {
		// Find the matching closing paren at the same depth.
		depth := 0
		paramEnd := -1
		for i := paramStart; i < len(sig); i++ {
			if sig[i] == '(' {
				depth++
			} else if sig[i] == ')' {
				depth--
				if depth == 0 {
					paramEnd = i
					break
				}
			}
		}
		if paramEnd >= 0 {
			paramsPart := strings.ToLower(sig[paramStart : paramEnd+1])
			returnPart := strings.ToLower(sig[paramEnd+1:])
			inParams = containsTypeName(paramsPart, nl)
			inReturn = containsTypeName(returnPart, nl)
		}
	}

	switch {
	case inParams && inReturn:
		return "param and return type"
	case inParams:
		return "param type"
	case inReturn:
		return "return type"
	default:
		return "in signature: " + sig
	}
}
