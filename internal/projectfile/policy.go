package projectfile

import (
	"errors"
	"os"
	"path"

	"github.com/ozgurcd/gograph/internal/sourcefs"
)

// CommittedPolicyDir holds policy files meant to be committed: gograph adds
// .gograph/ to .gitignore, so policies there are lost on a fresh clone.
const CommittedPolicyDir = "gograph"

// LegacyPolicyDir is the original, git-ignored policy location.
const LegacyPolicyDir = ".gograph"

// PolicyPath returns the repository-relative policy file to use for name
// (for example "boundaries.json"): gograph/<name> when it exists, otherwise
// .gograph/<name> when it exists. When neither exists it returns the
// committed location, where writers should create the file, and found=false.
func PolicyPath(projectRoot, name string) (relative string, found bool) {
	committed := path.Join(CommittedPolicyDir, name)
	legacy := path.Join(LegacyPolicyDir, name)
	reader, err := sourcefs.Open(projectRoot)
	if err != nil {
		return committed, false
	}
	defer func() { _ = reader.Close() }()
	for _, candidate := range []string{committed, legacy} {
		_, err := reader.ReadRegularFileLimit(candidate, 64<<20)
		if err == nil {
			return candidate, true
		}
		// A present but unsafe or unreadable file is still selected so the
		// caller reports why it cannot be used instead of silently skipping.
		if !errors.Is(err, os.ErrNotExist) {
			return candidate, true
		}
	}
	return committed, false
}
