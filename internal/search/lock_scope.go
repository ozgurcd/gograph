package search

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

// LockScopeReport lists every Lock/RLock call in one function and what runs
// lexically between it and its release.
type LockScopeReport struct {
	Function  string      `json:"function"`
	File      string      `json:"file"`
	StartLine int         `json:"start_line"`
	EndLine   int         `json:"end_line"`
	Locks     []LockScope `json:"locks"`
}

// LockScope is one Lock or RLock call. Release is "unlock", "deferred" (held
// until the function returns) or "none" (not released in this function).
type LockScope struct {
	Lock        string     `json:"lock"`
	Line        int        `json:"line"`
	Release     string     `json:"release"`
	ReleaseLine int        `json:"release_line,omitempty"`
	UnlockLines []int      `json:"unlock_lines,omitempty"`
	EndLine     int        `json:"end_line"`
	Calls       []LockCall `json:"calls"`
}

// LockCall is a call that appears between a lock and its release.
type LockCall struct {
	Call string `json:"call"`
	Line int    `json:"line"`
}

type lockSite struct {
	mutex    string
	verb     string
	unlock   string
	pos      token.Pos
	end      token.Pos
	line     int
	deferred bool
}

// LockScopes parses the named function's body at query time and reports, for
// each mutex Lock/RLock call, how far the lock reaches and the calls inside.
// Spans are lexical: a branch that unlocks early still counts as one span up
// to the last matching unlock before the next lock of the same mutex.
func LockScopes(g *graph.Graph, rootDir, term string) (LockScopeReport, error) {
	if g == nil {
		return LockScopeReport{}, fmt.Errorf("graph is unavailable")
	}
	targets, err := sourceTargets(g, term)
	if err != nil {
		return LockScopeReport{}, err
	}
	target := targets[0]
	if target.Kind != graph.KindFunction && target.Kind != graph.KindMethod {
		return LockScopeReport{}, fmt.Errorf("%s is a %s; lock scopes need a function or method", target.ID, target.Kind)
	}
	reader, err := sourcefs.Open(rootDir)
	if err != nil {
		return LockScopeReport{}, fmt.Errorf("open repository source root: %w", err)
	}
	defer func() { _ = reader.Close() }()
	data, err := reader.ReadFile(target.File)
	if err != nil {
		return LockScopeReport{}, fmt.Errorf("read source file %s: %w", target.File, err)
	}
	if recorded := indexedFileDigest(g, target.File); recorded != "" && recorded != graph.SourceDigest(data) {
		return LockScopeReport{}, fmt.Errorf("%s changed after the graph was built; run gograph build . and retry", target.File)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, target.File, data, parser.SkipObjectResolution)
	if err != nil {
		return LockScopeReport{}, fmt.Errorf("parse %s: %w", target.File, err)
	}
	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		candidate, ok := decl.(*ast.FuncDecl)
		if !ok || candidate.Body == nil || candidate.Name.Name != target.Name {
			continue
		}
		if line := fset.Position(candidate.Name.Pos()).Line; line >= target.Line && line <= target.EndLine {
			fn = candidate
			break
		}
	}
	if fn == nil {
		return LockScopeReport{}, fmt.Errorf("no body for %s at %s:%d", target.ID, target.File, target.Line)
	}
	report := LockScopeReport{
		Function:  target.ID,
		File:      target.File,
		StartLine: fset.Position(fn.Pos()).Line,
		EndLine:   fset.Position(fn.End()).Line,
		Locks:     []LockScope{},
	}

	var locks, unlocks []lockSite
	type callSite struct {
		text string
		pos  token.Pos
		line int
	}
	var calls []callSite
	deferredCalls := map[*ast.CallExpr]bool{}
	var walk func(n ast.Node, deferred bool)
	walk = func(n ast.Node, deferred bool) {
		ast.Inspect(n, func(node ast.Node) bool {
			switch x := node.(type) {
			case *ast.DeferStmt:
				deferredCalls[x.Call] = true
				// A deferred closure releases at return too.
				if lit, ok := x.Call.Fun.(*ast.FuncLit); ok {
					walk(lit.Body, true)
					for _, arg := range x.Call.Args {
						walk(arg, deferred)
					}
					return false
				}
			case *ast.CallExpr:
				line := fset.Position(x.Pos()).Line
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok && len(x.Args) == 0 {
					mutex := types.ExprString(sel.X)
					switch sel.Sel.Name {
					case "Lock", "RLock":
						verb := sel.Sel.Name
						locks = append(locks, lockSite{mutex: mutex, verb: verb, unlock: strings.TrimSuffix(verb, "Lock") + "Unlock", pos: x.Pos(), end: x.End(), line: line})
						return true
					case "Unlock", "RUnlock":
						unlocks = append(unlocks, lockSite{mutex: mutex, unlock: sel.Sel.Name, pos: x.Pos(), end: x.End(), line: line, deferred: deferred || deferredCalls[x]})
						return true
					}
				}
				calls = append(calls, callSite{text: callText(x.Fun), pos: x.Pos(), line: line})
			}
			return true
		})
	}
	walk(fn.Body, false)
	sort.Slice(locks, func(i, j int) bool { return locks[i].pos < locks[j].pos })

	for i, lock := range locks {
		limit := fn.Body.End()
		for _, next := range locks[i+1:] {
			if next.mutex == lock.mutex {
				limit = next.pos
				break
			}
		}
		scope := LockScope{Lock: lock.mutex + "." + lock.verb, Line: lock.line, Calls: []LockCall{}}
		end := lock.end
		deferredLine := 0
		for _, unlock := range unlocks {
			if unlock.mutex != lock.mutex || unlock.unlock != lock.unlock || unlock.pos <= lock.pos || unlock.pos >= limit {
				continue
			}
			if unlock.deferred {
				if deferredLine == 0 {
					deferredLine = unlock.line
				}
				continue
			}
			scope.UnlockLines = append(scope.UnlockLines, unlock.line)
			if unlock.pos > end {
				end = unlock.pos
			}
		}
		switch {
		case deferredLine != 0:
			scope.Release, scope.ReleaseLine, end = "deferred", deferredLine, limit
			scope.UnlockLines = nil
		case len(scope.UnlockLines) > 0:
			scope.Release, scope.ReleaseLine = "unlock", scope.UnlockLines[len(scope.UnlockLines)-1]
		default:
			scope.Release, end = "none", limit
		}
		scope.EndLine = fset.Position(end).Line
		if end == fn.Body.End() {
			scope.EndLine = report.EndLine
		}
		for _, call := range calls {
			if call.pos > lock.end && call.pos < end {
				scope.Calls = append(scope.Calls, LockCall{Call: call.text, Line: call.line})
			}
		}
		report.Locks = append(report.Locks, scope)
	}
	return report, nil
}

