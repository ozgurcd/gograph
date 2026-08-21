package validation

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ozgurcd/gograph/internal/graph"
)

const (
	maxDiagnostics     = 16
	maxDiagnosticBytes = 512
)

type Evaluator struct {
	version string
	loader  SnapshotLoader
	now     func() time.Time
}

func NewEvaluator(version string) *Evaluator {
	return NewEvaluatorWithLoader(version, RepositoryLoader{})
}

func NewEvaluatorWithLoader(version string, loader SnapshotLoader) *Evaluator {
	return &Evaluator{version: version, loader: loader, now: func() time.Time { return time.Now().UTC() }}
}

func InvalidRequestResult(version, root, message string) Result {
	evaluator := NewEvaluator(version)
	result := evaluator.baseResult(root)
	result.Evaluation = cannotEvaluate(ReasonInvalidRequest, "invalid_request", message)
	return result
}

func (e *Evaluator) Validate(ctx context.Context, request Request) Result {
	result := e.baseResult(request.RepositoryRoot)
	binding, fingerprint, err := ParseBinding(request.BindingJSON)
	if err != nil {
		result.Request.BindingFingerprint = fingerprint
		result.Evaluation = cannotEvaluate(bindingReason(err), "invalid_binding", err.Error())
		return result
	}
	result.Request = RequestRecord{BindingFingerprint: fingerprint, Binding: &binding}

	snapshot, err := e.loader.Load(ctx, request.RepositoryRoot)
	e.applySnapshot(&result, snapshot)
	if err != nil {
		result.Evaluation = evaluationFromError(err)
		return result
	}
	if request.ExpectedSourceFingerprint != "" && request.ExpectedSourceFingerprint != snapshot.SourceFingerprint {
		result.Evaluation = cannotEvaluate(ReasonRepositoryMismatch, "source_fingerprint_mismatch", "repository source fingerprint does not match the caller-selected snapshot")
		return result
	}

	result.Evaluation, result.Evidence = evaluatePredicate(binding, snapshot)
	if err := e.loader.VerifyCurrent(ctx, snapshot); err != nil {
		result.Evaluation = evaluationFromError(err)
		result.Evidence = emptyEvidence()
	}
	return result
}

func (e *Evaluator) baseResult(root string) Result {
	return Result{
		SchemaVersion:  ResultSchemaVersion,
		Command:        "validate",
		GographVersion: e.version,
		GeneratedAt:    e.now(),
		Repository:     Repository{Root: root},
		Evaluation:     cannotEvaluate(ReasonInternalError, "not_evaluated", "validation did not complete"),
		Evidence:       emptyEvidence(),
	}
}

func (e *Evaluator) applySnapshot(result *Result, snapshot Snapshot) {
	if snapshot.Root != "" {
		result.Repository.Root = snapshot.Root
	}
	result.Repository.SourceFingerprint = snapshot.SourceFingerprint
	result.Analysis.GraphFingerprint = snapshot.GraphFingerprint
	result.Analysis.Freshness = snapshot.Freshness
	if snapshot.Graph == nil {
		return
	}
	build := snapshot.Graph.Build
	result.Analysis.GraphSchemaVersion = snapshot.Graph.Version
	generatedAt := snapshot.Graph.GeneratedAt
	result.Analysis.GraphGeneratedAt = &generatedAt
	if build == nil {
		return
	}
	result.Analysis.SourcePolicyVersion = build.SourcePolicyVersion
	result.Analysis.BuildContextFingerprint = build.BuildContextFingerprint
	result.Analysis.Completeness = "partial"
	if build.Complete {
		result.Analysis.Completeness = "complete"
	}
	switch build.EffectivePrecision() {
	case graph.PrecisionPrecise:
		result.Analysis.Mode = "precise"
		result.Analysis.Precision = PrecisionPrecise
	case graph.PrecisionFallback:
		result.Analysis.Mode = "precise"
		result.Analysis.Precision = PrecisionAST
	default:
		result.Analysis.Mode = "ast"
		result.Analysis.Precision = PrecisionAST
	}
}

