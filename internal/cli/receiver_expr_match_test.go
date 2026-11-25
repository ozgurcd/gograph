package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A method called directly on T{}, &T{}, new(T) or x.(T) binds by that
// spelled type, and a receiver of a package outside the graph never calls a
// repository method.
func TestASTCallsBindReceiverExpressionsAndExternalTypes(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/recvexpr\n\ngo 1.22\n",
		"app/errs.go": `package app

import "strings"

type errA struct{}

func (errA) Error() string { return "a" }

type errB struct{}

func (errB) Error() string { return "b" }

type named struct{}

func (named) String() string { return "named" }

func useBuilder() string {
	var sb strings.Builder
	return sb.String() + new(strings.Builder).String()
}
`,
		"app/errs_test.go": `package app

import "testing"

func TestLiteralB(t *testing.T) { _ = errB{}.Error() }
func TestNewB(t *testing.T)     { _ = new(errB).Error() }
func TestParenB(t *testing.T)   { _ = (&errB{}).Error() }
func TestAssertA(t *testing.T) {
	var x interface{} = errA{}
	_ = x.(errA).Error()
}
func TestBuilder(t *testing.T) { _ = useBuilder() }
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
	hasAll := func(label, out string, want ...string) {
		t.Helper()
		for _, name := range want {
			if !strings.Contains(out, name) {
				t.Errorf("%s: missing %s:\n%s", label, name, out)
			}
		}
	}
	hasNone := func(label, out string, unwanted ...string) {
		t.Helper()
		for _, name := range unwanted {
			if strings.Contains(out, name) {
				t.Errorf("%s: unexpected %s:\n%s", label, name, out)
			}
		}
	}
	for _, args := range [][]string{{"callers", "errB.Error"}, {"tests", "errB.Error"}, {"tests", "errB.Error", "--transitive"}} {
		out := run(args...)
		hasAll(strings.Join(args, " "), out, "TestLiteralB", "TestNewB", "TestParenB")
		hasNone(strings.Join(args, " "), out, "TestAssertA")
	}
	for _, args := range [][]string{{"callers", "errA.Error"}, {"tests", "errA.Error"}, {"tests", "errA.Error", "--transitive"}} {
		out := run(args...)
		hasAll(strings.Join(args, " "), out, "TestAssertA")
		hasNone(strings.Join(args, " "), out, "TestLiteralB", "TestNewB", "TestParenB")
	}
	callers, _, _ := runCLIParityInDir(t, root, func() int { return Run([]string{"callers", "named.String"}) })
	hasNone("callers named.String", callers, "useBuilder")
	transitive, _, _ := runCLIParityInDir(t, root, func() int { return Run([]string{"tests", "named.String", "--transitive"}) })
	hasNone("tests named.String --transitive", transitive, "TestBuilder")
}
