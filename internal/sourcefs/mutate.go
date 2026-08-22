package sourcefs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AtomicReplaceRegularFile durably stages data beside name and atomically
// renames it over an absent or regular, non-linked destination. Every path
// operation remains anchored to the opened root.
func (r *Reader) AtomicReplaceRegularFile(name string, data []byte, perm os.FileMode) error {
	clean, expected, existed, err := r.prepareRegularWrite(name)
	if err != nil {
		return err
	}
	parent := filepath.Dir(clean)
	var temporary string
	for range 16 {
		var token [12]byte
		if _, err := rand.Read(token[:]); err != nil {
			return fmt.Errorf("create random staging name for %q: %w", name, err)
		}
		temporary = filepath.Join(parent, ".gograph-"+hex.EncodeToString(token[:])+".tmp")
		file, openErr := r.root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if os.IsExist(openErr) {
			continue
		}
		if openErr != nil {
			return fmt.Errorf("stage repository file %q: %w", name, openErr)
		}
		cleanup := true
		defer func() {
			if cleanup {
				_ = r.root.Remove(temporary)
			}
		}()
		if err := file.Chmod(perm); err != nil {
			_ = file.Close()
			return fmt.Errorf("set staged repository file %q permissions: %w", name, err)
		}
		n, writeErr := file.Write(data)
		if writeErr == nil && n != len(data) {
			writeErr = errors.New("short write")
		}
		if writeErr != nil {
			_ = file.Close()
			return fmt.Errorf("write staged repository file %q: %w", name, writeErr)
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return fmt.Errorf("sync staged repository file %q: %w", name, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close staged repository file %q: %w", name, err)
		}

		current, statErr := r.root.Lstat(clean)
		if existed {
			if statErr != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(expected, current) {
				return fmt.Errorf("%w %q: destination changed before atomic replacement", ErrUnsafeSourcePath, name)
			}
		} else if !os.IsNotExist(statErr) {
			if statErr == nil {
				return fmt.Errorf("%w %q: destination appeared before atomic replacement", ErrUnsafeSourcePath, name)
			}
			return fmt.Errorf("inspect repository path %q before atomic replacement: %w", name, statErr)
		}
		if err := r.root.Rename(temporary, clean); err != nil {
			return fmt.Errorf("commit repository file %q: %w", name, err)
		}
		cleanup = false
		directory, err := r.root.Open(parent)
		if err != nil {
			return fmt.Errorf("open repository directory for %q sync: %w", name, err)
		}
		defer func() { _ = directory.Close() }()
		if err := directory.Sync(); err != nil {
			return fmt.Errorf("sync repository directory for %q: %w", name, err)
		}
		return nil
	}
	return fmt.Errorf("stage repository file %q: exhausted random names", name)
}

// EnsureRealDirectory creates missing directory components beneath the opened
// repository and rejects descendant symlinks or non-directory components.
func (r *Reader) EnsureRealDirectory(name string, perm os.FileMode) error {
	if r == nil || r.root == nil {
		return errors.New("repository filesystem is closed")
	}
	clean, err := localName(name, true)
	if err != nil {
		return err
	}
	if clean == "." {
		return nil
	}
	prefix := ""
	for component := range strings.SplitSeq(clean, string(filepath.Separator)) {
		prefix = filepath.Join(prefix, component)
		info, statErr := r.root.Lstat(prefix)
		if os.IsNotExist(statErr) {
			if mkdirErr := r.root.Mkdir(prefix, perm); mkdirErr != nil && !os.IsExist(mkdirErr) {
				return fmt.Errorf("create repository directory %q: %w", prefix, mkdirErr)
			}
			info, statErr = r.root.Lstat(prefix)
		}
		if statErr != nil {
			return fmt.Errorf("inspect repository directory %q: %w", prefix, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("%w %q: directory component %q is not a real directory", ErrUnsafeSourcePath, name, prefix)
		}
	}
	return nil
}

// WriteRegularFile writes a regular file beneath the repository without
// following descendant links. When exclusive is true, an existing entry is
// rejected. Missing files are always created with O_EXCL so a dangling link
// cannot redirect creation.
func (r *Reader) WriteRegularFile(name string, data []byte, perm os.FileMode, exclusive bool) error {
	clean, expected, exists, err := r.prepareRegularWrite(name)
	if err != nil {
		return err
	}
	if exclusive && exists {
		return fmt.Errorf("repository file %q already exists: %w", name, os.ErrExist)
	}
	flags := os.O_WRONLY
	if !exists {
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	file, err := r.root.OpenFile(clean, flags, perm)
	if err != nil {
		return fmt.Errorf("open repository file %q for write: %w", name, err)
	}
	defer func() { _ = file.Close() }()
	if err := verifyOpenedRegular(name, file, expected); err != nil {
		return err
	}
	// Truncate only after the opened file's identity has been verified. Opening
	// with O_TRUNC would modify a different in-root file before a concurrent
	// entry swap could be detected.
	if exists {
		if err := file.Truncate(0); err != nil {
			return fmt.Errorf("truncate repository file %q: %w", name, err)
		}
	}
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write repository file %q: %w", name, err)
	}
	return nil
}

// AppendRegularFile appends bytes to an existing regular repository file and
// rejects symlink components, special files, and identity changes during open.
func (r *Reader) AppendRegularFile(name string, data []byte) error {
	if r == nil || r.root == nil {
		return errors.New("repository filesystem is closed")
	}
	clean, expected, err := r.inspectExisting(name, false)
	if err != nil {
		return err
	}
	file, err := r.root.OpenFile(clean, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("open repository file %q for append: %w", name, err)
	}
	defer func() { _ = file.Close() }()
	if err := verifyOpenedRegular(name, file, expected); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("append repository file %q: %w", name, err)
	}
	return nil
}

// ReadDirectory returns entries from a real repository directory without
// following descendant directory links.
func (r *Reader) ReadDirectory(name string) ([]os.DirEntry, error) {
	if r == nil || r.root == nil {
		return nil, errors.New("repository filesystem is closed")
	}
	clean, expected, err := r.inspectExisting(name, true)
	if err != nil {
		return nil, err
	}
	directory, err := r.root.Open(clean)
	if err != nil {
		return nil, fmt.Errorf("open repository directory %q: %w", name, err)
	}
	defer func() { _ = directory.Close() }()
	opened, err := directory.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened repository directory %q: %w", name, err)
	}
	if !opened.IsDir() || !os.SameFile(expected, opened) {
		return nil, fmt.Errorf("%w %q: repository directory changed during open", ErrUnsafeSourcePath, name)
	}
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("read repository directory %q: %w", name, err)
	}
	return entries, nil
}

