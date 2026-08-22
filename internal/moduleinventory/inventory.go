// Package moduleinventory discovers and verifies the Go modules represented by
// a repository graph. The filesystem, rather than serialized graph metadata,
// is authoritative for module identity.
package moduleinventory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/sourcefs"
	"golang.org/x/mod/modfile"
)

// Discover reads go.mod files at the repository root and at every ancestor of
// a represented package. Package directories must themselves be confined,
// repository-relative paths.
func Discover(root string, packages []graph.PackageNode) ([]graph.ModuleNode, error) {
	reader, err := sourcefs.Open(root)
	if err != nil {
		return nil, fmt.Errorf("open repository for module discovery: %w", err)
	}
	defer func() { _ = reader.Close() }()

	dirs := map[string]bool{".": true}
	for _, pkg := range packages {
		dir := filepath.Clean(filepath.FromSlash(pkg.Dir))
		if pkg.Dir == "" || !filepath.IsLocal(dir) {
			return nil, fmt.Errorf("package %q has unsafe module-discovery directory %q", pkg.ID, pkg.Dir)
		}
		for {
			dirs[dir] = true
			if dir == "." {
				break
			}
			dir = filepath.Dir(dir)
		}
	}

	modules := make([]graph.ModuleNode, 0)
	seen := make(map[string]bool)
	for dir := range dirs {
		name := filepath.Join(dir, "go.mod")
		data, readErr := reader.ReadRegularFile(name)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read module file %q: %w", filepath.ToSlash(name), readErr)
		}
		modulePath := modfile.ModulePath(data)
		if modulePath == "" {
			return nil, fmt.Errorf("module file %q has no valid module directive", filepath.ToSlash(name))
		}
		key := modulePath + "\x00" + dir
		if seen[key] {
			continue
		}
		seen[key] = true
		modules = append(modules, graph.ModuleNode{ID: modulePath, Path: modulePath, Dir: filepath.ToSlash(dir)})
	}
	sort.Slice(modules, func(i, j int) bool {
		if modules[i].Path != modules[j].Path {
			return modules[i].Path < modules[j].Path
		}
		return modules[i].Dir < modules[j].Dir
	})
	return modules, nil
}

// Verify compares graph-declared modules with a fresh inventory rooted at the
// configured repository path. It rejects both forged entries and omissions.
func Verify(root string, packages []graph.PackageNode, declared []graph.ModuleNode) ([]graph.ModuleNode, error) {
	actual, err := Discover(root, packages)
	if err != nil {
		return nil, err
	}
	want := append([]graph.ModuleNode(nil), declared...)
	sort.Slice(want, func(i, j int) bool {
		if want[i].Path != want[j].Path {
			return want[i].Path < want[j].Path
		}
		return want[i].Dir < want[j].Dir
	})
	if len(actual) != len(want) {
		return nil, fmt.Errorf("graph declares %d modules but repository contains %d represented modules", len(want), len(actual))
	}
	for i := range actual {
		if actual[i].ID != want[i].ID || actual[i].Path != want[i].Path || actual[i].Dir != want[i].Dir {
			return nil, fmt.Errorf("graph module inventory does not match repository: declared %+v, actual %+v", want[i], actual[i])
		}
	}
	return actual, nil
}
