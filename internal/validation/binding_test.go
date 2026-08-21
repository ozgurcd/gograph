package validation

import (
	"strings"
	"testing"
)

func TestParseBindingStrictness(t *testing.T) {
	tests := []struct {
		name string
		json string
		want bool
	}{
		{name: "valid", json: symbolBinding("example.com/project/pkg::Serve", PrecisionAST), want: true},
		{name: "unknown field", json: `{"schema_version":"gograph.binding.v1","predicate":"symbol_exists","subject":{"language":"go","kind":"symbol","id":"example.com/project/pkg::Serve"},"required_precision":"ast","extra":true}`},
		{name: "nested unknown field", json: `{"schema_version":"gograph.binding.v1","predicate":"symbol_exists","subject":{"language":"go","kind":"symbol","id":"example.com/project/pkg::Serve","name":"Serve"},"required_precision":"ast"}`},
		{name: "duplicate key", json: `{"schema_version":"gograph.binding.v1","predicate":"symbol_exists","predicate":"symbol_exists","subject":{"language":"go","kind":"symbol","id":"example.com/project/pkg::Serve"},"required_precision":"ast"}`},
		{name: "invalid utf8", json: string([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'})},
		{name: "nested duplicate key", json: `{"schema_version":"gograph.binding.v1","predicate":"symbol_exists","subject":{"language":"go","language":"go","kind":"symbol","id":"example.com/project/pkg::Serve"},"required_precision":"ast"}`},
		{name: "unsupported schema", json: strings.Replace(symbolBinding("example.com/project/pkg::Serve", PrecisionAST), BindingSchemaVersion, "gograph.binding.v2", 1)},
		{name: "unsupported predicate", json: strings.Replace(symbolBinding("example.com/project/pkg::Serve", PrecisionAST), string(PredicateSymbolExists), "reachability", 1)},
		{name: "unsupported language", json: strings.Replace(symbolBinding("example.com/project/pkg::Serve", PrecisionAST), `"language":"go"`, `"language":"rust"`, 1)},
		{name: "display name", json: symbolBinding("Serve", PrecisionAST)},
		{name: "absolute fallback ID", json: symbolBinding("_/private/tmp/project::Serve", PrecisionAST)},
		{name: "path traversal", json: symbolBinding("example.com/project/../outside::Serve", PrecisionAST)},
		{name: "shell fragment", json: symbolBinding("example.com/project/pkg::Serve;rm", PrecisionAST)},
		{name: "unexpected object", json: `{"schema_version":"gograph.binding.v1","predicate":"symbol_exists","subject":{"language":"go","kind":"symbol","id":"example.com/project/pkg::Serve"},"object":{"language":"go","kind":"symbol","id":"example.com/project/pkg::Other"},"required_precision":"ast"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, fingerprint, err := ParseBinding([]byte(test.json))
			if test.want && err != nil {
				t.Fatalf("ParseBinding() error = %v", err)
			}
			if !test.want && err == nil {
				t.Fatal("ParseBinding() unexpectedly succeeded")
			}
			if test.want && len(fingerprint) != 64 {
				t.Fatalf("fingerprint length = %d, want 64", len(fingerprint))
			}
		})
	}
}

func TestBindingFingerprintIsCanonical(t *testing.T) {
	compact := symbolBinding("example.com/project/pkg::Serve", PrecisionAST)
	spaced := `{ "schema_version": "gograph.binding.v1", "predicate": "symbol_exists", "subject": { "language": "go", "kind": "symbol", "id": "example.com/project/pkg::Serve" }, "required_precision": "ast" }`
	_, first, err := ParseBinding([]byte(compact))
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := ParseBinding([]byte(spaced))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("canonical fingerprints differ: %s != %s", first, second)
	}
}

func symbolBinding(id string, precision Precision) string {
	return `{"schema_version":"gograph.binding.v1","predicate":"symbol_exists","subject":{"language":"go","kind":"symbol","id":"` + id + `"},"required_precision":"` + string(precision) + `"}`
}
