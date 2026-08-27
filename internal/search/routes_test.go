package search

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestQueryRoutesFiltersTestsTermsAndModules(t *testing.T) {
	g := &graph.Graph{
		Root: "/workspace/identuum-idp",
		Modules: []graph.ModuleNode{
			{ID: "example.com/root", Path: "example.com/root", Dir: "."},
			{ID: "example.com/auth", Path: "example.com/auth", Dir: "services/auth"},
			{ID: "example.com/idp", Path: "example.com/idp", Dir: "services/idp"},
		},
		Routes: []graph.HTTPRoute{
			{Method: "GET", Path: "/health", Handler: "Health", File: "routes.go", Line: 10},
			{Method: "GET", Path: "/users", Handler: "ListUsers", File: "services/auth/routes.go", Line: 20},
			{Method: "DELETE", Path: "/users/:id", Handler: "DeleteUser", File: "services/auth/routes_test.go", Line: 30},
			{Method: "POST", Path: "/token", Handler: "IssueToken", File: "services/idp/routes.go", Line: 40},
		},
	}

	page, err := QueryRoutes(g, RouteQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || page.IncludeTests || len(page.Routes) != 3 {
		t.Fatalf("default page = %+v, want three production routes", page)
	}
	for _, route := range page.Routes {
		if strings.HasSuffix(route.File, "_test.go") {
			t.Fatalf("default page includes test route %+v", route)
		}
	}

	auth, err := QueryRoutes(g, RouteQuery{Module: "auth", IncludeTests: true})
	if err != nil {
		t.Fatal(err)
	}
	if auth.Total != 2 {
		t.Fatalf("auth total = %d, want 2", auth.Total)
	}
	for _, route := range auth.Routes {
		if !strings.HasPrefix(route.File, "services/auth/") {
			t.Fatalf("auth filter leaked route %+v", route)
		}
	}

	root, err := QueryRoutes(g, RouteQuery{Module: "example.com/root"})
	if err != nil {
		t.Fatal(err)
	}
	if root.Total != 1 || root.Routes[0].Name != "GET /health" {
		t.Fatalf("root module page = %+v, want only root-owned route", root)
	}
	rootByRepositoryName, err := QueryRoutes(g, RouteQuery{Module: "identuum-idp"})
	if err != nil {
		t.Fatal(err)
	}
	if rootByRepositoryName.Total != 1 || rootByRepositoryName.Routes[0].Name != "GET /health" {
		t.Fatalf("root module basename page = %+v, want only root-owned route", rootByRepositoryName)
	}

	filtered, err := QueryRoutes(g, RouteQuery{Term: "issueTOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 1 || filtered.Routes[0].Name != "POST /token" {
		t.Fatalf("term-filtered page = %+v", filtered)
	}

	if _, err := QueryRoutes(g, RouteQuery{Module: "missing"}); err == nil || !strings.Contains(err.Error(), "available modules") {
		t.Fatalf("missing module error = %v", err)
	}
}

func TestQueryRoutesPaginatesDeterministically(t *testing.T) {
	g := &graph.Graph{}
	for index := range 205 {
		g.Routes = append(g.Routes, graph.HTTPRoute{
			Method:  "GET",
			Path:    fmt.Sprintf("/route/%03d", index),
			Handler: fmt.Sprintf("Handler%03d", index),
			File:    "routes.go",
			Line:    index + 1,
		})
	}

	first, err := QueryRoutes(g, RouteQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 205 || first.Returned != DefaultRoutesLimit || !first.Truncated || first.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := QueryRoutes(g, RouteQuery{Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	third, err := QueryRoutes(g, RouteQuery{Cursor: second.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if second.Returned != DefaultRoutesLimit || third.Returned != 5 || third.Truncated || third.NextCursor != "" {
		t.Fatalf("later pages: second=%+v third=%+v", second, third)
	}

	seen := map[string]bool{}
	for _, page := range []RoutePage{first, second, third} {
		for _, route := range page.Routes {
			if seen[route.Name] {
				t.Fatalf("duplicate route across pages: %s", route.Name)
			}
			seen[route.Name] = true
		}
	}
	if len(seen) != 205 {
		t.Fatalf("paginated routes = %d, want 205", len(seen))
	}
}

func TestQueryRoutesEnforcesSerializedPageBudget(t *testing.T) {
	g := &graph.Graph{}
	for index := range MaxRoutesLimit {
		g.Routes = append(g.Routes, graph.HTTPRoute{
			Method:  "GET",
			Path:    fmt.Sprintf("/route/%03d", index),
			Handler: strings.Repeat("handler", 100),
			File:    "routes.go",
			Line:    index + 1,
		})
	}
	page, err := QueryRoutes(g, RouteQuery{Limit: MaxRoutesLimit})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > MaxRoutesResultBytes {
		t.Fatalf("encoded page bytes = %d, limit = %d", len(encoded), MaxRoutesResultBytes)
	}
	if !page.Truncated || page.Returned >= MaxRoutesLimit || page.NextCursor == "" {
		t.Fatalf("byte-bounded page = %+v", page)
	}
}

func TestQueryRoutesRejectsInvalidCursorAndAmbiguousModule(t *testing.T) {
	g := &graph.Graph{Modules: []graph.ModuleNode{
		{Path: "example.com/a", Dir: "services/a"},
		{Path: "example.com/b", Dir: "plugins/a"},
	}}
	if _, err := QueryRoutes(g, RouteQuery{Cursor: "not-a-cursor"}); err == nil || !strings.Contains(err.Error(), "invalid route cursor") {
		t.Fatalf("invalid cursor error = %v", err)
	}
	if _, err := QueryRoutes(g, RouteQuery{Module: "a"}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous module error = %v", err)
	}
}

func TestQueryRoutesBoundsFiltersAndEmptyPages(t *testing.T) {
	g := &graph.Graph{}
	if _, err := QueryRoutes(g, RouteQuery{Term: strings.Repeat("x", maxRouteTermBytes+1)}); err == nil || !strings.Contains(err.Error(), "term") {
		t.Fatalf("oversized term error = %v", err)
	}
	if _, err := QueryRoutes(g, RouteQuery{Module: strings.Repeat("x", maxRouteModuleBytes+1)}); err == nil || !strings.Contains(err.Error(), "module selector") {
		t.Fatalf("oversized module error = %v", err)
	}
	if _, err := QueryRoutes(g, RouteQuery{Cursor: strings.Repeat("x", maxRouteCursorBytes+1)}); err == nil || !strings.Contains(err.Error(), "invalid route cursor") {
		t.Fatalf("oversized cursor error = %v", err)
	}

	page, err := QueryRoutes(g, RouteQuery{Term: strings.Repeat("x", maxRouteTermBytes)})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > MaxRoutesResultBytes {
		t.Fatalf("empty page size = %d, max = %d", len(encoded), MaxRoutesResultBytes)
	}
}
