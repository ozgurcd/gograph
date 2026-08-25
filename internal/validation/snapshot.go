package validation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ozgurcd/gograph/internal/buildctx"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/repositoryfingerprint"
	"github.com/ozgurcd/gograph/internal/scanner"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

const graphPath = ".gograph/graph.json"

type Snapshot struct {
	Root                 string
	Graph                *graph.Graph
	GraphFingerprint     string
	SourceFingerprint    string
	SelectionFingerprint string
	Freshness            string
}

type SnapshotLoader interface {
	Load(context.Context, string) (Snapshot, error)
	VerifyCurrent(context.Context, Snapshot) error
}

type SnapshotError struct {
	Reason     Reason
	Diagnostic Diagnostic
}

func (e *SnapshotError) Error() string { return e.Diagnostic.Message }

type RepositoryLoader struct {
	BuildTags []string
	// AllowCheckoutSourceAuthority permits go.work members outside a nested
	// module root when ToolchainSourceRoots has confined them beneath the
	// nearest real Git checkout. Machine validation leaves this false so its
	// explicit --repo path remains the complete toolchain authority.
	AllowCheckoutSourceAuthority bool
}

func (loader RepositoryLoader) Load(ctx context.Context, repositoryRoot string) (Snapshot, error) {
	root, err := canonicalRoot(repositoryRoot)
	if err != nil {
		return Snapshot{}, snapshotError(ReasonInvalidRequest, "invalid_repository", err.Error(), "")
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{Root: root}, snapshotError(ReasonInternalError, "context_done", err.Error(), "")
	}

	reader, err := sourcefs.Open(root)
	if err != nil {
		return Snapshot{Root: root}, snapshotError(ReasonInternalError, "repository_open_failed", err.Error(), "")
	}
	defer func() { _ = reader.Close() }()
	graphBytes, err := reader.ReadRegularFileLimit(graphPath, graph.MaxArtifactBytes)
	if err != nil {
		reason := ReasonGraphInvalid
		if errors.Is(err, fs.ErrNotExist) {
			reason = ReasonGraphMissing
		}
		return Snapshot{Root: root}, snapshotError(reason, string(reason), err.Error(), graphPath)
	}
	var persisted graph.Graph
	if err := jsonv2.Unmarshal(graphBytes, &persisted); err != nil {
		return Snapshot{Root: root}, snapshotError(ReasonGraphInvalid, "graph_json_invalid", err.Error(), graphPath)
	}
	if persisted.Version != graph.Version {
		return Snapshot{Root: root}, snapshotError(ReasonGraphSchemaUnsupported, "graph_schema_unsupported", "persisted graph schema is unsupported", graphPath)
	}
	if persisted.Build == nil {
		return Snapshot{Root: root}, snapshotError(ReasonGraphInvalid, "graph_metadata_missing", "persisted graph has no build metadata", graphPath)
	}
	if persisted.GeneratedAt.IsZero() {
		return Snapshot{Root: root}, snapshotError(ReasonGraphInvalid, "graph_timestamp_invalid", "persisted graph generated_at timestamp is missing", graphPath)
	}
	if !persisted.UsesCurrentSourcePolicy() {
		return Snapshot{Root: root}, snapshotError(ReasonSourcePolicyUnsupported, "source_policy_unsupported", "persisted graph source policy is unsupported", graphPath)
	}
	persisted.Root = root
	graphSum := sha256.Sum256(graphBytes)
	snapshot := Snapshot{
		Root:             root,
		Graph:            &persisted,
		GraphFingerprint: hex.EncodeToString(graphSum[:]),
		Freshness:        "unknown",
	}

	current, err := captureSourceState(ctx, root, loader.BuildTags, loader.AllowCheckoutSourceAuthority)
	if err != nil {
		return snapshot, err
	}
	snapshot.SourceFingerprint = current.SourceFingerprint
	snapshot.SelectionFingerprint = current.SelectionFingerprint
	if persisted.Build.BuildContextFingerprint != current.SelectionFingerprint {
		snapshot.Freshness = "stale"
		return snapshot, snapshotError(ReasonGraphStale, "build_context_changed", "persisted graph build selection differs from the current repository selection", "")
	}
	if persisted.Build.SourceFingerprint == "" || persisted.Build.SourceFingerprint != current.SourceFingerprint {
		snapshot.Freshness = "stale"
		return snapshot, snapshotError(ReasonGraphStale, "source_fingerprint_changed", "persisted graph source fingerprint differs from the current selected source and build metadata", "")
	}
	if err := compareGraphFiles(root, &persisted, current.Files); err != nil {
		snapshot.Freshness = "stale"
		return snapshot, snapshotError(ReasonGraphStale, "source_changed", err.Error(), "")
	}
	if persisted.Build.Complete {
		snapshot.Freshness = "current"
	}
	return snapshot, nil
}

