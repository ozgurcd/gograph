package projectfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPolicyPathPrefersCommittedThenLegacy(t *testing.T) {
	root := t.TempDir()
	if got, found := PolicyPath(root, "boundaries.json"); got != "gograph/boundaries.json" || found {
		t.Fatalf("empty repo = %q, %v; want committed default for writers", got, found)
	}
	write := func(rel string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".gograph/boundaries.json")
	if got, found := PolicyPath(root, "boundaries.json"); got != ".gograph/boundaries.json" || !found {
		t.Fatalf("legacy only = %q, %v", got, found)
	}
	write("gograph/boundaries.json")
	if got, found := PolicyPath(root, "boundaries.json"); got != "gograph/boundaries.json" || !found {
		t.Fatalf("both = %q, %v; committed must win", got, found)
	}
	if err := os.Symlink(filepath.Join(root, "gograph", "boundaries.json"), filepath.Join(root, "gograph", "flow.json")); err == nil {
		if got, found := PolicyPath(root, "flow.json"); got != "gograph/flow.json" || !found {
			t.Fatalf("linked committed file = %q, %v; must be selected so its reader reports the refusal", got, found)
		}
	}
}
