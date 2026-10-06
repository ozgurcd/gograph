package session

import "strings"

// IsInvocationError recognizes request-shape diagnostics only. Operational
// failures and empty or ambiguous lookup results remain ordinary failures.
func IsInvocationError(message string) bool {
	return strings.HasPrefix(message, "usage:") ||
		strings.Contains(message, "requires an intention") ||
		strings.Contains(message, "flag requires a value") ||
		strings.Contains(message, "malformed selector") ||
		message == "invalid arguments" ||
		strings.HasSuffix(message, "must be a string") ||
		strings.HasSuffix(message, "must be a non-empty string")
}
