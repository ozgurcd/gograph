package parser_test

import (
	"go/token"
	"testing"

	"github.com/ozgurcd/gograph/internal/parser"
)

func TestParseSourceResolvesGroupedAndNestedRoutes(t *testing.T) {
	source := []byte(`package routes

func register(router *Router) {
	v1 := router.Group("/api/v1")
	users := v1.Group("/users")
	users.POST("/:id", updateUser)
	router.Group("/health").GET("/ready", ready)
	router.Route("/admin", func(r Router) {
		r.With(auth).Get("/audit", audit)
		r.Route("/teams", func(team Router) {
			team.Delete("/{id}", deleteTeam)
		})
	})
}
`)
	result, err := parser.ParseSource(token.NewFileSet(), "routes.go", source, "routes.go", "example.com/routes")
	if err != nil {
		t.Fatalf("ParseSource returned error: %v", err)
	}

	got := make(map[string]string)
	for _, route := range result.Routes {
		got[route.Method+" "+route.Path] = route.Handler
	}
	want := map[string]string{
		"POST /api/v1/users/:id":   "updateUser",
		"GET /health/ready":        "ready",
		"GET /admin/audit":         "audit",
		"DELETE /admin/teams/{id}": "deleteTeam",
	}
	for route, handler := range want {
		if got[route] != handler {
			t.Errorf("route %q handler = %q, want %q; all routes: %#v", route, got[route], handler, got)
		}
	}
}

func TestParseSourceDoesNotInventDynamicGroupPrefixes(t *testing.T) {
	source := []byte(`package routes

func register(router *Router, prefix string) {
	group := router.Group(prefix)
	group.GET("/status", status)
}
`)
	result, err := parser.ParseSource(token.NewFileSet(), "routes.go", source, "routes.go", "example.com/routes")
	if err != nil {
		t.Fatalf("ParseSource returned error: %v", err)
	}
	if len(result.Routes) != 1 {
		t.Fatalf("routes = %#v, want one route", result.Routes)
	}
	if got := result.Routes[0].Path; got != "/status" {
		t.Fatalf("dynamic group route path = %q, want conservative %q", got, "/status")
	}
}
