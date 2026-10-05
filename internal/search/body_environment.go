package search

import (
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// bodyEnvironment retains known keys and explicitly labels direct reads whose
// key cannot be named. It inspects graph evidence, never the process environment.
func bodyEnvironment(g *graph.Graph, names []string) []string {
	var result []string
	seen := make(map[string]bool)
	add := func(key string) {
		if !seen[key] {
			seen[key] = true
			result = append(result, key)
		}
	}
	for _, name := range names {
		for _, sym := range FindSymbols(g, name) {
			for _, call := range sourceCallSites(g.Calls) {
				if call.File != sym.File || call.Line < sym.Line || call.Line > sym.EndLine {
					continue
				}
				accessor := ""
				for _, imp := range g.Imports {
					if imp.FromFile != call.File || imp.ImportPath != "os" {
						continue
					}
					alias := imp.Alias
					if alias == "" {
						alias = "os"
					}
					prefix := alias + "."
					if alias == "." {
						prefix = ""
					}
					for _, method := range []string{"Getenv", "LookupEnv"} {
						if call.CalleeRaw == prefix+method && (call.CalleeSymbolID == "" || strings.HasPrefix(call.CalleeSymbolID, "os::")) {
							accessor = "os." + method
						}
					}
				}
				if accessor == "" {
					continue
				}
				known := false
				for _, env := range g.EnvReads {
					if env.File == call.File && env.Line == call.Line {
						add(env.Key)
						known = true
					}
				}
				if !known {
					add(accessor + " (key not statically known)")
				}
			}
		}
	}
	return result
}
