package parser_test

import (
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/parser"
)

func TestParseFileExtractsSecurityFlowFacts(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "handler.go")
	source := `package sample

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
)

type input struct { Query, URL string }

func handle(r *http.Request, body []byte, db interface{ Exec(string, ...any) error }) {
	path := r.URL.Path
	_ = os.WriteFile(path, body, 0600)
	command := os.Getenv(path)
	_ = exec.Command(command).Run()
	var payload input
	_ = json.Unmarshal(body, &payload)
	_ = db.Exec(payload.Query)
	_, _ = http.Get(payload.URL)
}
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := parser.ParseFile(token.NewFileSet(), path, "handler.go", "example.com/sample")
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}
	if len(result.FlowFunctions) != 1 {
		t.Fatalf("expected one flow function, got %d", len(result.FlowFunctions))
	}

	sources := make(map[string]bool)
	sinks := make(map[string]bool)
	hasPathTransfer := false
	for _, fact := range result.FlowFunctions[0].Facts {
		sources[fact.SourceKind] = sources[fact.SourceKind] || fact.Kind == "source"
		sinks[fact.SinkKind] = sinks[fact.SinkKind] || fact.Kind == "sink"
		if fact.Kind == "transfer" && fact.Target == "path" {
			hasPathTransfer = true
		}
	}
	for _, kind := range []string{"http_request", "environment", "decoded_json"} {
		if !sources[kind] {
			t.Errorf("expected %s source fact", kind)
		}
	}
	for _, kind := range []string{"filesystem", "process_execution", "sql_query", "outbound_http"} {
		if !sinks[kind] {
			t.Errorf("expected %s sink fact", kind)
		}
	}
	if !hasPathTransfer {
		t.Error("expected request path assignment transfer")
	}
}

func TestParseFileExtractsFlowFactsForFunctionLiterals(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "closure.go")
	source := `package sample

import "os"

func register() {
	callback := func() { _ = os.Remove(os.Getenv("TARGET")) }
	callback()
}
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := parser.ParseFile(token.NewFileSet(), path, "closure.go", "example.com/sample")
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}
	if len(result.FlowFunctions) != 2 {
		t.Fatalf("expected named function and closure facts, got %d", len(result.FlowFunctions))
	}
	foundClosureSink := false
	for _, function := range result.FlowFunctions {
		if function.Name != "<func@6>" {
			continue
		}
		for _, fact := range function.Facts {
			if fact.Kind == "sink" && fact.SinkKind == "filesystem" {
				foundClosureSink = true
			}
		}
	}
	if !foundClosureSink {
		t.Error("expected filesystem sink in closure")
	}
}

func TestParseFileRestrictsDecodeAndBindSources(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "decode.go")
	source := `package sample

import (
	"encoding/json"
	"io"
	gin "github.com/gin-gonic/gin"
)

type fakeDecoder struct{}
func (fakeDecoder) Decode(any) error { return nil }

func handle(ctx *gin.Context, body io.Reader) {
	var decoded, fake, bound any
	decoder := json.NewDecoder(body)
	_ = decoder.Decode(&decoded)
	_ = (fakeDecoder{}).Decode(&fake)
	_ = ctx.ShouldBindJSON(&bound)
}
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := parser.ParseFile(token.NewFileSet(), path, "decode.go", "example.com/sample")
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}
	targets := make(map[string]bool)
	for _, function := range result.FlowFunctions {
		if function.Name != "handle" {
			continue
		}
		for _, fact := range function.Facts {
			if fact.Kind == "source" && fact.SourceKind == "decoded_json" {
				targets[fact.Target] = true
			}
		}
	}
	if !targets["decoded"] || !targets["bound"] {
		t.Fatalf("expected encoding/json and Gin binding sources, got %v", targets)
	}
	if targets["fake"] {
		t.Fatal("unrelated Decode method must not be classified as decoded JSON")
	}
}

func TestParseFileIndexesMultipleReturnValues(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "returns.go")
	source := `package sample

import "fmt"

func parse(input string) (string, error) {
	return "", fmt.Errorf("bad input: %s", input)
}

func use(input string) {
	value, err := parse(input)
	_, _ = value, err
}
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := parser.ParseFile(token.NewFileSet(), path, "returns.go", "example.com/sample")
	if err != nil {
		t.Fatal(err)
	}

	returnIndexFound := false
	valueIndexFound := false
	errorIndexFound := false
	for _, function := range result.FlowFunctions {
		for _, fact := range function.Facts {
			if function.Name == "parse" && fact.Kind == "return" && fact.Target == "$return:1" {
				returnIndexFound = true
			}
			if function.Name == "use" && fact.Kind == "transfer" && fact.Target == "value" && len(fact.Inputs) == 1 && strings.HasSuffix(fact.Inputs[0], ":0") {
				valueIndexFound = true
			}
			if function.Name == "use" && fact.Kind == "transfer" && fact.Target == "err" && len(fact.Inputs) == 1 && strings.HasSuffix(fact.Inputs[0], ":1") {
				errorIndexFound = true
			}
		}
	}
	if !returnIndexFound || !valueIndexFound || !errorIndexFound {
		t.Fatalf("missing indexed return facts: return=%v value=%v error=%v", returnIndexFound, valueIndexFound, errorIndexFound)
	}
}
