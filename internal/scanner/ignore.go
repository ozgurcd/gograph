// Package scanner walks the target repository and identifies Go files to parse.
package scanner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"go/build"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/ozgurcd/gograph/internal/buildctx"
)

// UnsafeSourceFileError reports a Go build input or metadata entry that could
// make source resolution escape the selected tree. Unsafe entries are never
// opened or selected for indexing.
type UnsafeSourceFileError struct {
	Path string
	Mode os.FileMode
}

func (e *UnsafeSourceFileError) Error() string {
	return fmt.Sprintf("skip unsafe repository input %s: mode %s is not a regular file", e.Path, e.Mode)
}

// IsUnsafeSourceFileError reports whether err identifies an excluded unsafe
// repository source entry.
func IsUnsafeSourceFileError(err error) bool {
	var unsafe *UnsafeSourceFileError
	return errors.As(err, &unsafe)
}

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
	f, err := openRegularSource(path, nil)
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

// ValidateNoSourceLinks fails when any descendant entry is a symlink, or when
// a recognized Go build input or Go tool metadata entry is a special file.
// Rejecting every link avoids following even an otherwise-unrecognized entry
// merely to determine whether its target is a directory that cmd/go may inspect.
// It intentionally scans directories such as testdata and underscore-prefixed
// packages because go doc can address them explicitly. Only gograph and Git's
// own metadata directories are excluded. An explicitly symlinked repository
// root remains supported.
func ValidateNoSourceLinks(root string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve source validation root: %w", err)
	}
	walkRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return fmt.Errorf("resolve source validation root links: %w", err)
	}
	rootInfo, err := os.Stat(walkRoot)
	if err != nil {
		return fmt.Errorf("inspect source validation root: %w", err)
	}
	if !rootInfo.IsDir() {
		return fmt.Errorf("source validation root %s is not a directory", root)
	}

	return filepath.Walk(walkRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if path != walkRoot && (info.Name() == ".git" || info.Name() == ".gograph") {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			// Cmd/go never opens unrelated data-file links during package
			// discovery or type loading. Keep rejecting every linked recognized
			// build input and metadata entry, plus links that currently name a
			// directory or another special file.
			// Dangling and regular-file links with unrelated extensions are not
			// Go tool inputs and must not make precise analysis unusable.
			target, statErr := os.Stat(path)
			if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				return fmt.Errorf("inspect linked repository input %s: %w", path, statErr)
			}
			if isGoBuildInput(info.Name()) || isGoToolMetadata(path) || statErr == nil && !target.Mode().IsRegular() {
				return &UnsafeSourceFileError{Path: path, Mode: info.Mode()}
			}
			return nil
		}
		if (isGoBuildInput(info.Name()) || isGoToolMetadata(path)) && !info.Mode().IsRegular() {
			return &UnsafeSourceFileError{Path: path, Mode: info.Mode()}
		}
		return nil
	})
}

// ValidateToolchainSourceInputs validates metadata discovery and then rejects
// links or unsafe recognized inputs in the confined source trees: the selected
// root plus its effective module root, or the workspace root and all workspace
// members. Dependency and external local-replacement roots remain governed by
// the user's open-world Go environment.
func ValidateToolchainSourceInputs(root string) error {
	sourceRoots, err := buildctx.ToolchainSourceRoots(root)
	if err != nil {
		return fmt.Errorf("validate Go tool metadata: %w", err)
	}
	sourceRoots = append([]string{root}, sourceRoots...)
	seen := make(map[string]struct{}, len(sourceRoots))
	for _, sourceRoot := range sourceRoots {
		absolute, err := filepath.Abs(sourceRoot)
		if err != nil {
			return fmt.Errorf("resolve Go tool source root %s: %w", sourceRoot, err)
		}
		key := filepath.Clean(absolute)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := ValidateNoSourceLinks(sourceRoot); err != nil {
			return fmt.Errorf("validate Go tool source tree %s: %w", sourceRoot, err)
		}
	}
	return nil
}

