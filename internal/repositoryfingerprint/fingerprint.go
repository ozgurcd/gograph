// Package repositoryfingerprint produces the location-independent source and
// build-selection identity persisted with a graph and recomputed by validators.
package repositoryfingerprint

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/ozgurcd/gograph/internal/buildctx"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

type Result struct {
	Fingerprint string
	Files       map[string]string
}

type entry struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

type selection struct {
	GOOS           string   `json:"goos"`
	GOARCH         string   `json:"goarch"`
	Compiler       string   `json:"compiler"`
	CgoEnabled     bool     `json:"cgo_enabled"`
	BuildTags      []string `json:"build_tags"`
	ToolTags       []string `json:"tool_tags"`
	ReleaseTags    []string `json:"release_tags"`
	InstallSuffix  string   `json:"install_suffix,omitempty"`
	ModulesEnabled bool     `json:"modules_enabled"`
	ModulePath     string   `json:"module_path,omitempty"`
}

type manifest struct {
	SchemaVersion       string    `json:"schema_version"`
	SourcePolicyVersion int       `json:"source_policy_version"`
	Build               selection `json:"build"`
	Metadata            []entry   `json:"metadata"`
	Files               []entry   `json:"files"`
}

func Compute(ctx context.Context, root string, config buildctx.Config, selectedPaths []string) (Result, error) {
	reader, err := sourcefs.Open(root)
	if err != nil {
		return Result{}, fmt.Errorf("open repository for fingerprinting: %w", err)
	}
	defer func() { _ = reader.Close() }()

	files := make(map[string]string, len(selectedPaths))
	entries := make([]entry, 0, len(selectedPaths))
	for _, selectedPath := range selectedPaths {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		relative, err := filepath.Rel(root, selectedPath)
		if err != nil || relative == ".." || filepath.IsAbs(relative) {
			return Result{}, fmt.Errorf("selected source %q is outside repository root", selectedPath)
		}
		relative = filepath.Clean(relative)
		data, err := reader.ReadRegularFile(relative)
		if err != nil {
			return Result{}, fmt.Errorf("read selected source %s: %w", relative, err)
		}
		digest := digest(data)
		normalized := filepath.ToSlash(relative)
		files[normalized] = digest
		entries = append(entries, entry{Path: normalized, Digest: digest})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	metadata, err := metadataEntries(reader, root, selectedPaths)
	if err != nil {
		return Result{}, err
	}
	buildContext := config.BuildContext()
	buildTags := sortedCopy(buildContext.BuildTags)
	toolTags := sortedCopy(buildContext.ToolTags)
	releaseTags := sortedCopy(buildContext.ReleaseTags)
	document := manifest{
		SchemaVersion:       "gograph.source-manifest.v1",
		SourcePolicyVersion: graph.CurrentSourcePolicyVersion,
		Build: selection{
			GOOS: buildContext.GOOS, GOARCH: buildContext.GOARCH, Compiler: buildContext.Compiler,
			CgoEnabled: buildContext.CgoEnabled, BuildTags: buildTags, ToolTags: toolTags,
			ReleaseTags: releaseTags, InstallSuffix: buildContext.InstallSuffix,
			ModulesEnabled: config.ModulesEnabled(), ModulePath: config.ModulePath(),
		},
		Metadata: metadata,
		Files:    entries,
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return Result{}, fmt.Errorf("encode source fingerprint manifest: %w", err)
	}
	sum := sha256.Sum256(append(canonical, '\n'))
	return Result{Fingerprint: hex.EncodeToString(sum[:]), Files: files}, nil
}

func metadataEntries(reader *sourcefs.Reader, root string, selectedPaths []string) ([]entry, error) {
	pathSet := map[string]struct{}{
		"go.mod": {}, "go.sum": {}, "go.work": {}, "go.work.sum": {},
		"vendor/modules.txt": {}, ".gitignore": {},
	}
	for _, selectedPath := range selectedPaths {
		directory := filepath.Dir(selectedPath)
		for {
			relative, err := filepath.Rel(root, directory)
			if err != nil || relative == ".." || filepath.IsAbs(relative) {
				break
			}
			for _, base := range []string{"go.mod", "go.sum"} {
				pathSet[filepath.ToSlash(filepath.Join(relative, base))] = struct{}{}
			}
			if filepath.Clean(directory) == filepath.Clean(root) {
				break
			}
			directory = filepath.Dir(directory)
		}
	}
	paths := make([]string, 0, len(pathSet))
	for name := range pathSet {
		if name == "./go.mod" {
			name = "go.mod"
		}
		if name == "./go.sum" {
			name = "go.sum"
		}
		paths = append(paths, name)
	}
	sort.Strings(paths)
	entries := make([]entry, 0, len(paths))
	for _, name := range paths {
		data, err := reader.ReadRegularFile(filepath.FromSlash(name))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read repository selection metadata %s: %w", name, err)
		}
		entries = append(entries, entry{Path: name, Digest: digest(data)})
	}
	return entries, nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sortedCopy(values []string) []string {
	copyOfValues := append([]string(nil), values...)
	sort.Strings(copyOfValues)
	return copyOfValues
}
