package validation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ozgurcd/gograph/internal/graph"
)

const (
	testPackage = "example.com/project/pkg"
	callerID    = testPackage + "::Caller"
	calleeID    = testPackage + "::Callee"
	otherCallID = testPackage + "::Other"
	concreteID  = testPackage + "::Service"
	otherTypeID = testPackage + "::OtherService"
	interfaceID = testPackage + "::Runner"
)

type fakeLoader struct {
	snapshot  Snapshot
	loadErr   error
	verifyErr error
}

type contextLoader struct{}

func (contextLoader) Load(ctx context.Context, _ string) (Snapshot, error) {
	<-ctx.Done()
	return Snapshot{}, ctx.Err()
}
func (contextLoader) VerifyCurrent(context.Context, Snapshot) error { return nil }

func (f fakeLoader) Load(context.Context, string) (Snapshot, error) { return f.snapshot, f.loadErr }
func (f fakeLoader) VerifyCurrent(context.Context, Snapshot) error  { return f.verifyErr }

func TestPredicateCompletenessAndOutcomes(t *testing.T) {
	tests := []struct {
		name    string
		binding Binding
		mutate  func(*Snapshot)
		outcome Outcome
		reason  Reason
	}{
		{name: "symbol pass", binding: binding(PredicateSymbolExists, symbolRef(callerID), nil, PrecisionAST), outcome: OutcomePass, reason: ReasonPredicatePassed},
		{name: "symbol complete absence fails", binding: binding(PredicateSymbolExists, symbolRef(testPackage+"::Missing"), nil, PrecisionAST), outcome: OutcomeFail, reason: ReasonSymbolNotFound},
		{name: "symbol partial absence cannot evaluate", binding: binding(PredicateSymbolExists, symbolRef(testPackage+"::Missing"), nil, PrecisionAST), mutate: makePartial, outcome: OutcomeCannotEvaluate, reason: ReasonAnalysisIncomplete},
		{name: "symbol fallback presence passes", binding: binding(PredicateSymbolExists, symbolRef(callerID), nil, PrecisionAST), mutate: makeFallback, outcome: OutcomePass, reason: ReasonPredicatePassed},
		{name: "symbol fallback absence cannot evaluate", binding: binding(PredicateSymbolExists, symbolRef(testPackage+"::Missing"), nil, PrecisionAST), mutate: makeFallback, outcome: OutcomeCannotEvaluate, reason: ReasonPrecisionInsufficient},
		{name: "package import pass", binding: binding(PredicatePackageImports, packageRef(testPackage), ref(packageRef("fmt")), PrecisionAST), outcome: OutcomePass, reason: ReasonPredicatePassed},
		{name: "package import complete absence fails", binding: binding(PredicatePackageImports, packageRef(testPackage), ref(packageRef("net/http")), PrecisionAST), outcome: OutcomeFail, reason: ReasonRelationNotFound},
		{name: "package import fallback absence cannot evaluate", binding: binding(PredicatePackageImports, packageRef(testPackage), ref(packageRef("net/http")), PrecisionAST), mutate: makeFallback, outcome: OutcomeCannotEvaluate, reason: ReasonPrecisionInsufficient},
		{name: "package import missing subject cannot evaluate", binding: binding(PredicatePackageImports, packageRef("example.com/project/missing"), ref(packageRef("fmt")), PrecisionAST), outcome: OutcomeCannotEvaluate, reason: ReasonPackageNotFound},
		{name: "call pass", binding: binding(PredicateCallEdgeExists, symbolRef(callerID), ref(symbolRef(calleeID)), PrecisionPrecise), outcome: OutcomePass, reason: ReasonPredicatePassed},
		{name: "call complete absence fails", binding: binding(PredicateCallEdgeExists, symbolRef(calleeID), ref(symbolRef(callerID)), PrecisionPrecise), outcome: OutcomeFail, reason: ReasonRelationNotFound},
		{name: "call unresolved absence cannot evaluate", binding: binding(PredicateCallEdgeExists, symbolRef(callerID), ref(symbolRef(otherCallID)), PrecisionPrecise), mutate: addUnresolvedCall, outcome: OutcomeCannotEvaluate, reason: ReasonAnalysisIncomplete},
		{name: "call ast cannot evaluate", binding: binding(PredicateCallEdgeExists, symbolRef(callerID), ref(symbolRef(calleeID)), PrecisionPrecise), mutate: makeAST, outcome: OutcomeCannotEvaluate, reason: ReasonPrecisionInsufficient},
		{name: "implements pass", binding: binding(PredicateTypeImplements, symbolRef(concreteID), ref(symbolRef(interfaceID)), PrecisionPrecise), outcome: OutcomePass, reason: ReasonPredicatePassed},
		{name: "implements precise complete absence fails", binding: binding(PredicateTypeImplements, symbolRef(otherTypeID), ref(symbolRef(interfaceID)), PrecisionPrecise), outcome: OutcomeFail, reason: ReasonRelationNotFound},
		{name: "implements ast cannot evaluate", binding: binding(PredicateTypeImplements, symbolRef(concreteID), ref(symbolRef(interfaceID)), PrecisionPrecise), mutate: makeAST, outcome: OutcomeCannotEvaluate, reason: ReasonPrecisionInsufficient},
		{name: "missing relation symbol is not automatically fail", binding: binding(PredicateTypeImplements, symbolRef(testPackage+"::Missing"), ref(symbolRef(interfaceID)), PrecisionPrecise), outcome: OutcomeCannotEvaluate, reason: ReasonSymbolNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := completeSnapshot()
			if test.mutate != nil {
				test.mutate(&snapshot)
			}
			result := evaluate(t, snapshot, test.binding, "")
			if result.Evaluation.Outcome != test.outcome || result.Evaluation.Reason != test.reason {
				t.Fatalf("evaluation = %s/%s, want %s/%s; diagnostics=%v", result.Evaluation.Outcome, result.Evaluation.Reason, test.outcome, test.reason, result.Evaluation.Diagnostics)
			}
		})
	}
}

