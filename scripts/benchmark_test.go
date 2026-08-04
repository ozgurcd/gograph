package main

import (
	"testing"
	"time"
)

func TestValidateSuite(t *testing.T) {
	suite := benchmarkSuite{
		SchemaVersion: "1", Name: "suite", Fixture: "fixture", AnalysisMode: "precise",
		Scenarios: []benchmarkScenario{{
			ID: "scenario", Evidence: []benchmarkEvidence{{ID: "evidence", Needle: "proof"}},
			Gograph:  benchmarkWorkflow{Steps: []benchmarkStep{{Program: "gograph"}}},
			Baseline: benchmarkWorkflow{Steps: []benchmarkStep{{Program: "rg"}}},
		}},
	}
	if err := validateSuite(suite); err != nil {
		t.Fatalf("validateSuite: %v", err)
	}
	suite.Scenarios = append(suite.Scenarios, suite.Scenarios[0])
	if err := validateSuite(suite); err == nil {
		t.Fatal("validateSuite accepted duplicate scenario ID")
	}
}

func TestEvaluateEvidence(t *testing.T) {
	results := evaluateEvidence("MemoryRepository\nCheckout\n", []benchmarkEvidence{
		{ID: "memory", Description: "memory implementation", Needle: "MemoryRepository"},
		{ID: "sql", Description: "SQL implementation", Needle: "SQLRepository"},
	})
	if len(results) != 2 || !results[0].Found || results[1].Found {
		t.Fatalf("evaluateEvidence = %#v", results)
	}
}

func TestMedianMillisAndOutputNormalization(t *testing.T) {
	got := medianMillis([]time.Duration{30 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond})
	if got != 20 {
		t.Fatalf("medianMillis = %d, want 20", got)
	}
	if got := normalizeOutput("/tmp/fixture/service.go", "/tmp/fixture"); got != "<fixture>/service.go" {
		t.Fatalf("normalizeOutput = %q", got)
	}
}
