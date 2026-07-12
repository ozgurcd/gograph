package graph

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLegacyV2GraphDefaultsToASTPrecisionAndLineOnlyCalls(t *testing.T) {
	const legacy = `{
		"version":"2",
		"generated_at":"2026-01-01T00:00:00Z",
		"root":"/tmp/project",
		"packages":[],"files":[],"symbols":[],"imports":[],
		"calls":[{"caller_name":"Run","callee_raw":"Delete","file":"run.go","line":12}],
		"env_reads":[],"dependencies":[],
		"build":{"scanned_files":1,"parsed_files":1,"complete":true}
	}`

	var g Graph
	if err := json.Unmarshal([]byte(legacy), &g); err != nil {
		t.Fatalf("unmarshal legacy graph: %v", err)
	}
	if got := g.Build.EffectivePrecision(); got != PrecisionAST {
		t.Fatalf("legacy precision = %q, want %q", got, PrecisionAST)
	}
	if g.Build.PreciseRequested() {
		t.Fatal("legacy graph unexpectedly requests precise refresh")
	}
	if len(g.Calls) != 1 || g.Calls[0].Column != 0 || g.Calls[0].Synthetic {
		t.Fatalf("legacy call provenance = %#v, want line-only ordinary call", g.Calls)
	}
}

func TestOptionalPrecisionAndCallProvenanceMetadataRemainAdditive(t *testing.T) {
	g := Graph{
		Version: Version,
		Build:   &BuildMetadata{Complete: true, Precision: PrecisionPrecise},
		Calls: []CallEdge{{
			CallerName: "Run",
			CalleeRaw:  "Delete",
			File:       "run.go",
			Line:       12,
			Column:     21,
			Synthetic:  true,
		}},
	}
	encoded, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, field := range []string{`"precision":"precise"`, `"column":21`, `"synthetic":true`} {
		if !strings.Contains(text, field) {
			t.Fatalf("encoded graph missing %s: %s", field, text)
		}
	}

	zeroColumn, err := json.Marshal(CallEdge{Line: 12})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(zeroColumn), `"column"`) {
		t.Fatalf("zero legacy column should remain omitted: %s", zeroColumn)
	}
	if strings.Contains(string(zeroColumn), `"synthetic"`) {
		t.Fatalf("ordinary calls should omit synthetic traversal metadata: %s", zeroColumn)
	}
}
