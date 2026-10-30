//go:build linux

package sourcefs

import (
	"errors"
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames from (in source) to to (in target) with
// RENAME_NOREPLACE, falling back to link and unlink where the filesystem or
// kernel does not support it.
func renameNoReplace(source *os.File, from string, target *os.File, to string) error {
	err := unix.Renameat2(int(source.Fd()), from, int(target.Fd()), to, unix.RENAME_NOREPLACE)
	runtime.KeepAlive(source)
	runtime.KeepAlive(target)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
		return linkThenUnlink(source, from, target, to)
	}
	return err
}
