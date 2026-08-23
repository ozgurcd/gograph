// Package report generates the human-readable markdown reports for gograph.
package report

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// GenerateIndex produces the main GRAPH_REPORT.md file containing the summary and routing table.
func GenerateIndex(g *graph.Graph) string {
	var sb strings.Builder
	writeHeader(&sb, g, "GoGraph Report Index")
	writeSummary(&sb, g)
	writeRoutingTable(&sb)
	writeEntryPoints(&sb, g)
	writeUsageInstructions(&sb)
	return sb.String()
}

// GenerateSymbols produces graph-symbols.md (important files, important symbols, all packages).
func GenerateSymbols(g *graph.Graph) string {
	var sb strings.Builder
	writeHeader(&sb, g, "Symbols & Packages")
	writeImportantFiles(&sb, g)
	writeImportantSymbols(&sb, g)
	writePackages(&sb, g)
	return sb.String()
}

// GenerateDeps produces graph-deps.md (external dependencies, package imports).
func GenerateDeps(g *graph.Graph) string {
	var sb strings.Builder
	writeHeader(&sb, g, "Dependencies & Imports")
	writeDependencies(&sb, g)
	writeImports(&sb, g)
	return sb.String()
}

// GenerateRoutes produces graph-routes.md (HTTP routes).
func GenerateRoutes(g *graph.Graph) string {
	var sb strings.Builder
	writeHeader(&sb, g, "HTTP Routes")
	if len(g.Routes) == 0 {
		sb.WriteString("_No HTTP routes detected._\n\n")
		return sb.String()
	}
	sb.WriteString("| Method | Path | Handler | File | Line |\n|--------|------|---------|------|------|\n")
	for _, r := range g.Routes {
		fmt.Fprintf(&sb, "| `%s` | `%s` | `%s` | `%s` | %d |\n", r.Method, r.Path, r.Handler, r.File, r.Line)
	}
	sb.WriteString("\n")
	return sb.String()
}

// GenerateSQL produces graph-sql.md (SQL queries).
func GenerateSQL(g *graph.Graph) string {
	var sb strings.Builder
	writeHeader(&sb, g, "SQL Queries")
	if len(g.SQLs) == 0 {
		sb.WriteString("_No SQL queries detected._\n\n")
		return sb.String()
	}
	sb.WriteString("| Query | Function | File | Line |\n|-------|----------|------|------|\n")
	for _, sql := range g.SQLs {
		cleanQuery := strings.ReplaceAll(sql.Query, "\n", " ")
		cleanQuery = strings.ReplaceAll(cleanQuery, "\r", "")
		fmt.Fprintf(&sb, "| `%s` | `%s` | `%s` | %d |\n", cleanQuery, sql.Function, sql.File, sql.Line)
	}
	sb.WriteString("\n")
	return sb.String()
}

// GenerateErrors produces graph-errors.md (Errors and panics).
func GenerateErrors(g *graph.Graph) string {
	var sb strings.Builder
	writeHeader(&sb, g, "Errors & Panics")
	if len(g.Errors) == 0 {
		sb.WriteString("_No explicit errors/panics detected._\n\n")
		return sb.String()
	}
	sb.WriteString("| Message | Function | File | Line |\n|---------|----------|------|------|\n")
	for _, e := range g.Errors {
		fmt.Fprintf(&sb, "| `%s` | `%s` | `%s` | %d |\n", e.Message, e.Function, e.File, e.Line)
	}
	sb.WriteString("\n")
	return sb.String()
}

// GenerateConfig produces graph-config.md (Environment Variables).
func GenerateConfig(g *graph.Graph) string {
	var sb strings.Builder
	writeHeader(&sb, g, "Environment Configuration")
	if len(g.EnvReads) == 0 {
		sb.WriteString("_No environment variables detected._\n\n")
		return sb.String()
	}
	sb.WriteString("| Key | Accessor | File | Line | Function |\n|-----|----------|------|------|----------|\n")
	for _, ev := range g.EnvReads {
		fmt.Fprintf(&sb, "| `%s` | `%s` | `%s` | %d | `%s` |\n", ev.Key, ev.Accessor, ev.File, ev.Line, ev.Function)
	}
	sb.WriteString("\n")
	return sb.String()
}

