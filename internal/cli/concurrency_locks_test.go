package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConcurrencyLocksReportsScopeAndCallsUnderLock(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/locks\n\ngo 1.22\n",
		"m.go": `package locks

import "sync"

type M struct {
	mu   sync.Mutex
	rw   sync.RWMutex
	data map[string]int
}

func (m *M) deferred(k string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flush()
	return m.data[k]
}

func (m *M) explicit(k string) bool {
	m.mu.Lock()
	if m.data == nil {
		m.mu.Unlock()
		return false
	}
	m.notify(k)
	m.mu.Unlock()
	m.flush()
	m.rw.RLock()
	v := m.data[k]
	m.rw.RUnlock()
	return v > 0
}

func (m *M) leak() {
	m.mu.Lock()
	m.flush()
}

func (m *M) closure() {
	m.mu.Lock()
	defer func() {
		m.notify("x")
		m.mu.Unlock()
	}()
	m.flush()
}

func (m *M) flush()        {}
func (m *M) notify(string) {}
`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) }); code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}
	run := func(args ...string) (string, string, int) {
		return runCLIParityInDir(t, root, func() int { return Run(args) })
	}
	cases := map[string][]string{
		"M.deferred":        {"m.mu.Lock() line 12: held until return (deferred unlock at line 13), lines 12-16", "  14: m.flush()"},
		"locks::M.explicit": {"m.mu.Lock() line 19: unlocked at lines 21, 25, lines 19-25", "  24: m.notify()", "m.rw.RLock() line 27: unlocked at line 29, lines 27-29\n  no calls under the lock"},
		"m.go:M.leak":       {"m.mu.Lock() line 34: not released in this function, lines 34-36", "  35: m.flush()"},
		"M.closure":         {"held until return (deferred unlock at line 42)", "  41: m.notify()", "  44: m.flush()"},
		"M.flush":           {"no Lock/RLock calls in this function"},
	}
	for selector, want := range cases {
		out, stderr, code := run("concurrency", "--locks", selector)
		if code != 0 {
			t.Fatalf("%s: code=%d err=%s", selector, code, stderr)
		}
		for _, w := range want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: missing %q in:\n%s", selector, w, out)
			}
		}
	}
	if out, _, _ := run("concurrency", "--locks", "M.explicit"); strings.Contains(out, "m.flush()") {
		t.Errorf("m.flush at line 26 runs after the last unlock:\n%s", out)
	}

	out, stderr, code := run("--json", "concurrency", "--locks=M.deferred")
	var env struct {
		Count   int `json:"count"`
		Results struct {
			Locks []struct {
				Lock    string `json:"lock"`
				Release string `json:"release"`
			} `json:"locks"`
		} `json:"results"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &env) != nil || env.Count != 1 || env.Results.Locks[0].Lock != "m.mu.Lock" || env.Results.Locks[0].Release != "deferred" {
		t.Fatalf("json: code=%d out=%s err=%s", code, out, stderr)
	}

	for _, args := range [][]string{
		{"concurrency", "--locks"},
		{"concurrency", "--locks", "M.deferred", "extra"},
		{"concurrency", "--locks", "M"},
		{"concurrency", "--locks", "nosuch"},
	} {
		if _, _, code := run(args...); code == 0 {
			t.Errorf("%v must fail", args)
		}
	}
}
