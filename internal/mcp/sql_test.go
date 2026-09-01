package mcp_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	mcppkg "github.com/ozgurcd/gograph/internal/mcp"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestMCPSQLUsesSharedPostgreSQLFiltersAndPagination(t *testing.T) {
	g := &graph.Graph{
		Modules: []graph.ModuleNode{{Path: "example.com/auth", Dir: "auth-service"}, {Path: "example.com/audit", Dir: "audit-service"}},
		SQLs: []graph.SQLEdge{
			{Query: "SELECT id FROM public.users", Function: "ListUsers", File: "auth-service/users.go", Line: 10},
			{Query: "UPDATE public.users SET active = false", Function: "DisableUser", File: "auth-service/users.go", Line: 20},
			{Query: "DELETE FROM public.users", Function: "DeleteFixture", File: "auth-service/users_test.go", Line: 30},
			{Query: "INSERT INTO audit.events (id) SELECT id FROM public.users", Function: "Archive", File: "audit-service/archive.go", Line: 40},
		},
	}
	handler := setupHandlers(t, g)["gograph_sql"]
	text := callTool(t, handler, map[string]any{
		"tables": []any{"users"}, "verbs": []any{"SELECT", "UPDATE"}, "accesses": []any{"read"},
		"function": "list", "module": "auth-service", "no_tests": true, "limit": float64(1),
	})
	var filtered search.SQLPage
	if err := json.Unmarshal([]byte(text), &filtered); err != nil {
		t.Fatalf("decode filtered SQL page: %v\n%s", err, text)
	}
	expected, err := search.QuerySQL(g, search.SQLQuery{
		Tables: []string{"users"}, Verbs: []string{"SELECT", "UPDATE"}, Accesses: []string{"read"},
		Function: "list", Module: "auth-service", NoTests: true, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(filtered, expected) {
		t.Fatalf("MCP SQL page differs from shared native contract:\nMCP:    %+v\nshared: %+v", filtered, expected)
	}
	if filtered.SchemaVersion != search.SQLSchemaVersion || filtered.Total != 1 || filtered.Returned != 1 || filtered.IncludeTests {
		t.Fatalf("filtered SQL page = %+v", filtered)
	}
	if filtered.Queries[0].Verb != "SELECT" || filtered.Queries[0].Tables[0].Name != "public.users" {
		t.Fatalf("filtered SQL result = %+v", filtered.Queries[0])
	}

	firstText := callTool(t, handler, map[string]any{"tables": []any{"users"}, "no_tests": true, "limit": float64(2)})
	var first search.SQLPage
	if err := json.Unmarshal([]byte(firstText), &first); err != nil {
		t.Fatal(err)
	}
	if first.Total != 3 || first.Returned != 2 || !first.Truncated || first.NextCursor == "" {
		t.Fatalf("first SQL page = %+v", first)
	}
	secondText := callTool(t, handler, map[string]any{
		"tables": []any{"users"}, "no_tests": true, "limit": float64(2), "cursor": first.NextCursor,
	})
	var second search.SQLPage
	if err := json.Unmarshal([]byte(secondText), &second); err != nil {
		t.Fatal(err)
	}
	if second.Total != first.Total || second.Returned != 1 || second.Truncated {
		t.Fatalf("second SQL page = %+v", second)
	}
	if len(firstText) > search.MaxSQLResultBytes || len(secondText) > search.MaxSQLResultBytes {
		t.Fatalf("SQL pages exceed %d-byte budget: first=%d second=%d", search.MaxSQLResultBytes, len(firstText), len(secondText))
	}
}

func TestMCPSQLAdvertisesEveryCLISelector(t *testing.T) {
	g := &graph.Graph{}
	registered, ok := mcppkg.NewServer(g, mockRebuild(g), mockBuildGraph(), mockBuildBaseline(), "dev").ListTools()["gograph_sql"]
	if !ok {
		t.Fatal("gograph_sql is not registered")
	}
	for _, name := range []string{"term", "tables", "verbs", "accesses", "function", "module", "no_tests", "limit", "cursor"} {
		if _, ok := registered.Tool.InputSchema.Properties[name]; !ok {
			t.Errorf("gograph_sql schema omits %q", name)
		}
	}
	for _, name := range []string{"tables", "verbs", "accesses"} {
		encoded, err := json.Marshal(registered.Tool.InputSchema.Properties[name])
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), `"type":"array"`) {
			t.Fatalf("%s schema = %s, want array", name, encoded)
		}
	}
	encoded, err := json.Marshal(registered.Tool.InputSchema.Properties["limit"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"type":"integer"`) {
		t.Fatalf("limit schema = %s, want integer", encoded)
	}
}

func TestMCPSQLBoundsARealisticLargeCensus(t *testing.T) {
	g := &graph.Graph{Modules: []graph.ModuleNode{{Path: "example.com/large", Dir: "."}}}
	for i := 0; i < 2782; i++ {
		g.SQLs = append(g.SQLs, graph.SQLEdge{
			Query:    fmt.Sprintf("SELECT id, '%s' AS payload FROM table_%04d WHERE id = $1", strings.Repeat("x", 400), i),
			Function: fmt.Sprintf("Query%04d", i), File: fmt.Sprintf("service-%d/query.go", i%9), Line: i + 1,
		})
	}
	text := callTool(t, setupHandlers(t, g)["gograph_sql"], nil)
	if len(text) > search.MaxSQLResultBytes {
		t.Fatalf("MCP SQL page size = %d, max = %d", len(text), search.MaxSQLResultBytes)
	}
	var page search.SQLPage
	if err := json.Unmarshal([]byte(text), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 2782 || page.Returned >= search.DefaultSQLLimit || page.Returned == 0 || !page.Truncated || page.NextCursor == "" {
		t.Fatalf("large SQL census page = %+v", page)
	}
}
