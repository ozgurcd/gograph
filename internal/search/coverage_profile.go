package search

import (
	"bufio"
	"bytes"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// CoverageBlock is one statement block of a `go test -coverprofile` file.
type CoverageBlock struct {
	StartLine, StartCol, EndLine, EndCol int
	Statements                           int
	Count                                int
}

// CoverageProfile maps repository-relative files to their coverage blocks.
// Blocks for the same position from merged profiles are combined: counts
// are summed, so a block executed in any run counts as executed.
type CoverageProfile struct {
	Mode  string
	Files map[string][]CoverageBlock
	// UnmappedFiles counts profile files that belong to no graph package.
	UnmappedFiles int
}

// ParseCoverProfile reads one or more concatenated `go test -coverprofile`
// profiles (for example from `go tool covdata textfmt` or several packages)
// and maps their import-path file names to repository-relative paths using
// the graph's packages.
func ParseCoverProfile(data []byte, g *graph.Graph) (*CoverageProfile, error) {
	dirByImport := make(map[string]string)
	if g != nil {
		for _, pkg := range g.Packages {
			if len(pkg.Files) == 0 || pkg.ImportPathBestEffort == "" {
				continue
			}
			dirByImport[pkg.ImportPathBestEffort] = path.Dir(strings.ReplaceAll(pkg.Files[0], "\\", "/"))
		}
	}
	profile := &CoverageProfile{Files: make(map[string][]CoverageBlock)}
	type key struct {
		file                                 string
		startLine, startCol, endLine, endCol int
	}
	index := make(map[key]int)
	unmapped := make(map[string]bool)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if mode, ok := strings.CutPrefix(line, "mode:"); ok {
			mode = strings.TrimSpace(mode)
			if profile.Mode != "" && profile.Mode != mode {
				return nil, fmt.Errorf("coverage profile line %d: mixed modes %q and %q", lineNumber, profile.Mode, mode)
			}
			profile.Mode = mode
			continue
		}
		if profile.Mode == "" {
			return nil, fmt.Errorf("coverage profile line %d: missing mode line; pass a go test -coverprofile file", lineNumber)
		}
		block, name, err := parseCoverLine(line)
		if err != nil {
			return nil, fmt.Errorf("coverage profile line %d: %w", lineNumber, err)
		}
		dir, ok := dirByImport[path.Dir(name)]
		if !ok {
			unmapped[name] = true
			continue
		}
		file := path.Join(dir, path.Base(name))
		k := key{file, block.StartLine, block.StartCol, block.EndLine, block.EndCol}
		if existing, ok := index[k]; ok {
			profile.Files[file][existing].Count += block.Count
			continue
		}
		index[k] = len(profile.Files[file])
		profile.Files[file] = append(profile.Files[file], block)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read coverage profile: %w", err)
	}
	if profile.Mode == "" {
		return nil, fmt.Errorf("coverage profile is empty")
	}
	profile.UnmappedFiles = len(unmapped)
	for file := range profile.Files {
		blocks := profile.Files[file]
		sort.Slice(blocks, func(i, j int) bool {
			if blocks[i].StartLine != blocks[j].StartLine {
				return blocks[i].StartLine < blocks[j].StartLine
			}
			return blocks[i].StartCol < blocks[j].StartCol
		})
	}
	return profile, nil
}

// parseCoverLine parses "name.go:12.3,15.2 3 1".
func parseCoverLine(line string) (CoverageBlock, string, error) {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return CoverageBlock{}, "", fmt.Errorf("want \"file:start,end statements count\", got %q", line)
	}
	colon := strings.LastIndex(fields[0], ":")
	if colon < 0 {
		return CoverageBlock{}, "", fmt.Errorf("missing position in %q", fields[0])
	}
	name, span := fields[0][:colon], fields[0][colon+1:]
	start, end, ok := strings.Cut(span, ",")
	if !ok {
		return CoverageBlock{}, "", fmt.Errorf("malformed span %q", span)
	}
	var block CoverageBlock
	var err error
	if block.StartLine, block.StartCol, err = parseLineCol(start); err != nil {
		return CoverageBlock{}, "", err
	}
	if block.EndLine, block.EndCol, err = parseLineCol(end); err != nil {
		return CoverageBlock{}, "", err
	}
	if block.Statements, err = strconv.Atoi(fields[1]); err != nil || block.Statements < 0 {
		return CoverageBlock{}, "", fmt.Errorf("malformed statement count %q", fields[1])
	}
	if block.Count, err = strconv.Atoi(fields[2]); err != nil || block.Count < 0 {
		return CoverageBlock{}, "", fmt.Errorf("malformed execution count %q", fields[2])
	}
	return block, name, nil
}

func parseLineCol(value string) (int, int, error) {
	line, column, ok := strings.Cut(value, ".")
	if !ok {
		return 0, 0, fmt.Errorf("malformed position %q", value)
	}
	l, err1 := strconv.Atoi(line)
	c, err2 := strconv.Atoi(column)
	if err1 != nil || err2 != nil || l < 1 || c < 0 {
		return 0, 0, fmt.Errorf("malformed position %q", value)
	}
	return l, c, nil
}

// statementsIn returns the statements and executed statements of every
// block starting within [startLine, endLine] of file, and whether the file
// has profile data at all.
func (profile *CoverageProfile) statementsIn(file string, startLine, endLine int) (statements, executed int, known bool) {
	blocks, known := profile.Files[path.Clean(strings.ReplaceAll(file, "\\", "/"))]
	for _, block := range blocks {
		if block.StartLine < startLine || block.StartLine > endLine {
			continue
		}
		statements += block.Statements
		if block.Count > 0 {
			executed += block.Statements
		}
	}
	return statements, executed, known
}

// UncoveredLines returns, for one file, the given lines that sit in a block
// with no executions and in no executed block. Lines outside every block
// (comments, declarations, blank lines) are never reported.
func (profile *CoverageProfile) UncoveredLines(file string, lines []int) []int {
	blocks := profile.Files[path.Clean(strings.ReplaceAll(file, "\\", "/"))]
	var uncovered []int
	for _, line := range lines {
		inZero, inExecuted := false, false
		for _, block := range blocks {
			if line < block.StartLine || line > block.EndLine {
				continue
			}
			if block.Count > 0 {
				inExecuted = true
			} else if block.Statements > 0 {
				inZero = true
			}
		}
		if inZero && !inExecuted {
			uncovered = append(uncovered, line)
		}
	}
	return uncovered
}

// HasFile reports whether the profile has data for a repository file.
func (profile *CoverageProfile) HasFile(file string) bool {
	_, ok := profile.Files[path.Clean(strings.ReplaceAll(file, "\\", "/"))]
	return ok
}
