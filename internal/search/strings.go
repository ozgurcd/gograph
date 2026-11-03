package search

import (
	"errors"
	"fmt"
	"go/scanner"
	"go/token"
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
	var unreadable []string
	for _, file := range g.Files {
		if !strings.HasSuffix(file.Path, ".go") || (!includeTests && isTestFile(file.Path)) {
			continue
		}
		data, err := reader.ReadRegularFileLimit(file.Path, maxStringSourceBytes)
		if err != nil {
			unreadable = append(unreadable, file.Path)
			continue
		}
		fileSet := token.NewFileSet()
		tokenFile := fileSet.AddFile(file.Path, -1, len(data))
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
			if function := enclosingFunction(functionsByFile[file.Path], line); function != "" {
				detail = "string literal in " + function
			}
			results = append(results, Result{
				Kind: "string", Name: strconv.Quote(truncateLiteral(value)),
				File: file.Path, Line: line, Detail: detail, Score: 1,
			})
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].File != results[j].File {
			return results[i].File < results[j].File
		}
		return results[i].Line < results[j].Line
	})
	diagnostic := ""
	if len(unreadable) > 0 {
		sort.Strings(unreadable)
		shown := unreadable
		if len(shown) > 5 {
			shown = shown[:5]
		}
		diagnostic = fmt.Sprintf("%d indexed files could not be read safely and were not searched (for example %s); results may be incomplete.", len(unreadable), strings.Join(shown, ", "))
	}
	return results, diagnostic, nil
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
