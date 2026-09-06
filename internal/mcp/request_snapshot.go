package mcp

import (
	"context"
	"fmt"

	mcpprotocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/graphstate"
	"github.com/ozgurcd/gograph/internal/search"
)

type requestSnapshotKey struct{}

type requestSnapshot struct {
	graph   *graph.Graph
	state   graphstate.State
	queries *search.Snapshot
}

// snapshotManager serializes the refresh callback, not the subsequent analysis.
// Refresh callbacks publish immutable graph values. A request retains its own
// graph and state even when the next request publishes a different snapshot.
type snapshotManager struct {
	gate               chan struct{}
	refresh            func(context.Context) (*graph.Graph, error)
	state              func() graphstate.State
	currentGraph       *graph.Graph
	currentQueries     *search.Snapshot
	currentFingerprint string
}

func newSnapshotManager(refresh func(context.Context) (*graph.Graph, error), state func() graphstate.State) *snapshotManager {
	return &snapshotManager{gate: make(chan struct{}, 1), refresh: refresh, state: state}
}

func (m *snapshotManager) acquire(ctx context.Context) (requestSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return requestSnapshot{}, err
	}
	select {
	case m.gate <- struct{}{}:
		defer func() { <-m.gate }()
	case <-ctx.Done():
		return requestSnapshot{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return requestSnapshot{}, err
	}
	g, err := m.refresh(ctx)
	if err != nil {
		return requestSnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return requestSnapshot{}, err
	}
	if g == nil {
		return requestSnapshot{}, fmt.Errorf("graph refresh returned no graph")
	}
	if m.currentQueries == nil || m.currentGraph != g {
		queries := search.NewSnapshot(g)
		fingerprint, err := queries.Fingerprint()
		if err != nil {
			return requestSnapshot{}, err
		}
		if m.currentQueries == nil || fingerprint != m.currentFingerprint {
			m.currentQueries = queries
			m.currentFingerprint = fingerprint
		}
		m.currentGraph = g
	}
	if err := ctx.Err(); err != nil {
		return requestSnapshot{}, err
	}
	return requestSnapshot{graph: g, state: m.state(), queries: m.currentQueries}, nil
}

func graphForRequest(ctx context.Context) *graph.Graph {
	return ctx.Value(requestSnapshotKey{}).(requestSnapshot).graph
}

func queriesForRequest(ctx context.Context) *search.Snapshot {
	return ctx.Value(requestSnapshotKey{}).(requestSnapshot).queries
}

func requestRefreshesGraph(name string, request mcpprotocol.CallToolRequest) bool {
	if name == "gograph_changes" {
		args, _ := request.Params.Arguments.(map[string]any)
		ref, _ := args["git_ref"].(string)
		return ref != ""
	}
	return toolRefreshesGraph(name)
}
