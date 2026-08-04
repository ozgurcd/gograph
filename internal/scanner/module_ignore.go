package scanner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/sourcefs"
	"golang.org/x/mod/modfile"
)

// moduleIgnoreTracker mirrors the directory-pruning semantics used by
// cmd/go when it expands ./... while still allowing nested modules to be
// scanned so precise coverage can report that boundary explicitly.
type moduleIgnoreTracker struct {
	enabled bool
	scopes  []moduleIgnoreScope
}

type moduleIgnoreScope struct {
	root          string
	canonicalRoot string
	patterns      ignorePatterns
	state         string
	module        string
}

type ignorePatterns struct {
	relative []string
	anywhere []string
}

func newModuleIgnoreTracker(enabled bool, moduleRoot, scanRoot string) (*moduleIgnoreTracker, error) {
	tracker := &moduleIgnoreTracker{enabled: enabled}
	if !enabled || moduleRoot == "" {
		return tracker, nil
	}
	moduleRoot = alignModuleRoot(moduleRoot, scanRoot)
	if err := tracker.addScope(moduleRoot); err != nil {
		return tracker, err
	}
	return tracker, nil
}

// alignModuleRoot preserves the scan root's lexical path while matching the
// canonical path returned by cmd/go. This matters on systems such as macOS,
// where /var and /private/var name the same directory.
func alignModuleRoot(moduleRoot, scanRoot string) string {
	moduleInfo, err := os.Stat(moduleRoot)
	if err != nil {
		return moduleRoot
	}
	absScanRoot, err := filepath.Abs(scanRoot)
	if err != nil {
		return moduleRoot
	}
	for candidate := filepath.Clean(absScanRoot); ; candidate = filepath.Dir(candidate) {
		if candidateInfo, statErr := os.Stat(candidate); statErr == nil && os.SameFile(moduleInfo, candidateInfo) {
			return candidate
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return moduleRoot
		}
	}
}

func (t *moduleIgnoreTracker) enterDir(dir string) (bool, error) {
	if !t.enabled {
		return false, nil
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false, fmt.Errorf("resolve module directory %s: %w", dir, err)
	}
	absDir = filepath.Clean(absDir)

	if scope, rel := t.nearestScope(absDir); scope != nil {
		if scope.patterns.shouldIgnore(rel) {
			return true, nil
		}
	}
	if t.hasScope(absDir) {
		return false, nil
	}

	modPath := filepath.Join(absDir, "go.mod")
	info, err := os.Lstat(modPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", modPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("unsafe module file %s: mode %s is not a regular file", modPath, info.Mode())
	}
	return false, t.addScope(absDir)
}

func (t *moduleIgnoreTracker) addScope(root string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve module directory %s: %w", root, err)
	}
	absRoot = filepath.Clean(absRoot)
	canonicalRoot := absRoot
	if resolved, resolveErr := filepath.EvalSymlinks(absRoot); resolveErr == nil {
		canonicalRoot = filepath.Clean(resolved)
	}
	if t.hasScope(absRoot) {
		return nil
	}
	modPath := filepath.Join(absRoot, "go.mod")
	moduleFiles, err := sourcefs.Open(absRoot)
	if err != nil {
		t.scopes = append(t.scopes, moduleIgnoreScope{root: absRoot, canonicalRoot: canonicalRoot, state: "unreadable"})
		return fmt.Errorf("open module root %s: %w", absRoot, err)
	}
	data, err := moduleFiles.ReadRegularFile("go.mod")
	_ = moduleFiles.Close()
	if err != nil {
		t.scopes = append(t.scopes, moduleIgnoreScope{root: absRoot, canonicalRoot: canonicalRoot, state: "unreadable"})
		return fmt.Errorf("read %s: %w", modPath, err)
	}

	parsed, parseErr := modfile.Parse(modPath, data, nil)
	patterns := ignorePatterns{}
	modulePath := ""
	if parseErr == nil {
		if parsed.Module != nil {
			modulePath = parsed.Module.Mod.Path
		}
		values := make([]string, 0, len(parsed.Ignore))
		for _, directive := range parsed.Ignore {
			values = append(values, directive.Path)
		}
		patterns = newIgnorePatterns(values)
	}
	// A nested module starts a new scope even when its go.mod is malformed.
	// Failing open preserves AST analysis without incorrectly inheriting a
	// parent module's ignore directives below the module boundary.
	state := "valid"
	if parseErr != nil {
		state = "malformed"
	}
	t.scopes = append(t.scopes, moduleIgnoreScope{root: absRoot, canonicalRoot: canonicalRoot, patterns: patterns, state: state, module: modulePath})
	if parseErr != nil {
		return fmt.Errorf("parse %s for ignore directives: %w", modPath, parseErr)
	}
	return nil
}

