// Package gitenv builds the environment for git child processes that must act
// on the repository named by their -C argument and nothing else.
package gitenv

import "strings"

// keep lists the GIT_* variables that do not select a repository, its objects,
// its index, or extra configuration, and that a child may still need (prompts,
// transport, the user's own config files).
var keep = map[string]bool{
	"GIT_EXEC_PATH":                   true,
	"GIT_CONFIG_GLOBAL":               true,
	"GIT_CONFIG_SYSTEM":               true,
	"GIT_CONFIG_NOSYSTEM":             true,
	"GIT_TERMINAL_PROMPT":             true,
	"GIT_ASKPASS":                     true,
	"GIT_SSH":                         true,
	"GIT_SSH_COMMAND":                 true,
	"GIT_OPTIONAL_LOCKS":              true,
	"GIT_FLUSH":                       true,
	"GIT_AUTHOR_NAME":                 true,
	"GIT_AUTHOR_EMAIL":                true,
	"GIT_AUTHOR_DATE":                 true,
	"GIT_COMMITTER_NAME":              true,
	"GIT_COMMITTER_EMAIL":             true,
	"GIT_COMMITTER_DATE":              true,
	"GIT_TRACE":                       true,
	"GIT_TRACE_PERFORMANCE":           true,
	"GIT_PAGER":                       true,
	"GIT_DISCOVERY_ACROSS_FILESYSTEM": true,
}

// Clean returns environ without every GIT_* variable that could make git act on
// a repository other than the one given with -C: GIT_DIR, GIT_WORK_TREE,
// GIT_COMMON_DIR, GIT_INDEX_FILE, GIT_OBJECT_DIRECTORY, GIT_NAMESPACE,
// GIT_CEILING_DIRECTORIES, GIT_CONFIG_PARAMETERS and GIT_CONFIG_COUNT/KEY_n/
// VALUE_n (which can set core.worktree), and the rest of the GIT_* family. A
// variable is dropped unless it is on the explicit keep list, so a variable git
// adds later is dropped by default. The input slice is not modified.
func Clean(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(name), "GIT_") && !keep[name] {
			continue
		}
		out = append(out, entry)
	}
	return out
}