// SourceValidationRoot selects the working-tree preflight root for Go tool
// commands. Workspace-auto mode uses the nearest enclosing workspace;
// otherwise the nearest enabled module is used. An explicit GOWORK selection
// is validated separately by ToolchainSourceRoots. A real .gograph directory
// is retained only as a fallback when no applicable ancestor metadata exists,
// so a nested artifact directory cannot narrow validation below the selected
// tree.
func SourceValidationRoot(start string) (string, error) {
	absStart, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve validation start: %w", err)
	}
	startInfo, err := os.Stat(absStart)
	if err != nil {
		return "", fmt.Errorf("inspect validation start: %w", err)
	}
	if !startInfo.IsDir() {
		absStart = filepath.Dir(absStart)
	}

	dir := filepath.Clean(absStart)
	fallback := ""
	moduleRoot := ""
	modulesEnabled := os.Getenv("GO111MODULE") != "off"
	goWork := os.Getenv("GOWORK")
	workspaceAuto := modulesEnabled && (goWork == "" || goWork == "auto")
	for {
		metadataNames := make([]string, 0, 2)
		if workspaceAuto {
			metadataNames = append(metadataNames, "go.work")
		}
		if modulesEnabled {
			metadataNames = append(metadataNames, "go.mod")
		}
		for _, name := range metadataNames {
			metadataFile := filepath.Join(dir, name)
			metadataInfo, metadataErr := os.Lstat(metadataFile)
			switch {
			case metadataErr == nil:
				if metadataInfo.Mode()&os.ModeSymlink != 0 || !metadataInfo.Mode().IsRegular() {
					return "", fmt.Errorf("unsafe Go metadata file %s: mode %s is not a regular file", metadataFile, metadataInfo.Mode())
				}
				// Cmd/go in workspace-auto mode searches past an enclosing
				// module for go.work. Validate the whole nearest workspace; only
				// fall back to the nearest module when no workspace is present.
				if name == "go.work" {
					return dir, nil
				}
				if moduleRoot == "" {
					moduleRoot = dir
				}
			case !os.IsNotExist(metadataErr):
				return "", fmt.Errorf("inspect Go metadata file %s: %w", metadataFile, metadataErr)
			}
		}

		if fallback == "" {
			artifactDir := filepath.Join(dir, ".gograph")
			artifactInfo, artifactErr := os.Lstat(artifactDir)
			switch {
			case artifactErr == nil && artifactInfo.Mode()&os.ModeSymlink == 0 && artifactInfo.IsDir():
				fallback = dir
			case artifactErr != nil && !os.IsNotExist(artifactErr):
				return "", fmt.Errorf("inspect artifact directory %s: %w", artifactDir, artifactErr)
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if moduleRoot != "" {
		return moduleRoot, nil
	}
	if fallback != "" {
		return fallback, nil
	}
	return filepath.Clean(absStart), nil
}

func isGoToolMetadata(path string) bool {
	name := filepath.Base(path)
	switch name {
	case "go.mod", "go.sum", "go.work", "go.work.sum":
		return true
	case "modules.txt":
		return filepath.Base(filepath.Dir(path)) == "vendor"
	default:
		return false
	}
}

// isGoBuildInput mirrors the source extensions recognized by go/build. ImportDir
// reads comment headers from every listed extension except .syso, so all of
// them require the same regular-file and identity checks as Go source.
func isGoBuildInput(name string) bool {
	switch filepath.Ext(name) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".m", ".h", ".hh", ".hpp", ".hxx",
		".f", ".F", ".for", ".f90", ".s", ".S", ".sx", ".swig", ".swigcxx", ".syso":
		return true
	default:
		return false
	}
}

// ValidateGoDocQuery accepts Go import-path and symbol notation while refusing
// flags and filesystem-shaped operands. The latter would let go doc read a
// caller-selected file or directory instead of resolving a package query.
func ValidateGoDocQuery(query string) error {
	if query == "" {
		return errors.New("go doc query is required")
	}
	if strings.TrimSpace(query) != query || strings.ContainsAny(query, " \t\r\n") {
		return fmt.Errorf("unsafe go doc query %q: whitespace is not allowed", query)
	}
	if strings.HasPrefix(query, ".") || strings.HasPrefix(query, "-") || strings.HasPrefix(query, "~") ||
		filepath.IsAbs(query) || filepath.VolumeName(query) != "" || strings.ContainsAny(query, "\\:\x00") {
		return fmt.Errorf("unsafe go doc query %q: filesystem paths and flags are not allowed", query)
	}
	for segment := range strings.SplitSeq(query, "/") {
		if segment == "" || strings.HasPrefix(segment, ".") {
			return fmt.Errorf("unsafe go doc query %q: filesystem paths are not allowed", query)
		}
	}
	return nil
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
// Generated files are excluded. A non-fatal error slice reports source entries
// that could not be inspected or were excluded as unsafe, plus build-context
// and source-selection warnings.
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
	buildContext := confinedBuildContext(config.BuildContext())
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
		if isGoBuildInput(info.Name()) && !info.Mode().IsRegular() {
			errs = append(errs, &UnsafeSourceFileError{Path: path, Mode: info.Mode()})
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

// confinedBuildContext prevents go/build.ImportDir from independently opening
// a linked or special build input that the filepath walk excluded. ReadDir
// hides non-regular recognized inputs, while OpenFile revalidates identity at
// the point of opening.
func confinedBuildContext(buildContext build.Context) build.Context {
	readDir := buildContext.ReadDir
	buildContext.ReadDir = func(dir string) ([]fs.FileInfo, error) {
		var (
			infos []fs.FileInfo
			err   error
		)
		if readDir != nil {
			infos, err = readDir(dir)
		} else {
			infos, err = readDirectoryInfos(dir)
		}
		if err != nil {
			return nil, err
		}
		filtered := make([]fs.FileInfo, 0, len(infos))
		for _, info := range infos {
			if !isGoBuildInput(info.Name()) {
				filtered = append(filtered, info)
				continue
			}
			entry, lstatErr := os.Lstat(filepath.Join(dir, info.Name()))
			if lstatErr != nil {
				return nil, lstatErr
			}
			if entry.Mode().IsRegular() {
				filtered = append(filtered, info)
			}
		}
		return filtered, nil
	}

	openFile := buildContext.OpenFile
	buildContext.OpenFile = func(path string) (io.ReadCloser, error) {
		if isGoBuildInput(filepath.Base(path)) {
			return openRegularSource(path, openFile)
		}
		if openFile != nil {
			return openFile(path)
		}
		return os.Open(path)
	}
	return buildContext
}

func readDirectoryInfos(dir string) ([]fs.FileInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	infos := make([]fs.FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, infoErr
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func openRegularSource(path string, openFile func(string) (io.ReadCloser, error)) (io.ReadCloser, error) {
	expected, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !expected.Mode().IsRegular() {
		return nil, &UnsafeSourceFileError{Path: path, Mode: expected.Mode()}
	}
	var opened io.ReadCloser
	if openFile != nil {
		opened, err = openFile(path)
	} else {
		opened, err = os.Open(path)
	}
	if err != nil {
		return nil, err
	}
	if statter, ok := opened.(interface{ Stat() (os.FileInfo, error) }); ok {
		actual, statErr := statter.Stat()
		if statErr != nil {
			_ = opened.Close()
			return nil, statErr
		}
		if !actual.Mode().IsRegular() || !os.SameFile(expected, actual) {
			_ = opened.Close()
			return nil, &UnsafeSourceFileError{Path: path, Mode: actual.Mode()}
		}
	}
	return opened, nil
}
