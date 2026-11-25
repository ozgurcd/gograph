package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A call that follows an unlock in the same block (an early-return branch or
// a case clause) runs unlocked, so it is not listed under the lock.
func TestConcurrencyLocksSkipsCallsAfterUnlockInSameBlock(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/lockbranch\n\ngo 1.22\n",
		"m.go": `package lockbranch

import (
	"errors"
	"sync"
)

type M struct {
	mu sync.Mutex
	n  int
}

func (m *M) start() error {
	m.mu.Lock()
	if m.n > 3 {
		m.mu.Unlock()
		return errors.New("full")
	}
	switch m.n {
	case 1:
		m.mu.Unlock()
		m.afterCase()
		return nil
	case 2:
		m.underCase()
	}
	m.underLock()
	m.mu.Unlock()
	m.afterUnlock()
	return nil
}

func (m *M) afterCase()   {}
func (m *M) underCase()   {}
func (m *M) underLock()   {}
func (m *M) afterUnlock() {}
`,
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) }); code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}
	out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"concurrency", "--locks", "M.start"}) })
	if code != 0 {
		t.Fatalf("concurrency --locks: code=%d err=%s", code, stderr)
	}
	for _, want := range []string{"unlocked at lines 16, 21, 28", "m.underCase()", "m.underLock()"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"errors.New", "m.afterCase", "m.afterUnlock"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("unexpected %q:\n%s", unwanted, out)
		}
	}
}
