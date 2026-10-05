package search

// EmptyAnswerWarning keeps missing AST facts distinct from proved absence.
// It adds no result rows and leaves precise answers unchanged.
func EmptyAnswerWarning(command, precision string, count int) string {
	if count != 0 || (precision != "ast" && precision != "precise_fallback") {
		return ""
	}
	switch command {
	case "tests", "envs", "usages":
		return "This AST-only answer may be incomplete; run gograph build . --precise and repeat the query."
	default:
		return ""
	}
}
