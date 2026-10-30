//go:build darwin

package sourcefs

import (
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames from (in source) to to (in target) with RENAME_EXCL,
// which fails rather than replaces an existing destination.
func renameNoReplace(source *os.File, from string, target *os.File, to string) error {
	err := unix.RenameatxNp(int(source.Fd()), from, int(target.Fd()), to, unix.RENAME_EXCL)
	runtime.KeepAlive(source)
	runtime.KeepAlive(target)
	return err
}
