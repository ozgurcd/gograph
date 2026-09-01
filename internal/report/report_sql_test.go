package report_test

import (
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/report"
)

func TestGenerateSQLReportsPostgreSQLClassification(t *testing.T) {
	g := &graph.Graph{SQLs: []graph.SQLEdge{{
		Query: "INSERT INTO audit.events SELECT id FROM public.users", Verb: "INSERT", Access: "write",
		Classification: "exact", Function: "ArchiveUsers", File: "archive.go", Line: 42,
		Tables: []graph.SQLTableRef{{Name: "audit.events", Access: "write"}, {Name: "public.users", Access: "read"}},
	}}}
	got := report.GenerateSQL(g)
	for _, want := range []string{
		"| Query | Verb | Access | Tables | Classification | Function | File | Line |",
		"`INSERT`", "`write`", "`audit.events (write), public.users (read)`", "`exact`", "`ArchiveUsers`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("SQL report omits %q:\n%s", want, got)
		}
	}
}
