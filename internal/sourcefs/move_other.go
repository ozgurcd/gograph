//go:build freebsd || netbsd || openbsd || dragonfly

package sourcefs

import "os"

func renameNoReplace(source *os.File, from string, target *os.File, to string) error {
	return linkThenUnlink(source, from, target, to)
}
