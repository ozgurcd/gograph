package mcp_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	mcppkg "github.com/ozgurcd/gograph/internal/mcp"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestMCPRoutesIsFilteredProductionOnlyAndPaginated(t *testing.T) {
	g := &graph.Graph{
		Modules: []graph.ModuleNode{
			{Path: "example.com/auth", Dir: "auth-service"},
			{Path: "example.com/idp", Dir: "identuum-idp"},
		},
	}
	for index := range 125 {
		g.Routes = append(g.Routes, graph.HTTPRoute{
			Method:  "GET",
			Path:    fmt.Sprintf("/users/%03d", index),
			Handler: fmt.Sprintf("UserHandler%03d", index),
			File:    "auth-service/routes.go",
			Line:    index + 1,
		})
	}
	g.Routes = append(g.Routes,
		graph.HTTPRoute{Method: "DELETE", Path: "/users/test", Handler: "DeleteFixture", File: "auth-service/routes_test.go", Line: 1},
		graph.HTTPRoute{Method: "POST", Path: "/token", Handler: "IssueToken", File: "identuum-idp/routes.go", Line: 1},
	)

	handler := setupHandlers(t, g)["gograph_routes"]
	text := callTool(t, handler, nil)
	var first search.RoutePage
	if err := json.Unmarshal([]byte(text), &first); err != nil {
		t.Fatalf("decode first route page: %v\n%s", err, text)
	}
	if first.SchemaVersion != search.RoutesSchemaVersion || first.Total != 126 || first.Returned != search.DefaultRoutesLimit || first.IncludeTests {
		t.Fatalf("first route page = %+v", first)
	}
	if len(text) > search.MaxRoutesResultBytes {
		t.Fatalf("MCP route text bytes = %d, limit = %d", len(text), search.MaxRoutesResultBytes)
	}
	for _, route := range first.Routes {
		if strings.HasSuffix(route.File, "_test.go") {
			t.Fatalf("production-default MCP page includes test route %+v", route)
		}
	}

	filteredText := callTool(t, handler, map[string]any{
		"module":        "auth-service",
		"term":          "DELETE",
		"include_tests": true,
		"limit":         float64(10),
	})
	var filtered search.RoutePage
	if err := json.Unmarshal([]byte(filteredText), &filtered); err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 1 || filtered.Routes[0].Name != "DELETE /users/test" {
		t.Fatalf("filtered route page = %+v", filtered)
	}

	secondText := callTool(t, handler, map[string]any{"cursor": first.NextCursor})
	var second search.RoutePage
	if err := json.Unmarshal([]byte(secondText), &second); err != nil {
		t.Fatal(err)
	}
	if second.Total != first.Total || second.Returned != 26 || second.Truncated {
		t.Fatalf("second route page = %+v", second)
	}
}

func TestMCPRoutesAdvertisesEveryCLISelector(t *testing.T) {
	g := &graph.Graph{}
	registered, ok := mcppkg.NewServer(g, mockRebuild(g), mockBuildGraph(), mockBuildBaseline(), "dev").ListTools()["gograph_routes"]
	if !ok {
		t.Fatal("gograph_routes is not registered")
	}
	for _, name := range []string{"term", "module", "include_tests", "limit", "cursor"} {
		if _, ok := registered.Tool.InputSchema.Properties[name]; !ok {
			t.Errorf("gograph_routes schema omits %q", name)
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

func TestMCPRoutesBoundsARealisticLargeCensus(t *testing.T) {
	g := &graph.Graph{Modules: []graph.ModuleNode{{Path: "example.com/identuum", Dir: "."}}}
	for i := 0; i < 2782; i++ {
		g.Routes = append(g.Routes, graph.HTTPRoute{
			Method:  "GET",
			Path:    fmt.Sprintf("/route/%04d", i),
			Handler: fmt.Sprintf("HandleRoute%04d", i),
			File:    fmt.Sprintf("service-%d/routes.go", i%9),
			Line:    i + 1,
		})
	}

	textResult := callTool(t, setupHandlers(t, g)["gograph_routes"], nil)
	if len(textResult) > search.MaxRoutesResultBytes {
		t.Fatalf("MCP route page size = %d, max = %d", len(textResult), search.MaxRoutesResultBytes)
	}
	var page search.RoutePage
	if err := json.Unmarshal([]byte(textResult), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 2782 || page.Returned != search.DefaultRoutesLimit || !page.Truncated || page.NextCursor == "" {
		t.Fatalf("large census page = %+v", page)
	}
}
