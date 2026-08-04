// Package sourcefs provides confined access to repository source and metadata.
package sourcefs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnsafeSourcePath marks graph-derived paths that are not regular Go source
// files rooted directly beneath the repository. In particular, source paths
// may not contain symlink components.
var ErrUnsafeSourcePath = errors.New("unsafe repository source path")

// Reader keeps repository reads and writes anchored to one directory handle.
// OpenRoot follows an explicitly supplied symlink for the repository root, but
// Root operations cannot follow descendant links outside that directory.
type Reader struct {
	root *os.Root
}

// Open creates a confined repository filesystem handle.
func Open(repositoryRoot string) (*Reader, error) {
	root, err := os.OpenRoot(repositoryRoot)
	if err != nil {
		return nil, fmt.Errorf("open repository root: %w", err)
	}
	return &Reader{root: root}, nil
}

// Close releases the repository directory handle.
func (r *Reader) Close() error {
	if r == nil || r.root == nil {
		return nil
	}
	return r.root.Close()
}

// ReadFile reads a regular .go file without permitting absolute paths,
// traversal, descendant symlinks, special files, or a check/open identity
// swap. The rooted open is the final confinement boundary even if an entry is
// changed concurrently after the component checks.
func (r *Reader) ReadFile(name string) ([]byte, error) {
	if filepath.Ext(filepath.Clean(name)) != ".go" {
		return nil, fmt.Errorf("%w %q: source is not a .go file", ErrUnsafeSourcePath, name)
	}
	return r.ReadRegularFile(name)
}

// ReadRegularFile reads any regular repository file without permitting
// absolute paths, traversal, symlink components, special files, or a
// check/open identity swap. Callers should use ReadFile for Go source so the
// extension contract is enforced as well.
func (r *Reader) ReadRegularFile(name string) ([]byte, error) {
	if r == nil || r.root == nil {
		return nil, errors.New("repository filesystem is closed")
	}
	clean := filepath.Clean(name)
	if name == "" || clean == "." || !filepath.IsLocal(clean) {
		return nil, fmt.Errorf("%w %q", ErrUnsafeSourcePath, name)
	}

	components := strings.Split(clean, string(filepath.Separator))
	prefix := ""
	var expected os.FileInfo
	for index, component := range components {
		prefix = filepath.Join(prefix, component)
		info, err := r.root.Lstat(prefix)
		if err != nil {
			return nil, fmt.Errorf("inspect repository path %q: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w %q: symlink component %q", ErrUnsafeSourcePath, name, prefix)
		}
		if index < len(components)-1 {
			if !info.IsDir() {
				return nil, fmt.Errorf("%w %q: non-directory component %q", ErrUnsafeSourcePath, name, prefix)
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%w %q: repository entry is not a regular file", ErrUnsafeSourcePath, name)
		}
		expected = info
	}

	file, err := r.root.Open(clean)
	if err != nil {
		return nil, fmt.Errorf("open repository file %q: %w", name, err)
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened repository file %q: %w", name, err)
	}
	if !opened.Mode().IsRegular() || expected == nil || !os.SameFile(expected, opened) {
		return nil, fmt.Errorf("%w %q: repository entry changed during open", ErrUnsafeSourcePath, name)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read repository file %q: %w", name, err)
	}
	return data, nil
}

// ValidateRegularFile verifies that name can be opened as the same regular,
// non-linked file observed beneath the repository root without reading its
// contents. It is intended for metadata that another trusted local tool will
// open after gograph's preflight.
func (r *Reader) ValidateRegularFile(name string) error {
	if r == nil || r.root == nil {
		return errors.New("repository filesystem is closed")
	}
	clean, expected, err := r.inspectExisting(name, false)
	if err != nil {
		return err
	}
	file, err := r.root.Open(clean)
	if err != nil {
		return fmt.Errorf("open repository file %q: %w", name, err)
	}
	defer func() { _ = file.Close() }()
	return verifyOpenedRegular(name, file, expected)
}