// GenerateConcurrency produces graph-concurrency.md (goroutines, channels, sync).
func GenerateConcurrency(g *graph.Graph) string {
	var sb strings.Builder
	writeHeader(&sb, g, "Concurrency Primitives")
	if len(g.Concurrency) == 0 {
		sb.WriteString("_No concurrency primitives detected._\n\n")
		return sb.String()
	}
	sb.WriteString("| Kind | Function | File | Line | Detail |\n|------|----------|------|------|--------|\n")
	for _, c := range g.Concurrency {
		fmt.Fprintf(&sb, "| `%s` | `%s` | `%s` | %d | `%s` |\n", c.Kind, c.Function, c.File, c.Line, c.Detail)
	}
	sb.WriteString("\n")
	return sb.String()
}

// GenerateTests produces graph-tests.md (Test edges).
func GenerateTests(g *graph.Graph) string {
	var sb strings.Builder
	writeHeader(&sb, g, "Test Coverage Edges")
	if len(g.TestEdges) == 0 {
		sb.WriteString("_No test edges detected._\n\n")
		return sb.String()
	}
	// Deduplicate
	type testKey struct{ test, target, targetID string }
	seen := make(map[testKey]graph.TestEdge)
	var keys []testKey
	for _, te := range g.TestEdges {
		k := testKey{te.TestFunc, te.Target, te.TargetSymbolID}
		if _, ok := seen[k]; !ok {
			seen[k] = te
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].targetID != keys[j].targetID {
			return keys[i].targetID < keys[j].targetID
		}
		if keys[i].target != keys[j].target {
			return keys[i].target < keys[j].target
		}
		return keys[i].test < keys[j].test
	})

	sb.WriteString("| Target Symbol | Resolution | Tested By | File | Line |\n|---------------|------------|-----------|------|------|\n")
	for _, k := range keys {
		te := seen[k]
		target := te.Target
		if te.TargetSymbolID != "" {
			target = te.TargetSymbolID
		}
		resolution := string(te.Resolution)
		if resolution == "" {
			resolution = "ast_heuristic"
		}
		fmt.Fprintf(&sb, "| `%s` | `%s` | `%s` | `%s` | %d |\n", target, resolution, te.TestFunc, te.File, te.Line)
	}
	sb.WriteString("\n")
	return sb.String()
}

//
// Private helpers below
//

func writeHeader(sb *strings.Builder, g *graph.Graph, title string) {
	fmt.Fprintf(sb, "# %s\n\n", title)
	fmt.Fprintf(sb, "**Root:** `%s`  \n", g.Root)
	fmt.Fprintf(sb, "**Generated:** %s  \n", g.GeneratedAt.Format("2006-01-02 15:04:05 MST"))
	sb.WriteString("\n---\n\n")
}

func writeSummary(sb *strings.Builder, g *graph.Graph) {
	sb.WriteString("## 1. Summary\n\n")
	funcs, methods, structs, ifaces := symbolCounts(g)
	sb.WriteString("| Metric | Count |\n|--------|-------|\n")
	fmt.Fprintf(sb, "| Packages | %d |\n", len(g.Packages))
	fmt.Fprintf(sb, "| Files | %d |\n", len(g.Files))
	fmt.Fprintf(sb, "| Symbols | %d |\n", len(g.Symbols))
	fmt.Fprintf(sb, "| Functions | %d |\n", funcs)
	fmt.Fprintf(sb, "| Methods | %d |\n", methods)
	fmt.Fprintf(sb, "| Structs | %d |\n", structs)
	fmt.Fprintf(sb, "| Interfaces | %d |\n", ifaces)
	fmt.Fprintf(sb, "| Call edges | %d |\n", len(g.Calls))
	fmt.Fprintf(sb, "| Env var reads | %d |\n", len(g.EnvReads))
	sb.WriteString("\n")
}