func TestValidationIdentityAndAuthenticity(t *testing.T) {
	t.Run("duplicate exact symbol is ambiguous", func(t *testing.T) {
		snapshot := completeSnapshot()
		snapshot.Graph.Symbols = append(snapshot.Graph.Symbols, snapshot.Graph.Symbols[0])
		result := evaluate(t, snapshot, binding(PredicateSymbolExists, symbolRef(callerID), nil, PrecisionAST), "")
		if result.Evaluation.Reason != ReasonSymbolAmbiguous {
			t.Fatalf("reason = %s", result.Evaluation.Reason)
		}
	})

	t.Run("expected source fingerprint mismatch", func(t *testing.T) {
		result := evaluate(t, completeSnapshot(), binding(PredicateSymbolExists, symbolRef(callerID), nil, PrecisionAST), "different")
		if result.Evaluation.Reason != ReasonRepositoryMismatch {
			t.Fatalf("reason = %s", result.Evaluation.Reason)
		}
	})

	t.Run("graph missing", func(t *testing.T) {
		loadErr := &SnapshotError{Reason: ReasonGraphMissing, Diagnostic: Diagnostic{Code: "graph_missing", Message: "missing"}}
		evaluator := NewEvaluatorWithLoader("test", fakeLoader{snapshot: Snapshot{Root: "/repo"}, loadErr: loadErr})
		result := evaluator.Validate(context.Background(), Request{RepositoryRoot: "/repo", BindingJSON: []byte(symbolBinding(callerID, PrecisionAST))})
		if result.Evaluation.Reason != ReasonGraphMissing {
			t.Fatalf("reason = %s", result.Evaluation.Reason)
		}
	})

	t.Run("repository changes during evaluation", func(t *testing.T) {
		verifyErr := &SnapshotError{Reason: ReasonGraphStale, Diagnostic: Diagnostic{Code: "repository_changed", Message: "changed"}}
		evaluator := NewEvaluatorWithLoader("test", fakeLoader{snapshot: completeSnapshot(), verifyErr: verifyErr})
		result := evaluator.Validate(context.Background(), Request{RepositoryRoot: "/repo", BindingJSON: []byte(symbolBinding(callerID, PrecisionAST))})
		if result.Evaluation.Reason != ReasonGraphStale || result.Evaluation.Outcome != OutcomeCannotEvaluate {
			t.Fatalf("evaluation = %s/%s", result.Evaluation.Outcome, result.Evaluation.Reason)
		}
	})
}

