package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestMCPInterfaceCallerFormsUseSharedResolver(t *testing.T) {
	const (
		interfaceID = "example.com/app/repository::StateRepository"
		memoryID    = "example.com/app/repository::(*MemoryStateRepository).Delete"
		sqlID       = "example.com/app/repository::(*SQLStateRepository).Delete"
		callerID    = "example.com/app/service::Purge"
	)
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: interfaceID, Kind: graph.KindInterface, Name: "StateRepository", PackageName: "repository", InterfaceMethods: map[string]string{"Delete": "func(string) error"}},
			{ID: memoryID, Kind: graph.KindMethod, Name: "Delete", Receiver: "*MemoryStateRepository", PackageName: "repository", MethodSignature: "func(string) error"},
			{ID: sqlID, Kind: graph.KindMethod, Name: "Delete", Receiver: "*SQLStateRepository", PackageName: "repository", MethodSignature: "func(string) error"},
			{ID: callerID, Kind: graph.KindFunction, Name: "Purge", PackageName: "service", File: "service/purge.go", Line: 10},
		},
		Implements: []graph.ImplementsEdge{
			{Interface: "StateRepository", InterfaceID: interfaceID, Concrete: "MemoryStateRepository", ConcreteID: "example.com/app/repository::MemoryStateRepository"},
			{Interface: "StateRepository", InterfaceID: interfaceID, Concrete: "SQLStateRepository", ConcreteID: "example.com/app/repository::SQLStateRepository"},
		},
		Calls: []graph.CallEdge{
			{CallerSymbolID: callerID, CallerName: "Purge", CalleeRaw: "states.Delete", CalleeSymbolID: memoryID, File: "service/purge.go", Line: 12},
			{CallerSymbolID: callerID, CallerName: "Purge", CalleeRaw: "states.Delete", CalleeSymbolID: sqlID, File: "service/purge.go", Line: 12},
		},
	}
	handler := setupHandlers(t, g)["gograph_callers"]
	queries := []string{
		"Delete",
		"MemoryStateRepository.Delete",
		"SQLStateRepository.Delete",
		memoryID,
		sqlID,
		"StateRepository.Delete",
		interfaceID + ".Delete",
	}
	var baseline string
	for _, exact := range []bool{false, true} {
		for _, query := range queries {
			text := callTool(t, handler, map[string]any{"function": query, "exact": exact})
			rows := decodeResultRows(t, text)
			if count := len(rows); count != 1 {
				t.Fatalf("gograph_callers(%q, exact=%v) returned %d caller rows, want one:\n%s", query, exact, count, text)
			}
			if rows[0].Name != "Purge" || rows[0].CallSiteFile != "service/purge.go" || rows[0].CallSiteLine != 12 {
				t.Fatalf("gograph_callers(%q, exact=%v) returned wrong source site:\n%s", query, exact, text)
			}
			if baseline == "" {
				baseline = text
				continue
			}
			if text != baseline {
				t.Fatalf("MCP caller forms disagree for %q (exact=%v):\nbaseline: %s\nactual: %s", query, exact, baseline, text)
			}
		}
	}
}

func TestMCPStatsExposesPersistedPrecision(t *testing.T) {
	g := &graph.Graph{
		Root:  t.TempDir(),
		Build: &graph.BuildMetadata{Precision: graph.PrecisionFallback, Complete: true},
	}
	text := callTool(t, setupHandlers(t, g)["gograph_stats"], nil)
	var stats struct {
		Precision graph.PrecisionMode `json:"precision"`
	}
	if err := json.Unmarshal([]byte(text), &stats); err != nil {
		t.Fatalf("decode gograph_stats: %v\n%s", err, text)
	}
	if stats.Precision != graph.PrecisionFallback {
		t.Fatalf("gograph_stats precision = %q, want %q", stats.Precision, graph.PrecisionFallback)
	}
}
