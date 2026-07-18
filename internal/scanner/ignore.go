// Package scanner walks the target repository and identifies Go files to parse.
package scanner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/ozgurcd/gograph/internal/buildctx"
)

// ignoredDirs are directory names skipped below the explicit scan root,
// regardless of .gitignore. This is a fast O(1) check that requires no I/O.
var ignoredDirs = map[string]bool{
	".git":         true,
	".gograph":     true,
	"vendor":       true,
	"node_modules": true,
	"dist":         true,
	"build":        true,
	".terraform":   true,
	// AI agent work directories that park scratch copies of the project.
	// Picking these up would duplicate every symbol and call edge.
	".claude": true, // Claude Code worktrees (e.g. .claude/worktrees/agent-*/...)
	".cursor": true, // Cursor AI agent scratch directories
	".agents": true, // Generic agent framework scratch/worktree directories
	// testdata is a Go-tool convention (cmd/go ignores it for builds and
	// vet). Per the spec, directories named "testdata" hold ancillary
	// fixture files for tests; their Go files are loaded explicitly by
	// test code, not built as part of the project. Including them in
	// gograph's graph pollutes every report — most visibly, fixture
	// routes appear in `gograph routes` and fixture symbols appear in
	// orphans/callees as cross-codebase noise.
	"testdata": true,
}

// ShouldIgnoreDir reports whether the directory with the given base name should
// be skipped entirely.
func ShouldIgnoreDir(base string) bool {
	return ignoredDirs[base]
}

// shouldIgnoreGoWildcardDir reports directories cmd/go excludes while
// expanding ./.... The caller must exempt the explicit scan root so a project
// whose own basename starts with '.' or '_' remains analyzable.
func shouldIgnoreGoWildcardDir(base string) bool {
	return (strings.HasPrefix(base, ".") && base != "." && base != "..") ||
		strings.HasPrefix(base, "_") || base == "testdata"
}

// ShouldIgnoreFile reports whether a file should be skipped before parsing.
// It checks name suffixes and, for .go files, inspects the first few lines for
// a generated-file marker.
func ShouldIgnoreFile(path string) (bool, error) {
	base := filepath.Base(path)

	// Suffix checks (cheap, no I/O).
	if strings.HasSuffix(base, ".pb.go") {
		return true, nil
	}
	if strings.HasSuffix(base, "_generated.go") {
		return true, nil
	}

	// Content check: look for "Code generated" in the first 10 lines.
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() && lineNum < 10 {
		if strings.Contains(scanner.Text(), "Code generated") {
			return true, nil
		}
		lineNum++
	}
	return false, scanner.Err()
}

// gitIgnoreChecker consults `git check-ignore` to determine whether a path is
// excluded by any .gitignore rule in the repository. It is initialised lazily
// and is no-op when git is unavailable or the directory is not inside a git
// repository.
type gitIgnoreChecker struct {
	once   sync.Once
	root   string // absolute repository root (output of git rev-parse)
	hasGit bool   // false when git is unavailable or not a git repo
}

// newGitIgnoreChecker returns a checker rooted at the given directory.
func newGitIgnoreChecker(root string) *gitIgnoreChecker {
	return &gitIgnoreChecker{root: root}
}

func (g *gitIgnoreChecker) init() {
	g.once.Do(func() {
		// Verify git is available and the directory is inside a repository.
		cmd := exec.Command("git", "-C", g.root, "rev-parse", "--show-toplevel")
		out, err := cmd.Output()
		if err != nil {
			g.hasGit = false
			return
		}
		g.root = strings.TrimSpace(string(out))
		g.hasGit = true
	})
}

// isIgnored returns true when git considers the absolute path to be gitignored.
// It returns false (not ignored) if git is unavailable, the path is not in a
// git repo, or the git invocation fails for any reason — failing open is safe
// here because the existing ignoredDirs guard already handles the most common
// noise directories.
func (g *gitIgnoreChecker) isIgnored(absPath string) bool {
	g.init()
	if !g.hasGit {
		return false
	}
	// `git check-ignore --quiet` exits 0 if the path is ignored, 1 if not.
	cmd := exec.Command("git", "-C", g.root, "check-ignore", "--quiet", absPath)
	return cmd.Run() == nil
}

// Walk traverses root and returns paths of .go files that should be parsed.
// It respects:
//  1. The effective Go build and module context.
//  2. cmd/go's package-directory and module-ignore rules.
//  3. Generated-file and hardcoded directory exclusions.
//  4. The repository's .gitignore rules via `git check-ignore` — this
//     eliminates duplicates caused by AI agent worktrees (e.g. .claude/worktrees/
//     or any other tool-managed copy of the project) that are listed in .gitignore
//     but live inside the project directory.
//
// Generated files are excluded. An error slice is also returned for files that
// could not be inspected (non-fatal).
func Walk(root string) (paths []string, errs []error) {
	paths, _, errs = WalkWithFingerprint(root)
	return paths, errs
}

