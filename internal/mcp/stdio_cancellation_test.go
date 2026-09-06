package mcp

import (
	"context"
	"encoding/json"
	"io"
	"sync/atomic"
	"testing"
	"time"

	protocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/ozgurcd/gograph/internal/graph"
)

// Exercise JSON-RPC notifications/cancelled through the real stdio transport,
// not direct invocation of an exposed handler with an already-canceled context.
func TestStdioCancellationReachesRefreshAndKeepsServerResponsive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	input, inputWriter := io.Pipe()
	outputReader, output := io.Pipe()
	g := &graph.Graph{Root: t.TempDir()}
	started, canceled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	mcpServer := NewServer(g, nil, nil, nil, "stdio-test", ServerOptions{RefreshContext: func(ctx context.Context) (*graph.Graph, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			close(canceled)
			return nil, ctx.Err()
		}
		return g, nil
	}})
	finished := make(chan error, 1)
	go func() { finished <- server.NewStdioServer(mcpServer).Listen(ctx, input, output) }()
	t.Cleanup(func() {
		cancel()
		_ = inputWriter.Close()
		_ = input.Close()
		_ = output.Close()
		_ = outputReader.Close()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("stdio server did not stop")
		}
	})
	type response struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	responses := make(chan response, 10)
	go func() {
		decoder := json.NewDecoder(outputReader)
		for {
			var value response
			if err := decoder.Decode(&value); err != nil {
				return
			}
			select {
			case responses <- value:
			case <-ctx.Done():
				return
			}
		}
	}()
	send := func(value any) {
		t.Helper()
		if err := json.NewEncoder(inputWriter).Encode(value); err != nil {
			t.Fatal(err)
		}
	}
	receive := func(id int) response {
		t.Helper()
		select {
		case value := <-responses:
			if value.ID != id {
				t.Fatalf("got response %d, want %d", value.ID, id)
			}
			return value
		case <-ctx.Done():
			t.Fatalf("timeout waiting for response %d", id)
		}
		return response{}
	}
	send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}}})
	if reply := receive(1); len(reply.Error) != 0 {
		t.Fatalf("initialize: %s", reply.Error)
	}
	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	call := func(id int, name string, args map[string]any) {
		send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	}
	call(2, "gograph_query", map[string]any{"term": "Foo"})
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("refresh did not start")
	}
	call(3, "gograph_capabilities", map[string]any{})
	if reply := receive(3); len(reply.Error) != 0 {
		t.Fatalf("capabilities: %s", reply.Error)
	}
	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": 2, "reason": "no longer needed"}})
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("wire cancellation did not reach refresh")
	}
	var canceledResult protocol.CallToolResult
	reply := receive(2)
	if len(reply.Error) == 0 {
		if err := json.Unmarshal(reply.Result, &canceledResult); err != nil || !canceledResult.IsError {
			t.Fatalf("canceled query reported success: %s, %v", reply.Result, err)
		}
	}
	call(4, "gograph_query", map[string]any{"term": "Foo"})
	reply = receive(4)
	var recovered protocol.CallToolResult
	if err := json.Unmarshal(reply.Result, &recovered); err != nil || len(reply.Error) != 0 || recovered.IsError {
		t.Fatalf("server remained blocked after cancellation: %+v, %v", reply, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("refresh calls = %d", calls.Load())
	}
}
