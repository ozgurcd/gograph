package search

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

// maxSourceLinesBytes is the budget for one line-range answer, the same as a
// whole source answer.
const maxSourceLinesBytes = 64 * 1024

// SourceLines returns file lines start..end (1-based, inclusive) of what term
// names. A term that is an indexed .go file path selects a region of that
// file, including package clause, imports and comments. Any other term is a
// source selector for one declaration; the range is limited to that
// declaration's lines, so a region of a body too large for Source can be read.
// Reads use the confined repository reader and refuse files changed since the
// graph was built.
func SourceLines(g *graph.Graph, rootDir, term string, start, end int) (string, error) {
	if start < 1 || end < start {
		return "", fmt.Errorf("invalid line range %d-%d: use START-END with 1 <= START <= END", start, end)
	}
	if g == nil {
		return "", fmt.Errorf("graph is unavailable")
	}
	label, file, first, last := "", "", 0, 0
	if indexed, ok := indexedGoFile(g, term); ok {
		label, file = indexed, indexed
	} else {
		targets, err := sourceTargetsOrField(g, rootDir, term)
		if err != nil {
			return "", err
		}
		target := targets[0]
		label, file, first, last = target.ID, target.File, target.Line, target.EndLine
		if start > last || end < first {
			return "", fmt.Errorf("lines %d-%d are outside %s, which spans %s:%d-%d", start, end, target.ID, target.File, first, last)
		}
		start, end = max(start, first), min(end, last)
	}

	reader, err := sourcefs.Open(rootDir)
	if err != nil {
		return "", fmt.Errorf("open repository source root: %w", err)
	}
	defer func() { _ = reader.Close() }()
	data, err := reader.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("read source file %s: %w", file, err)
	}
	if recorded := indexedFileDigest(g, file); recorded != "" && recorded != graph.SourceDigest(data) {
		return "", fmt.Errorf("%s changed after the graph was built, so its indexed lines are stale; run gograph build . and retry", file)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if start > len(lines) {
		return "", fmt.Errorf("%s has %d lines; line %d is past the end", file, len(lines), start)
	}
	end = min(end, len(lines))
	code := fmt.Sprintf("// %s (%s:%d-%d)\n%s", label, file, start, end, strings.Join(lines[start-1:end], "\n"))
	if len(code) > maxSourceLinesBytes {
		return "", fmt.Errorf("lines %d-%d of %s are %d bytes, exceeding the %d byte budget; request a smaller range", start, end, file, len(code), maxSourceLinesBytes)
	}
	return code, nil
}

// indexedGoFile reports whether term names a .go file in the graph, by its
// repository-relative path.
func indexedGoFile(g *graph.Graph, term string) (string, bool) {
	if !strings.HasSuffix(term, ".go") {
		return "", false
	}
	cleaned := strings.TrimPrefix(path.Clean(strings.ReplaceAll(term, "\\", "/")), "./")
	for _, f := range g.Files {
		if f.Path == cleaned {
			return f.Path, true
		}
	}
	return "", false
}

// ParseLineRange parses START-END, 1-based and inclusive.
// FileLineSelector reports whether term is path/file.go:START-END, a file
// region selector. A Go identifier cannot start with a digit, so this never
// shadows the file.go:Name declaration selector.
func FileLineSelector(term string) (string, int, int, bool) {
	position := strings.LastIndex(term, ".go:")
	if position < 0 {
		return "", 0, 0, false
	}
	file, rest := term[:position+3], term[position+4:]
	if rest == "" || rest[0] < '0' || rest[0] > '9' {
		return "", 0, 0, false
	}
	start, end, err := ParseLineRange(rest)
	if err != nil {
		return "", 0, 0, false
	}
	return file, start, end, true
}

func ParseLineRange(value string) (int, int, error) {
	first, last, ok := strings.Cut(value, "-")
	start, startErr := strconv.Atoi(strings.TrimSpace(first))
	end, endErr := strconv.Atoi(strings.TrimSpace(last))
	if !ok || startErr != nil || endErr != nil || start < 1 || end < start {
		return 0, 0, fmt.Errorf("invalid line range %q: use START-END with 1 <= START <= END", value)
	}
	return start, end, nil
}
