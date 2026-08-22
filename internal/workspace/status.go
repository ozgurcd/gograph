package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/moduleinventory"
	"github.com/ozgurcd/gograph/internal/validation"
)

type AggregateState string

const (
	StateComplete       AggregateState = "complete"
	StatePartial        AggregateState = "partial"
	StateCannotEvaluate AggregateState = "cannot_evaluate"
)

type MemberStatus struct {
	RepositoryID            string               `json:"repository_id"`
	Path                    string               `json:"path"`
	Available               bool                 `json:"available"`
	Fresh                   bool                 `json:"fresh"`
	ArtifactFingerprint     string               `json:"artifact_fingerprint,omitempty"`
	SourceFingerprint       string               `json:"source_fingerprint,omitempty"`
	BuildContextFingerprint string               `json:"build_context_fingerprint,omitempty"`
	AnalysisMode            string               `json:"analysis_mode,omitempty"`
	Capabilities            AnalysisCapabilities `json:"capabilities"`
	RepositoryRevision      string               `json:"repository_revision,omitempty"`
	Dirty                   bool                 `json:"dirty"`
	Diagnostics             []string             `json:"diagnostics"`
}

type OverlayStatus struct {
	Present             bool              `json:"present"`
	Fresh               bool              `json:"fresh"`
	InputFingerprint    string            `json:"input_fingerprint,omitempty"`
	ArtifactFingerprint string            `json:"artifact_fingerprint,omitempty"`
	ResolverVersions    map[string]string `json:"resolver_versions,omitempty"`
	Diagnostics         []string          `json:"diagnostics"`
}

type Status struct {
	SchemaVersion  string         `json:"schema_version"`
	WorkspaceName  string         `json:"workspace_name"`
	AggregateState AggregateState `json:"aggregate_state"`
	DefaultScope   string         `json:"default_scope,omitempty"`
	Members        []MemberStatus `json:"members"`
	Overlay        OverlayStatus  `json:"overlay"`
}

func InspectStatus(ctx context.Context, root string, manifest Manifest) Status {
	status := Status{SchemaVersion: StatusSchemaVersion, WorkspaceName: manifest.Name, AggregateState: StateComplete, DefaultScope: manifest.DefaultScope, Overlay: OverlayStatus{Diagnostics: []string{}}}
	loader := validation.RepositoryLoader{}
	var loaded []LoadedMember
	availableMembers := 0
	for _, config := range manifest.Repositories {
		entry := MemberStatus{RepositoryID: config.ID, Path: config.Path, Diagnostics: []string{}}
		memberRoot, rootErr := ResolveMemberRoot(root, config)
		if rootErr != nil {
			entry.Diagnostics = append(entry.Diagnostics, rootErr.Error())
			status.AggregateState = StatePartial
			status.Members = append(status.Members, entry)
			continue
		}
		revision, dirty := repositoryRevision(ctx, memberRoot)
		entry.RepositoryRevision, entry.Dirty = revision, dirty
		snapshot, err := loader.Load(ctx, memberRoot)
		if snapshot.Graph != nil {
			entry.Available = true
			availableMembers++
			entry.ArtifactFingerprint = snapshot.GraphFingerprint
			entry.SourceFingerprint = snapshot.SourceFingerprint
			entry.BuildContextFingerprint = snapshot.SelectionFingerprint
			entry.AnalysisMode = string(snapshot.Graph.Build.EffectivePrecision())
			entry.Capabilities = memberRecord(config, snapshot).Capabilities
		}
		if err != nil {
			entry.Diagnostics = append(entry.Diagnostics, err.Error())
			status.AggregateState = StatePartial
		} else if snapshot.Freshness != "current" || !snapshot.Graph.Build.Complete {
			entry.Diagnostics = append(entry.Diagnostics, "repository graph is not complete and current")
			status.AggregateState = StatePartial
		} else if snapshot.Graph.Build.WorkspaceFactsVersion != graph.CurrentWorkspaceFactsVersion {
			entry.Diagnostics = append(entry.Diagnostics, "repository graph lacks current workspace facts")
			status.AggregateState = StatePartial
		} else if config.Precision == "precise" && snapshot.Graph.Build.EffectivePrecision() != graph.PrecisionPrecise {
			entry.Diagnostics = append(entry.Diagnostics, "configured precise analysis is unavailable")
			status.AggregateState = StatePartial
		} else if modules, verifyErr := moduleinventory.Verify(memberRoot, snapshot.Graph.Packages, snapshot.Graph.Modules); verifyErr != nil {
			entry.Diagnostics = append(entry.Diagnostics, "verify graph module ownership: "+verifyErr.Error())
			status.AggregateState = StatePartial
		} else {
			snapshot.Graph.Modules = modules
			entry.Fresh = true
			loaded = append(loaded, LoadedMember{Config: config, Root: memberRoot, Graph: snapshot.Graph, Record: memberRecord(config, snapshot)})
		}
		status.Members = append(status.Members, entry)
	}
	artifact, fingerprint, artifactErr := LoadArtifact(root)
	if artifactErr != nil {
		status.Overlay.Diagnostics = append(status.Overlay.Diagnostics, artifactErr.Error())
		if status.AggregateState == StateComplete {
			status.AggregateState = StatePartial
		}
	} else {
		status.Overlay.Present = true
		status.Overlay.ArtifactFingerprint = fingerprint
		status.Overlay.InputFingerprint = artifact.InputFingerprint
		status.Overlay.ResolverVersions = artifact.ResolverVersions
		if len(loaded) == len(manifest.Repositories) {
			input, inputErr := InputFingerprint(manifest, loaded)
			if inputErr != nil {
				status.Overlay.Diagnostics = append(status.Overlay.Diagnostics, inputErr.Error())
			} else if input == artifact.InputFingerprint {
				expected, resolveErr := Resolve(manifest, loaded)
				if resolveErr != nil {
					status.Overlay.Diagnostics = append(status.Overlay.Diagnostics, resolveErr.Error())
				} else if expectedBytes, encodeErr := EncodeArtifact(expected); encodeErr != nil {
					status.Overlay.Diagnostics = append(status.Overlay.Diagnostics, encodeErr.Error())
				} else if exactArtifactFingerprint(expectedBytes) == fingerprint {
					status.Overlay.Fresh = true
				} else {
					status.Overlay.Diagnostics = append(status.Overlay.Diagnostics, "workspace artifact differs from the deterministic overlay for its current inputs")
				}
			} else {
				status.Overlay.Diagnostics = append(status.Overlay.Diagnostics, "workspace input fingerprint differs from current manifest and member artifacts")
			}
		}
		if !status.Overlay.Fresh && status.AggregateState == StateComplete {
			status.AggregateState = StatePartial
		}
	}
	if len(status.Members) == 0 || availableMembers == 0 {
		status.AggregateState = StateCannotEvaluate
	}
	return status
}

func exactArtifactFingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func repositoryRevision(ctx context.Context, root string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	git := func(arguments ...string) *exec.Cmd {
		// Status is advisory and must not execute a repository-configured
		// fsmonitor hook or update the member index as a side effect.
		command := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-C", root}, arguments...)...)
		command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
		return command
	}
	output, err := git("rev-parse", "HEAD").Output()
	if err != nil {
		return "", false
	}
	status, statusErr := git("status", "--porcelain", "--untracked-files=no").Output()
	return strings.TrimSpace(string(output)), statusErr == nil && len(strings.TrimSpace(string(status))) > 0
}
