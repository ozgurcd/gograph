// Package workspace defines the federated cross-repository overlay. Repository
// graphs remain authoritative; this package persists only ownership,
// cross-repository resolutions, and contract relationships.
package workspace

import "github.com/ozgurcd/gograph/internal/graph"

const (
	ManifestSchemaVersion = "gograph.workspace-manifest.v1"
	ArtifactSchemaVersion = "gograph.workspace-artifact.v1"
	QuerySchemaVersion    = "gograph.workspace-query.v1"
	StatusSchemaVersion   = "gograph.workspace-status.v1"
	ManifestFile          = ".gograph-workspace.yml"
	ArtifactFile          = ".gograph/workspace.json"
)

var ResolverVersions = map[string]string{
	"go_module": "go-module-v1",
	"go_symbol": "go-symbol-v1",
	"http":      "http-contract-v1",
}

type Manifest struct {
	SchemaVersion string             `yaml:"schema_version" json:"schema_version"`
	Name          string             `yaml:"name" json:"name"`
	DefaultScope  string             `yaml:"default_scope,omitempty" json:"default_scope,omitempty"`
	Defaults      ManifestDefaults   `yaml:"defaults,omitempty" json:"defaults,omitempty"`
	Repositories  []RepositoryConfig `yaml:"repositories" json:"repositories"`
	Scopes        []ScopeConfig      `yaml:"scopes,omitempty" json:"scopes,omitempty"`
}

type ManifestDefaults struct {
	Precision string `yaml:"precision,omitempty" json:"precision,omitempty"`
}

type RepositoryConfig struct {
	ID        string          `yaml:"id" json:"id"`
	Path      string          `yaml:"path" json:"path"`
	Precision string          `yaml:"precision,omitempty" json:"precision,omitempty"`
	Services  []ServiceConfig `yaml:"services,omitempty" json:"services,omitempty"`
}

type ServiceConfig struct {
	ID   string            `yaml:"id" json:"id"`
	HTTP HTTPServiceConfig `yaml:"http,omitempty" json:"http,omitempty"`
}

type HTTPServiceConfig struct {
	Authorities     []string `yaml:"authorities,omitempty" json:"authorities,omitempty"`
	SharedAuthority bool     `yaml:"shared_authority,omitempty" json:"shared_authority,omitempty"`
}

type ScopeConfig struct {
	ID           string   `yaml:"id" json:"id"`
	Repositories []string `yaml:"repositories" json:"repositories"`
}

type NodeRef struct {
	RepositoryID string `json:"repository_id,omitempty"`
	ModuleID     string `json:"module_id,omitempty"`
	NodeID       string `json:"node_id"`
	Kind         string `json:"kind"`
	Language     string `json:"language,omitempty"`
}

type ResolutionStatus string

const (
	ResolutionExact     ResolutionStatus = "exact"
	ResolutionAmbiguous ResolutionStatus = "ambiguous"
	ResolutionPossible  ResolutionStatus = "possible"
)

type EvidenceOrigin string

const (
	EvidenceStructural EvidenceOrigin = "structural"
	EvidenceConfigured EvidenceOrigin = "configured"
	EvidenceDerived    EvidenceOrigin = "derived"
)

type AnalysisCapabilities struct {
	ASTComplete        bool   `json:"ast_complete"`
	PrecisionRequested bool   `json:"precision_requested"`
	PreciseEnrichment  string `json:"precise_enrichment"`
	CallResolution     string `json:"call_resolution"`
	TestCallResolution string `json:"test_call_resolution"`
	HTTPExtraction     string `json:"http_extraction"`
	RPCExtraction      string `json:"rpc_extraction"`
	TopicExtraction    string `json:"topic_extraction"`
}

type Member struct {
	RepositoryID            string               `json:"repository_id"`
	Path                    string               `json:"path"`
	GraphSchemaVersion      string               `json:"graph_schema_version"`
	ArtifactFingerprint     string               `json:"artifact_fingerprint"`
	SourceFingerprint       string               `json:"source_fingerprint"`
	BuildContextFingerprint string               `json:"build_context_fingerprint"`
	Capabilities            AnalysisCapabilities `json:"capabilities"`
	Modules                 []graph.ModuleNode   `json:"modules,omitempty"`
}

