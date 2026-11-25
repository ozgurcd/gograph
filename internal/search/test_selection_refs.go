package search

import (
	"regexp"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// declarationUsers returns the functions and methods that use a changed
// non-function declaration, with the certainty of each use:
//
//   - a type's own methods (exact);
//   - type-resolved field uses of a struct and uses of a package variable or
//     constant, recorded by precise builds (exact);
//   - composite literals of the type (possible: recorded by bare name) and
//     signatures that spell the type (possible: matched by name).
//
// AST graphs record no variable or constant uses, so those select nothing.
func (snapshot *Snapshot) declarationUsers(symbol graph.SymbolNode) map[string]string {
	g := snapshot.g
	users := map[string]string{}
	add := func(id, resolution string) {
		if id == "" || id == symbol.ID {
			return
		}
		if previous, ok := users[id]; !ok || previous == "possible" && resolution == "exact" {
			users[id] = resolution
		}
	}
	pkg := stableIDPackagePath(symbol.ID)
	functions := functionIndex(g)
	switch symbol.Kind {
	case graph.KindStruct, graph.KindInterface, graph.KindType:
		identifier := regexp.MustCompile(`(^|[^.\w])` + regexp.QuoteMeta(symbol.Name) + `\b`)
		qualified := regexp.MustCompile(`\b` + regexp.QuoteMeta(symbol.PackageName+"."+symbol.Name) + `\b`)
		for _, candidate := range g.Symbols {
			if candidate.Kind != graph.KindFunction && candidate.Kind != graph.KindMethod {
				continue
			}
			samePackage := stableIDPackagePath(candidate.ID) == pkg
			if candidate.Kind == graph.KindMethod && samePackage && receiverBaseName(candidate.Receiver) == symbol.Name {
				add(candidate.ID, "exact")
				continue
			}
			if samePackage && identifier.MatchString(candidate.Signature) || !samePackage && qualified.MatchString(candidate.Signature) {
				add(candidate.ID, "possible")
			}
		}
		importers := filesImporting(g, pkg)
		packages := filePackages(g)
		for _, literal := range g.Literals {
			if literal.TypeName != symbol.Name {
				continue
			}
			if packages[literal.File] != pkg && !importers[literal.File] {
				continue
			}
			add(functions[literal.File+"\x00"+literal.Function], "possible")
		}
		for _, use := range g.FieldUses {
			if use.TypeName == symbol.Name && use.Package == pkg {
				add(functions[use.File+"\x00"+use.Function], "exact")
			}
		}
	case graph.KindVar, graph.KindConst:
		for _, use := range g.VariableUses {
			if use.SymbolID == symbol.ID {
				add(functions[use.File+"\x00"+use.Function], "exact")
			}
		}
	}
	return users
}

// functionIndex maps "file\x00name" to a function or method ID. Uses name
// their enclosing function as "Run", "(*Manager).start" or, for variable
// uses, the bare method name "start"; a bare name shared by several
// declarations in one file maps to "".
func functionIndex(g *graph.Graph) map[string]string {
	index := make(map[string]string, len(g.Symbols))
	bare := map[string]string{}
	for _, symbol := range g.Symbols {
		switch symbol.Kind {
		case graph.KindFunction:
			index[symbol.File+"\x00"+symbol.Name] = symbol.ID
		case graph.KindMethod:
			index[symbol.File+"\x00("+symbol.Receiver+")."+symbol.Name] = symbol.ID
			key := symbol.File + "\x00" + symbol.Name
			if previous, ok := bare[key]; ok && previous != symbol.ID {
				bare[key] = ""
			} else {
				bare[key] = symbol.ID
			}
		}
	}
	for key, id := range bare {
		if _, ok := index[key]; !ok {
			index[key] = id
		}
	}
	return index
}

func filesImporting(g *graph.Graph, importPath string) map[string]bool {
	files := map[string]bool{}
	for _, edge := range g.Imports {
		if edge.ImportPath == importPath {
			files[edge.FromFile] = true
		}
	}
	return files
}

func filePackages(g *graph.Graph) map[string]string {
	packages := make(map[string]string, len(g.Files))
	for _, symbol := range g.Symbols {
		if _, ok := packages[symbol.File]; !ok {
			packages[symbol.File] = stableIDPackagePath(symbol.ID)
		}
	}
	return packages
}

// processTests maps a main package's import path to its test functions that
// start a process (exec.Command or exec.CommandContext, directly or through
// helpers in the same package): such tests usually run the package's built
// binary, so a change reaching its main function may affect them.
func (snapshot *Snapshot) processTests() map[string][]graph.SymbolNode {
	g := snapshot.g
	mains := map[string]bool{}
	byPackage := map[string][]graph.SymbolNode{}
	byName := map[string]string{}
	for _, symbol := range g.Symbols {
		pkg := stableIDPackagePath(symbol.ID)
		if symbol.Kind == graph.KindFunction && symbol.Name == "main" && symbol.PackageName == "main" && !isTestFile(symbol.File) {
			mains[pkg] = true
		}
		if symbol.Kind == graph.KindFunction || symbol.Kind == graph.KindMethod {
			byName[pkg+"\x00"+symbol.Name] = symbol.ID
		}
	}
	calls := map[string][]graph.CallEdge{}
	for _, call := range g.Calls {
		calls[call.CallerSymbolID] = append(calls[call.CallerSymbolID], call)
	}
	startsProcess := map[string]bool{}
	var reaches func(id string, seen map[string]bool) bool
	reaches = func(id string, seen map[string]bool) bool {
		if done, ok := startsProcess[id]; ok {
			return done
		}
		if seen[id] {
			return false
		}
		seen[id] = true
		pkg := stableIDPackagePath(id)
		for _, call := range calls[id] {
			if call.CalleeRaw == "exec.Command" || call.CalleeRaw == "exec.CommandContext" {
				startsProcess[id] = true
				return true
			}
			next := call.CalleeSymbolID
			if next == "" && !strings.Contains(call.CalleeRaw, ".") {
				next = byName[pkg+"\x00"+call.CalleeRaw]
			}
			if next != "" && stableIDPackagePath(next) == pkg && reaches(next, seen) {
				startsProcess[id] = true
				return true
			}
		}
		return false
	}
	for _, symbol := range g.Symbols {
		pkg := stableIDPackagePath(symbol.ID)
		if !mains[pkg] || !isTestEntrySymbol(symbol) {
			continue
		}
		if reaches(symbol.ID, map[string]bool{}) {
			byPackage[pkg] = append(byPackage[pkg], symbol)
		}
	}
	for pkg := range byPackage {
		sort.Slice(byPackage[pkg], func(i, j int) bool { return byPackage[pkg][i].ID < byPackage[pkg][j].ID })
	}
	return byPackage
}

// processTestStates returns, for each upstream main function in best whose
// package has process-starting tests, those tests as possible matches one
// step past main.
func processTestStates(processTests map[string][]graph.SymbolNode, best map[string]reverseAttributionState) []struct {
	test  graph.SymbolNode
	state reverseAttributionState
} {
	var out []struct {
		test  graph.SymbolNode
		state reverseAttributionState
	}
	ids := make([]string, 0, len(best))
	for id := range best {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		pkg, name, ok := strings.Cut(id, "::")
		if !ok || name != "main" {
			continue
		}
		for _, test := range processTests[pkg] {
			out = append(out, struct {
				test  graph.SymbolNode
				state reverseAttributionState
			}{test, reverseAttributionState{id: test.ID, resolution: "possible", depth: best[id].depth + 1, next: id}})
		}
	}
	return out
}

// ResolveDeclarations returns the type, variable and constant declarations
// that term names exactly (Name, pkg.Name, a file selector or a stable ID),
// optionally limited to packageName.
func ResolveDeclarations(g *graph.Graph, term, packageName string) []graph.SymbolNode {
	if g == nil {
		return nil
	}
	file, name, hasFile := splitFileSelector(term)
	var out []graph.SymbolNode
	for _, symbol := range g.Symbols {
		switch symbol.Kind {
		case graph.KindStruct, graph.KindInterface, graph.KindType, graph.KindVar, graph.KindConst:
		default:
			continue
		}
		if packageName != "" && symbol.PackageName != packageName {
			continue
		}
		if hasFile {
			if symbol.Name == name && (symbol.File == file || strings.HasSuffix(symbol.File, "/"+file)) {
				out = append(out, symbol)
			}
			continue
		}
		if matchSymbolExact(symbol, term) {
			out = append(out, symbol)
		}
	}
	return out
}

// splitFileSelector splits "dir/file.go:Name".
func splitFileSelector(term string) (file, name string, ok bool) {
	file, name, ok = strings.Cut(term, ".go:")
	if !ok {
		return "", "", false
	}
	return file + ".go", name, true
}
