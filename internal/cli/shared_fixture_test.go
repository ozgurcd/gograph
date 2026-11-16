package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
	mcppkg "github.com/ozgurcd/gograph/internal/mcp"
)

type fixtureHandlers = map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)

// buildAnswersFixture copies testdata/<testdata> into root, persists its
// precise graph, and returns the handlers of an MCP server over that graph.
func buildAnswersFixture(root, testdata, version string) (fixtureHandlers, error) {
	if err := os.CopyFS(root, os.DirFS(filepath.Join("../../testdata", testdata))); err != nil {
		return nil, err
	}
	g, err := buildPreciseGraph(root)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(g)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(root, ".gograph", "graph.json"), data, 0o600); err != nil {
		return nil, err
	}
	// NewServer reads ExposeToolsForTesting only while registering tools.
	previous := mcppkg.ExposeToolsForTesting
	handlers := make(fixtureHandlers)
	mcppkg.ExposeToolsForTesting = handlers
	defer func() { mcppkg.ExposeToolsForTesting = previous }()
	mcppkg.NewServer(g, func() (*graph.Graph, error) { return g, nil }, BuildGraph,
		func(context.Context, string) (*graph.Graph, error) { return g, nil }, version)
	return handlers, nil
}

// freshAnswersFixture builds a private fixture for a test that changes the
// fixture or the build environment it is built under.
func freshAnswersFixture(t *testing.T, testdata, version string) (string, fixtureHandlers) {
	t.Helper()
	root := t.TempDir()
	handlers, err := buildAnswersFixture(root, testdata, version)
	if err != nil {
		t.Fatal(err)
	}
	return root, handlers
}

var sharedFixtures struct {
	sync.Mutex
	dir      string
	fixtures map[string]sharedFixture
}

type sharedFixture struct {
	root     string
	handlers fixtureHandlers
	err      error
}

// sharedAnswersFixture builds one fixture per testdata directory for the life
// of the test process. A precise build takes seconds, and tens of seconds
// under -race, so tests that only read the fixture share it. A test that
// writes into the fixture, or sets environment such as GOFLAGS before
// building it, must use freshAnswersFixture.
func sharedAnswersFixture(t *testing.T, testdata, version string) (string, fixtureHandlers) {
	t.Helper()
	sharedFixtures.Lock()
	defer sharedFixtures.Unlock()
	fixture, ok := sharedFixtures.fixtures[testdata]
	if !ok {
		fixture = newSharedFixture(testdata, version)
		if sharedFixtures.fixtures == nil {
			sharedFixtures.fixtures = make(map[string]sharedFixture)
		}
		sharedFixtures.fixtures[testdata] = fixture
	}
	if fixture.err != nil {
		t.Fatalf("shared %s fixture: %v", testdata, fixture.err)
	}
	return fixture.root, fixture.handlers
}

func newSharedFixture(testdata, version string) sharedFixture {
	if sharedFixtures.dir == "" {
		dir, err := os.MkdirTemp("", "gograph-cli-shared-fixtures-*")
		if err != nil {
			return sharedFixture{err: err}
		}
		sharedFixtures.dir = dir
	}
	root := filepath.Join(sharedFixtures.dir, testdata)
	if err := os.Mkdir(root, 0o755); err != nil {
		return sharedFixture{err: err}
	}
	handlers, err := buildAnswersFixture(root, testdata, version)
	return sharedFixture{root: root, handlers: handlers, err: err}
}

// RemoveSharedTestFixtures deletes the shared fixtures; TestMain calls it.
func RemoveSharedTestFixtures() error {
	sharedFixtures.Lock()
	defer sharedFixtures.Unlock()
	if sharedFixtures.dir == "" {
		return nil
	}
	if err := os.RemoveAll(sharedFixtures.dir); err != nil {
		return fmt.Errorf("remove shared CLI test fixtures: %w", err)
	}
	sharedFixtures.dir = ""
	sharedFixtures.fixtures = nil
	return nil
}