type ModuleOwnership struct {
	ModulePath       string           `json:"module_path"`
	Owner            NodeRef          `json:"owner"`
	ResolutionStatus ResolutionStatus `json:"resolution_status"`
	EvidenceOrigin   EvidenceOrigin   `json:"evidence_origin"`
	Resolver         string           `json:"resolver"`
}

type ModuleImportResolution struct {
	Source           NodeRef          `json:"source"`
	ImportPath       string           `json:"import_path"`
	Target           NodeRef          `json:"target"`
	File             string           `json:"file"`
	ResolutionStatus ResolutionStatus `json:"resolution_status"`
	EvidenceOrigin   EvidenceOrigin   `json:"evidence_origin"`
	Resolver         string           `json:"resolver"`
}

type LocalCallRef struct {
	RepositoryID   string `json:"repository_id"`
	CallerSymbolID string `json:"caller_symbol_id"`
	File           string `json:"file"`
	Line           int    `json:"line"`
	Column         int    `json:"column,omitempty"`
	ExternalTarget string `json:"external_target"`
}

type GoCallResolution struct {
	LocalCall        LocalCallRef     `json:"local_call"`
	Source           NodeRef          `json:"source"`
	Targets          []NodeRef        `json:"targets"`
	ResolutionStatus ResolutionStatus `json:"resolution_status"`
	EvidenceOrigin   EvidenceOrigin   `json:"evidence_origin"`
	Resolver         string           `json:"resolver"`
}

type HTTPContractID struct {
	AuthorityID    string `json:"authority_id"`
	Method         string `json:"method"`
	NormalizedPath string `json:"normalized_path"`
}

type HTTPQualifier struct {
	Scheme string `json:"scheme,omitempty"`
	Host   string `json:"host,omitempty"`
	Port   string `json:"port,omitempty"`
}

type HTTPContract struct {
	ID         HTTPContractID  `json:"id"`
	Qualifiers []HTTPQualifier `json:"qualifiers,omitempty"`
}

type HTTPRelation struct {
	Kind             string           `json:"kind"`
	Source           NodeRef          `json:"source"`
	Contract         HTTPContractID   `json:"contract"`
	File             string           `json:"file,omitempty"`
	Line             int              `json:"line,omitempty"`
	ResolutionStatus ResolutionStatus `json:"resolution_status"`
	EvidenceOrigin   EvidenceOrigin   `json:"evidence_origin"`
	Resolver         string           `json:"resolver"`
}

type ScopeOverlay struct {
	ID            string                   `json:"id"`
	Repositories  []string                 `json:"repositories"`
	Modules       []ModuleOwnership        `json:"module_ownership,omitempty"`
	Imports       []ModuleImportResolution `json:"module_imports,omitempty"`
	GoCalls       []GoCallResolution       `json:"go_call_resolutions,omitempty"`
	HTTPContracts []HTTPContract           `json:"http_contracts,omitempty"`
	HTTPRelations []HTTPRelation           `json:"http_relations,omitempty"`
}

type Artifact struct {
	SchemaVersion    string            `json:"schema_version"`
	WorkspaceName    string            `json:"workspace_name"`
	DefaultScope     string            `json:"default_scope,omitempty"`
	InputFingerprint string            `json:"input_fingerprint"`
	ResolverVersions map[string]string `json:"resolver_versions"`
	Members          []Member          `json:"members"`
	Scopes           []ScopeOverlay    `json:"scopes"`
}

type LoadedMember struct {
	Config RepositoryConfig
	Root   string
	Graph  *graph.Graph
	Record Member
}

type LoadedWorkspace struct {
	Root                string
	Manifest            Manifest
	ManifestFingerprint string
	Members             []LoadedMember
	Artifact            *Artifact
	ArtifactFingerprint string
}