func (loader RepositoryLoader) VerifyCurrent(ctx context.Context, snapshot Snapshot) error {
	current, err := captureSourceState(ctx, snapshot.Root, loader.BuildTags, loader.AllowCheckoutSourceAuthority)
	if err != nil {
		return err
	}
	if current.SourceFingerprint != snapshot.SourceFingerprint || current.SelectionFingerprint != snapshot.SelectionFingerprint {
		return snapshotError(ReasonGraphStale, "repository_changed_during_validation", "repository source or build selection changed during validation", "")
	}
	return nil
}

type sourceState struct {
	SourceFingerprint    string
	SelectionFingerprint string
	Files                map[string]string
}

func captureSourceState(ctx context.Context, root string, buildTags []string, allowCheckoutSourceAuthority bool) (sourceState, error) {
	if err := ctx.Err(); err != nil {
		return sourceState{}, snapshotError(ReasonInternalError, "context_done", err.Error(), "")
	}
	config, err := buildctx.ResolveWithOptions(ctx, root, buildctx.ResolveOptions{BuildTags: buildTags})
	if err != nil {
		return sourceState{}, snapshotError(ReasonAnalysisIncomplete, "build_context_unavailable", err.Error(), "")
	}
	toolchainRoots, err := buildctx.ToolchainSourceRoots(root)
	if err != nil {
		return sourceState{}, snapshotError(ReasonAnalysisIncomplete, "toolchain_roots_unavailable", err.Error(), "")
	}
	if !allowCheckoutSourceAuthority {
		for _, toolchainRoot := range toolchainRoots {
			if err := requireCanonicalConfinement(root, toolchainRoot); err != nil {
				return sourceState{}, snapshotError(ReasonRepositoryMismatch, "build_context_escape", "validation build context includes a module or workspace tree outside the explicit repository root", "")
			}
		}
	}
	paths, selectionFingerprint, selectionErrors := scanner.WalkWithConfigAndFingerprint(root, config)
	if len(selectionErrors) > 0 {
		return sourceState{}, snapshotError(ReasonAnalysisIncomplete, "source_selection_incomplete", selectionErrors[0].Error(), "")
	}

	identity, err := repositoryfingerprint.Compute(ctx, root, config, paths)
	if err != nil {
		return sourceState{}, snapshotError(ReasonAnalysisIncomplete, "fingerprint_failed", err.Error(), "")
	}
	return sourceState{
		SourceFingerprint:    identity.Fingerprint,
		SelectionFingerprint: selectionFingerprint,
		Files:                identity.Files,
	}, nil
}

func compareGraphFiles(root string, persisted *graph.Graph, current map[string]string) error {
	if persisted.Build == nil || !persisted.Build.Complete {
		return nil
	}
	if len(persisted.Files) != len(current) {
		return fmt.Errorf("selected source inventory changed: graph has %d files, repository has %d", len(persisted.Files), len(current))
	}
	for _, file := range persisted.Files {
		relative, err := normalizePersistedPath(root, file.Path)
		if err != nil {
			return err
		}
		actual, exists := current[relative]
		if !exists || file.ContentDigest == "" || actual != file.ContentDigest {
			return fmt.Errorf("selected source %s differs from the persisted graph", relative)
		}
	}
	return nil
}

func canonicalRoot(root string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("repository root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("repository root is not a directory")
	}
	return filepath.Clean(abs), nil
}

func confinedRelativePath(root, name string) (string, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, abs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes repository root", name)
	}
	return filepath.ToSlash(filepath.Clean(relative)), nil
}

func requireCanonicalConfinement(root, name string) error {
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	canonicalName, err := filepath.EvalSymlinks(name)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(canonicalRoot, canonicalName)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %q escapes repository root", name)
	}
	return nil
}

func normalizePersistedPath(root, name string) (string, error) {
	if filepath.IsAbs(name) {
		return confinedRelativePath(root, name)
	}
	clean := filepath.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("persisted path %q escapes repository root", name)
	}
	return filepath.ToSlash(clean), nil
}

func snapshotError(reason Reason, code, message, path string) error {
	return &SnapshotError{Reason: reason, Diagnostic: Diagnostic{Code: code, Message: boundedMessage(message), Path: path}}
}