func evaluatePredicate(binding Binding, snapshot Snapshot) (Evaluation, Evidence) {
	evidence := emptyEvidence()
	if snapshot.Graph == nil || snapshot.Graph.Build == nil {
		return cannotEvaluate(ReasonGraphInvalid, "graph_metadata_missing", "persisted graph has no trusted build metadata"), evidence
	}
	if snapshot.Freshness != "current" {
		if !snapshot.Graph.Build.Complete {
			return cannotEvaluate(ReasonAnalysisIncomplete, "analysis_incomplete", "persisted graph analysis is incomplete"), evidence
		}
		return cannotEvaluate(ReasonGraphStale, "graph_not_current", "persisted graph freshness cannot be established"), evidence
	}
	if !snapshot.Graph.Build.Complete {
		return cannotEvaluate(ReasonAnalysisIncomplete, "analysis_incomplete", "persisted graph analysis is incomplete"), evidence
	}
	actualPrecision := PrecisionAST
	if snapshot.Graph.Build.EffectivePrecision() == graph.PrecisionPrecise {
		actualPrecision = PrecisionPrecise
	}
	if binding.RequiredPrecision == PrecisionPrecise && actualPrecision != PrecisionPrecise {
		return cannotEvaluate(ReasonPrecisionInsufficient, "required_precision_unavailable", "persisted graph is not precise-complete"), evidence
	}

	switch binding.Predicate {
	case PredicateSymbolExists:
		return evaluateSymbolExists(binding, snapshot)
	case PredicatePackageImports:
		return evaluatePackageImports(binding, snapshot)
	case PredicateCallEdgeExists:
		if actualPrecision != PrecisionPrecise {
			return cannotEvaluate(ReasonPrecisionInsufficient, "call_resolution_not_precise", "call-edge absence requires precise-complete call resolution"), evidence
		}
		return evaluateCallEdge(binding, snapshot)
	case PredicateTypeImplements:
		if actualPrecision != PrecisionPrecise {
			return cannotEvaluate(ReasonPrecisionInsufficient, "implementation_analysis_not_precise", "implementation absence requires precise-complete analysis"), evidence
		}
		return evaluateTypeImplements(binding, snapshot)
	default:
		return cannotEvaluate(ReasonUnsupportedPredicate, "unsupported_predicate", "predicate is not supported by validation schema v1"), evidence
	}
}

func evaluateSymbolExists(binding Binding, snapshot Snapshot) (Evaluation, Evidence) {
	matches := exactSymbols(snapshot.Graph, binding.Subject.ID)
	if len(matches) > 1 {
		return cannotEvaluate(ReasonSymbolAmbiguous, "duplicate_symbol_identity", "persisted graph contains duplicate exact symbol identities"), emptyEvidence()
	}
	if len(matches) == 0 {
		if snapshot.Graph.Build.EffectivePrecision() == graph.PrecisionFallback {
			return cannotEvaluate(ReasonPrecisionInsufficient, "precise_fallback_absence", "a precise-fallback graph cannot establish evaluated symbol absence"), emptyEvidence()
		}
		return failed(ReasonSymbolNotFound, "symbol_not_found", "exact symbol identity is absent from the complete selected build graph"), emptyEvidence()
	}
	evidence := emptyEvidence()
	evidence.ResolvedSubject = resolvedSymbol(snapshot.Root, matches[0])
	return passed(), evidence
}

func evaluatePackageImports(binding Binding, snapshot Snapshot) (Evaluation, Evidence) {
	packages := exactPackages(snapshot.Graph, binding.Subject.ID)
	if len(packages) != 1 {
		return unresolvedPackage(packages, "subject package"), emptyEvidence()
	}
	evidence := emptyEvidence()
	evidence.ResolvedSubject = resolvedPackage(snapshot.Root, packages[0])
	evidence.ResolvedObject = &ResolvedReference{Kind: ReferencePackage, ID: binding.Object.ID, Locations: []Location{}}
	files := make(map[string]struct{}, len(packages[0].Files))
	for _, name := range packages[0].Files {
		files[name] = struct{}{}
	}
	for _, edge := range snapshot.Graph.Imports {
		if edge.ImportPath != binding.Object.ID {
			continue
		}
		if _, ok := files[edge.FromFile]; !ok {
			continue
		}
		evidence.MatchedRelations = append(evidence.MatchedRelations, MatchedRelation{
			Kind: "package_imports", SubjectID: binding.Subject.ID, ObjectID: binding.Object.ID,
			Classification: "direct", Locations: locations(location(snapshot.Root, edge.FromFile, 0, 0)),
		})
	}
	if len(evidence.MatchedRelations) == 0 {
		if snapshot.Graph.Build.EffectivePrecision() == graph.PrecisionFallback {
			return cannotEvaluate(ReasonPrecisionInsufficient, "precise_fallback_absence", "a precise-fallback graph cannot establish evaluated import absence"), evidence
		}
		return failed(ReasonRelationNotFound, "import_not_found", "direct import is absent from the complete selected build graph"), evidence
	}
	return passed(), evidence
}

