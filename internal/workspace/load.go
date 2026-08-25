package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"sort"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/moduleinventory"
	"github.com/ozgurcd/gograph/internal/sourcefs"
	"github.com/ozgurcd/gograph/internal/validation"
)

type MemberInspection struct {
	Config RepositoryConfig
	Root   string
	Loaded *LoadedMember
	Error  error
}

func InspectMembers(ctx context.Context, root string, manifest Manifest) []MemberInspection {
	return InspectMembersWithBuildTags(ctx, root, manifest, nil)
}

func InspectMembersWithBuildTags(ctx context.Context, root string, manifest Manifest, buildTags []string) []MemberInspection {
	inspections := make([]MemberInspection, 0, len(manifest.Repositories))
	for _, config := range manifest.Repositories {
		inspections = append(inspections, InspectMemberWithBuildTags(ctx, root, config, buildTags))
	}
	return inspections
}

// InspectMember validates one configured member against its current source,
// build selection, workspace capabilities, and on-disk module inventory.
func InspectMember(ctx context.Context, root string, config RepositoryConfig) MemberInspection {
	return InspectMemberWithBuildTags(ctx, root, config, nil)
}

func InspectMemberWithBuildTags(ctx context.Context, root string, config RepositoryConfig, buildTags []string) MemberInspection {
	memberRoot, rootErr := ResolveMemberRoot(root, config)
	inspection := MemberInspection{Config: config, Root: memberRoot}
	if rootErr != nil {
		inspection.Error = rootErr
		return inspection
	}
	snapshot, err := (validation.RepositoryLoader{BuildTags: buildTags}).Load(ctx, memberRoot)
	if err != nil {
		inspection.Error = err
		return inspection
	}
	if snapshot.Graph == nil || snapshot.Graph.Build == nil {
		inspection.Error = fmt.Errorf("repository graph is missing build metadata")
		return inspection
	}
	if snapshot.Graph.Build.WorkspaceFactsVersion != graph.CurrentWorkspaceFactsVersion {
		inspection.Error = fmt.Errorf("repository graph lacks current workspace facts; rebuild it with this gograph version")
		return inspection
	}
	if snapshot.Freshness != "current" || !snapshot.Graph.Build.Complete {
		inspection.Error = fmt.Errorf("repository graph is not complete and current")
		return inspection
	}
	if config.Precision == "precise" && snapshot.Graph.Build.EffectivePrecision() != graph.PrecisionPrecise {
		inspection.Error = fmt.Errorf("repository requires precise analysis but graph precision is %s", snapshot.Graph.Build.EffectivePrecision())
		return inspection
	}
	modules, err := moduleinventory.Verify(memberRoot, snapshot.Graph.Packages, snapshot.Graph.Modules)
	if err != nil {
		inspection.Error = fmt.Errorf("verify graph module ownership: %w", err)
		return inspection
	}
	snapshot.Graph.Modules = modules
	record := memberRecord(config, snapshot)
	inspection.Loaded = &LoadedMember{Config: config, Root: memberRoot, Graph: snapshot.Graph, Record: record}
	return inspection
}

func LoadMembers(ctx context.Context, root string, manifest Manifest) ([]LoadedMember, error) {
	return LoadMembersWithBuildTags(ctx, root, manifest, nil)
}

func LoadMembersWithBuildTags(ctx context.Context, root string, manifest Manifest, buildTags []string) ([]LoadedMember, error) {
	inspections := InspectMembersWithBuildTags(ctx, root, manifest, buildTags)
	members := make([]LoadedMember, 0, len(inspections))
	for _, inspection := range inspections {
		if inspection.Error != nil {
			return nil, fmt.Errorf("repository %q: %w", inspection.Config.ID, inspection.Error)
		}
		members = append(members, *inspection.Loaded)
	}
	return members, nil
}

