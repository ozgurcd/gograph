package session

import (
	"encoding/json"
	"os"
	"testing"
)

func TestImplicitCallerSessionAdoptsOnlyAnUnambiguousLocalCLISession(t *testing.T) {
	host, _ := os.Hostname()

	t.Run("no session", func(t *testing.T) {
		if id, err := ImplicitCallerSessionAt(t.TempDir()); err != nil || id != "" {
			t.Fatalf("got %q, %v; want nothing", id, err)
		}
	})

	t.Run("only local CLI session", func(t *testing.T) {
		root := t.TempDir()
		created, err := StartCallerSessionAt(root, "owner", "")
		if err != nil {
			t.Fatal(err)
		}
		if id, err := ImplicitCallerSessionAt(root); err != nil || id != created {
			t.Fatalf("got %q, %v; want %q", id, err, created)
		}
		if _, err := EndCallerSessionAt(root, created); err != nil {
			t.Fatal(err)
		}
		if id, _ := ImplicitCallerSessionAt(root); id != "" {
			t.Fatalf("an ended session must not be adopted, got %q", id)
		}
	})

	t.Run("two sessions are ambiguous", func(t *testing.T) {
		root := t.TempDir()
		for _, word := range []string{"one", "two"} {
			if _, err := StartCallerSessionAt(root, word, ""); err != nil {
				t.Fatal(err)
			}
		}
		if id, _ := ImplicitCallerSessionAt(root); id != "" {
			t.Fatalf("several sessions must adopt nothing, got %q", id)
		}
	})

	t.Run("MCP-owned session", func(t *testing.T) {
		root := t.TempDir()
		if _, err := StartCallerSessionOwnedAt(root, "server", "", OwnerMCP); err != nil {
			t.Fatal(err)
		}
		if id, _ := ImplicitCallerSessionAt(root); id != "" {
			t.Fatalf("an MCP session must not be adopted by the CLI, got %q", id)
		}
	})

	for name, meta := range map[string]*CallerMarker{
		"other host":      {Owner: OwnerCLI, Host: host + ".other"},
		"unknown owner":   {Owner: "", Host: host},
		"no owner record": nil,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			id, err := StartCallerSessionAt(root, "foreign", "")
			if err != nil {
				t.Fatal(err)
			}
			path := sessionFile(root, id, ".meta.json")
			if meta == nil {
				err = os.Remove(path)
			} else {
				meta.ID = id
				data, _ := json.Marshal(meta)
				err = os.WriteFile(path, data, 0o644)
			}
			if err != nil {
				t.Fatal(err)
			}
			if adopted, _ := ImplicitCallerSessionAt(root); adopted != "" {
				t.Fatalf("session without proof of local CLI ownership must not be adopted, got %q", adopted)
			}
		})
	}
}