func evaluateCallEdge(binding Binding, snapshot Snapshot) (Evaluation, Evidence) {
	callers := exactSymbols(snapshot.Graph, binding.Subject.ID)
	callees := exactSymbols(snapshot.Graph, binding.Object.ID)
	if len(callers) != 1 {
		return unresolvedIdentity(callers, "caller symbol"), emptyEvidence()
	}
	if len(callees) != 1 {
		return unresolvedIdentity(callees, "callee symbol"), emptyEvidence()
	}
	if !callable(callers[0]) || !callable(callees[0]) {
		return cannotEvaluate(ReasonInvalidRequest, "symbol_kind_invalid", "call_edge_exists requires function or method identities"), emptyEvidence()
	}
	evidence := emptyEvidence()
	evidence.ResolvedSubject = resolvedSymbol(snapshot.Root, callers[0])
	evidence.ResolvedObject = resolvedSymbol(snapshot.Root, callees[0])
	unresolved := false
	for _, edge := range snapshot.Graph.Calls {
		if edge.Synthetic || edge.CallerSymbolID != binding.Subject.ID {
			continue
		}
		if edge.CalleeSymbolID == "" {
			unresolved = true
			continue
		}
		if edge.CalleeSymbolID != binding.Object.ID {
			continue
		}
		if edge.Resolution != graph.CallResolutionStatic && edge.Resolution != graph.CallResolutionCHA {
			return cannotEvaluate(ReasonAnalysisIncomplete, "call_resolution_unknown", "matched call edge lacks trustworthy resolution provenance"), evidence
		}
		evidence.MatchedRelations = append(evidence.MatchedRelations, MatchedRelation{
			Kind: "call_edge_exists", SubjectID: binding.Subject.ID, ObjectID: binding.Object.ID,
			Classification: string(edge.Resolution), Locations: locations(location(snapshot.Root, edge.File, edge.Line, edge.Column)),
		})
	}
	if len(evidence.MatchedRelations) > 0 {
		return passed(), evidence
	}
	if unresolved {
		return cannotEvaluate(ReasonAnalysisIncomplete, "call_resolution_incomplete", "one or more relevant caller edges lack an exact resolved target"), evidence
	}
	return failed(ReasonRelationNotFound, "call_edge_not_found", "resolved call edge is absent from the precise-complete graph"), evidence
}

func evaluateTypeImplements(binding Binding, snapshot Snapshot) (Evaluation, Evidence) {
	concrete := exactSymbols(snapshot.Graph, binding.Subject.ID)
	iface := exactSymbols(snapshot.Graph, binding.Object.ID)
	if len(concrete) != 1 {
		return unresolvedIdentity(concrete, "concrete type"), emptyEvidence()
	}
	if len(iface) != 1 {
		return unresolvedIdentity(iface, "interface type"), emptyEvidence()
	}
	if concrete[0].Kind != graph.KindStruct && concrete[0].Kind != graph.KindType {
		return cannotEvaluate(ReasonInvalidRequest, "concrete_kind_invalid", "type_implements subject must resolve to a named struct or type"), emptyEvidence()
	}
	if iface[0].Kind != graph.KindInterface {
		return cannotEvaluate(ReasonInvalidRequest, "interface_kind_invalid", "type_implements object must resolve to a named interface"), emptyEvidence()
	}
	evidence := emptyEvidence()
	evidence.ResolvedSubject = resolvedSymbol(snapshot.Root, concrete[0])
	evidence.ResolvedObject = resolvedSymbol(snapshot.Root, iface[0])
	for _, edge := range snapshot.Graph.Implements {
		if edge.ConcreteID == binding.Subject.ID && edge.InterfaceID == binding.Object.ID {
			evidence.MatchedRelations = append(evidence.MatchedRelations, MatchedRelation{
				Kind: "type_implements", SubjectID: binding.Subject.ID, ObjectID: binding.Object.ID,
				Classification: "precise_static", Locations: []Location{},
			})
		}
	}
	if len(evidence.MatchedRelations) == 0 {
		return failed(ReasonRelationNotFound, "implementation_not_found", "implementation edge is absent from the precise-complete graph"), evidence
	}
	return passed(), evidence
}

func exactSymbols(g *graph.Graph, id string) []graph.SymbolNode {
	var matches []graph.SymbolNode
	for _, symbol := range g.Symbols {
		if symbol.ID == id {
			matches = append(matches, symbol)
		}
	}
	return matches
}

