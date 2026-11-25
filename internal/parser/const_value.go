package parser

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
)

// maxConstValueBytes bounds the recorded constant expression so a long
// literal does not bloat the graph.
const maxConstValueBytes = 200

// constValue renders the value of the index-th name of a const spec as
// "[Type ]= expr". Within a const group a spec without values repeats the
// previous spec's type and expressions; that form is marked with its iota.
// It returns "" when no expression is available.
func constValue(spec, previous *ast.ValueSpec, index, iota int) string {
	source, implicit := spec, false
	if len(spec.Values) == 0 && spec.Type == nil && previous != nil {
		source, implicit = previous, true
	}
	expr, ok := expressionAt(source.Values, index, len(source.Names))
	if !ok {
		return ""
	}
	value := "= " + nodeText(expr)
	if source.Type != nil {
		value = nodeText(source.Type) + " " + value
	}
	if implicit {
		value += fmt.Sprintf(" (implicit repeat, iota=%d)", iota)
	}
	if len(value) > maxConstValueBytes {
		value = value[:maxConstValueBytes] + "…"
	}
	return value
}

func nodeText(node ast.Node) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, token.NewFileSet(), node); err != nil {
		return ""
	}
	return buf.String()
}
