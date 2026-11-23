package search

import (
	"errors"
	"fmt"
	"go/scanner"
	"go/token"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

// maxStringSourceBytes bounds one source file read by a string-literal search.
const maxStringSourceBytes = 16 << 20

// maxStringLiteralDisplay bounds the literal text echoed in one result row.
const maxStringLiteralDisplay = 200

// StringLiterals finds Go string literals in the graph's indexed files that
// contain any term, case-insensitively. Route-shaped terms also match other
// path-parameter spellings (:id, {id}, *id). Files are read from the graph
// root through the confined repository reader; unreadable files are reported
// in the returned diagnostic instead of being silently skipped.
func StringLiterals(g *graph.Graph, terms []string, includeTests bool) ([]Result, string, error) {
	return StringLiteralsIn(g, terms, includeTests, nil)
}

// StringLiteralsIn is StringLiterals that also searches extraFiles: selected
// Go files the stale check found added since the graph was built. Their rows
// have no enclosing function, because the graph has no symbols for them.
func StringLiteralsIn(g *graph.Graph, terms []string, includeTests bool, extraFiles []string) ([]Result, string, error) {
	if g == nil {
		return nil, "", errors.New("string search requires a graph")
	}
	var lowered []string
	for _, term := range terms {
		if term = strings.ToLower(strings.TrimSpace(term)); term != "" {
			lowered = append(lowered, term)
		}
	}
	if len(lowered) == 0 {
		return nil, "", errors.New("string search requires at least one non-empty term")
	}
	reader, err := sourcefs.Open(g.Root)
	if err != nil {
		return nil, "", fmt.Errorf("open graph root for string search: %w", err)
	}
	defer func() { _ = reader.Close() }()

	functionsByFile := make(map[string][]graph.SymbolNode)
	for _, symbol := range g.Symbols {
		if symbol.Kind == graph.KindFunction || symbol.Kind == graph.KindMethod {
			functionsByFile[symbol.File] = append(functionsByFile[symbol.File], symbol)
		}
	}

	var results []Result
	var missing, unreadable []string
	var unreadableReason error
	indexed := make(map[string]bool, len(g.Files))
	paths := make([]string, 0, len(g.Files)+len(extraFiles))
	for _, file := range g.Files {
		indexed[file.Path] = true
		paths = append(paths, file.Path)
	}
	added := make(map[string]bool)
	for _, file := range extraFiles {
		if !indexed[file] {
			indexed[file], added[file] = true, true
			paths = append(paths, file)
		}
	}
	for _, filePath := range paths {
		if !strings.HasSuffix(filePath, ".go") || (!includeTests && isTestFile(filePath)) {
			continue
		}
		data, err := reader.ReadRegularFileLimit(filePath, maxStringSourceBytes)
		if errors.Is(err, fs.ErrNotExist) {
			missing = append(missing, filePath)
			continue
		}
		if err != nil {
			if unreadableReason == nil {
				unreadableReason = err
			}
			unreadable = append(unreadable, filePath)
			continue
		}
		fileSet := token.NewFileSet()
		tokenFile := fileSet.AddFile(filePath, -1, len(data))
		var lexer scanner.Scanner
		lexer.Init(tokenFile, data, nil, 0)
		for {
			position, kind, literal := lexer.Scan()
			if kind == token.EOF {
				break
			}
			if kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(literal)
			if err != nil {
				continue
			}
			if !stringLiteralMatches(value, lowered) {
				continue
			}
			line := tokenFile.Line(position)
			detail := "string literal"
			if function := enclosingFunction(functionsByFile[filePath], line); function != "" {
				detail = "string literal in " + function
			} else if added[filePath] {
				detail = "string literal in a file added since the last build"
			}
			results = append(results, Result{
				Kind: "string", Name: strconv.Quote(truncateLiteral(value)),
				File: filePath, Line: line, Detail: detail, Score: 1,
			})
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].File != results[j].File {
			return results[i].File < results[j].File
		}
		return results[i].Line < results[j].Line
	})
	return results, stringSearchDiagnostic(missing, unreadable, unreadableReason), nil
}

// stringSearchDiagnostic says which indexed files were not searched and why.
func stringSearchDiagnostic(missing, unreadable []string, reason error) string {
	example := func(files []string) string {
		sort.Strings(files)
		if len(files) > 5 {
			files = files[:5]
		}
		return strings.Join(files, ", ")
	}
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, fmt.Sprintf("%d indexed file(s) no longer exist, deleted or moved since the last build (for example %s)", len(missing), example(missing)))
	}
	if len(unreadable) > 0 {
		parts = append(parts, fmt.Sprintf("%d indexed file(s) could not be read safely (for example %s; %v)", len(unreadable), example(unreadable), reason))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ") + ". They were not searched, so results may be incomplete; run `gograph build .` to refresh the index."
}

func stringLiteralMatches(value string, terms []string) bool {
	lower := strings.ToLower(value)
	for _, term := range terms {
		if strings.Contains(lower, term) || (strings.Contains(term, "/") && RoutePatternContains(value, term)) {
			return true
		}
	}
	return false
}

func enclosingFunction(functions []graph.SymbolNode, line int) string {
	best := graph.SymbolNode{}
	for _, function := range functions {
		if function.Line <= line && line <= function.EndLine && (best.ID == "" || function.Line >= best.Line) {
			best = function
		}
	}
	if best.ID == "" {
		return ""
	}
	if best.Receiver != "" {
		return fmt.Sprintf("(%s).%s", best.Receiver, best.Name)
	}
	return best.Name
}

func truncateLiteral(value string) string {
	if len(value) <= maxStringLiteralDisplay {
		return value
	}
	cut := maxStringLiteralDisplay
	for cut > 0 && !utf8Start(value[cut]) {
		cut--
	}
	return value[:cut] + "…"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