func callText(fun ast.Expr) string {
	if _, ok := fun.(*ast.FuncLit); ok {
		return "func literal"
	}
	return types.ExprString(fun)
}

// FormatLockScopes renders a LockScopeReport as plain text.
func FormatLockScopes(report LockScopeReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s:%d-%d)\n", report.Function, report.File, report.StartLine, report.EndLine)
	if len(report.Locks) == 0 {
		b.WriteString("no Lock/RLock calls in this function\n")
		return b.String()
	}
	for _, scope := range report.Locks {
		switch scope.Release {
		case "deferred":
			fmt.Fprintf(&b, "%s() line %d: held until return (deferred unlock at line %d), lines %d-%d\n", scope.Lock, scope.Line, scope.ReleaseLine, scope.Line, scope.EndLine)
		case "unlock":
			lines := make([]string, len(scope.UnlockLines))
			for i, line := range scope.UnlockLines {
				lines[i] = fmt.Sprint(line)
			}
			label := "line"
			if len(lines) > 1 {
				label = "lines"
			}
			fmt.Fprintf(&b, "%s() line %d: unlocked at %s %s, lines %d-%d\n", scope.Lock, scope.Line, label, strings.Join(lines, ", "), scope.Line, scope.EndLine)
		default:
			fmt.Fprintf(&b, "%s() line %d: not released in this function, lines %d-%d\n", scope.Lock, scope.Line, scope.Line, scope.EndLine)
		}
		if len(scope.Calls) == 0 {
			b.WriteString("  no calls under the lock\n")
			continue
		}
		for _, call := range scope.Calls {
			fmt.Fprintf(&b, "  %d: %s()\n", call.Line, call.Call)
		}
	}
	return b.String()
}