// WalkWithFingerprint returns the active source files and the fingerprint of
// the effective build context used to select them.
func WalkWithFingerprint(root string) (paths []string, fingerprint string, errs []error) {
	config, configErr := buildctx.ResolveOrDefault(context.Background(), root)
	paths, fingerprint, errs = WalkWithConfigAndFingerprint(root, config)
	if configErr != nil {
		errs = append([]error{fmt.Errorf("using default Go build context: %w", configErr)}, errs...)
	}
	return paths, fingerprint, errs
}

// WalkWithContext traverses root and applies the supplied Go build context to
// every candidate directory. ImportDir is intentionally used instead of
// MatchFile: in addition to filename and header constraints, it classifies an
// otherwise-unconstrained import "C" file as inactive when cgo is disabled.
func WalkWithContext(root string, buildContext build.Context) (paths []string, errs []error) {
	return WalkWithConfig(root, buildctx.FromBuildContext(buildContext, nil))
}

// WalkWithConfig traverses root using one explicit source of file-selection
// truth, including both the Go build context and module-mode state.
func WalkWithConfig(root string, config buildctx.Config) (paths []string, errs []error) {
	paths, _, errs = WalkWithConfigAndFingerprint(root, config)
	return paths, errs
}

// WalkWithConfigAndFingerprint also returns a fingerprint of every effective
// source-selection input observed during the walk, including nested module
// boundaries and ignore directives.
func WalkWithConfigAndFingerprint(root string, config buildctx.Config) (paths []string, fingerprint string, errs []error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}
	buildContext := config.BuildContext()
	gitIgnore := newGitIgnoreChecker(absRoot)
	moduleIgnores, moduleErr := newModuleIgnoreTracker(config.ModulesEnabled(), config.ModuleRoot(), absRoot)
	if moduleErr != nil {
		errs = append(errs, moduleErr)
	}
	candidatesByDir := make(map[string][]string)

	walkRoot := root
	if rootInfo, lstatErr := os.Lstat(root); lstatErr == nil && rootInfo.Mode()&os.ModeSymlink != 0 {
		if targetInfo, statErr := os.Stat(root); statErr == nil && targetInfo.IsDir() {
			walkRoot = filepath.Clean(root) + string(filepath.Separator)
		}
	}
	err = filepath.Walk(walkRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			errs = append(errs, err)
			return nil // keep walking
		}
		if info.IsDir() {
			absPath, aerr := filepath.Abs(path)
			isRoot := aerr == nil && filepath.Clean(absPath) == filepath.Clean(absRoot)
			// Fast blocklist check (no subprocess).
			if !isRoot && (ShouldIgnoreDir(info.Name()) || shouldIgnoreGoWildcardDir(info.Name())) {
				return filepath.SkipDir
			}
			// Gitignore check for directories: if the directory itself is
			// gitignored skip the whole subtree with one syscall instead of
			// checking every file inside it individually. This is what catches
			// `.claude/worktrees/agent-*/` and similar AI agent scratch trees.
			if aerr == nil && gitIgnore.isIgnored(absPath) {
				return filepath.SkipDir
			}
			skipModuleDir, moduleErr := moduleIgnores.enterDir(path)
			if moduleErr != nil {
				errs = append(errs, moduleErr)
			}
			if skipModuleDir {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		absPath, aerr := filepath.Abs(path)
		if aerr == nil && gitIgnore.isIgnored(absPath) {
			return nil
		}
		skip, serr := ShouldIgnoreFile(path)
		if serr != nil {
			errs = append(errs, serr)
			return nil
		}
		if !skip {
			dir := filepath.Dir(path)
			candidatesByDir[dir] = append(candidatesByDir[dir], path)
		}
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}

	dirs := make([]string, 0, len(candidatesByDir))
	for dir := range candidatesByDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		candidates := candidatesByDir[dir]
		pkg, importErr := buildContext.ImportDir(dir, 0)
		if pkg == nil {
			// Without partial package metadata there is no trustworthy way to
			// classify the directory. Preserve AST mode's historical fail-open
			// behavior, but surface the selection failure as a warning.
			paths = append(paths, candidates...)
			if importErr != nil {
				errs = append(errs, fmt.Errorf("evaluate Go build constraints in %s: %w", dir, importErr))
			}
			continue
		}

		active := make(map[string]struct{}, len(pkg.GoFiles)+len(pkg.CgoFiles)+len(pkg.TestGoFiles)+len(pkg.XTestGoFiles)+len(pkg.InvalidGoFiles))
		for _, names := range [][]string{pkg.GoFiles, pkg.CgoFiles, pkg.TestGoFiles, pkg.XTestGoFiles, pkg.InvalidGoFiles} {
			for _, name := range names {
				active[name] = struct{}{}
			}
		}
		for _, path := range candidates {
			if _, ok := active[filepath.Base(path)]; ok {
				paths = append(paths, path)
			}
		}

		var noGoErr *build.NoGoError
		if importErr != nil && !errors.As(importErr, &noGoErr) {
			errs = append(errs, fmt.Errorf("evaluate Go build constraints in %s: %w", dir, importErr))
		}
	}
	return paths, moduleIgnores.selectionFingerprint(config.Fingerprint()), errs
}
