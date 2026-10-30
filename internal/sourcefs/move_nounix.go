//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package sourcefs

import (
	"errors"
	"os"
)

// renameNoReplace has no atomic no-replace primitive here, so it refuses
// rather than risk replacing a file.
func renameNoReplace(*os.File, string, *os.File, string) error {
	return errors.New("no-replace move is not supported on this platform")
}
