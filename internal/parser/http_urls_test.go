package parser

import (
	"fmt"
	"go/token"
	"strings"
	"testing"
)

func TestHTTPURLStaticAndConfiguredEvidence(t *testing.T) {
	tests := []struct {
		name, body, url, base, suffix, method string
		dynamic, requestOnly                  bool
	}{
		{name: "literal", body: `h.Get("https://api/items")`, url: "https://api/items", method: "GET"},
		{name: "raw", body: "h.Head(`https://api/items`)", url: "https://api/items", method: "HEAD"},
		{name: "local const", body: `const q = "https://api/" + "items"; h.Get(q)`, url: "https://api/items", method: "GET"},
		{name: "config selector", body: `h.Get(cfg.API + "/items")`, url: "cfg.API/items", base: "cfg.API", suffix: "/items", method: "GET", dynamic: true},
		{name: "alias", body: `endpointBase := cfg.API; u := endpointBase + "/" + "items"; h.Post(u, "", nil)`, url: "cfg.API/items", base: "cfg.API", suffix: "/items", method: "POST", dynamic: true},
		{name: "environment key", body: `u := env.Getenv("API_URL") + "/items"; h.Get(u)`, url: "env:API_URL/items", base: "env:API_URL", suffix: "/items", method: "GET", dynamic: true},
		{name: "parameter", body: `h.Get(base + "/items")`, url: "base/items", base: "base", suffix: "/items", method: "GET", dynamic: true},
		{name: "dynamic tail", body: `h.Get(cfg.API + "/items/" + id)`, url: "<dynamic>", method: "GET", dynamic: true},
		{name: "reassigned alias", body: `u := cfg.API + "/items"; u = other; h.Get(u)`, url: "<dynamic>", method: "GET", dynamic: true},
		{name: "reassigned parameter", body: `base = other; h.Get(base + "/items")`, url: "<dynamic>", method: "GET", dynamic: true},
		{name: "shadowed environment", body: `env := cfg; h.Get(env.Getenv("API_URL") + "/items")`, url: "<dynamic>", method: "GET", dynamic: true},
		{name: "request", body: `h.NewRequest("POST", cfg.API + "/items", nil)`, url: "cfg.API/items", base: "cfg.API", suffix: "/items", method: "POST", dynamic: true, requestOnly: true},
		{name: "context request", body: `h.NewRequestWithContext(ctx, "", cfg.API + "/items", nil)`, url: "cfg.API/items", base: "cfg.API", suffix: "/items", method: "GET", dynamic: true, requestOnly: true},
		{name: "dynamic method", body: `h.NewRequest(method, "https://api/items", nil)`, url: "https://api/items", method: "ANY", requestOnly: true},
		{name: "method constant", body: `h.NewRequest(h.MethodPatch, "https://api/items", nil)`, url: "https://api/items", method: "PATCH", requestOnly: true},
		{name: "escaped local", body: `u := "https://api/items"; mutate(&u); h.Get(u)`, url: "<dynamic>", method: "GET", dynamic: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("API_URL", "https://secret.invalid/must-not-be-read")
			src := "package p\nimport (h \"net/http\"; env \"os\")\nfunc Call(base string) { " + test.body + " }"
			result, err := ParseSource(token.NewFileSet(), "client.go", []byte(src), "client.go", "example.com/client")
			if err != nil || len(result.HTTPCalls) != 1 {
				t.Fatalf("parse = %+v, %v", result, err)
			}
			got := result.HTTPCalls[0]
			if got.URL != test.url || got.URLBase != test.base || got.URLSuffix != test.suffix || got.Method != test.method || got.HasDynamic != test.dynamic || got.RequestOnly != test.requestOnly || got.URLSuffixStatic != (test.base != "") {
				t.Fatalf("got %+v; want %+v", got, test)
			}
		})
	}
}

func TestHTTPURLIgnoresShadowedOrUnrelatedPackage(t *testing.T) {
	for _, src := range []string{
		`package p; import "net/http"; func f(http Client) { http.Get("https://api/items") }`,
		`package p; import "example.com/http"; func f() { http.Get("https://api/items") }`,
		`package p; import h "net/http"; func f() { h := client; h.Get("https://api/items") }`,
	} {
		result, err := ParseSource(token.NewFileSet(), "client.go", []byte(src), "client.go", "example.com/client")
		if err != nil || len(result.HTTPCalls) != 0 {
			t.Fatalf("shadowed import extracted: %+v, %v", result, err)
		}
	}
}

func TestHTTPURLAliasDepthIsBounded(t *testing.T) {
	var body strings.Builder
	body.WriteString("package p; import \"net/http\"; func f() { u0 := cfg.API; ")
	for i := 1; i <= maxStaticStringDepth+2; i++ {
		fmt.Fprintf(&body, "u%d := u%d; ", i, i-1)
	}
	fmt.Fprintf(&body, "http.Get(u%d + \"/items\") }", maxStaticStringDepth+2)
	result, err := ParseSource(token.NewFileSet(), "client.go", []byte(body.String()), "client.go", "example.com/client")
	if err != nil || len(result.HTTPCalls) != 1 || result.HTTPCalls[0].URLBase != "" {
		t.Fatalf("depth bound = %+v, %v", result, err)
	}
}
