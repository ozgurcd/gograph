package parser

import (
	"go/token"
	"testing"
)

func TestConstValuesAreRecorded(t *testing.T) {
	src := []byte(`package x

import "time"

type Level int

const (
	Low Level = iota
	Mid
	High
)

const wait = 5 * time.Second

const a, b = 1, "two"

var notConst = 3
`)
	result, err := ParseSource(token.NewFileSet(), "x.go", src, "x.go", "example.com/x")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Low":      "Level = iota",
		"Mid":      "Level = iota (implicit repeat, iota=1)",
		"High":     "Level = iota (implicit repeat, iota=2)",
		"wait":     "= 5 * time.Second",
		"a":        "= 1",
		"b":        `= "two"`,
		"notConst": "",
	}
	for _, sym := range result.Symbols {
		expected, ok := want[sym.Name]
		if !ok {
			continue
		}
		if sym.Value != expected {
			t.Errorf("%s value = %q, want %q", sym.Name, sym.Value, expected)
		}
		delete(want, sym.Name)
	}
	if len(want) != 0 {
		t.Errorf("symbols not found: %v", want)
	}
}
