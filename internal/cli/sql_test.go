package cli

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestCLISQLUsesSharedFilteredPageContract(t *testing.T) {
	g := &graph.Graph{
		Modules: []graph.ModuleNode{{Path: "example.com/auth", Dir: "auth-service"}, {Path: "example.com/audit", Dir: "audit-service"}},
		SQLs: []graph.SQLEdge{
			{Query: "SELECT id FROM public.users", Function: "ListUsers", File: "auth-service/users.go", Line: 10},
			{Query: "UPDATE public.users SET active = false", Function: "DisableUser", File: "auth-service/users.go", Line: 20},
			{Query: "DELETE FROM public.users", Function: "DeleteFixture", File: "auth-service/users_test.go", Line: 30},
			{Query: "INSERT INTO audit.events (id) SELECT id FROM public.users", Function: "Archive", File: "audit-service/archive.go", Line: 40},
		},
	}
	root := writeCLIParityGraph(t, g)
	type sqlEnvelope struct {
		Count      int            `json:"count"`
		Total      *int           `json:"total"`
		Returned   *int           `json:"returned"`
		Truncated  *bool          `json:"truncated"`
		NextCursor *string        `json:"next_cursor"`
		Results    search.SQLPage `json:"results"`
	}
	runPage := func(args ...string) sqlEnvelope {
		stdout, stderr, code := runCLIParityInDir(t, root, func() int {
			return Run(append([]string{"sql"}, args...))
		})
		if code != 0 {
			t.Fatalf("sql %v failed with code %d: %s", args, code, stderr)
		}
		var envelope sqlEnvelope
		if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
			t.Fatalf("sql %v did not return JSON: %v\n%s", args, err, stdout)
		}
		return envelope
	}

	filteredEnvelope := runPage("--table", "users", "--verb", "SELECT", "--verb", "UPDATE", "--access", "read", "--function", "list", "--module", "auth-service", "--no-tests", "--limit", "1", "--json")
	filtered := filteredEnvelope.Results
	expected, err := search.QuerySQL(g, search.SQLQuery{
		Tables: []string{"users"}, Verbs: []string{"SELECT", "UPDATE"}, Accesses: []string{"read"},
		Function: "list", Module: "auth-service", NoTests: true, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(filtered, expected) {
		t.Fatalf("CLI SQL page differs from shared native contract:\nCLI:    %+v\nshared: %+v", filtered, expected)
	}
	if filtered.Total != 1 || filtered.Returned != 1 || filtered.IncludeTests || filtered.Queries[0].Verb != "SELECT" {
		t.Fatalf("filtered CLI SQL page = %+v", filtered)
	}
	if filteredEnvelope.Total == nil || *filteredEnvelope.Total != filtered.Total ||
		filteredEnvelope.Returned == nil || *filteredEnvelope.Returned != filtered.Returned ||
		filteredEnvelope.Truncated == nil || *filteredEnvelope.Truncated != filtered.Truncated ||
		filteredEnvelope.NextCursor == nil || *filteredEnvelope.NextCursor != filtered.NextCursor ||
		filteredEnvelope.Count != filtered.Returned {
		t.Fatalf("CLI SQL envelope does not expose the MCP pagination contract: envelope=%+v page=%+v", filteredEnvelope, filtered)
	}

	firstEnvelope := runPage("--table", "users", "--no-tests", "--limit", "2", "--json")
	first := firstEnvelope.Results
	if first.Total != 3 || first.Returned != 2 || !first.Truncated || first.NextCursor == "" {
		t.Fatalf("first CLI SQL page = %+v", first)
	}
	secondEnvelope := runPage("--table", "users", "--no-tests", "--limit", "2", "--cursor", first.NextCursor, "--json")
	second := secondEnvelope.Results
	if second.Total != first.Total || second.Returned != 1 || second.Truncated || second.NextCursor != "" {
		t.Fatalf("second CLI SQL page = %+v", second)
	}
	if secondEnvelope.NextCursor == nil || *secondEnvelope.NextCursor != "" {
		t.Fatalf("terminal CLI SQL envelope must expose an empty next_cursor: %+v", secondEnvelope)
	}

	stdout, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"sql", "SELECT"}) })
	if code != 0 || !strings.Contains(stdout, "executed by ListUsers") || stderr != "" {
		t.Fatalf("legacy SQL text changed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	large := &graph.Graph{Modules: []graph.ModuleNode{{Path: "example.com/large", Dir: "."}}}
	for i := 0; i < 205; i++ {
		large.SQLs = append(large.SQLs, graph.SQLEdge{
			Query: fmt.Sprintf("SELECT id FROM table_%03d", i), Function: "Query",
			File: fmt.Sprintf("query-%03d.go", i), Line: i + 1,
		})
	}
	largeRoot := writeCLIParityGraph(t, large)
	stdout, stderr, code = runCLIParityInDir(t, largeRoot, func() int { return Run([]string{"sql", "--files-only"}) })
	if code != 0 || len(strings.Fields(stdout)) != 205 {
		t.Fatalf("sql --files-only must preserve the complete file census: code=%d files=%d stderr=%s", code, len(strings.Fields(stdout)), stderr)
	}
}

func TestCLISQLHelpDocumentsEveryFilterAndContract(t *testing.T) {
	stdout, stderr, code := runCLIParityInDir(t, t.TempDir(), func() int {
		return Run([]string{"sql", "--help"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("sql --help failed: code=%d stderr=%q", code, stderr)
	}
	for _, text := range []string{
		"gograph.sql.v1", "PostgreSQL", "--table", "--verb", "--access", "--function",
		"--module", "--no-tests", "--limit", "--cursor", "next_cursor", "partial", "unknown",
	} {
		if !strings.Contains(stdout, text) {
			t.Errorf("sql --help omits %q:\n%s", text, stdout)
		}
	}
}
