// Package rootfind provides shared gograph project root discovery.
//
// Both CLI graph loading and session telemetry need to anchor paths at the
// repository root (the nearest ancestor directory containing .gograph/).
// This package avoids coupling telemetry and graph-loading concerns by
// providing a single, importable FindRoot() function.
package rootfind

import (
	"os"
	"path/filepath"
)

const gographDir = ".gograph"

// FindRoot walks up from the current working directory until it finds a
// directory that contains a ".gograph" subdirectory (i.e. the project root
// where `gograph build` was run). Falls back to "." when none is found so
// that fresh directories and test temp dirs work without a pre-existing index.
func FindRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	if root, ok := FindRootFrom(dir); ok {
		return root
	}
	return "."
}

// FindRootFrom walks up from start until it finds a directory that contains a
// real ".gograph" directory. start may name a directory, file, or not-yet-
// expanded glob; in each case its ancestors are considered. The boolean is
// false when no indexed ancestor exists.
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
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
