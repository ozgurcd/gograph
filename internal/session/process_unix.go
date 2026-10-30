//go:build unix

package session

import (
	"errors"
	"syscall"
)

// processAlive reports whether pid names a running process. A permission
// error still proves the process exists.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
