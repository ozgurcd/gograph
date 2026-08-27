package parser

import (
	"go/token"
	"testing"
)

func TestParseSourceKeepsEchoHandlerBeforeVariadicMiddleware(t *testing.T) {
	t.Parallel()

	source := []byte(`package routes

import echov4 "github.com/labstack/echo/v4"

type Dependencies struct{}

type CustomFactory struct{}
type CustomRouter struct{}

const ScopeOrgsUpdate = "orgs:update"

func Handler(Dependencies) echov4.HandlerFunc { return nil }
func RequireScopes(...string) echov4.MiddlewareFunc { return nil }
func (CustomFactory) New() *CustomRouter { return nil }
func (*CustomRouter) POST(string, ...any) {}

func Register(e *echov4.Echo, deps Dependencies) {
	e.POST("/parameter", Handler(deps), RequireScopes(ScopeOrgsUpdate))

	group := e.Group("/v1")
	group.GET("/group", Handler(deps), RequireScopes(ScopeOrgsUpdate))

	direct := echov4.New()
	direct.PUT("/constructor", Handler(deps), RequireScopes(ScopeOrgsUpdate))

	echov4.New().DELETE("/direct", Handler(deps), RequireScopes(ScopeOrgsUpdate))

	echov4 := CustomFactory{}
	shadowed := echov4.New()
	shadowed.POST("/shadowed-import", RequireScopes(ScopeOrgsUpdate), Handler(deps))
}
`)

	result, err := ParseSource(token.NewFileSet(), "routes.go", source, "routes.go", "example.com/routes")
	if err != nil {
		t.Fatalf("ParseSource: %v", err)
	}

	want := map[string]bool{
		"/parameter":       true,
		"/v1/group":        true,
		"/constructor":     true,
		"/direct":          true,
		"/shadowed-import": true,
	}
	for _, route := range result.Routes {
		if !want[route.Path] {
			continue
		}
		if route.Handler != "Handler" {
			t.Errorf("route %s selected %q instead of Echo's handler argument", route.Path, route.Handler)
		}
		if !route.DynamicHandler {
			t.Errorf("route %s should retain the dynamic factory annotation", route.Path)
		}
		delete(want, route.Path)
	}
	for path := range want {
		t.Errorf("missing Echo route %s", path)
	}
}