func exactPackages(g *graph.Graph, importPath string) []graph.PackageNode {
	var matches []graph.PackageNode
	for _, pkg := range g.Packages {
		if pkg.ImportPathBestEffort == importPath && !strings.HasPrefix(pkg.ImportPathBestEffort, "_") {
			matches = append(matches, pkg)
		}
	}
	return matches
}

func callable(symbol graph.SymbolNode) bool {
	return symbol.Kind == graph.KindFunction || symbol.Kind == graph.KindMethod
}

func resolvedSymbol(root string, symbol graph.SymbolNode) *ResolvedReference {
	return &ResolvedReference{Kind: ReferenceSymbol, ID: symbol.ID, SymbolKind: string(symbol.Kind), Locations: locations(location(root, symbol.File, symbol.Line, 0))}
}

func resolvedPackage(root string, pkg graph.PackageNode) *ResolvedReference {
	resolved := &ResolvedReference{Kind: ReferencePackage, ID: pkg.ImportPathBestEffort, Locations: []Location{}}
	for _, file := range pkg.Files {
		if loc := location(root, file, 0, 0); loc.Path != "" {
			resolved.Locations = append(resolved.Locations, loc)
		}
	}
	return resolved
}

func unresolvedIdentity(matches any, label string) Evaluation {
	count := 0
	switch values := matches.(type) {
	case []graph.SymbolNode:
		count = len(values)
	case []graph.PackageNode:
		count = len(values)
	}
	if count > 1 {
		return cannotEvaluate(ReasonSymbolAmbiguous, "identity_ambiguous", fmt.Sprintf("exact %s identity resolves more than once", label))
	}
	return cannotEvaluate(ReasonSymbolNotFound, "identity_not_found", fmt.Sprintf("exact %s identity cannot be resolved", label))
}

func unresolvedPackage(matches []graph.PackageNode, label string) Evaluation {
	if len(matches) > 1 {
		return cannotEvaluate(ReasonSymbolAmbiguous, "identity_ambiguous", fmt.Sprintf("exact %s identity resolves more than once", label))
	}
	return cannotEvaluate(ReasonPackageNotFound, "package_not_found", fmt.Sprintf("exact %s identity cannot be resolved", label))
}

func location(root, name string, line, column int) Location {
	if name == "" {
		return Location{}
	}
	path, err := normalizePersistedPath(root, name)
	if err != nil {
		return Location{}
	}
	return Location{Path: path, Line: line, Column: column}
}

func locations(value Location) []Location {
	if value.Path == "" {
		return []Location{}
	}
	return []Location{value}
}

func passed() Evaluation {
	return Evaluation{Outcome: OutcomePass, Reason: ReasonPredicatePassed, Diagnostics: []Diagnostic{}}
}

func failed(reason Reason, code, message string) Evaluation {
	return Evaluation{Outcome: OutcomeFail, Reason: reason, Diagnostics: boundedDiagnostics(Diagnostic{Code: code, Message: message})}
}

func cannotEvaluate(reason Reason, code, message string) Evaluation {
	return Evaluation{Outcome: OutcomeCannotEvaluate, Reason: reason, Diagnostics: boundedDiagnostics(Diagnostic{Code: code, Message: message})}
}

func evaluationFromError(err error) Evaluation {
	if typed, ok := err.(*SnapshotError); ok {
		return Evaluation{Outcome: OutcomeCannotEvaluate, Reason: typed.Reason, Diagnostics: boundedDiagnostics(typed.Diagnostic)}
	}
	return cannotEvaluate(ReasonInternalError, "internal_error", err.Error())
}

func emptyEvidence() Evidence {
	return Evidence{MatchedRelations: []MatchedRelation{}}
}

func boundedDiagnostics(values ...Diagnostic) []Diagnostic {
	if len(values) > maxDiagnostics {
		values = values[:maxDiagnostics]
	}
	for i := range values {
		values[i].Message = boundedMessage(values[i].Message)
	}
	return values
}

func boundedMessage(message string) string {
	if len(message) <= maxDiagnosticBytes {
		return message
	}
	message = message[:maxDiagnosticBytes]
	for !utf8.ValidString(message) {
		message = message[:len(message)-1]
	}
	return message
}

func bindingReason(err error) Reason {
	message := err.Error()
	if strings.Contains(message, "unsupported predicate") {
		return ReasonUnsupportedPredicate
	}
	if strings.Contains(message, "unsupported language") {
		return ReasonUnsupportedLanguage
	}
	if strings.Contains(message, "absolute-path-derived") {
		return ReasonSymbolIdentityUnstable
	}
	return ReasonInvalidRequest
}