func writeRoutingTable(sb *strings.Builder) {
	sb.WriteString("## 2. Structural Index\n\n")
	sb.WriteString("> **🚨 CRITICAL WARNING FOR AI AGENTS:** DO NOT READ `graph.json` DIRECTLY. It is a massive database file that will exhaust your context window and crash your session. Use the `gograph` CLI commands (e.g., `gograph query`, `gograph focus`) to extract targeted slices of data instead.\n\n")
	sb.WriteString("To save token context, the full graph report has been split into targeted files. Read only what you need:\n\n")
	sb.WriteString("| Category | File | Description |\n|----------|------|-------------|\n")
	sb.WriteString("| **Symbols** | [`graph-symbols.md`](graph-symbols.md) | Top files, heavily called symbols, and package layouts |\n")
	sb.WriteString("| **Deps** | [`graph-deps.md`](graph-deps.md) | `go.mod` tech stack and package import relationships |\n")
	sb.WriteString("| **Config** | [`graph-config.md`](graph-config.md) | Every `os.Getenv` and configuration read across the repo |\n")
	sb.WriteString("| **Concurrency** | [`graph-concurrency.md`](graph-concurrency.md) | Goroutines, channels, mutexes, and WaitGroups |\n")
	sb.WriteString("| **Routes** | [`graph-routes.md`](graph-routes.md) | HTTP REST API routes and handlers |\n")
	sb.WriteString("| **SQL** | [`graph-sql.md`](graph-sql.md) | Raw database queries mapped to functions |\n")
	sb.WriteString("| **Errors** | [`graph-errors.md`](graph-errors.md) | Custom errors and panics mapped to origin lines |\n")
	sb.WriteString("| **Tests** | [`graph-tests.md`](graph-tests.md) | Which test functions exercise which production symbols |\n\n")
}

func writeEntryPoints(sb *strings.Builder, g *graph.Graph) {
	sb.WriteString("## 3. Likely Entry Points\n\n")
	found := false
	for _, f := range g.Files {
		base := filepath.Base(f.Path)
		dir := filepath.Dir(f.Path)
		dirBase := filepath.Base(dir)
		parentBase := filepath.Base(filepath.Dir(dir))
		isCmdMain := base == "main.go" && (dirBase == "main" || parentBase == "cmd" || strings.HasPrefix(f.Path, "cmd/"))
		isMainPkg := f.PackageName == "main"
		if isCmdMain || isMainPkg {
			fmt.Fprintf(sb, "- `%s` (package `%s`)\n", f.Path, f.PackageName)
			found = true
		}
	}
	if !found {
		sb.WriteString("_No `main` package files detected._\n")
	}
	sb.WriteString("\n")
}

func writePackages(sb *strings.Builder, g *graph.Graph) {
	sb.WriteString("## Packages\n\n")
	sb.WriteString("| Package | Dir | Files | Symbols |\n|---------|-----|-------|---------|\n")
	symCount := make(map[string]int)
	for _, s := range g.Symbols {
		symCount[s.PackageName]++
	}
	for _, pkg := range g.Packages {
		fmt.Fprintf(sb, "| `%s` | `%s` | %d | %d |\n", pkg.Name, pkg.Dir, len(pkg.Files), symCount[pkg.Name])
	}
	sb.WriteString("\n")
}

func writeImportantFiles(sb *strings.Builder, g *graph.Graph) {
	sb.WriteString("## Important Files (top 20 by symbol+call density)\n\n")
	type scored struct {
		path  string
		score int
	}
	symPerFile := make(map[string]int)
	for _, s := range g.Symbols {
		symPerFile[s.File]++
	}
	callPerFile := make(map[string]int)
	for _, c := range g.Calls {
		callPerFile[c.File]++
	}
	scoredFiles := make([]scored, 0, len(g.Files))
	for _, f := range g.Files {
		scoredFiles = append(scoredFiles, scored{path: f.Path, score: symPerFile[f.Path] + callPerFile[f.Path]})
	}
	sort.Slice(scoredFiles, func(i, j int) bool {
		if scoredFiles[i].score != scoredFiles[j].score {
			return scoredFiles[i].score > scoredFiles[j].score
		}
		return scoredFiles[i].path < scoredFiles[j].path
	})
	if len(scoredFiles) > 20 {
		scoredFiles = scoredFiles[:20]
	}
	sb.WriteString("| File | Symbols | Calls |\n|------|---------|-------|\n")
	for _, sf := range scoredFiles {
		fmt.Fprintf(sb, "| `%s` | %d | %d |\n", sf.path, symPerFile[sf.path], callPerFile[sf.path])
	}
	sb.WriteString("\n")
}