func TestEvidenceAndFingerprints(t *testing.T) {
	result := evaluate(t, completeSnapshot(), binding(PredicateCallEdgeExists, symbolRef(callerID), ref(symbolRef(calleeID)), PrecisionPrecise), "")
	if len(result.Request.BindingFingerprint) != 64 || result.Repository.SourceFingerprint != "source-fingerprint" || result.Analysis.GraphFingerprint != "graph-fingerprint" {
		t.Fatalf("fingerprints missing from result: %+v", result)
	}
	if result.Evidence.ResolvedSubject == nil || result.Evidence.ResolvedObject == nil || len(result.Evidence.MatchedRelations) != 1 {
		t.Fatalf("evidence incomplete: %+v", result.Evidence)
	}
	if result.Evidence.MatchedRelations[0].Classification != "resolved_static" {
		t.Fatalf("classification = %q", result.Evidence.MatchedRelations[0].Classification)
	}
}

func TestResultSchemaAndSerializationAreDeterministic(t *testing.T) {
	value := binding(PredicateSymbolExists, symbolRef(callerID), nil, PrecisionAST)
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	evaluator := NewEvaluatorWithLoader("test-version", fakeLoader{snapshot: completeSnapshot()})
	evaluator.now = func() time.Time { return time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC) }
	first := evaluator.Validate(context.Background(), Request{RepositoryRoot: "/repo", BindingJSON: data})
	second := evaluator.Validate(context.Background(), Request{RepositoryRoot: "/repo", BindingJSON: data})
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("serialization differs:\n%s\n%s", firstJSON, secondJSON)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(firstJSON, &object); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"schema_version", "command", "gograph_version", "generated_at", "repository", "analysis", "request", "evaluation", "evidence"}
	if len(object) != len(wantKeys) {
		t.Fatalf("top-level fields = %v", object)
	}
	for _, key := range wantKeys {
		if _, ok := object[key]; !ok {
			t.Errorf("missing top-level field %q", key)
		}
	}
}

func TestCHAPossibleTargetIsExplicit(t *testing.T) {
	snapshot := completeSnapshot()
	snapshot.Graph.Calls[0].Resolution = graph.CallResolutionCHA
	snapshot.Graph.Calls = append(snapshot.Graph.Calls, graph.CallEdge{
		CallerSymbolID: callerID, CalleeRaw: "Run", CalleeSymbolID: concreteID,
		File: "pkg/service.go", Line: 12, Column: 3, Resolution: graph.CallResolutionCHA,
	})
	result := evaluate(t, snapshot, binding(PredicateCallEdgeExists, symbolRef(callerID), ref(symbolRef(calleeID)), PrecisionPrecise), "")
	if got := result.Evidence.MatchedRelations[0].Classification; got != "cha_possible_target" {
		t.Fatalf("classification = %q", got)
	}
}

func TestDiagnosticsAreBounded(t *testing.T) {
	message := strings.Repeat("x", maxDiagnosticBytes+100)
	loadErr := &SnapshotError{Reason: ReasonInternalError, Diagnostic: Diagnostic{Code: "large", Message: message}}
	evaluator := NewEvaluatorWithLoader("test", fakeLoader{loadErr: loadErr})
	result := evaluator.Validate(context.Background(), Request{BindingJSON: []byte(symbolBinding(callerID, PrecisionAST))})
	if len(result.Evaluation.Diagnostics) != 1 || len(result.Evaluation.Diagnostics[0].Message) > maxDiagnosticBytes {
		t.Fatalf("diagnostics not bounded: %+v", result.Evaluation.Diagnostics)
	}
}

func TestCancellationAndDeadlineDegradeToCannotEvaluate(t *testing.T) {
	tests := []struct {
		name    string
		context func() (context.Context, context.CancelFunc)
	}{
		{name: "cancellation", context: func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, func() {}
		}},
		{name: "deadline", context: func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), time.Nanosecond)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := test.context()
			defer cancel()
			result := NewEvaluatorWithLoader("test", contextLoader{}).Validate(ctx, Request{BindingJSON: []byte(symbolBinding(callerID, PrecisionAST))})
			if result.Evaluation.Outcome != OutcomeCannotEvaluate {
				t.Fatalf("outcome = %s", result.Evaluation.Outcome)
			}
		})
	}
}

