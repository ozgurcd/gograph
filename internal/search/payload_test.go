package search_test

import (
	"errors"
	"testing"

	"github.com/ozgurcd/gograph/internal/search"
)

func TestNewContextPayloadPreservesTransportEvidence(t *testing.T) {
	result := &search.ContextResult{
		Node: []search.Result{
			{Kind: "func", Name: "Open", File: "a.go", Line: 3},
			{Kind: "func", Name: "Open", File: "b.go", Line: 7},
		},
		SourceErr: errors.New("source unavailable"),
		Callers:   []search.Result{{Name: "Run"}},
		Callees:   []search.Result{{Name: "read"}},
		Tests:     []search.Result{{Name: "TestOpen", File: "a_test.go"}},
		Role:      "utility",
	}

	payload := search.NewContextPayload("Open", result)
	if payload.Symbol != "Open" || payload.Role != "utility" {
		t.Fatalf("identity fields = %#v", payload)
	}
	if payload.Node == nil || payload.Node.File != "a.go" || len(payload.Nodes) != 2 {
		t.Fatalf("node evidence was not preserved: %#v", payload)
	}
	if payload.SourceError != "source unavailable" {
		t.Fatalf("source_error = %q", payload.SourceError)
	}
	if len(payload.Tests) != 1 || payload.Tests[0] != "TestOpen" || len(payload.TestResults) != 1 {
		t.Fatalf("test evidence was not preserved: %#v", payload)
	}
}

func TestErrorFlowPayloadCountsAllEvidenceAndNormalizesArrays(t *testing.T) {
	report := &search.ErrorFlowReport{
		Term:            "ErrDenied",
		DefinitionSites: []search.Result{{Name: "ErrDenied"}},
		ReturnSites:     []search.Result{{Name: "authorize"}},
		RelatedTests:    []search.Result{{Name: "TestAuthorize"}},
	}

	payload := search.NewErrorFlowPayload(report)
	if payload.Count() != 3 {
		t.Fatalf("Count() = %d, want 3", payload.Count())
	}
	if payload.Paths == nil {
		t.Fatal("empty paths must be a JSON array, not null")
	}
	if len(payload.Tests) != 1 || payload.Tests[0] != "TestAuthorize" || len(payload.TestResults) != 1 {
		t.Fatalf("test evidence was not preserved: %#v", payload)
	}
	if len(payload.Limitations) == 0 {
		t.Fatal("expected static-analysis limitation")
	}
}
