package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ozgurcd/gograph/internal/graph"

	protocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	// DefaultMaxScopes bounds the graphs one server keeps in memory: the
	// configured repository plus its linked worktrees.
	DefaultMaxScopes = 4
	// DefaultScopeIdleTTL is how long an unused scope stays loaded. Eviction runs
	// on requests only; an idle server never wakes up.
	DefaultScopeIdleTTL = 10 * time.Minute

	worktreeListTTL     = 10 * time.Second
	worktreeListTimeout = 5 * time.Second
	clientRootsTimeout  = 2 * time.Second
)

// ToolHandler is the handler shape registered on the MCP server.
type ToolHandler = func(context.Context, protocol.CallToolRequest) (*protocol.CallToolResult, error)

// ScopeHandlers are the tool handlers of one fully built server for one root.
type ScopeHandlers map[string]ToolHandler

// HandlersOf returns the tool handlers registered on a server.
func HandlersOf(s *server.MCPServer) ScopeHandlers {
	handlers := ScopeHandlers{}
	for name, tool := range s.ListTools() {
		handlers[name] = tool.Handler
	}
	return handlers
}

// NewHandlers builds a complete single-graph server and returns its handlers.
func NewHandlers(
	g *graph.Graph,
	rebuild func() (*graph.Graph, error),
	buildGraph func(string) (*graph.Graph, error),
	buildBaseline func(context.Context, string) (*graph.Graph, error),
	version string,
	options ...ServerOptions,
) ScopeHandlers {
	return HandlersOf(NewServer(g, rebuild, buildGraph, buildBaseline, version, options...))
}

// OutOfScopeError is returned for a root that is neither the configured
// repository nor one of its git worktrees.
type OutOfScopeError struct {
	Indexed   string
	Asked     string
	Worktrees []string
	Reason    string
}

func (e *OutOfScopeError) Error() string {
	known := "no linked worktrees"
	if len(e.Worktrees) > 0 {
		known = "linked worktrees: " + strings.Join(e.Worktrees, ", ")
	}
	reason := ""
	if e.Reason != "" {
		reason = " (" + e.Reason + ")"
	}
	return fmt.Sprintf("this gograph server indexes %q (%s); the requested root %q is outside it%s. Nothing was answered. Use the gograph MCP server configured for that repository, or run the gograph CLI inside that directory.", e.Indexed, known, e.Asked, reason)
}

