package parser_test

import (
	"go/token"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ozgurcd/gograph/internal/parser"
)

func httpClientDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", "httpclient")
}

func TestParseFile_HTTPClientCalls(t *testing.T) {
	fset := token.NewFileSet()
	fixturePath := filepath.Join(httpClientDir(), "httpclient.go")
	result, err := parser.ParseFile(fset, fixturePath, "httpclient/httpclient.go", "example.com/httpclient")
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	t.Run("http.Get with static URL", func(t *testing.T) {
		found := false
		for _, h := range result.HttpCalls {
			if h.Method == "GET" && h.URL == "https://api.example.com/users" && !h.HasDynamic {
				found = true
				if len(h.StaticSegments) != 1 || h.StaticSegments[0] != "users" {
					t.Errorf("unexpected static segments: %v", h.StaticSegments)
				}
			}
		}
		if !found {
			t.Error("expected http.Get call to https://api.example.com/users")
		}
	})

	t.Run("http.Post with static URL", func(t *testing.T) {
		found := false
		for _, h := range result.HttpCalls {
			if h.Method == "POST" && h.URL == "https://api.example.com/users" && !h.HasDynamic {
				found = true
			}
		}
		if !found {
			t.Error("expected http.Post call")
		}
	})

	t.Run("http.PostForm with static URL", func(t *testing.T) {
		found := false
		for _, h := range result.HttpCalls {
			if h.Method == "POST" && h.URL == "https://api.example.com/login" && !h.HasDynamic {
				found = true
			}
		}
		if !found {
			t.Error("expected http.PostForm call")
		}
	})

	t.Run("http.Head with static URL", func(t *testing.T) {
		found := false
		for _, h := range result.HttpCalls {
			if h.Method == "HEAD" && h.URL == "https://api.example.com/health" && !h.HasDynamic {
				found = true
			}
		}
		if !found {
			t.Error("expected http.Head call")
		}
	})

	t.Run("client.Get with dynamic URL", func(t *testing.T) {
		found := false
		for _, h := range result.HttpCalls {
			if h.Method == "GET" && h.URL == "url" && h.HasDynamic && h.FunctionName == "dynamicURL" {
				found = true
			}
		}
		if !found {
			t.Error("expected client.Get call in dynamicURL")
		}
	})

	t.Run("client.Get with concatenated URL", func(t *testing.T) {
		found := false
		for _, h := range result.HttpCalls {
			if h.Method == "GET" && h.FunctionName == "getUser" {
				found = true
			}
		}
		if !found {
			t.Error("expected client.Get call in getUser")
		}
	})

	t.Run("multi-segment static URL", func(t *testing.T) {
		found := false
		for _, h := range result.HttpCalls {
			if h.Method == "GET" && h.URL == "https://user-svc/api/v2/users" && !h.HasDynamic {
				found = true
				if len(h.StaticSegments) != 3 || h.StaticSegments[0] != "api" || h.StaticSegments[1] != "v2" || h.StaticSegments[2] != "users" {
					t.Errorf("unexpected static segments for multi-segment URL: %v", h.StaticSegments)
				}
			}
		}
		if !found {
			t.Error("expected http.Get call to https://user-svc/api/v2/users")
		}
	})

	t.Run("client.Do detected", func(t *testing.T) {
		found := false
		for _, h := range result.HttpCalls {
			if h.FunctionName == "doRequest" && h.Method == "GET" {
				found = true
			}
		}
		if !found {
			t.Error("expected client.Do call in doRequest")
		}
	})

	t.Run("function name populated", func(t *testing.T) {
		for _, h := range result.HttpCalls {
			if h.FunctionName == "" {
				t.Error("expected non-empty FunctionName")
			}
			if h.SourceLine == 0 {
				t.Error("expected non-zero SourceLine")
			}
			if h.SourceFile == "" {
				t.Error("expected non-empty SourceFile")
			}
		}
	})
}
