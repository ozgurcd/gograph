package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Changed types, variables and constants select the tests of the functions
// that use them, and a change reaching a main package selects its tests that
// run the built binary.
func TestTestSelectionFollowsDeclarationUsesAndProcessTests(t *testing.T) {
	t.Setenv("GOGRAPH_NO_REFRESH", "1")
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/declsel\n\ngo 1.22\n",
		"lib/lib.go": `package lib

const Limit = 3

var Prefix = "p"

type Config struct{ N int }

func (c Config) Valid() bool { return c.N < Limit }

func New() Config { return Config{N: 1} }

func Label(c Config) string { return Prefix + "x" }

func Unrelated() int { return 7 }
`,
		"lib/lib_test.go": `package lib

import "testing"

func TestValid(t *testing.T)     { _ = Config{}.Valid() }
func TestNew(t *testing.T)       { _ = New() }
func TestLabel(t *testing.T)     { _ = Label(Config{}) }
func TestUnrelated(t *testing.T) { _ = Unrelated() }
`,
		"cmd/tool/main.go": `package main

import (
	"fmt"

	"example.com/declsel/lib"
)

func main() { fmt.Println(lib.Unrelated()) }
`,
		"cmd/tool/main_test.go": `package main

import (
	"os/exec"
	"testing"
)

func runTool(t *testing.T) string {
	out, _ := exec.Command("go", "run", ".").Output()
	return string(out)
}

func TestBinaryPrints(t *testing.T) { _ = runTool(t) }
func TestPure(t *testing.T)         {}
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
	run := func(args ...string) string {
		t.Helper()
		out, stderr, code := runCLIParityInDir(t, root, func() int { return Run(args) })
		if code != 0 {
			t.Fatalf("%v: code=%d %s", args, code, stderr)
		}
		return out
	}
	expect := func(args []string, want, unwanted []string) {
		t.Helper()
		out := run(args...)
		for _, name := range want {
			if !strings.Contains(out, name) {
				t.Errorf("%v: missing %s:\n%s", args, name, out)
			}
		}
		for _, name := range unwanted {
			if strings.Contains(out, name) {
				t.Errorf("%v: unexpected %s:\n%s", args, name, out)
			}
		}
	}
	run("build", ".")
	expect([]string{"tests", "Config", "--format", "go-test"}, []string{"TestValid", "TestNew", "TestLabel"}, []string{"TestUnrelated", "TestBinaryPrints"})
	expect([]string{"tests", "Unrelated", "--format", "go-test"}, []string{"TestUnrelated", "TestBinaryPrints"}, []string{"TestPure"})
	expect([]string{"tests", "Limit", "--format", "run"}, []string{"build --precise"}, []string{"TestValid"})
	expect([]string{"tests", "Limit"}, []string{"--format go-test selects"}, nil)

	run("build", ".", "--precise")
	expect([]string{"tests", "Limit", "--format", "go-test"}, []string{"TestValid"}, []string{"TestNew", "TestLabel", "TestUnrelated"})
	expect([]string{"tests", "lib.Prefix", "--format", "go-test"}, []string{"TestLabel"}, []string{"TestValid", "TestNew"})
	expect([]string{"tests", "lib/lib.go:Config", "--format", "go-test"}, []string{"TestValid", "TestNew", "TestLabel"}, []string{"TestUnrelated"})
}
