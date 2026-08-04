package search

import (
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

// ReadModulePath returns the module path declared in <root>/go.mod, or "" if
// the file is missing/unreadable/malformed. Used by `coupling --internal-only`
// (and any other command that wants to scope output to the project's own
// packages). Tolerant of comments and stray whitespace per Go's module-file
// grammar.
func ReadModulePath(root string) string {
	moduleFiles, err := sourcefs.Open(root)
	if err != nil {
		return ""
	}
	defer func() { _ = moduleFiles.Close() }()
	data, err := moduleFiles.ReadRegularFile("go.mod")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "module ") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "module"))
		rest = strings.Trim(rest, "\"`")
		if i := strings.Index(rest, "//"); i >= 0 {
			rest = strings.TrimSpace(rest[:i])
		}
		return rest
	}
	return ""
}

// PackageCoupling holds fan-in and fan-out metrics for a single package.
type PackageCoupling struct {
	// Package is the import path of the package (e.g. "github.com/foo/bar/internal/auth").
	Package string `json:"package"`
	// FanOut is the number of distinct packages this package imports.
	FanOut int `json:"fan_out"`
	// FanIn is the number of distinct packages that import this package.
	FanIn int `json:"fan_in"`
	// Instability is FanOut / (FanIn + FanOut), range [0.0, 1.0].
	// -1.0 is reported when FanIn + FanOut == 0 (isolated package).
	Instability float64 `json:"instability"`
}

// CouplingOptions controls which packages are included in the report.
type CouplingOptions struct {
	// IncludeStdlib keeps standard-library packages (fmt, net/http, etc.)
	// in the report. Default false because users asking "how coupled is my
	// code?" almost never care about stdlib coupling — it just clutters
	// the output. Detected by heuristic: first import-path segment has no
	// dot in it (stdlib packages never use a domain in their import path).
	IncludeStdlib bool
	// ModuleOnly, when non-empty, restricts the report to packages whose
	// import path starts with this module prefix (typically derived from
	// the project's go.mod "module ..." directive). When set it
	// effectively gives "show me only MY packages" — strictly stronger
	// than IncludeStdlib=false because it also excludes third-party
	// dependencies (github.com/foo/..., golang.org/..., etc.).
	ModuleOnly string
}

// isStdlibPackage heuristically reports whether an import path looks like
// a Go standard-library package. The convention: stdlib paths are
// dotless single names or slash-separated paths whose first segment
// contains no dot ("fmt", "net/http", "crypto/sha256"). Third-party and
// internal packages always start with a domain ("github.com/...",
// "golang.org/...", "identuum.ai/...").
func isStdlibPackage(importPath string) bool {
	if importPath == "" {
		return false
	}
	first := importPath
	if i := strings.Index(importPath, "/"); i >= 0 {
		first = importPath[:i]
	}
	return !strings.Contains(first, ".")
}

// Coupling computes fan-in, fan-out, and instability for every package in the
// graph. Results are sorted by instability descending (most unstable first).
// Pass a term to filter results by package name substring (case-insensitive).
// opts controls stdlib/third-party filtering — see CouplingOptions.
func Coupling(g *graph.Graph, term string, opts CouplingOptions) []PackageCoupling {
	tl := strings.ToLower(term)
	moduleLower := strings.ToLower(opts.ModuleOnly)

	// gograph's import edges use INCONSISTENT package identifiers:
	//   - imp.FromPackage is the importer's *short* name ("license")
	//   - imp.ImportPath is the imported's *full* path
	//     ("identuum.ai/internal/license" or "fmt")
	// Without canonicalisation, "license" and
	// "identuum.ai/internal/license" appear as two separate packages in
	// the report, fan-out lives on the short name and fan-in on the full
	// path, and any filter that targets "stdlib vs internal" misclassifies
	// half the project's own packages as stdlib (because their short name
	// has no dot).
	//
	// Build short-name → full-import-path lookup from the symbol table.
	// SymbolNode.ID is "fullpath::Name", so the prefix is the full import
	// path for any package the parser saw in source. stdlib and third-party
	// packages are never importers in our graph, so they're never in this
	// map and pass through unchanged.
	shortToFull := make(map[string]string)
	for _, s := range g.Symbols {
		if s.PackageName == "" {
			continue
		}
		if i := strings.Index(s.ID, "::"); i >= 0 {
			shortToFull[s.PackageName] = s.ID[:i]
		}
	}
	canonical := func(pkg string) string {
		if full, ok := shortToFull[pkg]; ok {
			return full
		}
		return pkg
	}

	includePkg := func(pkg string) bool {
		if pkg == "" {
			return false
		}
		if !opts.IncludeStdlib && isStdlibPackage(pkg) {
			return false
		}
		if moduleLower != "" && !strings.HasPrefix(strings.ToLower(pkg), moduleLower) {
			return false
		}
		return true
	}

	// Build a set of all known packages — both importers and importees.
	allPkgs := make(map[string]bool)
	for _, imp := range g.Imports {
		if from := canonical(imp.FromPackage); from != "" && includePkg(from) {
			allPkgs[from] = true
		}
		// Also include packages that are only imported (leaf/stable packages
		// that import nothing themselves still deserve fan-in metrics).
		if to := canonical(imp.ImportPath); to != "" && includePkg(to) {
			allPkgs[to] = true
		}
	}

	// fan-out: how many distinct packages does pkg import?
	// fan-in: how many distinct packages import pkg?
	fanOut := make(map[string]map[string]bool) // pkg -> set of imported paths
	fanIn := make(map[string]map[string]bool)  // pkg -> set of packages that import it

	for _, imp := range g.Imports {
		from := canonical(imp.FromPackage)
		to := canonical(imp.ImportPath)
		if from == "" || to == "" {
			continue
		}
		// Apply the same package filter to edges so a filtered-in
		// package's metrics don't inflate from filtered-out edges. E.g.
		// with default options, "service" should not get fan-out credit
		// for importing "fmt" — fmt is invisible in this report.
		if !includePkg(from) || !includePkg(to) {
			continue
		}
		if fanOut[from] == nil {
			fanOut[from] = make(map[string]bool)
		}
		fanOut[from][to] = true

		if fanIn[to] == nil {
			fanIn[to] = make(map[string]bool)
		}
		fanIn[to][from] = true
	}

	// Collect results for all known packages.
	var results []PackageCoupling
	for pkg := range allPkgs {
		if tl != "" && !strings.Contains(strings.ToLower(pkg), tl) {
			continue
		}
		fo := len(fanOut[pkg])
		fi := len(fanIn[pkg])
		instability := -1.0
		if fo+fi > 0 {
			instability = float64(fo) / float64(fo+fi)
		}
		results = append(results, PackageCoupling{
			Package:     pkg,
			FanOut:      fo,
			FanIn:       fi,
			Instability: instability,
		})
	}

	// Sort by instability descending; break ties by fan-out descending.
	sort.Slice(results, func(i, j int) bool {
		if results[i].Instability != results[j].Instability {
			return results[i].Instability > results[j].Instability
		}
		return results[i].FanOut > results[j].FanOut
	})

	return results
}
