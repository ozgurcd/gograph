// Package rootfind provides shared gograph project root discovery.
//
// Sessions, builds, queries and cleanup share one rule, ProjectRootFrom: the
// nearest .git or go.mod. A workspace overlay directory is not authority.
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

// ProjectRootFrom is the one project-root rule for sessions, builds, queries
// and cleanup: the nearest directory, walking up from start, that holds a
// .git entry (directory or worktree file) or a go.mod file. An ancestor
// .gograph above that directory never captures the project. The boolean is
// false when no such directory exists.
func ProjectRootFrom(start string) (string, bool) {
	dir, ok := startDirectory(start)
	if !ok {
		return "", false
	}
	for {
		if hasGitBoundary(dir) || hasRegularFile(dir, "go.mod") {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func startDirectory(start string) (string, bool) {
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
	return dir, true
}

// FindRepositoryRootFrom returns ProjectRootFrom. Outside any project marker
// it walks upward to a real .gograph directory with a supported graph.json.
func FindRepositoryRootFrom(start string) (string, bool) {
	if root, ok := ProjectRootFrom(start); ok {
		return root, true
	}
	dir, ok := startDirectory(start)
	if !ok {
		return "", false
	}
	for {
		if hasValidGraph(dir) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func hasRegularFile(dir, name string) bool {
	info, err := os.Lstat(filepath.Join(dir, name))
	return err == nil && info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular()
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

// FindRoot returns the session and state root for the current working
// directory: ProjectRootFrom when a .git or go.mod exists above it. Outside
// any project it walks up to a ".gograph" directory, and falls back to "."
// so that fresh directories and test temp dirs work without an index.
func FindRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	if root, ok := ProjectRootFrom(dir); ok {
		return root
	}
	if root, ok := FindRootFrom(dir); ok {
		return root
	}
	return "."
}

// FindRootFrom walks up from start until it finds a directory that contains a
// real ".gograph" directory. start may name a directory, file, or not-yet-
// expanded glob; in each case its ancestors are considered. The walk stops at
// the project root (ProjectRootFrom): an ancestor .gograph above a .git or
// go.mod never counts. The boolean is false when no indexed root exists.
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
		if hasGitBoundary(dir) || hasRegularFile(dir, "go.mod") {
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
