//go:build !unix

package session

// processAlive cannot inspect processes here, so it never declares an owner
// gone; idle time alone ends such sessions.
func processAlive(int) bool { return true }
