// Package validation evaluates closed structural predicates against a trusted
// persisted Gograph snapshot. It is transport-neutral and never rebuilds a graph.
package validation

import "time"

const (
	BindingSchemaVersion = "gograph.binding.v1"
	ResultSchemaVersion  = "gograph.validation.v1"
	VersionSchemaVersion = "gograph.version.v1"
)

type Predicate string

const (
	PredicateSymbolExists   Predicate = "symbol_exists"
	PredicatePackageImports Predicate = "package_imports"
	PredicateCallEdgeExists Predicate = "call_edge_exists"
	PredicateTypeImplements Predicate = "type_implements"
)

type Precision string

const (
	PrecisionAST     Precision = "ast"
	PrecisionPrecise Precision = "precise"
)

type ReferenceKind string

const (
	ReferenceSymbol  ReferenceKind = "symbol"
	ReferencePackage ReferenceKind = "package"
)

type Reference struct {
	Language string        `json:"language"`
	Kind     ReferenceKind `json:"kind"`
	ID       string        `json:"id"`
}

type Binding struct {
	SchemaVersion     string     `json:"schema_version"`
	Predicate         Predicate  `json:"predicate"`
	Subject           Reference  `json:"subject"`
	Object            *Reference `json:"object,omitempty"`
	RequiredPrecision Precision  `json:"required_precision"`
}

type Outcome string

const (
	OutcomePass           Outcome = "pass"
	OutcomeFail           Outcome = "fail"
	OutcomeCannotEvaluate Outcome = "cannot_evaluate"
)

type Reason string

const (
	ReasonPredicatePassed         Reason = "predicate_passed"
	ReasonPredicateFailed         Reason = "predicate_failed"
	ReasonSymbolNotFound          Reason = "symbol_not_found"
	ReasonSymbolAmbiguous         Reason = "symbol_ambiguous"
	ReasonPackageNotFound         Reason = "package_not_found"
	ReasonRelationNotFound        Reason = "relation_not_found"
	ReasonGraphMissing            Reason = "graph_missing"
	ReasonGraphInvalid            Reason = "graph_invalid"
	ReasonGraphSchemaUnsupported  Reason = "graph_schema_unsupported"
	ReasonSourcePolicyUnsupported Reason = "source_policy_unsupported"
	ReasonGraphStale              Reason = "graph_stale"
	ReasonPrecisionInsufficient   Reason = "precision_insufficient"
	ReasonAnalysisIncomplete      Reason = "analysis_incomplete"
	ReasonSymbolIdentityUnstable  Reason = "symbol_identity_unstable"
	ReasonUnsupportedPredicate    Reason = "unsupported_predicate"
	ReasonUnsupportedLanguage     Reason = "unsupported_language"
	ReasonRepositoryMismatch      Reason = "repository_mismatch"
	ReasonInvalidRequest          Reason = "invalid_request"
	ReasonInternalError           Reason = "internal_error"
)

type VersionDocument struct {
	SchemaVersion string `json:"schema_version"`
	Version       string `json:"version"`
}

type Repository struct {
	Root              string `json:"root"`
	SourceFingerprint string `json:"source_fingerprint,omitempty"`
	GitRevision       string `json:"git_revision,omitempty"`
}

type Analysis struct {
	GraphSchemaVersion      string     `json:"graph_schema_version,omitempty"`
	SourcePolicyVersion     int        `json:"source_policy_version,omitempty"`
	GraphFingerprint        string     `json:"graph_fingerprint,omitempty"`
	BuildContextFingerprint string     `json:"build_context_fingerprint,omitempty"`
	Mode                    string     `json:"mode,omitempty"`
	Precision               Precision  `json:"precision,omitempty"`
	Completeness            string     `json:"completeness,omitempty"`
	Freshness               string     `json:"freshness,omitempty"`
	GraphGeneratedAt        *time.Time `json:"graph_generated_at,omitempty"`
}

type RequestRecord struct {
	BindingFingerprint string   `json:"binding_fingerprint,omitempty"`
	Binding            *Binding `json:"binding,omitempty"`
}

type Diagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

type Evaluation struct {
	Outcome     Outcome      `json:"outcome"`
	Reason      Reason       `json:"reason"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type Location struct {
	Path   string `json:"path"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

type ResolvedReference struct {
	Kind       ReferenceKind `json:"kind"`
	ID         string        `json:"id"`
	SymbolKind string        `json:"symbol_kind,omitempty"`
	Locations  []Location    `json:"locations"`
}

type MatchedRelation struct {
	Kind           string     `json:"kind"`
	SubjectID      string     `json:"subject_id"`
	ObjectID       string     `json:"object_id"`
	Classification string     `json:"classification,omitempty"`
	Locations      []Location `json:"locations"`
}

type Evidence struct {
	ResolvedSubject  *ResolvedReference `json:"resolved_subject,omitempty"`
	ResolvedObject   *ResolvedReference `json:"resolved_object,omitempty"`
	MatchedRelations []MatchedRelation  `json:"matched_relations"`
}

type Result struct {
	SchemaVersion  string        `json:"schema_version"`
	Command        string        `json:"command"`
	GographVersion string        `json:"gograph_version"`
	GeneratedAt    time.Time     `json:"generated_at"`
	Repository     Repository    `json:"repository"`
	Analysis       Analysis      `json:"analysis"`
	Request        RequestRecord `json:"request"`
	Evaluation     Evaluation    `json:"evaluation"`
	Evidence       Evidence      `json:"evidence"`
}

type Request struct {
	RepositoryRoot            string
	BindingJSON               []byte
	ExpectedSourceFingerprint string
}
