package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests drive the real binary over stdio, so they also prove the startup
// behaviour (nothing loads until the first request) that unit tests cannot.

var (
	scopeBinaryOnce sync.Once
	scopeBinaryPath string
	scopeBinaryErr  error
)

func scopeTestBinary(t *testing.T) string {
	t.Helper()
	// GOGRAPH_SCOPE_TEST_BINARY runs these tests against another build, which is
	// how the RED run against the pre-change release is reproduced.
	if override := os.Getenv("GOGRAPH_SCOPE_TEST_BINARY"); override != "" {
		return override
	}
	scopeBinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "gograph-scope-bin-")
		if err != nil {
			scopeBinaryErr = err
			return
		}
		scopeBinaryPath = filepath.Join(dir, "gograph")
		out, err := exec.Command("go", "build", "-o", scopeBinaryPath, "../../cmd/gograph").CombinedOutput()
		if err != nil {
			scopeBinaryErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if scopeBinaryErr != nil {
		t.Fatal(scopeBinaryErr)
	}
	return scopeBinaryPath
}

func scopeGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func scopeWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type scopeFixture struct{ Main, Wt1, Wt2, Other, Parent string }

// newScopeFixture makes a repository with two linked worktrees that each define
// one symbol the others lack, and an unrelated repository.
func newScopeFixture(t *testing.T) scopeFixture {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := scopeFixture{Parent: parent, Main: filepath.Join(parent, "main"), Wt1: filepath.Join(parent, "wt1"), Wt2: filepath.Join(parent, "wt2"), Other: filepath.Join(parent, "other")}
	scopeWrite(t, filepath.Join(f.Main, "go.mod"), "module example.com/m\n\ngo 1.21\n")
	scopeWrite(t, filepath.Join(f.Main, "a.go"), "package m\n\nfunc MainOnly() {}\n")
	scopeGit(t, f.Main, "init", "-q", "-b", "main")
	scopeGit(t, f.Main, "add", "-A")
	scopeGit(t, f.Main, "commit", "-q", "-m", "init")
	scopeGit(t, f.Main, "worktree", "add", "-q", "-b", "b1", f.Wt1)
	scopeGit(t, f.Main, "worktree", "add", "-q", "-b", "b2", f.Wt2)
	scopeWrite(t, filepath.Join(f.Wt1, "sub", "b.go"), "package sub\n\nfunc Wt1Only() {}\n")
	scopeWrite(t, filepath.Join(f.Wt2, "c.go"), "package m\n\nfunc Wt2Only() {}\n")
	scopeWrite(t, filepath.Join(f.Other, "go.mod"), "module example.com/other\n\ngo 1.21\n")
	scopeWrite(t, filepath.Join(f.Other, "o.go"), "package other\n\nfunc OtherOnly() {}\n")
	scopeGit(t, f.Other, "init", "-q", "-b", "main")
	return f
}

type scopeReply struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

type scopeClient struct {
	t       *testing.T
	cmd     *exec.Cmd
	in      *json.Encoder
	replies chan scopeReply
	stderr  *safeBuffer
	nextID  int
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startScopeServer runs `gograph mcp <root> <extra...>`. clientRoots, when
// non-nil, makes the client advertise roots and answer roots/list with them.
func startScopeServer(t *testing.T, root string, clientRoots []string, extra ...string) *scopeClient {
	t.Helper()
	return startScopeServerEnv(t, root, clientRoots, nil, extra...)
}

// startScopeServerEnv is startScopeServer with extra NAME=VALUE entries in the
// server's environment (appended to the inherited one).
func startScopeServerEnv(t *testing.T, root string, clientRoots, env []string, extra ...string) *scopeClient {
	t.Helper()
	cmd := exec.Command(scopeTestBinary(t), append([]string{"mcp", root}, extra...)...)
	cmd.Dir = t.TempDir()
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c := &scopeClient{t: t, cmd: cmd, in: json.NewEncoder(stdin), replies: make(chan scopeReply, 64), stderr: &safeBuffer{}}
	cmd.Stderr = c.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1<<20), 64<<20)
		for scanner.Scan() {
			var reply scopeReply
			if json.Unmarshal(scanner.Bytes(), &reply) != nil {
				continue
			}
			if reply.Method == "roots/list" {
				roots := []map[string]string{}
				for _, r := range clientRoots {
					roots = append(roots, map[string]string{"uri": "file://" + r})
				}
				_ = c.in.Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(reply.ID), "result": map[string]any{"roots": roots}})
				continue
			}
			c.replies <- reply
		}
		close(c.replies)
	}()
	t.Cleanup(func() {
		_ = stdin.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	caps := map[string]any{}
	if clientRoots != nil {
		caps["roots"] = map[string]any{}
	}
	c.request("initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": caps, "clientInfo": map[string]any{"name": "t", "version": "1"}})
	if err := c.in.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		t.Fatal(err)
	}
	return c
}

