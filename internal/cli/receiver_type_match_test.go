package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AST graphs record the lexical type of a method call's receiver root, so
// callers, tests and plan stop conflating same-name methods of other types.
func TestASTCallsRejectSameNameMethodsOfKnownOtherType(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/recv\n\ngo 1.22\n",
		"app/cache.go": `package app

import "time"

type clock struct{}

func (clock) now() time.Time { return time.Now() }

type remoteReadinessCache struct {
	clock clock
	at    time.Time
}

func (c *remoteReadinessCache) now() time.Time { return c.clock.now() }

func (c *remoteReadinessCache) fresh() bool { return c.now().Sub(c.at) < time.Second }

func useParam(k clock) time.Time { return k.now() }

func useLocal() time.Time {
	k := clock{}
	return k.now()
}

func useInterface(x interface{ now() time.Time }) time.Time { return x.now() }

func useShadowed(k clock) time.Time {
	if true {
		k := &remoteReadinessCache{}
		return k.now()
	}
	return k.now()
}

type wrapper struct{ clock }

func useEmbedded(w wrapper) time.Time { return w.now() }

func useGeneric[T interface{ now() time.Time }](v T) time.Time { return v.now() }

type cacheErr struct{ msg string }

func (e cacheErr) Error() string { return e.msg }

type otherErr struct{}

func (otherErr) Error() string { return "other" }
`,
		"app/cache_test.go": `package app

import "testing"

func TestCacheErr(t *testing.T) {
	e := cacheErr{msg: "boom"}
	if e.Error() != "boom" {
		t.Fatal(e.Error())
	}
}

func TestOtherErr(t *testing.T) {
	var o otherErr
	_ = o.Error()
}
`,
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) }); code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}
	run := func(args ...string) string {
		out, stderr, code := runCLIParityInDir(t, root, func() int { return Run(args) })
		if code != 0 {
			t.Fatalf("%v: code=%d err=%s", args, code, stderr)
		}
		return out
	}
	callerNames := func(selector string) []string {
		var names []string
		for _, line := range strings.Split(strings.TrimSpace(run("callers", selector)), "\n") {
			name, _, _ := strings.Cut(strings.TrimPrefix(line, "[caller] "), " — ")
			names = append(names, name)
		}
		return names
	}
	assertSet := func(label string, got []string, want ...string) {
		t.Helper()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: got %v, want %v", label, got, want)
		}
	}
	// Unknown receivers (interface, shadowed name, type parameter) and a
	// struct that embeds stay candidates for both methods.
	assertSet("callers remoteReadinessCache.now", callerNames("app/cache.go:remoteReadinessCache.now"),
		"(*remoteReadinessCache).fresh", "useEmbedded", "useGeneric", "useInterface", "useShadowed", "useShadowed")
	assertSet("callers clock.now", callerNames("clock.now"),
		"(*remoteReadinessCache).now", "useEmbedded", "useGeneric", "useInterface", "useLocal", "useParam", "useShadowed", "useShadowed")

	if out := run("tests", "cacheErr.Error"); !strings.Contains(out, "TestCacheErr") || strings.Contains(out, "TestOtherErr") {
		t.Errorf("tests cacheErr.Error:\n%s", out)
	}
	if out := run("tests", "cacheErr.Error", "--transitive"); !strings.Contains(out, "TestCacheErr") || strings.Contains(out, "TestOtherErr") {
		t.Errorf("tests cacheErr.Error --transitive:\n%s", out)
	}
	if out := run("plan", "cacheErr.Error"); !strings.Contains(out, "TestCacheErr") || strings.Contains(out, "TestOtherErr") {
		t.Errorf("plan cacheErr.Error:\n%s", out)
	}
	if out := run("tests", "Error"); !strings.Contains(out, "TestCacheErr") || !strings.Contains(out, "TestOtherErr") {
		t.Errorf("a bare name stays name-based:\n%s", out)
	}
}
