package search

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

// ComplexityResult holds the cyclomatic complexity score for a single function.
type ComplexityResult struct {
	// Symbol is the fully-qualified symbol ID (e.g. "(Server).Start").
	Symbol string `json:"symbol"`
	// File is the source file containing the function.
	File string `json:"file"`
	// Line is the line number of the function declaration.
	Line int `json:"line"`
	// Score is the cyclomatic complexity estimate, or -1 when repository source
	// could not be read and parsed safely.
	// A score of 1 means a single straight-line path (no branches).
	// Each branch-inducing construct adds 1.
	Score int `json:"score"`
	// Label is a human-readable severity: "LOW", "MEDIUM", "HIGH", "VERY HIGH",
	// or "UNKNOWN" when Score is -1.
	Label string `json:"label"`
}

// complexityLabel converts a numeric score to a severity label using
// widely-accepted McCabe cyclomatic complexity thresholds.
func complexityLabel(score int) string {
	switch {
	case score <= 5:
		return "LOW"
	case score <= 10:
		return "MEDIUM"
	case score <= 20:
		return "HIGH"
	default:
		return "VERY HIGH"
	}
}

// countComplexity walks an AST function body and counts branch-inducing nodes.
// Starting value is 1 (the function itself always has at least one path).
// Each of the following increments the score by 1:
//   - if / else if
//   - for / range
//   - switch case clause (each non-default case)
//   - select case clause (each non-default case)
//   - && and || binary expressions (short-circuit paths)
//   - return inside a non-tail position (early return)
func countComplexity(body *ast.BlockStmt) int {
	if body == nil {
		return 1
	}
	score := 1
	ast.Inspect(body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.IfStmt:
			score++ // the if branch
		case *ast.ForStmt:
			score++
		case *ast.RangeStmt:
			score++
		case *ast.CaseClause:
			// Each non-default case in a switch adds a path.
			if v.List != nil {
				score++
			}
		case *ast.CommClause:
			// Each non-default case in a select adds a path.
			if v.Comm != nil {
				score++
			}
		case *ast.BinaryExpr:
			// Short-circuit operators create additional paths.
			if v.Op.String() == "&&" || v.Op.String() == "||" {
				score++
			}
		}
		return true
	})
	return score
}

// Complexity estimates the cyclomatic complexity of all functions/methods in
// the graph that match the given name (case-insensitive substring). It parses
// the source files on demand to inspect the AST. Pass an empty term to score
// all functions; the results are sorted highest-score first. A function whose
// source cannot be read or parsed safely is retained with Score -1 and Label
// UNKNOWN.
func Complexity(g *graph.Graph, term string) []ComplexityResult {
	tl := strings.ToLower(term)
	fset := token.NewFileSet()
	sourceReader, sourceRootErr := sourcefs.Open(g.Root)
	if sourceReader != nil {
		defer func() { _ = sourceReader.Close() }()
	}

	// Cache parsed files to avoid re-parsing the same file multiple times.
	type astFile struct {
		f   *ast.File
		err error
	}
	parsed := make(map[string]*astFile)

	parseFile := func(path string) (*ast.File, error) {
		if cached, ok := parsed[path]; ok {
			return cached.f, cached.err
		}
		if sourceRootErr != nil {
			parsed[path] = &astFile{err: sourceRootErr}
			return nil, sourceRootErr
		}
		data, err := sourceReader.ReadFile(path)
		if err != nil {
			parsed[path] = &astFile{err: err}
			return nil, err
		}
		f, err := parser.ParseFile(fset, path, data, 0)
		parsed[path] = &astFile{f: f, err: err}
		return f, err
	}

	var results []ComplexityResult

	for _, sym := range g.Symbols {
		if sym.Kind != graph.KindFunction && sym.Kind != graph.KindMethod {
			continue
		}
		// Build display name for this symbol.
		displayName := sym.Name
		if sym.Receiver != "" {
			displayName = "(" + sym.Receiver + ")." + sym.Name
		}

		// Filter by term if provided.
		if tl != "" && !strings.Contains(strings.ToLower(displayName), tl) &&
			!strings.Contains(strings.ToLower(sym.ID), tl) {
			continue
		}

		// Parse the source file.
		astf, err := parseFile(sym.File)
		if err != nil {
			// If we can't parse the file, report complexity as -1.
			results = append(results, ComplexityResult{
				Symbol: displayName,
				File:   sym.File,
				Line:   sym.Line,
				Score:  -1,
				Label:  "UNKNOWN",
			})
			continue
		}

		// Find the matching function declaration by line number.
		score := 1
		ast.Inspect(astf, func(n ast.Node) bool {
			switch decl := n.(type) {
			case *ast.FuncDecl:
				pos := fset.Position(decl.Pos())
				if pos.Line == sym.Line && decl.Body != nil {
					score = countComplexity(decl.Body)
					return false
				}
			}
			return true
		})

		results = append(results, ComplexityResult{
			Symbol: displayName,
			File:   sym.File,
			Line:   sym.Line,
			Score:  score,
			Label:  complexityLabel(score),
		})
	}

	// Sort by score descending (highest complexity first).
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Score > results[j-1].Score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
	return results
}
