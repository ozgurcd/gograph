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
	for {
		info, statErr := os.Lstat(filepath.Join(dir, gographDir))
		if statErr == nil && info.Mode()&os.ModeSymlink == 0 && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root — fall back to cwd.
			return "."
		}
		dir = parent
	}
}
