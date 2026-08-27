package parser_test

import (
	"go/token"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/parser"
)

// wrappedRoutesFixture returns the absolute path to the wrapped_routes test
// fixture. The fixture exercises middleware-wrapped HTTP handler
// registration patterns that the route extractor must unwrap so that
// orphan-reachability analysis can mark the inner method as reachable.
func wrappedRoutesFixture(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", "wrapped_routes", "wrapped_routes.go")
}

func TestParseFile_RouteExtractor_UnwrapsMiddleware(t *testing.T) {
	fset := token.NewFileSet()
	fixturePath := wrappedRoutesFixture(t)
	result, err := parser.ParseFile(fset, fixturePath, "wrapped_routes/wrapped_routes.go", "example.com/wrapped_routes")
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	// Build a path -> handler map. The test fixture intentionally uses
	// distinct URL paths for each registration shape so the assertions
	// can be unambiguous.
	handlers := map[string]string{}
	dynamic := map[string]bool{}
	for _, r := range result.Routes {
		handlers[r.Path] = r.Handler
		dynamic[r.Path] = r.DynamicHandler
	}

	cases := []struct {
		path        string
		expectedSub string // expected substring of the handler (best-effort match — normalizeSymbolName strips qualifications downstream)
		note        string
	}{
		{"/direct", "customers", "direct method value: existing behaviour, must not regress"},
		{"/wrapped", "licenses", "single wrapper: guard(h.licenses) → 'licenses', NOT 'guard'"},
		{"/nested", "downloadLic", "nested wrappers: cors(guard(h.downloadLic)) → 'downloadLic'"},
		{"/bare", "PlainHandler", "bare function through wrapper: guard(PlainHandler) → 'PlainHandler'"},
		{"/variadic", "Handler", "variadic middleware: the final Handler(deps) argument wins over RequireScopes(ScopeOrgsUpdate)"},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got, ok := handlers[tc.path]
			if !ok {
				paths := make([]string, 0, len(handlers))
				for p := range handlers {
					paths = append(paths, p)
				}
				sort.Strings(paths)
				t.Fatalf("no route recorded for path %q. Recorded paths: %v", tc.path, paths)
			}
			if !strings.Contains(got, tc.expectedSub) {
				t.Errorf("%s\n  path %q: handler = %q, expected to contain %q",
					tc.note, tc.path, got, tc.expectedSub)
			}
			// The whole point of the fix: middleware wrapper names must not
			// be recorded as the handler when an inner reference exists.
			if tc.path != "/direct" && (got == "guard" || got == "cors") {
				t.Errorf("%s\n  path %q: handler = %q — wrapper name recorded instead of inner callable",
					tc.note, tc.path, got)
			}
		})
	}
	if handlers["/variadic"] == "ScopeOrgsUpdate" || strings.Contains(handlers["/variadic"], "RequireScopes") {
		t.Fatalf("variadic route selected middleware as handler: %q", handlers["/variadic"])
	}
	if !dynamic["/variadic"] {
		t.Fatal("Handler(deps) should retain the truthful dynamic-factory annotation")
	}
}
