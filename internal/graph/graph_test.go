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
	if got := g.Build.EffectiveTestCallResolution(); got != TestCallResolutionAST {
		t.Fatalf("legacy test call resolution = %q, want %q", got, TestCallResolutionAST)
	}
	if g.Build.PreciseRequested() {
		t.Fatal("legacy graph unexpectedly requests precise refresh")
	}
	if g.Build.BuildContextFingerprint != "" {
		t.Fatalf("legacy graph fingerprint = %q, want empty", g.Build.BuildContextFingerprint)
	}
	if len(g.Calls) != 1 || g.Calls[0].Column != 0 || g.Calls[0].Synthetic {
		t.Fatalf("legacy call provenance = %#v, want line-only ordinary call", g.Calls)
	}
}

func TestOptionalPrecisionAndCallProvenanceMetadataRemainAdditive(t *testing.T) {
	g := Graph{
		Version:   Version,
		Build:     &BuildMetadata{Complete: true, Precision: PrecisionPrecise, TestCallResolution: TestCallResolutionTyped, BuildContextFingerprint: "selection-v1"},
		TestEdges: []TestEdge{{TestFunc: "TestRun", Target: "service.Run", TargetSymbolID: "example.com/app::(*Service).Run", Resolution: CallResolutionStatic, File: "run_test.go", Line: 12, Column: 9, Precise: true}},
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
	for _, field := range []string{`"precision":"precise"`, `"test_call_resolution":"typed_complete"`, `"build_context_fingerprint":"selection-v1"`, `"column":21`, `"synthetic":true`, `"target_symbol_id":"example.com/app::(*Service).Run"`, `"resolution":"resolved_static"`, `"precise":true`} {
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

func TestUsesCurrentSourcePolicyRequiresExplicitMarker(t *testing.T) {
	for _, test := range []struct {
		name string
		g    *Graph
		want bool
	}{
		{name: "nil graph"},
		{name: "missing metadata", g: &Graph{}},
		{name: "legacy metadata", g: &Graph{Build: &BuildMetadata{}}},
		{name: "current", g: &Graph{Build: &BuildMetadata{SourcePolicyVersion: CurrentSourcePolicyVersion}}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.g.UsesCurrentSourcePolicy(); got != test.want {
				t.Fatalf("UsesCurrentSourcePolicy() = %v, want %v", got, test.want)
			}
		})
	}
}
