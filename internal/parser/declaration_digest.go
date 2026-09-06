package parser

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/scanner"
	"go/token"

	"github.com/ozgurcd/gograph/internal/graph"
)

// declarationDigest ignores position/formatting changes but retains tokens and
// literal values. Comments are not executable declarations; their edits remain
// visible in ChangedFiles without falsely marking every function as modified.
func declarationDigest(fset *token.FileSet, node ast.Node) string {
	var source bytes.Buffer
	if err := printer.Fprint(&source, fset, node); err != nil {
		return ""
	}
	set := token.NewFileSet()
	file := set.AddFile("declaration", -1, source.Len())
	var scan scanner.Scanner
	scan.Init(file, source.Bytes(), nil, 0)
	var tokens bytes.Buffer
	type lexeme struct {
		tok     token.Token
		literal string
	}
	var lexemes []lexeme
	for {
		_, tok, lit := scan.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.ILLEGAL {
			return ""
		}
		// Explicit and automatically inserted semicolons have identical meaning.
		if tok == token.SEMICOLON {
			lit = ";"
		}
		lexemes = append(lexemes, lexeme{tok, lit})
	}
	for i, item := range lexemes {
		// Go permits omitting a semicolon immediately before ')' or '}'.
		if item.tok == token.SEMICOLON && i+1 < len(lexemes) && (lexemes[i+1].tok == token.RBRACE || lexemes[i+1].tok == token.RPAREN) {
			continue
		}
		fmt.Fprintf(&tokens, "%d:%q;", item.tok, item.literal)
	}
	return graph.SourceDigest(tokens.Bytes())
}