func (c *scopeClient) request(method string, params any) scopeReply {
	c.t.Helper()
	c.nextID++
	if err := c.in.Encode(map[string]any{"jsonrpc": "2.0", "id": c.nextID, "method": method, "params": params}); err != nil {
		c.t.Fatal(err)
	}
	for {
		select {
		case reply, ok := <-c.replies:
			if !ok {
				c.t.Fatalf("server exited; stderr:\n%s", c.stderr.String())
			}
			if string(reply.ID) == fmt.Sprint(c.nextID) {
				return reply
			}
		case <-time.After(3 * time.Minute):
			c.t.Fatalf("timeout waiting for %s; stderr:\n%s", method, c.stderr.String())
		}
	}
}

type scopeToolResult struct {
	Text    string
	IsError bool
	Meta    map[string]any
}

func (c *scopeClient) call(tool string, args map[string]any) scopeToolResult {
	c.t.Helper()
	reply := c.request("tools/call", map[string]any{"name": tool, "arguments": args})
	if len(reply.Error) != 0 {
		c.t.Fatalf("%s: rpc error %s", tool, reply.Error)
	}
	var body struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool           `json:"isError"`
		Meta    map[string]any `json:"_meta"`
	}
	if err := json.Unmarshal(reply.Result, &body); err != nil {
		c.t.Fatal(err)
	}
	var text []string
	for _, part := range body.Content {
		text = append(text, part.Text)
	}
	return scopeToolResult{Text: strings.Join(text, "\n"), IsError: body.IsError, Meta: body.Meta}
}

func (c *scopeClient) query(root, term string) scopeToolResult {
	args := map[string]any{"term": term}
	if root != "" {
		args["root"] = root
	}
	return c.call("gograph_query", args)
}

func TestMCPScopeWorktreeAnswersFromCallerWorktree(t *testing.T) {
	f := newScopeFixture(t)
	c := startScopeServer(t, f.Main, nil)

	if got := c.query(f.Wt1, "Wt1Only"); got.IsError || !strings.Contains(got.Text, "Wt1Only") {
		t.Fatalf("worktree symbol not found with root=wt1: %+v", got)
	}
	if got := c.query(f.Wt2, "Wt2Only"); got.IsError || !strings.Contains(got.Text, "Wt2Only") {
		t.Fatalf("worktree symbol not found with root=wt2: %+v", got)
	}
	// Each worktree answers from its own graph, never another checkout's.
	if got := c.query(f.Wt2, "Wt1Only"); strings.Contains(got.Text, "b.go") {
		t.Fatalf("wt2 answered with wt1 symbol: %+v", got)
	}
	if got := c.query(f.Wt1, "MainOnly"); got.IsError || !strings.Contains(got.Text, "MainOnly") {
		t.Fatalf("file shared with main must resolve in wt1: %+v", got)
	}
	// A subdirectory of a worktree (a caller's cwd) selects that worktree; cwd is an alias.
	sub := c.call("gograph_query", map[string]any{"term": "Wt1Only", "cwd": filepath.Join(f.Wt1, "sub")})
	if sub.IsError || !strings.Contains(sub.Text, "Wt1Only") {
		t.Fatalf("subdirectory of wt1 must select wt1: %+v", sub)
	}
	// No selector: the configured checkout answers, and the result says so.
	main := c.query("", "Wt1Only")
	if strings.Contains(main.Text, "b.go") {
		t.Fatalf("main checkout answered with a worktree-only symbol: %+v", main)
	}
	scope, _ := main.Meta["gograph_scope"].(map[string]any)
	if scope["indexed_root"] != f.Main || scope["scope_root"] != f.Main || scope["selected_by"] != "default" {
		t.Fatalf("result must name the checkout that answered: %+v", main.Meta)
	}
	// An empty answer names the checkout it was empty for.
	if !strings.Contains(main.Text, f.Main) {
		t.Fatalf("empty answer must carry the indexed root %s: %q", f.Main, main.Text)
	}
}