// ScopeConfig configures a ScopeRouter. It is immutable after NewScopeRouter.
type ScopeConfig struct {
	// Root is the configured repository checkout.
	Root string
	// MaxScopes bounds loaded plus loading scopes; 0 selects DefaultMaxScopes.
	MaxScopes int
	// IdleTTL evicts scopes unused for this long; 0 selects DefaultScopeIdleTTL.
	IdleTTL time.Duration
	// Load builds the full tool set for one root. It runs at most once at a time
	// across the router and never inside the router lock.
	Load func(root string) (ScopeHandlers, error)
	// Worktrees lists checkout roots of the repository at root. Nil uses git.
	Worktrees func(ctx context.Context, root string) ([]string, error)
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

type scopeEntry struct {
	root     string
	ready    chan struct{} // closed once handlers or err is set
	handlers ScopeHandlers // written before ready closes
	err      error         // written before ready closes
	loaded   bool          // guarded by ScopeRouter.mu; a loading scope is never evicted
	inflight int           // guarded by ScopeRouter.mu
	lastUsed time.Time     // guarded by ScopeRouter.mu
}

// ScopeRouter answers each request from the graph of the checkout the caller
// names. Graphs load lazily, are bounded, and are evicted when idle.
type ScopeRouter struct {
	cfg      ScopeConfig
	primary  string
	loadGate chan struct{}

	mu        sync.Mutex
	scopes    map[string]*scopeEntry
	listed    []string
	listedAt  time.Time
	listedErr error
}

// NewScopeRouter validates cfg and returns a router. It reads no graph and
// starts no goroutine.
func NewScopeRouter(cfg ScopeConfig) (*ScopeRouter, error) {
	if cfg.Load == nil {
		return nil, errors.New("scope router needs a Load function")
	}
	if cfg.MaxScopes < 0 || cfg.IdleTTL < 0 {
		return nil, errors.New("scope limits must not be negative")
	}
	if cfg.MaxScopes == 0 {
		cfg.MaxScopes = DefaultMaxScopes
	}
	if cfg.IdleTTL == 0 {
		cfg.IdleTTL = DefaultScopeIdleTTL
	}
	if cfg.Worktrees == nil {
		cfg.Worktrees = gitWorktreeRoots
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	primary, err := canonicalDir(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("scope root: %w", err)
	}
	return &ScopeRouter{cfg: cfg, primary: primary, loadGate: make(chan struct{}, 1), scopes: map[string]*scopeEntry{}}, nil
}

// Primary is the configured repository root.
func (r *ScopeRouter) Primary() string { return r.primary }

// MaxScopes reports the effective scope limit.
func (r *ScopeRouter) MaxScopes() int { return r.cfg.MaxScopes }

// IdleTTL reports the effective idle eviction time.
func (r *ScopeRouter) IdleTTL() time.Duration { return r.cfg.IdleTTL }

// Selection says which checkout answers a request and why.
type Selection struct {
	Root       string
	Asked      string
	SelectedBy string // default, argument or client_roots
}

// Resolve maps a caller-supplied path to a checkout: the configured root, or a
// git worktree of it. A subdirectory resolves to its checkout. Anything else is
// an *OutOfScopeError; the primary is never a silent fallback for a path.
func (r *ScopeRouter) Resolve(ctx context.Context, asked string) (Selection, error) {
	if asked == "" {
		return Selection{Root: r.primary, SelectedBy: "default"}, nil
	}
	root, err := r.match(ctx, asked)
	if err != nil {
		return Selection{}, err
	}
	return Selection{Root: root, Asked: asked, SelectedBy: "argument"}, nil
}

// ResolveClientRoots picks a checkout from the client's MCP roots. A root inside
// a known checkout selects it. A root that only contains the configured
// repository (a workspace folder) says nothing. If every informative root is
// elsewhere the request is refused rather than answered from the wrong repo.
func (r *ScopeRouter) ResolveClientRoots(ctx context.Context, roots []string) (Selection, error) {
	var foreign []string
	for _, asked := range roots {
		root, err := r.match(ctx, asked)
		if err == nil {
			return Selection{Root: root, Asked: asked, SelectedBy: "client_roots"}, nil
		}
		var scopeErr *OutOfScopeError
		if !errors.As(err, &scopeErr) {
			return Selection{}, err
		}
		if canonical, cerr := canonicalDir(asked); cerr == nil && within(canonical, r.primary) {
			continue
		}
		foreign = append(foreign, asked)
	}
	if len(foreign) > 0 {
		return Selection{}, r.outOfScope(ctx, strings.Join(foreign, ", "), "client roots")
	}
	return Selection{Root: r.primary, SelectedBy: "default"}, nil
}

func (r *ScopeRouter) outOfScope(ctx context.Context, asked, reason string) *OutOfScopeError {
	worktrees, _ := r.checkouts(ctx, false)
	return &OutOfScopeError{Indexed: r.primary, Asked: asked, Worktrees: worktrees, Reason: reason}
}

func (r *ScopeRouter) match(ctx context.Context, asked string) (string, error) {
	if !filepath.IsAbs(asked) {
		return "", r.outOfScope(ctx, asked, "root must be an absolute path")
	}
	canonical, err := canonicalDir(asked)
	if err != nil {
		return "", r.outOfScope(ctx, asked, "not an existing directory")
	}
	if canonical == r.primary {
		return r.primary, nil
	}
	for _, refresh := range []bool{false, true} {
		checkouts, err := r.checkouts(ctx, refresh)
		if err != nil {
			return "", r.outOfScope(ctx, asked, "git worktree list failed: "+err.Error())
		}
		best := ""
		for _, candidate := range append([]string{r.primary}, checkouts...) {
			if within(candidate, canonical) && len(candidate) > len(best) {
				best = candidate
			}
		}
		if best != "" {
			return best, nil
		}
	}
	return "", r.outOfScope(ctx, asked, "")
}

// checkouts returns canonical worktree roots other than the primary. The list is
// cached briefly; refresh forces a new git call so a worktree created a moment
// ago is still found.
func (r *ScopeRouter) checkouts(ctx context.Context, refresh bool) ([]string, error) {
	r.mu.Lock()
	if !refresh && !r.listedAt.IsZero() && r.cfg.Now().Sub(r.listedAt) < worktreeListTTL {
		listed, listedErr := r.listed, r.listedErr
		r.mu.Unlock()
		return listed, listedErr
	}
	r.mu.Unlock()
	raw, err := r.cfg.Worktrees(ctx, r.primary)
	var listed []string
	if err == nil {
		seen := map[string]bool{r.primary: true}
		for _, path := range raw {
			canonical, cerr := canonicalDir(path)
			if cerr != nil || seen[canonical] {
				continue
			}
			seen[canonical] = true
			listed = append(listed, canonical)
		}
		sort.Strings(listed)
	}
	r.mu.Lock()
	r.listed, r.listedErr, r.listedAt = listed, err, r.cfg.Now()
	r.mu.Unlock()
	return listed, err
}

// Call runs one tool against the checkout selected by sel.
func (r *ScopeRouter) Call(ctx context.Context, sel Selection, tool string, req protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	entry, err := r.acquire(ctx, sel.Root)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return protocol.NewToolResultError(err.Error()), nil
	}
	defer r.release(entry)
	handler := entry.handlers[tool]
	if handler == nil {
		return protocol.NewToolResultError(fmt.Sprintf("unknown tool %q", tool)), nil
	}
	result, err := handler(ctx, req)
	if result != nil && !result.IsError {
		r.decorate(result, sel)
	}
	return result, err
}

func (r *ScopeRouter) acquire(ctx context.Context, root string) (*scopeEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	entry, found := r.scopes[root]
	if !found {
		r.evictLocked()
		if len(r.scopes) >= r.cfg.MaxScopes {
			n := len(r.scopes)
			r.mu.Unlock()
			return nil, fmt.Errorf("gograph scope cache is full: %d checkouts of %q are in use (limit %d, --max-scopes); retry shortly", n, r.primary, r.cfg.MaxScopes)
		}
		entry = &scopeEntry{root: root, ready: make(chan struct{})}
		r.scopes[root] = entry
		go r.load(entry)
	}
	entry.inflight++
	entry.lastUsed = r.cfg.Now()
	r.mu.Unlock()
	select {
	case <-entry.ready:
	case <-ctx.Done():
		r.release(entry)
		return nil, ctx.Err()
	}
	if entry.err != nil {
		r.release(entry)
		return nil, fmt.Errorf("loading graph for %q: %w", root, entry.err)
	}
	return entry, nil
}

// load builds one scope off the request goroutines so a canceled request never
// poisons it. Loads are serialized to bound CPU and memory spikes.
func (r *ScopeRouter) load(entry *scopeEntry) {
	r.loadGate <- struct{}{}
	handlers, err := r.cfg.Load(entry.root)
	<-r.loadGate
	r.mu.Lock()
	entry.handlers, entry.err, entry.loaded = handlers, err, true
	if err != nil && r.scopes[entry.root] == entry {
		delete(r.scopes, entry.root)
	}
	r.mu.Unlock()
	close(entry.ready)
}

func (r *ScopeRouter) release(entry *scopeEntry) {
	r.mu.Lock()
	entry.inflight--
	entry.lastUsed = r.cfg.Now()
	r.mu.Unlock()
}

// evictLocked drops scopes idle past the TTL, then the least recently used idle
// scope while the cache is at its limit. A scope with a request running is never
// dropped. Callers hold r.mu.
func (r *ScopeRouter) evictLocked() {
	now := r.cfg.Now()
	for root, entry := range r.scopes {
		if entry.loaded && entry.inflight == 0 && now.Sub(entry.lastUsed) > r.cfg.IdleTTL {
			delete(r.scopes, root)
		}
	}
	for len(r.scopes) >= r.cfg.MaxScopes {
		var oldest *scopeEntry
		for _, entry := range r.scopes {
			if entry.loaded && entry.inflight == 0 && (oldest == nil || entry.lastUsed.Before(oldest.lastUsed)) {
				oldest = entry
			}
		}
		if oldest == nil {
			return
		}
		delete(r.scopes, oldest.root)
	}
}

// Loaded lists the checkouts currently held, for capabilities output.
func (r *ScopeRouter) Loaded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	roots := make([]string, 0, len(r.scopes))
	for root := range r.scopes {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	return roots
}

func (r *ScopeRouter) decorate(result *protocol.CallToolResult, sel Selection) {
	scope := map[string]any{
		"indexed_root": r.primary,
		"scope_root":   sel.Root,
		"worktree":     sel.Root != r.primary,
		"selected_by":  sel.SelectedBy,
	}
	if sel.Asked != "" {
		scope["requested_root"] = sel.Asked
	}
	if result.Meta == nil {
		result.Meta = protocol.NewMetaFromMap(map[string]any{"gograph_scope": scope})
	} else {
		if result.Meta.AdditionalFields == nil {
			result.Meta.AdditionalFields = map[string]any{}
		}
		result.Meta.AdditionalFields["gograph_scope"] = scope
	}
	// An empty answer must say which checkout it was empty for.
	if len(result.Content) == 1 {
		if text, ok := result.Content[0].(protocol.TextContent); ok && text.Text == "No results found." {
			result.Content[0] = protocol.NewTextContent(fmt.Sprintf("No results found in %s (selected by %s; server indexes %s).", sel.Root, sel.SelectedBy, r.primary))
		}
	}
}

// scopeCapabilities describes the checkout policy for the capabilities tool.
// It reads no graph, so calling capabilities never loads one.
func scopeCapabilities(r *ScopeRouter) map[string]any {
	if r == nil {
		return map[string]any{"mode": "single_graph", "note": "answers come from the one graph loaded at startup; root/cwd arguments are not accepted"}
	}
	return map[string]any{
		"mode":         "router",
		"indexed_root": r.primary,
		"selection":    "tool argument root (alias cwd), else the client's MCP roots, else indexed_root; the path must be indexed_root or one of its git worktrees (subdirectories resolve to their checkout), anything else is refused",
		"loaded":       r.Loaded(),
		"max_scopes":   r.cfg.MaxScopes,
		"idle_ttl":     r.cfg.IdleTTL.String(),
		"loading":      "a checkout's graph loads on its first request; idle checkouts are evicted on later requests, never by a background timer",
	}
}

// route selects the checkout for a request and either answers it from that
// checkout's server (local=false) or reports local=true when the caller should
// run its own handler with the returned arguments. A refusal is answered here.
func (r *ScopeRouter) route(ctx context.Context, s *server.MCPServer, tool string, req protocol.CallToolRequest) (result *protocol.CallToolResult, local bool, err error) {
	args, _ := req.Params.Arguments.(map[string]any)
	asked, stripped, argErr := callerRoot(args)
	if argErr != nil {
		return protocol.NewToolResultError(argErr.Error()), false, nil
	}
	var sel Selection
	if asked != "" {
		sel, err = r.Resolve(ctx, asked)
	} else if roots := clientRoots(ctx, s); len(roots) > 0 {
		sel, err = r.ResolveClientRoots(ctx, roots)
	} else {
		sel = Selection{Root: r.primary, SelectedBy: "default"}
	}
	if err != nil {
		return protocol.NewToolResultError(err.Error()), false, nil
	}
	if stripped != nil {
		req.Params.Arguments = stripped
	}
	if tool == "gograph_capabilities" {
		return nil, true, nil
	}
	result, err = r.Call(ctx, sel, tool, req)
	return result, false, err
}

// clientRoots asks the client for its MCP roots when it declared the capability.
// Any failure means "unknown", never a guess.
func clientRoots(ctx context.Context, s *server.MCPServer) []string {
	withInfo, ok := server.ClientSessionFromContext(ctx).(server.SessionWithClientInfo)
	if !ok || withInfo.GetClientCapabilities().Roots == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, clientRootsTimeout)
	defer cancel()
	listed, err := s.RequestRoots(ctx, protocol.ListRootsRequest{})
	if err != nil || listed == nil {
		return nil
	}
	var roots []string
	for _, root := range listed.Roots {
		if u, err := url.Parse(root.URI); err == nil && u.Scheme == "file" && u.Path != "" {
			roots = append(roots, u.Path)
		}
	}
	return roots
}

