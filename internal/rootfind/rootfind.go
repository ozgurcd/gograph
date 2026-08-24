// Package rootfind provides shared gograph project root discovery.
//
// Repository discovery and legacy session storage intentionally have separate
// rules: a workspace overlay directory is not repository authority.
package rootfind

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

const gographDir = ".gograph"
const graphFile = "graph.json"

// FindRepositoryRoot discovers repository analysis inputs or a valid persisted
// graph. Unlike legacy FindRoot, it does not treat a workspace-only .gograph
// directory as repository authority.
func FindRepositoryRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	if root, ok := FindRepositoryRootFrom(dir); ok {
		return root
	}
	return "."
}

// FindRepositoryRootFrom walks upward to a real .gograph directory containing
// a supported graph.json artifact, with Go/Git analysis inputs as a fallback.
// A nested module does not hide an enclosing graph built for the same Git
// repository, while a nested Git boundary prevents capture by an outer graph.
func FindRepositoryRootFrom(start string) (string, bool) {
	if start == "" {
		return "", false
	}
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	if info, statErr := os.Stat(dir); statErr == nil && !info.IsDir() {
		dir = filepath.Dir(dir)
	}
	analysisRoot := ""
	for {
		if hasValidGraph(dir) {
			return dir, true
		}
		if analysisRoot == "" && hasGoAnalysisInput(dir) {
			analysisRoot = dir
		}
		if hasGitBoundary(dir) {
			if analysisRoot != "" {
				return analysisRoot, true
			}
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			if analysisRoot != "" {
				return analysisRoot, true
			}
			return "", false
		}
		dir = parent
	}
}

func hasGoAnalysisInput(dir string) bool {
	for _, name := range []string{"go.mod", "go.work"} {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err == nil && info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

func hasGitBoundary(dir string) bool {
	git, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil && git.Mode()&os.ModeSymlink == 0 && (git.IsDir() || git.Mode().IsRegular())
}

func hasValidGraph(dir string) bool {
	directory, dirErr := os.Lstat(filepath.Join(dir, gographDir))
	if dirErr != nil || directory.Mode()&os.ModeSymlink != 0 || !directory.IsDir() {
		return false
	}
	reader, err := sourcefs.Open(dir)
	if err != nil {
		return false
	}
	defer func() { _ = reader.Close() }()
	data, err := reader.ReadRegularFileLimit(filepath.Join(gographDir, graphFile), graph.MaxArtifactBytes)
	if err != nil {
		return false
	}
	var header struct {
		Version string `json:"version"`
	}
	return json.Unmarshal(data, &header) == nil && header.Version == graph.Version
}

// FindRoot walks up from the current working directory until it finds a
// directory that contains a ".gograph" subdirectory (i.e. the project root
// where `gograph build` was run). Falls back to "." when none is found so
// that fresh directories and test temp dirs work without a pre-existing index.
func FindRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	if root, ok := FindRootFrom(dir); ok {
		return root
	}
	return "."
}

// FindRootFrom walks up from start until it finds a directory that contains a
// real ".gograph" directory. start may name a directory, file, or not-yet-
// expanded glob; in each case its ancestors are considered. The boolean is
// false when no indexed ancestor exists.
func FindRootFrom(start string) (string, bool) {
	if start == "" {
		return "", false
	}
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		info, statErr := os.Lstat(filepath.Join(dir, gographDir))
		if statErr == nil && info.Mode()&os.ModeSymlink == 0 && info.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
