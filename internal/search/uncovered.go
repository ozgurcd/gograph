package search

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ozgurcd/gograph/internal/gitenv"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

// UncoveredSchemaVersion identifies the changed-but-unexecuted lines report.
const UncoveredSchemaVersion = "gograph.uncovered.v1"

// UncoveredReport lists changed production lines that a coverage profile
// shows were never executed.
type UncoveredReport struct {
	SchemaVersion string          `json:"schema_version"`
	Selector      string          `json:"selector"`
	Files         []UncoveredFile `json:"files"`
	ChangedFiles  int             `json:"changed_files"`
	ChangedLines  int             `json:"changed_lines"`
	// UncoveredLines counts changed executable lines with no execution.
	UncoveredLines int `json:"uncovered_lines"`
	// FilesWithoutProfileData are changed files the profile has no blocks
	// for: untested packages without -coverpkg, or files outside the build.
	FilesWithoutProfileData []string `json:"files_without_profile_data"`
	Limitations             []string `json:"limitations"`
}

// UncoveredFile is one changed file with unexecuted changed lines.
type UncoveredFile struct {
	File           string      `json:"file"`
	ChangedLines   int         `json:"changed_lines"`
	UncoveredLines int         `json:"uncovered_lines"`
	Ranges         []LineRange `json:"ranges"`
}

// LineRange is an inclusive range of line numbers.
type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// ChangedGoLines returns the added or modified lines of non-test .go files,
// relative to root. With ref empty it compares the working tree with HEAD and
// includes untracked files; otherwise it compares the working tree with ref.
func ChangedGoLines(ctx context.Context, root, ref string) (map[string][]int, error) {
	base := "HEAD"
	if ref != "" {
		if strings.HasPrefix(ref, "-") || !safeGitRef.MatchString(ref) {
			return nil, fmt.Errorf("invalid git ref %q", ref)
		}
		base = ref
	}
	diff, err := gitOutputIn(ctx, root, "diff", "--no-color", "--no-ext-diff", "--relative", "-U0", base, "--", "*.go")
	if err != nil {
		return nil, fmt.Errorf("git diff %s: %w", base, err)
	}
	changed := parseUnifiedDiffAdditions(diff)
	if ref == "" {
		untracked, err := gitOutputIn(ctx, root, "ls-files", "--others", "--exclude-standard", "--", "*.go")
		if err != nil {
			return nil, fmt.Errorf("git ls-files: %w", err)
		}
		reader, err := sourcefs.Open(root)
		if err != nil {
			return nil, err
		}
		defer func() { _ = reader.Close() }()
		for _, name := range strings.Split(strings.TrimSpace(string(untracked)), "\n") {
			name = filepath.ToSlash(strings.TrimSpace(name))
			if name == "" || strings.HasSuffix(name, "_test.go") {
				continue
			}
			data, err := reader.ReadFile(name)
			if err != nil {
				continue
			}
			count := bytes.Count(data, []byte("\n"))
			if len(data) > 0 && data[len(data)-1] != '\n' {
				count++
			}
			for line := 1; line <= count; line++ {
				changed[name] = append(changed[name], line)
			}
		}
	}
	for name := range changed {
		if strings.HasSuffix(name, "_test.go") {
			delete(changed, name)
		}
	}
	return changed, nil
}

func gitOutputIn(ctx context.Context, root string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root, "-c", "core.quotePath=false"}, args...)...)
	cmd.Env = append(gitenv.Clean(os.Environ()), "GIT_OPTIONAL_LOCKS=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			return nil, fmt.Errorf("%w: %s", err, message)
		}
		return nil, err
	}
	return out, nil
}

// parseUnifiedDiffAdditions returns the new-side line numbers of every
// hunk in a -U0 unified diff, keyed by the new file path.
func parseUnifiedDiffAdditions(diff []byte) map[string][]int {
	changed := make(map[string][]int)
	current := ""
	scanner := bufio.NewScanner(bytes.NewReader(diff))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "+++ "):
			name := strings.TrimPrefix(line, "+++ ")
			if name == "/dev/null" {
				current = ""
				continue
			}
			current = path.Clean(strings.TrimPrefix(name, "b/"))
		case strings.HasPrefix(line, "@@ ") && current != "":
			// @@ -a,b +c,d @@
			fields := strings.Fields(line)
			if len(fields) < 3 || !strings.HasPrefix(fields[2], "+") {
				continue
			}
			startText, countText, hasCount := strings.Cut(strings.TrimPrefix(fields[2], "+"), ",")
			start, err := strconv.Atoi(startText)
			if err != nil {
				continue
			}
			count := 1
			if hasCount {
				if count, err = strconv.Atoi(countText); err != nil {
					continue
				}
			}
			for offset := 0; offset < count; offset++ {
				changed[current] = append(changed[current], start+offset)
			}
		}
	}
	return changed
}

// Uncovered reports changed lines that sit in coverage blocks that never
// executed. When root is not empty, lines whose source is blank, a comment,
// or only closing or opening punctuation (where coverage blocks end) are not
// counted.
func Uncovered(selector, root string, changed map[string][]int, profile *CoverageProfile) UncoveredReport {
	var reader *sourcefs.Reader
	if root != "" {
		if opened, err := sourcefs.Open(root); err == nil {
			reader = opened
			defer func() { _ = reader.Close() }()
		}
	}
	report := UncoveredReport{
		SchemaVersion:           UncoveredSchemaVersion,
		Selector:                selector,
		Files:                   []UncoveredFile{},
		FilesWithoutProfileData: []string{},
		Limitations: []string{
			"Only lines inside coverage blocks count: declarations, comments and blank lines are never reported.",
			"A changed file without profile data is listed separately, not as uncovered; profile untested packages with -coverpkg to include them.",
		},
	}
	files := make([]string, 0, len(changed))
	for file := range changed {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		lines := uniqueSortedLines(changed[file])
		if reader != nil {
			if data, err := reader.ReadFile(file); err == nil {
				lines = executableLines(data, lines)
			}
		}
		report.ChangedFiles++
		report.ChangedLines += len(lines)
		if !profile.HasFile(file) {
			report.FilesWithoutProfileData = append(report.FilesWithoutProfileData, file)
			continue
		}
		uncovered := profile.UncoveredLines(file, lines)
		if len(uncovered) == 0 {
			continue
		}
		report.UncoveredLines += len(uncovered)
		report.Files = append(report.Files, UncoveredFile{File: file, ChangedLines: len(lines), UncoveredLines: len(uncovered), Ranges: lineRanges(uncovered)})
	}
	return report
}

func uniqueSortedLines(lines []int) []int {
	sort.Ints(lines)
	out := lines[:0:0]
	for i, line := range lines {
		if i == 0 || line != lines[i-1] {
			out = append(out, line)
		}
	}
	return out
}

func lineRanges(lines []int) []LineRange {
	var ranges []LineRange
	for _, line := range lines {
		if n := len(ranges); n > 0 && ranges[n-1].End+1 == line {
			ranges[n-1].End = line
			continue
		}
		ranges = append(ranges, LineRange{Start: line, End: line})
	}
	return ranges
}

// executableLines drops lines that cannot hold a statement: blank lines,
// line comments, and lines made only of braces, parentheses and commas.
func executableLines(source []byte, lines []int) []int {
	text := strings.Split(string(source), "\n")
	kept := lines[:0:0]
	for _, line := range lines {
		if line < 1 || line > len(text) {
			continue
		}
		trimmed := strings.TrimSpace(text[line-1])
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.Trim(trimmed, "{}(),; \t") == "" {
			continue
		}
		kept = append(kept, line)
	}
	return kept
}
