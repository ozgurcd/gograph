package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	protocol "github.com/mark3labs/mcp-go/mcp"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// scopeHarness is a router over real temp directories with a counting loader.
// Everything is fixed before the router is built; tests only call the router.
type scopeHarness struct {
	t        *testing.T
	primary  string
	wt1, wt2 string
	other    string
	clock    *fakeClock
	loads    atomic.Int32
	running  atomic.Int32
	maxLoad  atomic.Int32
	loadWait chan struct{} // when non-nil each load blocks until it is closed
	loadErr  atomic.Pointer[error]
	handler  func(root string) ToolHandler
	router   *ScopeRouter
}

func newScopeHarness(t *testing.T, maxScopes int, ttl time.Duration, gate chan struct{}, handler func(root string) ToolHandler) *scopeHarness {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &scopeHarness{t: t, clock: &fakeClock{now: time.Unix(1_000_000, 0)}, loadWait: gate, handler: handler}
	for name, dst := range map[string]*string{"main": &h.primary, "wt1": &h.wt1, "wt2": &h.wt2, "other": &h.other} {
		*dst = filepath.Join(base, name)
		if err := os.MkdirAll(filepath.Join(*dst, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if h.handler == nil {
		h.handler = func(root string) ToolHandler {
			return func(context.Context, protocol.CallToolRequest) (*protocol.CallToolResult, error) {
				return protocol.NewToolResultText("answer from " + root), nil
			}
		}
	}
	h.router, err = NewScopeRouter(ScopeConfig{
		Root:      h.primary,
		MaxScopes: maxScopes,
		IdleTTL:   ttl,
		Now:       h.clock.Now,
		Worktrees: func(context.Context, string) ([]string, error) { return []string{h.primary, h.wt1, h.wt2}, nil },
		Load: func(root string) (ScopeHandlers, error) {
			h.loads.Add(1)
			n := h.running.Add(1)
			for {
				m := h.maxLoad.Load()
				if n <= m || h.maxLoad.CompareAndSwap(m, n) {
					break
				}
			}
			defer h.running.Add(-1)
			if h.loadWait != nil {
				<-h.loadWait
			}
			if e := h.loadErr.Load(); e != nil {
				return nil, *e
			}
			return ScopeHandlers{"tool": h.handler(root)}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *scopeHarness) call(ctx context.Context, root string) (*protocol.CallToolResult, error) {
	h.t.Helper()
	sel, err := h.router.Resolve(ctx, root)
	if err != nil {
		h.t.Fatalf("resolve %q: %v", root, err)
	}
	return h.router.Call(ctx, sel, "tool", protocol.CallToolRequest{})
}

func resultText(r *protocol.CallToolResult) string {
	var parts []string
	for _, c := range r.Content {
		if text, ok := c.(protocol.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestScopeRouterLoadsNothingUntilFirstCall(t *testing.T) {
	h := newScopeHarness(t, 0, 0, nil, nil)
	if h.router.MaxScopes() != DefaultMaxScopes || h.router.IdleTTL() != DefaultScopeIdleTTL {
		t.Fatalf("defaults: %d %v", h.router.MaxScopes(), h.router.IdleTTL())
	}
	if _, err := h.router.Resolve(context.Background(), h.wt1); err != nil {
		t.Fatal(err)
	}
	if h.loads.Load() != 0 || len(h.router.Loaded()) != 0 {
		t.Fatalf("resolving must not load: loads=%d", h.loads.Load())
	}
	got, err := h.call(context.Background(), h.wt1)
	if err != nil || resultText(got) != "answer from "+h.wt1 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestScopeRouterResolve(t *testing.T) {
	h := newScopeHarness(t, 0, 0, nil, nil)
	link := filepath.Join(filepath.Dir(h.primary), "alias")
	if err := os.Symlink(h.wt1, link); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for name, tc := range map[string]struct{ asked, want string }{
		"empty":            {"", h.primary},
		"primary":          {h.primary, h.primary},
		"primary subdir":   {filepath.Join(h.primary, "sub"), h.primary},
		"worktree":         {h.wt2, h.wt2},
		"worktree subdir":  {filepath.Join(h.wt1, "sub"), h.wt1},
		"symlink to wt":    {link, h.wt1},
		"trailing slash":   {h.wt1 + "/", h.wt1},
		"dot dot in path":  {filepath.Join(h.wt1, "sub", ".."), h.wt1},
		"dot dot to other": {filepath.Join(h.wt1, "sub", "..", "..", "wt2"), h.wt2},
	} {
		sel, err := h.router.Resolve(ctx, tc.asked)
		if err != nil || sel.Root != tc.want {
			t.Errorf("%s: got %+v, %v want %s", name, sel, err, tc.want)
		}
	}
	for name, asked := range map[string]string{
		"other repo":     h.other,
		"parent":         filepath.Dir(h.primary),
		"relative":       "main",
		"missing":        filepath.Join(h.primary, "nope"),
		"prefix sibling": h.primary + "x",
	} {
		_, err := h.router.Resolve(ctx, asked)
		var scopeErr *OutOfScopeError
		if !errors.As(err, &scopeErr) {
			t.Errorf("%s: want OutOfScopeError, got %v", name, err)
			continue
		}
		if scopeErr.Indexed != h.primary || scopeErr.Asked != asked || !strings.Contains(err.Error(), h.primary) || !strings.Contains(err.Error(), asked) {
			t.Errorf("%s: error must name both roots: %v", name, err)
		}
	}
}

func TestScopeRouterFindsWorktreeCreatedAfterListing(t *testing.T) {
	h := newScopeHarness(t, 0, 0, nil, nil)
	var calls atomic.Int32
	late := filepath.Join(filepath.Dir(h.primary), "late")
	if err := os.MkdirAll(late, 0o755); err != nil {
		t.Fatal(err)
	}
	var listed atomic.Bool
	router, err := NewScopeRouter(ScopeConfig{
		Root: h.primary, Now: h.clock.Now,
		Load: func(string) (ScopeHandlers, error) { return ScopeHandlers{}, nil },
		Worktrees: func(context.Context, string) ([]string, error) {
			calls.Add(1)
			if listed.Load() {
				return []string{h.primary, late}, nil
			}
			return []string{h.primary}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := router.Resolve(context.Background(), h.primary); err != nil {
		t.Fatal(err)
	}
	listed.Store(true) // the worktree appears inside the cache TTL
	if sel, err := router.Resolve(context.Background(), late); err != nil || sel.Root != late {
		t.Fatalf("a worktree created inside the listing TTL must be found: %+v, %v", sel, err)
	}
}

func TestScopeRouterRefusesWhenWorktreeListingFails(t *testing.T) {
	h := newScopeHarness(t, 0, 0, nil, nil)
	router, err := NewScopeRouter(ScopeConfig{
		Root: h.primary, Now: h.clock.Now,
		Load:      func(string) (ScopeHandlers, error) { return ScopeHandlers{}, nil },
		Worktrees: func(context.Context, string) ([]string, error) { return nil, errors.New("boom") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := router.Resolve(context.Background(), h.wt1); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("a failed listing must refuse a non-primary path, got %v", err)
	}
	if sel, err := router.Resolve(context.Background(), h.primary); err != nil || sel.Root != h.primary {
		t.Fatalf("the configured checkout needs no listing: %+v, %v", sel, err)
	}
}

func TestScopeRouterClientRoots(t *testing.T) {
	h := newScopeHarness(t, 0, 0, nil, nil)
	parent := filepath.Dir(h.primary)
	ctx := context.Background()
	for name, tc := range map[string]struct {
		roots   []string
		want    string
		refused bool
	}{
		"worktree root":          {[]string{h.wt1}, h.wt1, false},
		"worktree subdir":        {[]string{filepath.Join(h.wt2, "sub")}, h.wt2, false},
		"workspace folder only":  {[]string{parent}, h.primary, false},
		"foreign repo":           {[]string{h.other}, "", true},
		"foreign beats folder":   {[]string{parent, h.other}, "", true},
		"match beats foreign":    {[]string{h.other, h.wt1}, h.wt1, false},
		"no roots":               {nil, h.primary, false},
		"missing dir is foreign": {[]string{filepath.Join(parent, "gone")}, "", true},
	} {
		sel, err := h.router.ResolveClientRoots(ctx, tc.roots)
		if tc.refused {
			var scopeErr *OutOfScopeError
			if !errors.As(err, &scopeErr) || !strings.Contains(err.Error(), h.primary) {
				t.Errorf("%s: want refusal naming %s, got %+v, %v", name, h.primary, sel, err)
			}
			continue
		}
		if err != nil || sel.Root != tc.want {
			t.Errorf("%s: got %+v, %v want %s", name, sel, err, tc.want)
		}
	}
}

func TestScopeRouterConcurrentCallsShareOneLoad(t *testing.T) {
	gate := make(chan struct{})
	h := newScopeHarness(t, 0, 0, gate, nil)
	const callers = 8
	var wg sync.WaitGroup
	results := make(chan string, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := h.call(context.Background(), h.wt1)
			if err != nil {
				results <- "error: " + err.Error()
				return
			}
			results <- resultText(got)
		}()
	}
	waitFor(t, func() bool { return h.loads.Load() == 1 })
	close(gate)
	wg.Wait()
	close(results)
	for text := range results {
		if text != "answer from "+h.wt1 {
			t.Errorf("got %q", text)
		}
	}
	if h.loads.Load() != 1 {
		t.Fatalf("loads = %d, want 1", h.loads.Load())
	}
}

func TestScopeRouterDifferentWorktreesGetOwnAnswersAndLoadsAreSerialized(t *testing.T) {
	h := newScopeHarness(t, 4, 0, nil, nil)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		for _, root := range []string{h.primary, h.wt1, h.wt2} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := h.call(context.Background(), root)
				if err != nil || resultText(got) != "answer from "+root {
					t.Errorf("root %s: got %q, %v", root, resultText(got), err)
				}
			}()
		}
	}
	wg.Wait()
	if h.loads.Load() != 3 {
		t.Fatalf("loads = %d, want one per checkout", h.loads.Load())
	}
	if h.maxLoad.Load() != 1 {
		t.Fatalf("concurrent loads = %d, want 1", h.maxLoad.Load())
	}
}

func TestScopeRouterEvictsIdleScopesOnLaterRequestsOnly(t *testing.T) {
	h := newScopeHarness(t, 4, time.Minute, nil, nil)
	ctx := context.Background()
	if _, err := h.call(ctx, h.wt1); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(2 * time.Minute)
	if len(h.router.Loaded()) != 1 {
		t.Fatalf("time passing alone must not evict (no background timer): %v", h.router.Loaded())
	}
	if _, err := h.call(ctx, h.wt2); err != nil {
		t.Fatal(err)
	}
	if loaded := h.router.Loaded(); len(loaded) != 1 || loaded[0] != h.wt2 {
		t.Fatalf("the idle scope must be evicted by the next request: %v", loaded)
	}
	if _, err := h.call(ctx, h.wt1); err != nil || h.loads.Load() != 3 {
		t.Fatalf("an evicted scope reloads: loads=%d err=%v", h.loads.Load(), err)
	}
}

func TestScopeRouterEvictsLeastRecentlyUsedAtCapacity(t *testing.T) {
	h := newScopeHarness(t, 2, time.Hour, nil, nil)
	ctx := context.Background()
	for _, root := range []string{h.primary, h.wt1} {
		if _, err := h.call(ctx, root); err != nil {
			t.Fatal(err)
		}
		h.clock.Advance(time.Second)
	}
	if _, err := h.call(ctx, h.primary); err != nil { // primary is now the most recent
		t.Fatal(err)
	}
	h.clock.Advance(time.Second)
	if _, err := h.call(ctx, h.wt2); err != nil {
		t.Fatal(err)
	}
	loaded := h.router.Loaded()
	if len(loaded) != 2 || loaded[0] == h.wt1 || loaded[1] == h.wt1 {
		t.Fatalf("wt1 was least recently used and must be evicted: %v", loaded)
	}
}

func TestScopeRouterNeverEvictsAScopeWithARunningRequest(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h := newScopeHarness(t, 1, time.Nanosecond, nil, func(root string) ToolHandler {
		return func(context.Context, protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			once.Do(func() { close(entered) })
			<-release
			return protocol.NewToolResultText("answer from " + root), nil
		}
	})
	done := make(chan string, 1)
	go func() {
		got, err := h.call(context.Background(), h.wt1)
		if err != nil {
			done <- "error: " + err.Error()
			return
		}
		done <- resultText(got)
	}()
	<-entered
	h.clock.Advance(time.Hour) // far past the idle TTL; the request is still running
	got, err := h.call(context.Background(), h.wt2)
	if err != nil || !got.IsError || !strings.Contains(resultText(got), "full") {
		t.Fatalf("a full cache of busy scopes must refuse explicitly, got %+v, %v", got, err)
	}
	if loaded := h.router.Loaded(); len(loaded) != 1 || loaded[0] != h.wt1 {
		t.Fatalf("the running scope must stay loaded: %v", loaded)
	}
	close(release)
	if text := <-done; text != "answer from "+h.wt1 {
		t.Fatalf("running request must finish normally, got %q", text)
	}
	if got, err := h.call(context.Background(), h.wt2); err != nil || got.IsError {
		t.Fatalf("once idle, the scope can be replaced: %+v, %v", got, err)
	}
}

func TestScopeRouterCanceledFirstCallerDoesNotPoisonTheLoad(t *testing.T) {
	gate := make(chan struct{})
	h := newScopeHarness(t, 0, 0, gate, nil)
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() {
		_, err := h.call(ctx, h.wt1)
		first <- err
	}()
	waitFor(t, func() bool { return h.loads.Load() == 1 })
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller: %v", err)
	}
	second := make(chan string, 1)
	go func() {
		got, err := h.call(context.Background(), h.wt1)
		if err != nil {
			second <- "error: " + err.Error()
			return
		}
		second <- resultText(got)
	}()
	close(gate)
	if text := <-second; text != "answer from "+h.wt1 {
		t.Fatalf("second caller: %q", text)
	}
	if h.loads.Load() != 1 {
		t.Fatalf("loads = %d, want the canceled caller's load reused", h.loads.Load())
	}
}

func TestScopeRouterNeverEvictsAScopeThatIsStillLoading(t *testing.T) {
	gate := make(chan struct{})
	h := newScopeHarness(t, 1, time.Nanosecond, gate, nil)
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() {
		_, err := h.call(ctx, h.wt1)
		first <- err
	}()
	waitFor(t, func() bool { return h.loads.Load() == 1 })
	cancel()
	<-first // nobody waits on the load any more, and it is far past the idle TTL
	h.clock.Advance(time.Hour)
	got, err := h.call(context.Background(), h.wt2)
	if err != nil || !got.IsError || !strings.Contains(resultText(got), "full") {
		t.Fatalf("a loading scope must keep its slot, got %+v, %v", got, err)
	}
	close(gate)
	waitFor(t, func() bool { return len(h.router.Loaded()) == 1 })
}

func TestScopeRouterLoadFailureIsReportedAndNotCached(t *testing.T) {
	h := newScopeHarness(t, 1, 0, nil, nil)
	boom := errors.New("graph unreadable")
	h.loadErr.Store(&boom)
	got, err := h.call(context.Background(), h.wt1)
	if err != nil || !got.IsError || !strings.Contains(resultText(got), "graph unreadable") || !strings.Contains(resultText(got), h.wt1) {
		t.Fatalf("a failed load must be an explicit error naming the root: %+v, %v", got, err)
	}
	if len(h.router.Loaded()) != 0 {
		t.Fatalf("a failed load must not occupy a slot: %v", h.router.Loaded())
	}
	h.loadErr.Store(nil)
	if got, err := h.call(context.Background(), h.wt1); err != nil || got.IsError {
		t.Fatalf("the next call must retry: %+v, %v", got, err)
	}
}

func TestScopeRouterDecoratesResultsAndEmptyAnswers(t *testing.T) {
	h := newScopeHarness(t, 0, 0, nil, func(string) ToolHandler {
		return func(context.Context, protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			return protocol.NewToolResultText("No results found."), nil
		}
	})
	got, err := h.call(context.Background(), h.wt1)
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(got)
	if !strings.Contains(text, h.wt1) || !strings.Contains(text, h.primary) {
		t.Fatalf("an empty answer must name the checkout and the indexed root: %q", text)
	}
	scope, _ := got.Meta.AdditionalFields["gograph_scope"].(map[string]any)
	if scope["worktree"] != true || scope["scope_root"] != h.wt1 || scope["requested_root"] != h.wt1 {
		t.Fatalf("scope meta: %+v", scope)
	}
}

func TestCallerRoot(t *testing.T) {
	asked, stripped, err := callerRoot(map[string]any{"term": "x", "root": "/a", "cwd": "/a/"})
	if err != nil || asked != "/a" || len(stripped) != 1 || stripped["term"] != "x" {
		t.Fatalf("got %q %v %v", asked, stripped, err)
	}
	if _, _, err := callerRoot(map[string]any{"root": "/a", "cwd": "/b"}); err == nil {
		t.Fatal("conflicting root and cwd must fail")
	}
	if _, _, err := callerRoot(map[string]any{"root": 3}); err == nil {
		t.Fatal("non-string root must fail")
	}
	args := map[string]any{"term": "x"}
	if asked, stripped, err := callerRoot(args); err != nil || asked != "" || len(stripped) != 1 {
		t.Fatalf("no selector: %q %v %v", asked, stripped, err)
	}
	if asked, _, _ := callerRoot(map[string]any{"cwd": "/c"}); asked != "/c" {
		t.Fatalf("cwd alias: %q", asked)
	}
}

func TestNewScopeRouterValidation(t *testing.T) {
	load := func(string) (ScopeHandlers, error) { return nil, nil }
	if _, err := NewScopeRouter(ScopeConfig{Root: t.TempDir()}); err == nil {
		t.Error("missing Load must fail")
	}
	if _, err := NewScopeRouter(ScopeConfig{Root: filepath.Join(t.TempDir(), "gone"), Load: load}); err == nil {
		t.Error("missing root must fail")
	}
	if _, err := NewScopeRouter(ScopeConfig{Root: t.TempDir(), Load: load, MaxScopes: -1}); err == nil {
		t.Error("negative limit must fail")
	}
}

// waitFor polls a condition with a bound; it never orders events by sleeping.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}
