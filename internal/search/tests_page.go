package search

import (
	"fmt"
	"strings"
)

// DefaultTestsLimit bounds a transitive tests page when no limit is given.
// Hub symbols can be reached by thousands of tests; an unbounded answer ran
// to megabytes and exceeded agent tool-result limits.
const DefaultTestsLimit = 50

// TestsPageOptions selects a page of a transitive tests report.
type TestsPageOptions struct {
	// MaxDepth keeps tests whose shortest path is at most this many calls
	// long; 0 means unlimited.
	MaxDepth int
	// Limit is the page size; 0 selects DefaultTestsLimit and a negative
	// value returns every test.
	Limit  int
	Cursor string
	// Command is the CLI spelling used to build summary.next_command, for
	// example `gograph tests Foo --transitive`; empty omits it.
	Command string
}

type testsPageSelection struct {
	Query     string `json:"query"`
	Package   string `json:"package"`
	ExactOnly bool   `json:"exact_only"`
	MaxDepth  int    `json:"max_depth"`
}

// TransitiveTestsPage returns the transitive tests report sorted nearest
// first, with a summary of all reaching tests and one bounded page.
func (snapshot *Snapshot) TransitiveTestsPage(query, packageName string, exactOnly bool, options TestsPageOptions) (TestsReport, error) {
	if options.MaxDepth < 0 {
		return TestsReport{}, fmt.Errorf("max_depth must be 0 (unlimited) or positive")
	}
	report := snapshot.TransitiveTests(query, packageName, exactOnly)
	selection := testsPageSelection{Query: strings.TrimSpace(query), Package: strings.TrimSpace(packageName), ExactOnly: exactOnly, MaxDepth: options.MaxDepth}
	binding, err := snapshot.binding(TestsSchemaVersion, selection)
	if err != nil {
		return TestsReport{}, err
	}
	encodedOffset, err := cursorOffset(options.Cursor, binding, "tests")
	if err != nil {
		return TestsReport{}, err
	}
	offset, err := decodeRouteCursor(encodedOffset)
	if err != nil {
		return TestsReport{}, fmt.Errorf("invalid tests cursor; restart without cursor")
	}

	summary := &TestsSummary{ByDepth: []TestDepthCount{}}
	kept := report.Tests[:0:0]
	depthIndex := make(map[int]int)
	for _, test := range report.Tests {
		if options.MaxDepth > 0 && test.Depth > options.MaxDepth {
			summary.BeyondDepth++
			continue
		}
		kept = append(kept, test)
		index, ok := depthIndex[test.Depth]
		if !ok {
			index = len(summary.ByDepth)
			depthIndex[test.Depth] = index
			summary.ByDepth = append(summary.ByDepth, TestDepthCount{Depth: test.Depth})
		}
		summary.Total++
		if test.Resolution == "exact" {
			summary.Exact++
			summary.ByDepth[index].Exact++
		} else {
			summary.Possible++
			summary.ByDepth[index].Possible++
		}
	}

	limit := options.Limit
	if limit == 0 {
		limit = DefaultTestsLimit
	}
	total := len(kept)
	if offset > total {
		return TestsReport{}, fmt.Errorf("tests cursor is past the end of the result; restart without cursor")
	}
	end := total
	if limit > 0 && offset+limit < total {
		end = offset + limit
	}
	page := append([]AttributingTest{}, kept[offset:end]...)
	returned, truncated, nextCursor := len(page), end < total, ""
	if truncated {
		nextCursor = boundCursor(binding, encodeRouteCursor(end))
		if options.Command != "" {
			summary.NextCommand = options.Command + " --cursor " + nextCursor
		}
	}
	report.Tests = page
	report.Summary = summary
	report.MaxDepth = options.MaxDepth
	report.Total, report.Returned, report.Offset = &total, &returned, &offset
	if limit > 0 {
		report.Limit = limit
	}
	report.Truncated, report.NextCursor = &truncated, &nextCursor
	return report, nil
}
