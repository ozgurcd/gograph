// Package graphstore keeps complete graphs outside any one checkout, keyed by
// the exact selected source content and build context, so every worktree or
// clone of a repository at the same content loads a finished precise graph
// instead of repeating the type-checked analysis.
//
// The store is a cache, never an authority: a caller verifies a loaded graph
// against its own working tree before using it. Entries are published with an
// atomic rename, so a reader never sees a torn file, and the store is kept
// under a size cap by evicting the least recently used entries.
package graphstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/memorylimit"
)

const (
	// DirEnv overrides the store directory; "off" disables the store.
	DirEnv = "GOGRAPH_CACHE_DIR"
	// MaxEnv overrides the size cap, for example 8GiB; 0 disables the store.
	MaxEnv = "GOGRAPH_CACHE_MAX"
	// DefaultMax is the default size cap of the whole store.
	DefaultMax int64 = 4 << 30

	entrySuffix = ".json"
	lockSuffix  = ".lock"
	lockRetry   = 100 * time.Millisecond
)

// Store is one store directory with its size cap.
type Store struct {
	Dir string
	Max int64
}

// Open returns the store selected by the environment, or ok=false when it is
// disabled or unusable. The default directory is <user cache>/gograph/graphs.
func Open() (*Store, bool) {
	dir := strings.TrimSpace(os.Getenv(DirEnv))
	if strings.EqualFold(dir, "off") {
		return nil, false
	}
	if dir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return nil, false
		}
		dir = filepath.Join(cache, "gograph", "graphs")
	}
	limit := DefaultMax
	if value := strings.TrimSpace(os.Getenv(MaxEnv)); value != "" {
		if value == "0" {
			return nil, false
		}
		parsed, err := memorylimit.ParseSize(value)
		if err != nil || parsed <= 0 {
			return nil, false
		}
		limit = parsed
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, false
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, false
	}
	if info, err := os.Lstat(abs); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, false
	}
	return &Store{Dir: abs, Max: limit}, true
}

// AnalyzerIdentity names the analysis code that produced a graph: the release
// version plus, for development builds, the VCS revision and dirty state, so a
// changed analyzer never reuses another analyzer's graph.
func AnalyzerIdentity(version string) string {
	identity := version + " " + runtime.Version()
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision", "vcs.modified":
				identity += " " + setting.Key + "=" + setting.Value
			}
		}
	}
	return identity
}

// Key is the store key of a complete graph with a location-independent source
// fingerprint, or ok=false when the graph cannot be keyed. precision is the
// analysis the entry must hold (graph.PrecisionPrecise for precise builds).
func Key(g *graph.Graph, precision graph.PrecisionMode, analyzer string) (string, bool) {
	if g == nil || g.Build == nil || !g.Build.Complete || g.Build.SourceFingerprint == "" {
		return "", false
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		"gograph.graphstore.v1",
		analyzer,
		string(precision),
		// SourceFingerprint already binds the build selection (GOOS, GOARCH,
		// tags, excluded dirs, module path and metadata) without the checkout
		// location; BuildContextFingerprint is location-bound, so it is not
		// part of the key.
		g.Build.SourceFingerprint,
	}, "\x00")))
	return hex.EncodeToString(sum[:]), true
}

func (s *Store) entryPath(key string) string { return filepath.Join(s.Dir, key+entrySuffix) }

// Load returns the stored bytes for key and marks the entry recently used.
func (s *Store) Load(key string) ([]byte, bool) {
	path := s.entryPath(key)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > graph.MaxArtifactBytes {
		return nil, false
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, graph.MaxArtifactBytes+1))
	if err != nil || int64(len(data)) > graph.MaxArtifactBytes {
		return nil, false
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return data, true
}

// Save publishes data under key atomically, then evicts least recently used
// entries until the store fits its cap. The new entry is never evicted by its
// own save.
func (s *Store) Save(key string, data []byte) error {
	if int64(len(data)) > s.Max {
		return fmt.Errorf("graph of %d bytes exceeds the store cap %d", len(data), s.Max)
	}
	tmp, err := os.CreateTemp(s.Dir, key+".tmp-*")
	if err != nil {
		return fmt.Errorf("create store entry: %w", err)
	}
	tmpName := tmp.Name()
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("write store entry: %w", errors.Join(writeErr, closeErr))
	}
	if err := os.Rename(tmpName, s.entryPath(key)); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("publish store entry: %w", err)
	}
	_, err = Evict(s.Dir, s.Max, map[string]bool{key + entrySuffix: true}, entrySuffix)
	return err
}

// Lock serializes the production of one key across processes, so checkouts at
// the same content build it once: the first holder builds and saves, a waiter
// then loads. waited reports that another process held the lock.
func (s *Store) Lock(ctx context.Context, key string) (unlock func(), waited bool, err error) {
	lock := flock.New(filepath.Join(s.Dir, key+lockSuffix), flock.SetPermissions(0o600))
	locked, err := lock.TryLock()
	if err != nil {
		return nil, false, err
	}
	if !locked {
		waited = true
		locked, err = lock.TryLockContext(ctx, lockRetry)
		if err != nil || !locked {
			return nil, true, fmt.Errorf("waiting for another process building the same graph: %w", errors.Join(err, ctx.Err()))
		}
	}
	// The lock file stays: removing it while another process waits on the
	// same inode would let a third process lock a new file concurrently.
	// Evict removes lock files that have been idle for a day.
	return func() { _ = lock.Unlock() }, waited, nil
}

// Evict removes the least recently used entries of dir whose names end in
// suffix until their total size is at most max, never removing a name in keep.
// Stale temporary files older than an hour are removed too. It returns the
// number of bytes removed.
func Evict(dir string, max int64, keep map[string]bool, suffix string) (int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	type item struct {
		name string
		size int64
		used time.Time
	}
	var items []item
	var total, removed int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, lockSuffix) {
			if time.Since(info.ModTime()) > 24*time.Hour {
				_ = os.Remove(filepath.Join(dir, name))
			}
			continue
		}
		if strings.Contains(name, ".tmp-") {
			if time.Since(info.ModTime()) > time.Hour && os.Remove(filepath.Join(dir, name)) == nil {
				removed += info.Size()
			}
			continue
		}
		if !strings.HasSuffix(name, suffix) {
			continue
		}
		total += info.Size()
		if !keep[name] {
			items = append(items, item{name: name, size: info.Size(), used: info.ModTime()})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].used.Before(items[j].used) })
	for _, it := range items {
		if total <= max {
			break
		}
		if err := os.Remove(filepath.Join(dir, it.name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		total -= it.size
		removed += it.size
	}
	return removed, nil
}