func (t *moduleIgnoreTracker) selectionFingerprint(buildContextFingerprint string) string {
	type scopeFingerprint struct {
		Root     string   `json:"root"`
		State    string   `json:"state"`
		Module   string   `json:"module"`
		Relative []string `json:"relative"`
		Anywhere []string `json:"anywhere"`
	}
	scopes := make([]scopeFingerprint, 0, len(t.scopes))
	for _, scope := range t.scopes {
		relative := append([]string(nil), scope.patterns.relative...)
		anywhere := append([]string(nil), scope.patterns.anywhere...)
		sort.Strings(relative)
		sort.Strings(anywhere)
		scopes = append(scopes, scopeFingerprint{
			Root:     scope.root,
			State:    scope.state,
			Module:   scope.module,
			Relative: relative,
			Anywhere: anywhere,
		})
	}
	sort.Slice(scopes, func(i, j int) bool { return scopes[i].Root < scopes[j].Root })
	payload, err := json.Marshal(struct {
		Version      int                `json:"version"`
		BuildContext string             `json:"build_context"`
		Scopes       []scopeFingerprint `json:"module_scopes"`
	}{
		Version:      2,
		BuildContext: buildContextFingerprint,
		Scopes:       scopes,
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func (t *moduleIgnoreTracker) hasScope(root string) bool {
	for index := range t.scopes {
		if t.scopes[index].root == root {
			return true
		}
	}
	return false
}

func (t *moduleIgnoreTracker) nearestScope(dir string) (*moduleIgnoreScope, string) {
	canonicalDir := ""
	for index := len(t.scopes) - 1; index >= 0; index-- {
		scope := &t.scopes[index]
		if rel, ok := relativeWithin(scope.root, dir); ok {
			return scope, rel
		}
		if canonicalDir == "" {
			if resolved, err := filepath.EvalSymlinks(dir); err == nil {
				canonicalDir = filepath.Clean(resolved)
			}
		}
		if canonicalDir != "" {
			if rel, ok := relativeWithin(scope.canonicalRoot, canonicalDir); ok {
				return scope, rel
			}
		}
	}
	return nil, ""
}

func relativeWithin(root, path string) (string, bool) {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return relative, true
}

func newIgnorePatterns(values []string) ignorePatterns {
	patterns := ignorePatterns{}
	for _, value := range values {
		value, relative := strings.CutPrefix(value, "./")
		normalized := normalizeIgnorePath(value)
		if relative {
			patterns.relative = append(patterns.relative, normalized)
		} else {
			patterns.anywhere = append(patterns.anywhere, normalized)
		}
	}
	return patterns
}

func (p ignorePatterns) shouldIgnore(dir string) bool {
	if dir == "" || dir == "." {
		return false
	}
	normalized := normalizeIgnorePath(dir)
	for _, pattern := range p.relative {
		if strings.HasPrefix(normalized, pattern) {
			return true
		}
	}
	for _, pattern := range p.anywhere {
		if strings.Contains(normalized, pattern) {
			return true
		}
	}
	return false
}

func normalizeIgnorePath(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	return path
}
