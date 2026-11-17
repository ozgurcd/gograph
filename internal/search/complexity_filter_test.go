package search

import (
	"reflect"
	"testing"
)

func TestFilterComplexity(t *testing.T) {
	results := []ComplexityResult{
		{Symbol: "TestBig", File: "internal/a/a_test.go", Score: 90},
		{Symbol: "Big", File: "internal/a/a.go", Score: 50},
		{Symbol: "Other", File: "internal/ab/b.go", Score: 40},
		{Symbol: "Cmd", File: "cmd/x/main.go", Score: 30},
		{Symbol: "Small", File: "internal/a/sub/c.go", Score: 2},
	}
	names := func(values []ComplexityResult) []string {
		out := []string{}
		for _, value := range values {
			out = append(out, value.Symbol)
		}
		return out
	}
	for name, testCase := range map[string]struct {
		filter ComplexityFilter
		want   []string
	}{
		"no filter":         {ComplexityFilter{}, []string{"TestBig", "Big", "Other", "Cmd", "Small"}},
		"no tests top 2":    {ComplexityFilter{NoTests: true, Top: 2}, []string{"Big", "Other"}},
		"path is a subtree": {ComplexityFilter{Path: "internal/a/"}, []string{"TestBig", "Big", "Small"}},
		"path and no tests": {ComplexityFilter{Path: "./internal/a", NoTests: true}, []string{"Big", "Small"}},
		"root path":         {ComplexityFilter{Path: ".", Top: 1}, []string{"TestBig"}},
	} {
		if got := names(FilterComplexity(results, testCase.filter)); !reflect.DeepEqual(got, testCase.want) {
			t.Errorf("%s: got %v, want %v", name, got, testCase.want)
		}
	}
}
