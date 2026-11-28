package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ozgurcd/gograph/internal/memorylimit"
)

const (
	// revisionCacheMaxEnv overrides the size cap of .gograph/revs, for
	// example 4GiB; 0 removes the cap.
	revisionCacheMaxEnv = "GOGRAPH_REVS_MAX"
	// defaultRevisionCacheMax is the default size cap of .gograph/revs.
	defaultRevisionCacheMax int64 = 2 << 30
	// revisionSizeSuffix names the marker beside each tree that records its
	// size once, so eviction never walks every extracted tree.
	revisionSizeSuffix = ".size"
)

func revisionCacheMax() int64 {
	value := strings.TrimSpace(os.Getenv(revisionCacheMaxEnv))
	if value == "" {
		return defaultRevisionCacheMax
	}
	if value == "0" {
		return 0
	}
	limit, err := memorylimit.ParseSize(value)
	if err != nil || limit <= 0 {
		return defaultRevisionCacheMax
	}
	return limit
}

// markRevisionUsed records that a cached tree was read now, for LRU eviction.
func markRevisionUsed(treeDir string) {
	now := time.Now()
	_ = os.Chtimes(treeDir, now, now)
}

// capRevisionCache records keep's size and removes the least recently used
// other trees until .gograph/revs fits its cap. Failures only leave extra
// trees behind; they never fail the query.
func capRevisionCache(cacheRoot, keep string) {
	limit := revisionCacheMax()
	if limit == 0 {
		return
	}
	entries, err := os.ReadDir(cacheRoot)
	if err != nil {
		return
	}
	type tree struct {
		name string
		size int64
		used time.Time
	}
	var trees []tree
	var total int64
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || strings.Contains(name, ".tmp-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		size := revisionTreeSize(cacheRoot, name, name == keep)
		total += size
		if name != keep {
			trees = append(trees, tree{name: name, size: size, used: info.ModTime()})
		}
	}
	sort.Slice(trees, func(i, j int) bool { return trees[i].used.Before(trees[j].used) })
	for _, t := range trees {
		if total <= limit {
			break
		}
		if err := os.RemoveAll(filepath.Join(cacheRoot, t.name)); err != nil {
			continue
		}
		_ = os.Remove(filepath.Join(cacheRoot, t.name+revisionSizeSuffix))
		total -= t.size
		fmt.Fprintf(os.Stderr, "gograph: removed the least recently used revision tree %s to keep %s under %s (%s)\n", t.name, revisionCacheDir, memorylimit.FormatSize(limit), revisionCacheMaxEnv)
	}
}

// revisionTreeSize returns the recorded size of one tree, measuring and
// recording it when there is no marker or refresh is set (the tree just built).
func revisionTreeSize(cacheRoot, name string, refresh bool) int64 {
	marker := filepath.Join(cacheRoot, name+revisionSizeSuffix)
	if !refresh {
		if data, err := os.ReadFile(marker); err == nil {
			if size, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil && size >= 0 {
				return size
			}
		}
	}
	var size int64
	_ = filepath.WalkDir(filepath.Join(cacheRoot, name), func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type().IsRegular() {
			if info, err := entry.Info(); err == nil {
				size += info.Size()
			}
		}
		return nil
	})
	_ = os.WriteFile(marker, []byte(strconv.FormatInt(size, 10)+"\n"), 0o644)
	return size
}
