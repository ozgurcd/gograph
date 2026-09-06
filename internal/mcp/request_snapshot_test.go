package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpprotocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/graphstate"
	"github.com/ozgurcd/gograph/internal/session"
)

func TestSnapshotManagerPinsGraphAndProvenance(t *testing.T) {
	old := &graph.Graph{Build: &graph.BuildMetadata{Precision: graph.PrecisionAST}}
	next := &graph.Graph{Build: &graph.BuildMetadata{Precision: graph.PrecisionPrecise}}
	current := old
	manager := newSnapshotManager(func(context.Context) (*graph.Graph, error) { return current, nil }, func() graphstate.State { return graphstate.ManualPersisted(current, false) })
	first, err := manager.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	oldState := first.state
	current = next
	second, err := manager.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.graph != old || second.graph != next || first.state != oldState || second.state == oldState {
		t.Fatal("request snapshot changed across publication")
	}
}

func TestMCPRefreshFailureStillRecordsSessionTelemetry(t *testing.T) {
	root := t.TempDir()
	id, err := session.StartSessionAt(root, "refresh_failure")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = session.EndSessionAt(root) })
	previous := ExposeToolsForTesting
	handlers := make(map[string]func(context.Context, mcpprotocol.CallToolRequest) (*mcpprotocol.CallToolResult, error))
	ExposeToolsForTesting = handlers
	t.Cleanup(func() { ExposeToolsForTesting = previous })
	NewServer(&graph.Graph{Root: root}, nil, nil, nil, "test", ServerOptions{RefreshContext: func(context.Context) (*graph.Graph, error) { return nil, errors.New("deliberate refresh failure") }})
	request := mcpprotocol.CallToolRequest{}
	request.Params.Arguments = map[string]any{"term": "Foo"}
	result, err := handlers["gograph_query"](context.Background(), request)
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("expected refresh failure: %v %+v", err, result)
	}
	data, err := os.ReadFile(filepath.Join(root, ".gograph", "sessions", "session_"+id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"command":"query"`) || !strings.Contains(string(data), `"status":"failure"`) {
		t.Fatalf("refresh failure missing from telemetry: %s", data)
	}
}

func TestSnapshotManagerCanceledWaitDoesNotRefresh(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	manager := newSnapshotManager(func(context.Context) (*graph.Graph, error) {
		close(started)
		<-release
		return &graph.Graph{}, nil
	}, func() graphstate.State { return graphstate.State{} })
	finished := make(chan error, 1)
	go func() { _, err := manager.acquire(context.Background()); finished <- err }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotManagerCacheIsBoundToContentAndOnlyRetainsCurrent(t *testing.T) {
	current := &graph.Graph{Symbols: []graph.SymbolNode{{ID: "example.com/app::Foo", Name: "Foo", Kind: graph.KindFunction}}}
	manager := newSnapshotManager(func(context.Context) (*graph.Graph, error) { return current, nil }, func() graphstate.State { return graphstate.State{} })
	first, err := manager.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	copyOfGraph := *current
	current = &copyOfGraph
	second, err := manager.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.queries != second.queries {
		t.Fatal("identical fingerprint did not reuse derived indexes")
	}
	current = &graph.Graph{Symbols: []graph.SymbolNode{{ID: "example.com/app::Bar", Name: "Bar", Kind: graph.KindFunction}}}
	third, err := manager.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if third.queries == first.queries || manager.currentQueries != third.queries || first.graph.Symbols[0].Name != "Foo" {
		t.Fatal("refresh failed to replace cache or invalidated a live old snapshot")
	}
}

func TestMCPBlockedRefreshDoesNotBlockCapabilitiesAndReceivesCancellation(t *testing.T) {
	previous := ExposeToolsForTesting
	handlers := make(map[string]func(context.Context, mcpprotocol.CallToolRequest) (*mcpprotocol.CallToolResult, error))
	ExposeToolsForTesting = handlers
	t.Cleanup(func() { ExposeToolsForTesting = previous })
	started := make(chan struct{})
	NewServer(&graph.Graph{}, nil, nil, nil, "test", ServerOptions{RefreshContext: func(ctx context.Context) (*graph.Graph, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		request := mcpprotocol.CallToolRequest{}
		request.Params.Arguments = map[string]any{"term": "Foo"}
		_, _ = handlers["gograph_query"](ctx, request)
	}()
	<-started
	ready := make(chan error, 1)
	go func() {
		_, err := handlers["gograph_capabilities"](context.Background(), mcpprotocol.CallToolRequest{})
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("capabilities blocked behind unrelated refresh")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh ignored request cancellation")
	}
}
