package search

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestResultPagesAreBoundedAndSnapshotBound(t *testing.T) {
	rows := make([]Result, 543)
	for i := range rows {
		rows[i] = Result{Kind: "symbol", Name: fmt.Sprintf("S%04d", i), Detail: strings.Repeat("x", 400)}
	}
	cursor := ""
	seen := 0
	for {
		page, err := PageResults("snapshot", "query", rows, 200, cursor)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) > MaxResultsBytes || page.Returned < 1 || page.Total != 543 || page.Offset != seen {
			t.Fatalf("bad page: %+v", page)
		}
		seen += page.Returned
		if !page.Truncated {
			break
		}
		if _, err := PageResults("changed", "query", rows, 200, page.NextCursor); err == nil {
			t.Fatal("accepted changed snapshot")
		}
		if _, err := PageResults("snapshot", "callers", rows, 200, page.NextCursor); err == nil {
			t.Fatal("accepted changed operation")
		}
		cursor = page.NextCursor
	}
	if seen != len(rows) {
		t.Fatalf("lost results: %d", seen)
	}
}

func TestResultPageRejectsOversizedSingleRow(t *testing.T) {
	if _, err := PageResults("snapshot", "query", []Result{{Name: strings.Repeat("x", MaxResultsBytes)}}, 1, ""); err == nil {
		t.Fatal("accepted an oversized row")
	}
}

func TestQueryKeepsSameNamedSymbolsAtSameLineInDifferentFiles(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example/a::Save", Name: "Save", File: "a.go", Line: 3},
		{ID: "example/b::Save", Name: "Save", File: "b.go", Line: 3},
	}}
	rows := Query(g, []string{"Save"})
	if len(rows) != 2 || rows[0].StableID == rows[1].StableID {
		t.Fatalf("conflated symbol identities: %+v", rows)
	}
}
