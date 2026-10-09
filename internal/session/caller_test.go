package session

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCallerSessionsConcurrentIndependentOwners(t *testing.T) {
	root := t.TempDir()
	const callers = 8
	ids := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func() { defer wg.Done(); ids[i], errs[i] = StartCallerSessionAt(root, "same", "") }()
	}
	wg.Wait()
	seen := map[string]bool{}
	for i, id := range ids {
		if errs[i] != nil || id == "" || seen[id] {
			t.Fatalf("caller %d failed or collided: %v", i, errs[i])
		}
		seen[id] = true
	}
	for i, id := range ids {
		if err := GuardCallerMutationAt(root, id); err == nil {
			t.Fatal("mutation must refuse another active owner")
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = LogCallerCommandAt(root, id, "plan", nil, "independent owner", time.Millisecond, "success")
		}()
	}
	wg.Wait()
	for i, id := range ids {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		var audit bytes.Buffer
		if RunAuditToAt(root, id, true, &audit, &audit) != 0 {
			t.Fatal("audit failed")
		}
		if !bytes.Contains(audit.Bytes(), []byte(`"total_commands": 1`)) {
			t.Fatal("caller absorbed another audit")
		}
		if _, err := EndCallerSessionAt(root, id); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := CleanupCallerSessionsAt(root, ""); err != nil || n != callers {
		t.Fatalf("cleanup: %d %v", n, err)
	}
}

func TestCallerSessionMarkersAreConfined(t *testing.T) {
	root := t.TempDir()
	id, err := StartCallerSessionAt(root, "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, ".gograph", "sessions", "session_"+id+".active")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte(id), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, marker); err != nil {
		t.Fatal(err)
	}
	if _, err := ActiveCallerSessionAt(root, id); err == nil {
		t.Fatal("linked marker accepted")
	}
	if _, err := EndCallerSessionAt(root, id); err == nil {
		t.Fatal("linked marker ended")
	}
	if _, err := CleanupCallerSessionsAt(root, ""); err == nil {
		t.Fatal("linked marker cleaned")
	}
	if _, err := ActiveCallerSessionAt(root, "../escape"); err == nil {
		t.Fatal("traversal selector accepted")
	}
	data, _ := os.ReadFile(target)
	if string(data) != id {
		t.Fatal("linked target changed")
	}
}