func TestMCPScopeRefusesRootsOutsideTheRepository(t *testing.T) {
	f := newScopeFixture(t)
	c := startScopeServer(t, f.Main, nil)
	link := filepath.Join(f.Parent, "link-to-other")
	if err := os.Symlink(f.Other, link); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(f.Main, "a.go")
	for name, root := range map[string]string{
		"unrelated repository": f.Other,
		"symlink to other":     link,
		"parent directory":     f.Parent,
		"missing directory":    filepath.Join(f.Parent, "missing"),
		"relative path":        "wt1",
		"a file":               inside,
		"sibling prefix":       f.Main + "-extra",
	} {
		got := c.query(root, "OtherOnly")
		if !got.IsError {
			t.Errorf("%s: want refusal, got %+v", name, got)
			continue
		}
		if !strings.Contains(got.Text, f.Main) || !strings.Contains(got.Text, root) {
			t.Errorf("%s: refusal must name indexed root %s and asked root %s: %q", name, f.Main, root, got.Text)
		}
	}
	// The refusal covers every tool, not only query.
	if got := c.call("gograph_stats", map[string]any{"root": f.Other}); !got.IsError {
		t.Errorf("stats for another repository must be refused: %+v", got)
	}
	if got := c.call("gograph_query", map[string]any{"term": "x", "root": f.Wt1, "cwd": f.Wt2}); !got.IsError {
		t.Errorf("conflicting root and cwd must be refused: %+v", got)
	}
}

// A server started by a git hook, a rebase --exec or a wrapper can inherit
// GIT_DIR and friends. They must not make another repository's worktrees valid.
func TestMCPScopeIgnoresGitDiscoveryEnvironment(t *testing.T) {
	f := newScopeFixture(t)
	foreign := filepath.Join(f.Parent, "foreign")
	foreignWt := filepath.Join(f.Parent, "foreign-wt")
	scopeWrite(t, filepath.Join(foreign, "go.mod"), "module example.com/foreign\n\ngo 1.21\n")
	scopeWrite(t, filepath.Join(foreign, "a.go"), "package foreign\n\nfunc ForeignBase() {}\n")
	scopeGit(t, foreign, "init", "-q", "-b", "main")
	scopeGit(t, foreign, "add", "-A")
	scopeGit(t, foreign, "commit", "-q", "-m", "init")
	scopeGit(t, foreign, "worktree", "add", "-q", "-b", "fb", foreignWt)
	scopeWrite(t, filepath.Join(foreignWt, "b.go"), "package foreign\n\nfunc ForeignOnly() {}\n")
	for name, env := range map[string][]string{
		"GIT_DIR":        {"GIT_DIR=" + filepath.Join(foreign, ".git")},
		"GIT_WORK_TREE":  {"GIT_DIR=" + filepath.Join(foreign, ".git"), "GIT_WORK_TREE=" + foreign},
		"GIT_COMMON_DIR": {"GIT_COMMON_DIR=" + filepath.Join(foreign, ".git")},
	} {
		t.Run(name, func(t *testing.T) {
			c := startScopeServerEnv(t, f.Main, nil, env)
			if got := c.query(foreignWt, "ForeignOnly"); !got.IsError || strings.Contains(got.Text, "ForeignOnly") {
				t.Fatalf("a worktree of another repository must be refused, got %+v", got)
			}
			if got := c.query(f.Wt1, "Wt1Only"); got.IsError || !strings.Contains(got.Text, "Wt1Only") {
				t.Fatalf("the server's own worktree must still be answered: %+v", got)
			}
		})
	}
}

