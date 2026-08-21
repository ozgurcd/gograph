package parser_test

import (
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/parser"
)

func TestParseFileIndexesGo127GenericMethods(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "generic_method.go")
	source := `package sample

type Box struct{}

func (Box) Transform[T ~int | ~string](value T) T { return value }
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := parser.ParseFile(token.NewFileSet(), path, "generic_method.go", "example.com/sample")
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}
	for _, symbol := range result.Symbols {
		if symbol.Name != "Transform" {
			continue
		}
		if got, want := symbol.Signature, "func (Box) Transform[T ~int | ~string](value T) (T)"; got != want {
			t.Fatalf("generic method signature = %q, want %q", got, want)
		}
		if got, want := symbol.MethodSignature, "func[T ~int | ~string](T) (T)"; got != want {
			t.Fatalf("generic method matching signature = %q, want %q", got, want)
		}
		return
	}
	t.Fatal("generic method Transform was not indexed")
}

func fixtureDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", "widget")
}

func TestParseFile_Symbols(t *testing.T) {
	fset := token.NewFileSet()
	fixturePath := filepath.Join(fixtureDir(), "widget.go")
	result, err := parser.ParseFile(fset, fixturePath, "widget/widget.go", "example.com/widget")
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	byName := make(map[string]graph.SymbolNode)
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}

	t.Run("struct Widget", func(t *testing.T) {
		s, ok := byName["Widget"]
		if !ok {
			t.Fatal("expected struct Widget, not found")
		}
		if s.Kind != graph.KindStruct {
			t.Errorf("expected kind struct, got %s", s.Kind)
		}
	})
	t.Run("interface Runner", func(t *testing.T) {
		s, ok := byName["Runner"]
		if !ok {
			t.Fatal("expected interface Runner, not found")
		}
		if s.Kind != graph.KindInterface {
			t.Errorf("expected kind interface, got %s", s.Kind)
		}
	})
	t.Run("function NewWidget", func(t *testing.T) {
		s, ok := byName["NewWidget"]
		if !ok {
			t.Fatal("expected function NewWidget, not found")
		}
		if s.Kind != graph.KindFunction {
			t.Errorf("expected kind function, got %s", s.Kind)
		}
		if s.Receiver != "" {
			t.Errorf("expected no receiver, got %q", s.Receiver)
		}
	})
	t.Run("method String on Widget", func(t *testing.T) {
		s, ok := byName["String"]
		if !ok {
			t.Fatal("expected method String, not found")
		}
		if s.Kind != graph.KindMethod {
			t.Errorf("expected kind method, got %s", s.Kind)
		}
		if s.Receiver != "*Widget" {
			t.Errorf("expected receiver *Widget, got %q", s.Receiver)
		}
	})
	t.Run("method Double", func(t *testing.T) {
		s, ok := byName["Double"]
		if !ok {
			t.Fatal("expected method Double, not found")
		}
		if s.Kind != graph.KindMethod {
			t.Errorf("expected kind method, got %s", s.Kind)
		}
	})
	t.Run("unexported function helper", func(t *testing.T) {
		s, ok := byName["helper"]
		if !ok {
			t.Fatal("expected function helper, not found")
		}
		if s.Kind != graph.KindFunction {
			t.Errorf("expected kind function, got %s", s.Kind)
		}
	})
}

func TestParseFile_Imports(t *testing.T) {
	fset := token.NewFileSet()
	result, err := parser.ParseFile(fset, filepath.Join(fixtureDir(), "widget.go"), "widget/widget.go", "example.com/widget")
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}
	importSet := make(map[string]bool)
	for _, imp := range result.Imports {
		importSet[imp.ImportPath] = true
	}
	for _, expected := range []string{"fmt", "os"} {
		if !importSet[expected] {
			t.Errorf("expected import %q not found", expected)
		}
	}
}

func TestParseFile_Calls(t *testing.T) {
	fset := token.NewFileSet()
	result, err := parser.ParseFile(fset, filepath.Join(fixtureDir(), "widget.go"), "widget/widget.go", "example.com/widget")
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}
	callees := make(map[string]bool)
	for _, c := range result.Calls {
		callees[c.CalleeRaw] = true
	}
	for _, expected := range []string{"os.Getenv", "fmt.Println", "fmt.Sprintf"} {
		if !callees[expected] {
			t.Errorf("expected call %q not found; got: %v", expected, callees)
		}
	}
}

func TestParseFile_EnvReads(t *testing.T) {
	fset := token.NewFileSet()
	result, err := parser.ParseFile(fset, filepath.Join(fixtureDir(), "widget.go"), "widget/widget.go", "example.com/widget")
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}
	if len(result.Env) == 0 {
		t.Fatal("expected at least one env read")
	}
	found := false
	for _, ev := range result.Env {
		if ev.Key == "WIDGET_ENV_KEY" && ev.Accessor == "os.Getenv" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected env read WIDGET_ENV_KEY via os.Getenv; got: %+v", result.Env)
	}
}

func TestParseFile_LineNumbers(t *testing.T) {
	fset := token.NewFileSet()
	result, err := parser.ParseFile(fset, filepath.Join(fixtureDir(), "widget.go"), "widget/widget.go", "example.com/widget")
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}
	for _, s := range result.Symbols {
		if s.Line <= 0 {
			t.Errorf("symbol %q has invalid line %d", s.Name, s.Line)
		}
		if s.EndLine < s.Line {
			t.Errorf("symbol %q has EndLine %d < Line %d", s.Name, s.EndLine, s.Line)
		}
	}
}
