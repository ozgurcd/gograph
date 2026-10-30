// Package rootfind provides shared gograph project root discovery.
//
// Sessions, builds, queries, MCP servers and cleanup share one rule,
// ResolveProjectRoot: the nearest directory with .git or go.mod, otherwise the
// start directory itself. No operation searches ancestors for .gograph state.
package rootfind

import (
	"os"
	"path/filepath"
)

const gographDir = ".gograph"

// FindRepositoryRoot returns the project root of the working directory, or "."
// (the working directory itself, the explicit fallback) outside any project.
func FindRepositoryRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	if root, ok := ProjectRootFrom(dir); ok {
		return root
	}
	return "."
}

// ProjectRootFrom returns the nearest directory, walking up from start, that
// holds a .git entry (directory or worktree file) or a go.mod file. The
// boolean is false when no such directory exists.
//
// Links in the start directory are resolved before the walk, so a link to a
// subdirectory finds that subdirectory's real project. The result keeps the
// caller's lexical spelling when one of start's lexical ancestors is the same
// directory, and is the real path otherwise. Only the start is resolved: state
// below the root (.gograph and its descendants) is still refused when linked.
func ProjectRootFrom(start string) (string, bool) {
	lexical, ok := startDirectory(start)
	if !ok {
		return "", false
	}
	dir, err := filepath.EvalSymlinks(lexical)
	if err != nil {
		dir = lexical
	}
	for {
		if hasGitBoundary(dir) || hasRegularFile(dir, "go.mod") {
			return lexicalAlias(lexical, dir), true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// lexicalAlias returns the lexical ancestor of start that is the same
// directory as real, or real when no lexical ancestor is.
func lexicalAlias(start, real string) string {
	target, err := os.Stat(real)
	if err != nil {
		return real
	}
	for candidate := start; ; candidate = filepath.Dir(candidate) {
		if info, statErr := os.Stat(candidate); statErr == nil && os.SameFile(info, target) {
			return candidate
		}
		if filepath.Dir(candidate) == candidate {
			return real
		}
	}
}

// ResolveProjectRoot is the one root rule every operation uses: the
// ProjectRootFrom result, or the start directory itself when no .git or
// go.mod exists above it. The start directory is the nearest existing
// directory of start, which may name a file or a not-yet-expanded glob.
func ResolveProjectRoot(start string) string {
	if root, ok := ProjectRootFrom(start); ok {
		return root
	}
	if dir, ok := startDirectory(start); ok {
		return dir
	}
	return start
}

// startDirectory returns the absolute nearest existing directory of start.
func startDirectory(start string) (string, bool) {
	if start == "" {
		return "", false
	}
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		if info, statErr := os.Stat(dir); statErr == nil && info.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// FindRepositoryRootFrom returns ProjectRootFrom: the boolean is false when
// start is inside no .git or go.mod project.
func FindRepositoryRootFrom(start string) (string, bool) {
	return ProjectRootFrom(start)
}

func hasRegularFile(dir, name string) bool {
	info, err := os.Lstat(filepath.Join(dir, name))
	return err == nil && info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular()
}

func hasGitBoundary(dir string) bool {
	git, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil && git.Mode()&os.ModeSymlink == 0 && (git.IsDir() || git.Mode().IsRegular())
}

// FindRoot returns the session and state root for the working directory:
// ResolveProjectRoot, or "." when the working directory cannot be read.
func FindRoot() string {
	return FindRepositoryRoot()
}

// FindRootFrom returns ResolveProjectRoot(start) and whether that root holds
// a real ".gograph" directory. start may name a directory, file, or
// not-yet-expanded glob.
func FindRootFrom(start string) (string, bool) {
	if start == "" {
		return "", false
	}
	root := ResolveProjectRoot(start)
	info, err := os.Lstat(filepath.Join(root, gographDir))
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", false
	}
	return root, true
}