func memberRecord(config RepositoryConfig, snapshot validation.Snapshot) Member {
	build := snapshot.Graph.Build
	precise := "not_requested"
	callResolution := "ast_heuristic"
	if build.PreciseRequested() {
		precise = "fallback"
	}
	if build.EffectivePrecision() == graph.PrecisionPrecise {
		precise = "complete"
		callResolution = "typed_cha"
	}
	modules := append([]graph.ModuleNode(nil), snapshot.Graph.Modules...)
	sort.Slice(modules, func(i, j int) bool {
		if modules[i].Path != modules[j].Path {
			return modules[i].Path < modules[j].Path
		}
		return modules[i].Dir < modules[j].Dir
	})
	return Member{
		RepositoryID:            config.ID,
		Path:                    config.Path,
		GraphSchemaVersion:      snapshot.Graph.Version,
		ArtifactFingerprint:     snapshot.GraphFingerprint,
		SourceFingerprint:       build.SourceFingerprint,
		BuildContextFingerprint: build.BuildContextFingerprint,
		Capabilities: AnalysisCapabilities{
			ASTComplete:        build.Complete,
			PrecisionRequested: build.PreciseRequested(),
			PreciseEnrichment:  precise,
			CallResolution:     callResolution,
			TestCallResolution: string(build.EffectiveTestCallResolution()),
			HTTPExtraction:     "net_http_v1",
			RPCExtraction:      "unavailable",
			TopicExtraction:    "unavailable",
		},
		Modules: modules,
	}
}

func InputFingerprint(manifest Manifest, members []LoadedMember) (string, error) {
	type memberInput struct {
		RepositoryID        string `json:"repository_id"`
		ArtifactFingerprint string `json:"artifact_fingerprint"`
	}
	type inputDocument struct {
		Manifest         Manifest          `json:"manifest"`
		Members          []memberInput     `json:"members"`
		ResolverVersions map[string]string `json:"resolver_versions"`
	}
	memberInputs := make([]memberInput, 0, len(members))
	for _, member := range members {
		memberInputs = append(memberInputs, memberInput{member.Record.RepositoryID, member.Record.ArtifactFingerprint})
	}
	sort.Slice(memberInputs, func(i, j int) bool { return memberInputs[i].RepositoryID < memberInputs[j].RepositoryID })
	document := inputDocument{Manifest: manifest, Members: memberInputs, ResolverVersions: ResolverVersions}
	data, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("encode workspace input identity: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func LoadArtifact(root string) (*Artifact, string, error) {
	reader, err := sourcefs.Open(root)
	if err != nil {
		return nil, "", fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = reader.Close() }()
	data, err := reader.ReadRegularFileLimit(ArtifactFile, graph.MaxArtifactBytes)
	if err != nil {
		return nil, "", fmt.Errorf("read workspace artifact: %w", err)
	}
	var artifact Artifact
	if err := jsonv2.Unmarshal(data, &artifact, jsonv2.RejectUnknownMembers(true)); err != nil {
		return nil, "", fmt.Errorf("parse workspace artifact: %w", err)
	}
	if artifact.SchemaVersion != ArtifactSchemaVersion {
		return nil, "", fmt.Errorf("unsupported workspace artifact schema %q", artifact.SchemaVersion)
	}
	sum := sha256.Sum256(data)
	return &artifact, hex.EncodeToString(sum[:]), nil
}

func Load(ctx context.Context, start string) (*LoadedWorkspace, error) {
	return LoadWithBuildTags(ctx, start, nil)
}

func LoadWithBuildTags(ctx context.Context, start string, buildTags []string) (*LoadedWorkspace, error) {
	root, err := FindRoot(start)
	if err != nil {
		return nil, err
	}
	manifest, manifestFingerprint, err := LoadManifest(root)
	if err != nil {
		return nil, err
	}
	members, err := LoadMembersWithBuildTags(ctx, root, manifest, buildTags)
	if err != nil {
		return nil, err
	}
	artifact, artifactFingerprint, err := LoadArtifact(root)
	if err != nil {
		return nil, err
	}
	inputFingerprint, err := InputFingerprint(manifest, members)
	if err != nil {
		return nil, err
	}
	if artifact.InputFingerprint != inputFingerprint {
		return nil, fmt.Errorf("workspace overlay is stale; run `gograph workspace build`")
	}
	expected, err := Resolve(manifest, members)
	if err != nil {
		return nil, fmt.Errorf("re-resolve workspace overlay: %w", err)
	}
	expectedBytes, err := EncodeArtifact(expected)
	if err != nil {
		return nil, err
	}
	expectedSum := sha256.Sum256(expectedBytes)
	if artifactFingerprint != hex.EncodeToString(expectedSum[:]) {
		return nil, fmt.Errorf("workspace artifact does not match the deterministic overlay for its recorded inputs; run `gograph workspace build`")
	}
	return &LoadedWorkspace{Root: root, Manifest: manifest, ManifestFingerprint: manifestFingerprint, Members: members, Artifact: artifact, ArtifactFingerprint: artifactFingerprint}, nil
}
