package search

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// SkeletonFile returns only the declarations indexed for a repository-relative
// file. A missing file is an error, never a fallback to the repository skeleton.
func SkeletonFile(g *graph.Graph, file string) (string, error) {
	path := filepath.ToSlash(filepath.Clean(file))
	if file == "" || filepath.IsAbs(file) || path == ".." || strings.HasPrefix(path, "../") {
		return "", fmt.Errorf("skeleton: expected a repository-relative indexed file")
	}
	for _, indexed := range g.Files {
		if indexed.Path != path {
			continue
		}
		selected := &graph.Graph{}
		for _, symbol := range g.Symbols {
			if symbol.File == indexed.Path {
				selected.Symbols = append(selected.Symbols, symbol)
			}
		}
		header := fmt.Sprintf("package %s\n\n", indexed.PackageName)
		imports := skeletonImports(g, indexed.Path)
		if len(selected.Symbols) == 0 {
			return header + imports, nil
		}
		body := strings.TrimPrefix(Skeleton(selected), header)
		return header + imports + body, nil
	}
	return "", fmt.Errorf("skeleton: file %q is not indexed", file)
}

// Skeleton returns a pseudo-Go string representing the structural API of the repository
// with all function bodies stripped.
func Skeleton(g *graph.Graph) string {
	var sb strings.Builder

	// Group symbols by package
	pkgSymbols := make(map[string][]graph.SymbolNode)
	for _, sym := range g.Symbols {
		pkgSymbols[sym.PackageName] = append(pkgSymbols[sym.PackageName], sym)
	}

	// Sort packages
	var pkgs []string
	for pkg := range pkgSymbols {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)

	for _, pkg := range pkgs {
		fmt.Fprintf(&sb, "package %s\n\n", pkg)

		// Sort symbols by Name inside package
		syms := pkgSymbols[pkg]
		sort.Slice(syms, func(i, j int) bool {
			return syms[i].Name < syms[j].Name
		})

		for _, sym := range syms {
			switch sym.Kind {
			case graph.KindStruct:
				fmt.Fprintf(&sb, "type %s struct {\n", sym.Name)
				for _, emb := range sym.EmbeddedStructs {
					fmt.Fprintf(&sb, "\t%s\n", emb)
				}
				for _, field := range sym.StructFields {
					if field.Tag != "" {
						fmt.Fprintf(&sb, "\t%s %s `%s`\n", field.Name, field.Type, field.Tag)
					} else {
						fmt.Fprintf(&sb, "\t%s %s\n", field.Name, field.Type)
					}
				}
				sb.WriteString("}\n\n")
			case graph.KindInterface:
				fmt.Fprintf(&sb, "type %s interface {\n", sym.Name)
				// We don't have ordered interface methods, but we can print them
				var methods []string
				for m, sig := range sym.InterfaceMethods {
					methods = append(methods, fmt.Sprintf("\t%s%s", m, strings.TrimPrefix(sig, m)))
				}
				sort.Strings(methods)
				for _, m := range methods {
					sb.WriteString(m)
					sb.WriteString("\n")
				}
				sb.WriteString("}\n\n")
			case graph.KindFunction:
				fmt.Fprintf(&sb, "%s\n", sym.Signature)
			case graph.KindMethod:
				fmt.Fprintf(&sb, "%s\n", sym.Signature)
			}
		}
		sb.WriteString("\n// ")
		sb.WriteString(strings.Repeat("-", 40))
		sb.WriteString("\n\n")
	}

	return sb.String()
}

// skeletonImports renders a file's import block as gofmt groups it: standard
// library paths first, then the rest, each sorted, with any alias. It returns
// "" when the file imports nothing.
func skeletonImports(g *graph.Graph, file string) string {
	type spec struct{ alias, path string }
	var specs []spec
	seen := make(map[spec]bool)
	for _, edge := range g.Imports {
		item := spec{alias: edge.Alias, path: edge.ImportPath}
		if edge.FromFile == file && !seen[item] {
			seen[item] = true
			specs = append(specs, item)
		}
	}
	if len(specs) == 0 {
		return ""
	}
	standard := func(importPath string) bool {
		first, _, _ := strings.Cut(importPath, "/")
		return !strings.Contains(first, ".")
	}
	sort.Slice(specs, func(i, j int) bool {
		if si, sj := standard(specs[i].path), standard(specs[j].path); si != sj {
			return si
		}
		return specs[i].path < specs[j].path
	})
	render := func(item spec) string {
		if item.alias != "" {
			return item.alias + " " + strconv.Quote(item.path)
		}
		return strconv.Quote(item.path)
	}
	if len(specs) == 1 {
		return "import " + render(specs[0]) + "\n\n"
	}
	var sb strings.Builder
	sb.WriteString("import (\n")
	for i, item := range specs {
		if i > 0 && standard(specs[i-1].path) && !standard(item.path) {
			sb.WriteString("\n")
		}
		sb.WriteString("\t" + render(item) + "\n")
	}
	sb.WriteString(")\n\n")
	return sb.String()
}
