// Package wrapped_routes is a parser-test fixture exercising the
// middleware-wrapped HTTP handler registration pattern. The route
// extractor must record the *inner* method value as the route handler,
// not the outer middleware wrapper, so orphan-reachability analysis
// correctly marks the real handler as reachable.
package wrapped_routes

import "net/http"

type AdminHandler struct{}

type Router struct{}

type Dependencies struct{}

const ScopeOrgsUpdate = "orgs:update"

func (h *AdminHandler) customers(w http.ResponseWriter, r *http.Request)   {}
func (h *AdminHandler) licenses(w http.ResponseWriter, r *http.Request)    {}
func (h *AdminHandler) downloadLic(w http.ResponseWriter, r *http.Request) {}

// guard is a generic middleware wrapper.
func guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { next(w, r) }
}

// cors wraps another wrapper, exercising nested wrappers.
func cors(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { next(w, r) }
}

// PlainHandler is a non-method handler used to verify that bare function
// identifiers passed through a wrapper are also recovered.
func PlainHandler(w http.ResponseWriter, r *http.Request) {}

func (*Router) POST(_ string, _ ...any) {}

func RequireScopes(_ ...string) any { return nil }

func Handler(_ Dependencies) any { return nil }

// Register wires the routes. Three patterns intentionally exercised:
//
//  1. Direct method value:                    mux.HandleFunc("/direct", h.customers)
//  2. Single wrapper:                         mux.HandleFunc("/wrapped", guard(h.licenses))
//  3. Nested wrappers:                        mux.HandleFunc("/nested", cors(guard(h.downloadLic)))
//  4. Bare function through wrapper:          mux.HandleFunc("/bare", guard(PlainHandler))
//
// All four should produce HTTPRoute entries whose Handler is the inner
// callable, not "guard"/"cors".
func Register(mux *http.ServeMux, h *AdminHandler) {
	mux.HandleFunc("/direct", h.customers)
	mux.HandleFunc("/wrapped", guard(h.licenses))
	mux.HandleFunc("/nested", cors(guard(h.downloadLic)))
	mux.HandleFunc("/bare", guard(PlainHandler))
}

// RegisterVariadic verifies that route extraction selects the final variadic
// argument instead of mistaking middleware configuration for the handler.
func RegisterVariadic(group *Router, deps Dependencies) {
	group.POST("/variadic", RequireScopes(ScopeOrgsUpdate), Handler(deps))
}