func TestMCPScopeUsesClientRoots(t *testing.T) {
	f := newScopeFixture(t)

	c := startScopeServer(t, f.Main, []string{f.Wt1})
	if got := c.query("", "Wt1Only"); got.IsError || !strings.Contains(got.Text, "Wt1Only") {
		t.Fatalf("client root wt1 must select wt1: %+v", got)
	}
	// A workspace folder that merely contains the repository says nothing.
	c = startScopeServer(t, f.Main, []string{f.Parent})
	if got := c.query("", "MainOnly"); got.IsError || !strings.Contains(got.Text, "MainOnly") {
		t.Fatalf("workspace-folder root must fall back to the configured checkout: %+v", got)
	}
	// A client rooted in another repository must not get this repository's answer.
	c = startScopeServer(t, f.Main, []string{f.Other})
	if got := c.query("", "OtherOnly"); !got.IsError || !strings.Contains(got.Text, f.Other) || !strings.Contains(got.Text, f.Main) {
		t.Fatalf("foreign client root must be refused naming both roots: %+v", got)
	}
	// An explicit argument wins over client roots.
	c = startScopeServer(t, f.Main, []string{f.Wt1})
	if got := c.query(f.Wt2, "Wt2Only"); got.IsError || !strings.Contains(got.Text, "Wt2Only") {
		t.Fatalf("root argument must beat client roots: %+v", got)
	}
}

func TestMCPScopeIdleServerLoadsNoGraph(t *testing.T) {
	f := newScopeFixture(t)
	c := startScopeServer(t, f.Main, nil)
	c.request("tools/list", map[string]any{})
	if got := c.call("gograph_capabilities", map[string]any{}); got.IsError || !strings.Contains(got.Text, `"router"`) {
		t.Fatalf("capabilities must work without a graph and describe the scope policy: %+v", got)
	}
	if strings.Contains(c.stderr.String(), "building automatically") {
		t.Fatalf("startup, tools/list and capabilities must not build a graph; stderr:\n%s", c.stderr.String())
	}
	if _, err := os.Stat(filepath.Join(f.Main, ".gograph")); err == nil {
		t.Fatalf("an idle server must not create .gograph")
	}
	if got := c.query("", "MainOnly"); got.IsError {
		t.Fatalf("first query: %+v", got)
	}
	if !strings.Contains(c.stderr.String(), "building automatically") {
		t.Fatalf("the first query must load the graph; stderr:\n%s", c.stderr.String())
	}
}

func TestMCPScopeFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--max-scopes=0"}, {"--max-scopes", "x"}, {"--max-scopes=65"}, {"--max-scopes"},
		{"--scope-idle-ttl=0s"}, {"--scope-idle-ttl=abc"}, {"--scope-idle-ttl"},
	} {
		if _, err := parseMCPArgs(args); err == nil {
			t.Errorf("%v: want an error", args)
		}
	}
	options, err := parseMCPArgs([]string{"--max-scopes", "2", "--scope-idle-ttl=90s", "/repo"})
	if err != nil || options.MaxScopes != 2 || options.ScopeIdleTTL != 90*time.Second || options.Root != "/repo" {
		t.Fatalf("got %+v, %v", options, err)
	}
}