func TestInvalidBindingReasons(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		reason Reason
	}{
		{name: "unsupported predicate", input: strings.Replace(symbolBinding(callerID, PrecisionAST), "symbol_exists", "reachability", 1), reason: ReasonUnsupportedPredicate},
		{name: "unsupported language", input: strings.Replace(symbolBinding(callerID, PrecisionAST), `"language":"go"`, `"language":"rust"`, 1), reason: ReasonUnsupportedLanguage},
		{name: "malformed", input: `{`, reason: ReasonInvalidRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluator := NewEvaluatorWithLoader("test", fakeLoader{})
			result := evaluator.Validate(context.Background(), Request{BindingJSON: []byte(test.input)})
			if result.Evaluation.Reason != test.reason {
				t.Fatalf("reason = %s, want %s", result.Evaluation.Reason, test.reason)
			}
		})
	}
}

func evaluate(t *testing.T, snapshot Snapshot, value Binding, expected string) Result {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	evaluator := NewEvaluatorWithLoader("test-version", fakeLoader{snapshot: snapshot})
	return evaluator.Validate(context.Background(), Request{RepositoryRoot: snapshot.Root, BindingJSON: data, ExpectedSourceFingerprint: expected})
}

func completeSnapshot() Snapshot {
	generated := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	return Snapshot{
		Root: "/repo", GraphFingerprint: "graph-fingerprint", SourceFingerprint: "source-fingerprint", Freshness: "current",
		Graph: &graph.Graph{
			Version: graph.Version, GeneratedAt: generated,
			Build:    &graph.BuildMetadata{Complete: true, Precision: graph.PrecisionPrecise, SourcePolicyVersion: graph.CurrentSourcePolicyVersion, BuildContextFingerprint: "build-context"},
			Files:    []graph.FileNode{{Path: "pkg/service.go", ContentDigest: "digest"}},
			Packages: []graph.PackageNode{{ImportPathBestEffort: testPackage, Files: []string{"pkg/service.go"}}},
			Symbols: []graph.SymbolNode{
				{ID: callerID, Kind: graph.KindFunction, File: "pkg/service.go", Line: 10},
				{ID: calleeID, Kind: graph.KindFunction, File: "pkg/service.go", Line: 20},
				{ID: otherCallID, Kind: graph.KindFunction, File: "pkg/service.go", Line: 25},
				{ID: concreteID, Kind: graph.KindStruct, File: "pkg/service.go", Line: 30},
				{ID: otherTypeID, Kind: graph.KindStruct, File: "pkg/service.go", Line: 35},
				{ID: interfaceID, Kind: graph.KindInterface, File: "pkg/service.go", Line: 40},
			},
			Imports:    []graph.ImportEdge{{FromFile: "pkg/service.go", ImportPath: "fmt"}},
			Calls:      []graph.CallEdge{{CallerSymbolID: callerID, CalleeRaw: "Run", CalleeSymbolID: calleeID, File: "pkg/service.go", Line: 12, Column: 3, Resolution: graph.CallResolutionStatic}},
			Implements: []graph.ImplementsEdge{{ConcreteID: concreteID, InterfaceID: interfaceID}},
		},
	}
}

func binding(predicate Predicate, subject Reference, object *Reference, precision Precision) Binding {
	return Binding{SchemaVersion: BindingSchemaVersion, Predicate: predicate, Subject: subject, Object: object, RequiredPrecision: precision}
}

func symbolRef(id string) Reference { return Reference{Language: "go", Kind: ReferenceSymbol, ID: id} }
func packageRef(id string) Reference {
	return Reference{Language: "go", Kind: ReferencePackage, ID: id}
}
func ref(value Reference) *Reference { return &value }

func makePartial(snapshot *Snapshot) {
	snapshot.Graph.Build.Complete = false
	snapshot.Freshness = "unknown"
}

func makeAST(snapshot *Snapshot) {
	snapshot.Graph.Build.Precision = graph.PrecisionAST
}

func makeFallback(snapshot *Snapshot) {
	snapshot.Graph.Build.Precision = graph.PrecisionFallback
}

func addUnresolvedCall(snapshot *Snapshot) {
	snapshot.Graph.Calls = append(snapshot.Graph.Calls, graph.CallEdge{CallerSymbolID: callerID, CalleeRaw: "dynamic", File: "pkg/service.go", Line: 14})
}
