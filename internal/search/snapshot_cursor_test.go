package search

import (
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestRouteCursorRejectsChangedSnapshotAndFilters(t *testing.T) {
	g := &graph.Graph{Routes: []graph.HTTPRoute{
		{Method: "GET", Path: "/a", Handler: "A", File: "routes.go", Line: 1},
		{Method: "GET", Path: "/b", Handler: "B", File: "routes.go", Line: 2},
	}}
	first, err := QueryRoutes(g, RouteQuery{Limit: 1})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page: %+v, %v", first, err)
	}
	if _, err := QueryRoutes(g, RouteQuery{Cursor: first.NextCursor, IncludeTests: true}); err == nil {
		t.Fatal("cursor accepted changed filters")
	}
	// Page size may change; the result set and snapshot may not.
	second, err := QueryRoutes(g, RouteQuery{Cursor: first.NextCursor, Limit: 20})
	if err != nil || second.Returned != 1 {
		t.Fatalf("round trip: %+v, %v", second, err)
	}
	g.Routes = append(g.Routes, graph.HTTPRoute{Method: "GET", Path: "/0", File: "routes.go"})
	if _, err := QueryRoutes(g, RouteQuery{Cursor: first.NextCursor}); err == nil || !strings.Contains(err.Error(), "restart") {
		t.Fatalf("cursor accepted changed graph: %v", err)
	}
}

func TestSQLCursorRejectsChangedSnapshotAndFilters(t *testing.T) {
	g := &graph.Graph{SQLs: []graph.SQLEdge{
		{Query: "SELECT * FROM a", File: "sql.go", Line: 1},
		{Query: "SELECT * FROM b", File: "sql.go", Line: 2},
	}}
	first, err := QuerySQL(g, SQLQuery{Limit: 1})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page: %+v, %v", first, err)
	}
	if _, err := QuerySQL(g, SQLQuery{Cursor: first.NextCursor, NoTests: true}); err == nil {
		t.Fatal("cursor accepted changed filters")
	}
	second, err := QuerySQL(g, SQLQuery{Cursor: first.NextCursor, Limit: 20})
	if err != nil || second.Returned != 1 {
		t.Fatalf("round trip: %+v, %v", second, err)
	}
	g.SQLs[0].Query = "SELECT changed FROM a"
	if _, err := QuerySQL(g, SQLQuery{Cursor: first.NextCursor}); err == nil || !strings.Contains(err.Error(), "restart") {
		t.Fatalf("cursor accepted changed graph: %v", err)
	}
}
