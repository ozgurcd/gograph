package search_test

import (
	"path/filepath"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestQuerySQLFiltersPostgreSQLFactsAndPaginates(t *testing.T) {
	g := &graph.Graph{
		Modules: []graph.ModuleNode{
			{Path: "example.com/auth", Dir: "auth-service"},
			{Path: "example.com/audit", Dir: "audit-service"},
		},
		SQLs: []graph.SQLEdge{
			{Query: "SELECT id FROM public.users", Function: "ListUsers", File: "auth-service/users.go", Line: 10},
			{Query: "UPDATE public.users SET active = false WHERE id = $1", Function: "DisableUser", File: "auth-service/users.go", Line: 20},
			{Query: "DELETE FROM public.users WHERE id = $1", Function: "DeleteFixture", File: "auth-service/users_test.go", Line: 30},
			{Query: "INSERT INTO audit.events (id) SELECT id FROM public.users", Function: "ArchiveUsers", File: "audit-service/archive.go", Line: 40},
			{Query: "CREATE INDEX users_email_idx ON public.users (email)", Function: "Migrate", File: "auth-service/migrations.go", Line: 50},
		},
	}

	page, err := search.QuerySQL(g, search.SQLQuery{
		Tables: []string{"users"}, Verbs: []string{"select", "update"}, Accesses: []string{"read"},
		Function: "list", Module: "auth-service", NoTests: true, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.SchemaVersion != search.SQLSchemaVersion || page.Total != 1 || page.Returned != 1 || page.Truncated || page.IncludeTests {
		t.Fatalf("filtered SQL page = %+v", page)
	}
	if len(page.Queries) != 1 || page.Queries[0].Verb != "SELECT" || page.Queries[0].Access != "read" || page.Queries[0].Classification != "exact" {
		t.Fatalf("filtered SQL row = %+v", page.Queries)
	}
	if len(page.Queries[0].Tables) != 1 || page.Queries[0].Tables[0].Name != "public.users" {
		t.Fatalf("filtered SQL tables = %+v", page.Queries[0].Tables)
	}

	first, err := search.QuerySQL(g, search.SQLQuery{Tables: []string{"users"}, NoTests: true, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 4 || first.Returned != 2 || !first.Truncated || first.NextCursor == "" {
		t.Fatalf("first SQL page = %+v", first)
	}
	second, err := search.QuerySQL(g, search.SQLQuery{Tables: []string{"users"}, NoTests: true, Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if second.Total != first.Total || second.Returned != 2 || second.Truncated || second.NextCursor != "" {
		t.Fatalf("second SQL page = %+v", second)
	}
	if second.Queries[0].Query == first.Queries[0].Query && second.Queries[0].File == first.Queries[0].File && second.Queries[0].Line == first.Queries[0].Line {
		t.Fatalf("cursor repeated first-page row: first=%+v second=%+v", first, second)
	}

	withTests, err := search.QuerySQL(g, search.SQLQuery{Verbs: []string{"delete"}})
	if err != nil || withTests.Total != 1 || !withTests.IncludeTests {
		t.Fatalf("default compatibility must include tests: page=%+v err=%v", withTests, err)
	}
}

func TestQuerySQLRejectsInvalidOrUnboundedFilters(t *testing.T) {
	g := &graph.Graph{}
	for name, query := range map[string]search.SQLQuery{
		"verb":   {Verbs: []string{"UPSERT"}},
		"access": {Accesses: []string{"execute"}},
		"table":  {Tables: []string{"users; DROP TABLE users"}},
		"limit":  {Limit: search.MaxSQLLimit + 1},
		"cursor": {Cursor: "not-base64!"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := search.QuerySQL(g, query); err == nil {
				t.Fatalf("QuerySQL(%+v) succeeded, want error", query)
			}
		})
	}
}

func TestQuerySQLClassifiesLegacyEdgesWithoutPersistedMetadata(t *testing.T) {
	g := &graph.Graph{SQLs: []graph.SQLEdge{{
		Query:    "WITH active AS (SELECT id FROM users) UPDATE sessions SET active = true FROM active WHERE sessions.user_id = active.id",
		Function: "Refresh", File: "refresh.go", Line: 7,
	}}}
	page, err := search.QuerySQL(g, search.SQLQuery{Tables: []string{"sessions"}, Verbs: []string{"UPDATE"}})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Queries[0].Verb != "UPDATE" || page.Queries[0].Classification != "exact" {
		t.Fatalf("legacy edge classification = %+v", page)
	}
}

func TestQuerySQLAcceptsRepositoryBasenameForRootModule(t *testing.T) {
	g := &graph.Graph{
		Root:    filepath.Join(t.TempDir(), "identuum-idp"),
		Modules: []graph.ModuleNode{{Path: "github.com/identuum/identuum-idp", Dir: "."}},
		SQLs:    []graph.SQLEdge{{Query: "SELECT id FROM users", Function: "List", File: "users.go", Line: 1}},
	}
	page, err := search.QuerySQL(g, search.SQLQuery{Module: "identuum-idp"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Module != "identuum-idp" {
		t.Fatalf("root-module SQL page = %+v", page)
	}
}

func TestQuerySQLWriteAccessIncludesDataModifyingCTE(t *testing.T) {
	g := &graph.Graph{SQLs: []graph.SQLEdge{{
		Query:    "WITH changed AS (UPDATE users SET active = false RETURNING id) SELECT id FROM changed",
		Function: "DeactivateUsers", File: "users.go", Line: 12,
	}}}
	page, err := search.QuerySQL(g, search.SQLQuery{Accesses: []string{"write"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Queries[0].Verb != "SELECT" || page.Queries[0].Access != "write" {
		t.Fatalf("data-modifying CTE must be visible to write access filter: %+v", page)
	}
	if len(page.Queries[0].Tables) != 1 || page.Queries[0].Tables[0].Name != "users" || page.Queries[0].Tables[0].Access != "write" {
		t.Fatalf("data-modifying CTE table metadata = %+v", page.Queries[0].Tables)
	}
}