// ValidateDirectory verifies that name opens as the same real directory
// observed beneath the repository root, without reading its entries.
func (r *Reader) ValidateDirectory(name string) error {
	if r == nil || r.root == nil {
		return errors.New("repository filesystem is closed")
	}
	clean, expected, err := r.inspectExisting(name, true)
	if err != nil {
		return err
	}
	directory, err := r.root.Open(clean)
	if err != nil {
		return fmt.Errorf("open repository directory %q: %w", name, err)
	}
	defer func() { _ = directory.Close() }()
	opened, err := directory.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened repository directory %q: %w", name, err)
	}
	if !opened.IsDir() || !os.SameFile(expected, opened) {
		return fmt.Errorf("%w %q: repository directory changed during open", ErrUnsafeSourcePath, name)
	}
	return nil
}

// RemoveRegularFile removes a regular repository file without following a
// symlinked ancestor or accepting a symlink/special final entry.
func (r *Reader) RemoveRegularFile(name string) error {
	if r == nil || r.root == nil {
		return errors.New("repository filesystem is closed")
	}
	clean, _, err := r.inspectExisting(name, false)
	if err != nil {
		return err
	}
	if err := r.root.Remove(clean); err != nil {
		return fmt.Errorf("remove repository file %q: %w", name, err)
	}
	return nil
}

func (r *Reader) prepareRegularWrite(name string) (clean string, expected os.FileInfo, exists bool, err error) {
	if r == nil || r.root == nil {
		return "", nil, false, errors.New("repository filesystem is closed")
	}
	clean, err = localName(name, false)
	if err != nil {
		return "", nil, false, err
	}
	parent := filepath.Dir(clean)
	if parent != "." {
		if _, _, err := r.inspectExisting(parent, true); err != nil {
			return "", nil, false, err
		}
	}
	expected, err = r.root.Lstat(clean)
	if os.IsNotExist(err) {
		return clean, nil, false, nil
	}
	if err != nil {
		return "", nil, false, fmt.Errorf("inspect repository path %q: %w", name, err)
	}
	if expected.Mode()&os.ModeSymlink != 0 || !expected.Mode().IsRegular() {
		return "", nil, false, fmt.Errorf("%w %q: repository entry is not a regular file", ErrUnsafeSourcePath, name)
	}
	return clean, expected, true, nil
}

func (r *Reader) inspectExisting(name string, wantDirectory bool) (string, os.FileInfo, error) {
	clean, err := localName(name, wantDirectory)
	if err != nil {
		return "", nil, err
	}
	components := strings.Split(clean, string(filepath.Separator))
	prefix := ""
	var final os.FileInfo
	for index, component := range components {
		prefix = filepath.Join(prefix, component)
		info, statErr := r.root.Lstat(prefix)
		if statErr != nil {
			return "", nil, fmt.Errorf("inspect repository path %q: %w", name, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", nil, fmt.Errorf("%w %q: symlink component %q", ErrUnsafeSourcePath, name, prefix)
		}
		if index < len(components)-1 {
			if !info.IsDir() {
				return "", nil, fmt.Errorf("%w %q: non-directory component %q", ErrUnsafeSourcePath, name, prefix)
			}
			continue
		}
		if wantDirectory && !info.IsDir() {
			return "", nil, fmt.Errorf("%w %q: repository entry is not a directory", ErrUnsafeSourcePath, name)
		}
		if !wantDirectory && !info.Mode().IsRegular() {
			return "", nil, fmt.Errorf("%w %q: repository entry is not a regular file", ErrUnsafeSourcePath, name)
		}
		final = info
	}
	return clean, final, nil
}

func localName(name string, allowDot bool) (string, error) {
	clean := filepath.Clean(name)
	if name == "" || (!allowDot && clean == ".") || !filepath.IsLocal(clean) {
		return "", fmt.Errorf("%w %q", ErrUnsafeSourcePath, name)
	}
	return clean, nil
}

func verifyOpenedRegular(name string, file *os.File, expected os.FileInfo) error {
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened repository file %q: %w", name, err)
	}
	if !opened.Mode().IsRegular() || expected != nil && !os.SameFile(expected, opened) {
		return fmt.Errorf("%w %q: repository entry changed during open", ErrUnsafeSourcePath, name)
	}
	return nil
}
