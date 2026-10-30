//go:build linux || freebsd || netbsd || openbsd || dragonfly

package sourcefs

import (
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// linkThenUnlink is the portable no-replace move: linkat fails when the
// destination exists and never follows a final link, then the source name is
// removed.
func linkThenUnlink(source *os.File, from string, target *os.File, to string) error {
	defer runtime.KeepAlive(source)
	defer runtime.KeepAlive(target)
	if err := unix.Linkat(int(source.Fd()), from, int(target.Fd()), to, 0); err != nil {
		return err
	}
	return unix.Unlinkat(int(source.Fd()), from, 0)
}