// gitWorktreeRoots lists every checkout of the repository at root. A directory
// that is not a git checkout has only itself.
func gitWorktreeRoots(ctx context.Context, root string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, worktreeListTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "worktree", "list", "--porcelain")
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, nil
	}
	var roots []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok && filepath.IsAbs(path) {
			roots = append(roots, path)
		}
	}
	return roots, nil
}

func canonicalDir(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", path)
	}
	return resolved, nil
}

// within reports whether path is root or lies beneath it.
func within(root, path string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator))
}

// callerRoot extracts and removes the root/cwd selector from tool arguments.
// Both may be given only if they name the same path.
func callerRoot(args map[string]any) (asked string, stripped map[string]any, err error) {
	root, hasRoot := args["root"]
	cwd, hasCwd := args["cwd"]
	if !hasRoot && !hasCwd {
		return "", args, nil
	}
	stripped = make(map[string]any, len(args))
	for key, value := range args {
		if key != "root" && key != "cwd" {
			stripped[key] = value
		}
	}
	rootText, rootOK := root.(string)
	cwdText, cwdOK := cwd.(string)
	if (hasRoot && !rootOK) || (hasCwd && !cwdOK) {
		return "", nil, errors.New("root and cwd must be strings")
	}
	if hasRoot && hasCwd && filepath.Clean(rootText) != filepath.Clean(cwdText) {
		return "", nil, errors.New("root and cwd name different paths; pass only one")
	}
	if hasRoot {
		return rootText, stripped, nil
	}
	return cwdText, stripped, nil
}
