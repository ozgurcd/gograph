package search

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"time"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/scanner"
)

// ChangeStatus classifies a symbol relative to the current graph.
type ChangeStatus string

const (
	// ChangeModified means the symbol's source file is newer than graph.json.
	// The symbol may or may not have changed — agents should inspect it.
	ChangeModified ChangeStatus = "modified"
	// ChangeNew means the declaration was found in a changed file but is not
	// recorded in graph.json — it was likely added since the graph was persisted.
	ChangeNew ChangeStatus = "new"
	// ChangeDeleted means a symbol from graph.json lives in a file that no
	// longer exists on disk — it was likely removed.
	ChangeDeleted ChangeStatus = "deleted"
)

// ChangedSymbol is a symbol affected by source changes since the graph was persisted.
type ChangedSymbol struct {
	// Name is the symbol name or declaration identifier.
	Name string `json:"name"`
	// File is the source file path.
	File string `json:"file"`
	// Line is the line number (0 for deleted symbols where the file is gone).
	Line int `json:"line,omitempty"`
	// Status classifies how this symbol was affected.
	Status ChangeStatus `json:"status"`
}

// ChangesResult is returned by Changes.
type ChangesResult struct {
	// GraphAge is when graph.json was last generated.
	GraphAge time.Time `json:"graph_age"`
	// ChangedFiles lists source files newer than the graph.
	ChangedFiles []string `json:"changed_files"`
	// Symbols lists all symbols affected by the source changes.
	Symbols []ChangedSymbol `json:"symbols"`
}

// Changes compares the current source tree against graph.json to report what
// has likely changed since the graph was persisted. It identifies:
//   - Symbols in files newer than the graph (ChangeModified)
//   - Top-level declarations in changed files not found in the graph (ChangeNew)
//   - Graph symbols whose source files no longer exist (ChangeDeleted)
//
// root is the absolute path to the repository root.
func Changes(g *graph.Graph, root string) *ChangesResult {
	graphTime := g.GeneratedAt
	result := &ChangesResult{GraphAge: graphTime}

	// Step 1: Scan the same source set used by graph construction. This keeps
	// generated, vendored, and gitignored files out of freshness reports.
	changedFiles := make(map[string]bool)
	existingFiles := make(map[string]bool)
	files, _ := scanner.Walk(root)
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		rel = filepath.Clean(rel)
		existingFiles[rel] = true
		if info.ModTime().After(graphTime) {
			changedFiles[rel] = true
			result.ChangedFiles = append(result.ChangedFiles, rel)
		}
	}
	sortStrings(result.ChangedFiles)

	// Step 2: Build a set of graph symbols keyed by (name, file).
	type symKey struct{ name, file string }
	graphSymbols := make(map[symKey]bool)
	for _, s := range g.Symbols {
		rel := graphFileRelative(root, s.File)
		graphSymbols[symKey{s.Name, rel}] = true
		graphSymbols[symKey{s.Name, s.File}] = true
	}

	// Step 3: For each changed file, collect graph symbols (modified) and
	// parse for new declarations not in the graph.
	seenModified := make(map[symKey]bool)
	for _, s := range g.Symbols {
		rel := graphFileRelative(root, s.File)
		if !changedFiles[rel] && !changedFiles[s.File] {
			continue
		}
		key := symKey{s.Name, rel}
		if seenModified[key] {
			continue
		}
		seenModified[key] = true
		result.Symbols = append(result.Symbols, ChangedSymbol{
			Name:   s.Name,
			File:   rel,
			Line:   s.Line,
			Status: ChangeModified,
		})
	}

	// Parse changed files for NEW top-level declarations.
	fset := token.NewFileSet()
	for relPath := range changedFiles {
		absPath := filepath.Join(root, relPath)
		f, err := parser.ParseFile(fset, absPath, nil, 0)
		if err != nil {
			continue
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				name := d.Name.Name
				key := symKey{name, relPath}
				if !graphSymbols[key] && !seenModified[key] {
					result.Symbols = append(result.Symbols, ChangedSymbol{
						Name:   name,
						File:   relPath,
						Line:   fset.Position(d.Pos()).Line,
						Status: ChangeNew,
					})
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch ts := spec.(type) {
					case *ast.TypeSpec:
						name := ts.Name.Name
						key := symKey{name, relPath}
						if !graphSymbols[key] && !seenModified[key] {
							result.Symbols = append(result.Symbols, ChangedSymbol{
								Name:   name,
								File:   relPath,
								Line:   fset.Position(ts.Pos()).Line,
								Status: ChangeNew,
							})
						}
					}
				}
			}
		}
	}

	// Step 4: Detect deleted symbols — graph symbols whose files are gone.
	for _, s := range g.Symbols {
		rel := graphFileRelative(root, s.File)
		path := s.File
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		if _, statErr := os.Stat(path); statErr == nil {
			continue // file exists
		}
		if existingFiles[rel] {
			continue
		}
		result.Symbols = append(result.Symbols, ChangedSymbol{
			Name:   s.Name,
			File:   s.File,
			Line:   0,
			Status: ChangeDeleted,
		})
	}

	return result
}

func graphFileRelative(root, path string) string {
	if filepath.IsAbs(path) {
		if rel, err := filepath.Rel(root, path); err == nil {
			return filepath.Clean(rel)
		}
	}
	return filepath.Clean(path)
}