func writeImportantSymbols(sb *strings.Builder, g *graph.Graph) {
	sb.WriteString("## Important Symbols (top 30 by outgoing calls)\n\n")
	type scored struct {
		sym   graph.SymbolNode
		calls int
	}
	callsPerSym := make(map[string]int)
	for _, c := range g.Calls {
		callsPerSym[c.CallerSymbolID]++
	}
	var funcs []scored
	for _, s := range g.Symbols {
		if s.Kind == graph.KindFunction || s.Kind == graph.KindMethod {
			funcs = append(funcs, scored{sym: s, calls: callsPerSym[s.ID]})
		}
	}
	sort.Slice(funcs, func(i, j int) bool {
		if funcs[i].calls != funcs[j].calls {
			return funcs[i].calls > funcs[j].calls
		}
		return funcs[i].sym.ID < funcs[j].sym.ID
	})
	if len(funcs) > 30 {
		funcs = funcs[:30]
	}
	sb.WriteString("| Symbol | Kind | File | Line | Calls out |\n|--------|------|------|------|-----------|\n")
	for _, sf := range funcs {
		name := sf.sym.Name
		if sf.sym.Receiver != "" {
			name = fmt.Sprintf("(%s).%s", sf.sym.Receiver, sf.sym.Name)
		}
		fmt.Fprintf(sb, "| `%s` | %s | `%s` | %d | %d |\n", name, sf.sym.Kind, sf.sym.File, sf.sym.Line, sf.calls)
	}
	sb.WriteString("\n")
}

func writeDependencies(sb *strings.Builder, g *graph.Graph) {
	if len(g.Dependencies) == 0 {
		return
	}
	sb.WriteString("## External Dependencies (Tech Stack)\n\n")
	sb.WriteString("| Module | Version |\n|--------|---------|\n")
	for _, dep := range g.Dependencies {
		fmt.Fprintf(sb, "| `%s` | `%s` |\n", dep.Module, dep.Version)
	}
	sb.WriteString("\n")
}

func writeImports(sb *strings.Builder, g *graph.Graph) {
	sb.WriteString("## Package Imports\n\n")
	type pkgImport struct{ pkg, path string }
	seen := make(map[pkgImport]bool)
	var pairs []pkgImport
	for _, imp := range g.Imports {
		pi := pkgImport{imp.FromPackage, imp.ImportPath}
		if !seen[pi] {
			seen[pi] = true
			pairs = append(pairs, pi)
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].pkg != pairs[j].pkg {
			return pairs[i].pkg < pairs[j].pkg
		}
		return pairs[i].path < pairs[j].path
	})
	byPkg := make(map[string][]string)
	for _, pi := range pairs {
		byPkg[pi.pkg] = append(byPkg[pi.pkg], pi.path)
	}
	pkgs := make([]string, 0, len(byPkg))
	for k := range byPkg {
		pkgs = append(pkgs, k)
	}
	sort.Strings(pkgs)
	sb.WriteString("| Package | Imports |\n|---------|--------|\n")
	for _, pkg := range pkgs {
		imps := byPkg[pkg]
		fmt.Fprintf(sb, "| `%s` | %s |\n", pkg, strings.Join(wrap(imps, "`"), ", "))
	}
	sb.WriteString("\n")
}

func writeUsageInstructions(sb *strings.Builder) {
	sb.WriteString("## AI Assistant / Coding Agent Usage\n\n")
	sb.WriteString("Add the following to your AI assistant's system prompt, project instructions, or context file:\n\n")
	sb.WriteString("> Before answering architecture, dependency, or 'where is X?' questions about this\n")
	sb.WriteString("> repository, read `.gograph/GRAPH_REPORT.md` first. Use it as the repo map before\n")
	sb.WriteString("> searching raw files. Use `gograph query` and `gograph callers` for symbol lookup.\n")
}

func symbolCounts(g *graph.Graph) (funcs, methods, structs, ifaces int) {
	for _, s := range g.Symbols {
		switch s.Kind {
		case graph.KindFunction:
			funcs++
		case graph.KindMethod:
			methods++
		case graph.KindStruct:
			structs++
		case graph.KindInterface:
			ifaces++
		}
	}
	return
}

func wrap(ss []string, delim string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = delim + s + delim
	}
	return out
}
