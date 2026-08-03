package search_test

import (
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestReachableOrphans(t *testing.T) {
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			// Public package exported function: should be treated as root
			{ID: "github.com/org/repo/pkg/api::StartServer", Name: "StartServer", Kind: graph.KindFunction, File: "pkg/api/server.go"},

			// Public package unexported function: NOT a root, should be orphan unless called
			{ID: "github.com/org/repo/pkg/api::setupConfig", Name: "setupConfig", Kind: graph.KindFunction, File: "pkg/api/server.go"},

			// Internal package exported function: NOT a root, should be orphan unless called
			{ID: "github.com/org/repo/internal/db::Connect", Name: "Connect", Kind: graph.KindFunction, File: "internal/db/db.go"},

			// Test function in test file: should be treated as root, and excluded from final orphan list
			{ID: "github.com/org/repo/internal/db::TestConnect", Name: "TestConnect", Kind: graph.KindFunction, File: "internal/db/db_test.go"},

			// Helper function in test file: NOT a root, but should be EXCLUDED from orphan list
			{ID: "github.com/org/repo/internal/db::buildMockDB", Name: "buildMockDB", Kind: graph.KindFunction, File: "internal/db/db_test.go"},
		},
		Calls: []graph.CallEdge{
			// Connect is called by StartServer
			{CallerSymbolID: "github.com/org/repo/pkg/api::StartServer", CalleeSymbolID: "github.com/org/repo/internal/db::Connect", CalleeRaw: "Connect"},
		},
	}

	orphans := search.ReachableOrphans(g)

	// We expect:
	// - StartServer: NOT an orphan (it is public exported, so a root)
	// - setupConfig: Orphan (public unexported, no incoming calls)
	// - Connect: NOT an orphan (called by StartServer)
	// - TestConnect: NOT an orphan (test function in test file, and excluded from report)
	// - buildMockDB: NOT an orphan (excluded from report because it is in a test file)

	expectedOrphans := map[string]bool{
		"github.com/org/repo/pkg/api::setupConfig": true,
	}

	if len(orphans) != 1 {
		t.Errorf("expected exactly 1 orphan, got %d: %v", len(orphans), orphans)
	}

	for _, o := range orphans {
		if !expectedOrphans[o.Name] {
			t.Errorf("unexpected orphan reported: %s", o.Name)
		}
	}
}

func TestReachableOrphans_DoesNotConflateExactSameNamedSymbols(t *testing.T) {
	const (
		publicDeleteID   = "github.com/org/repo/pkg/api::(*API).Delete"
		internalDeleteID = "github.com/org/repo/internal/store::(*Store).Delete"
	)
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{
			ID:       publicDeleteID,
			Name:     "Delete",
			Receiver: "*API",
			Kind:     graph.KindMethod,
			File:     "pkg/api/delete.go",
		},
		{
			ID:       internalDeleteID,
			Name:     "Delete",
			Receiver: "*Store",
			Kind:     graph.KindMethod,
			File:     "internal/store/delete.go",
		},
	}}

	orphans := search.ReachableOrphans(g)
	if len(orphans) != 1 || orphans[0].Name != internalDeleteID {
		t.Fatalf("exact public root made an unrelated same-named method reachable: %#v", orphans)
	}
}

func TestReachableOrphans_UnresolvedCalleeUsesNameFallback(t *testing.T) {
	const (
		rootID = "github.com/org/repo/pkg/api::Start"
		workID = "github.com/org/repo/internal/jobs::work"
	)
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: rootID, Name: "Start", Kind: graph.KindFunction, File: "pkg/api/start.go"},
			{ID: workID, Name: "work", Kind: graph.KindFunction, File: "internal/jobs/work.go"},
		},
		Calls: []graph.CallEdge{{
			CallerSymbolID: rootID,
			CallerName:     "Start",
			CalleeRaw:      "jobs.work",
			File:           "pkg/api/start.go",
			Line:           12,
		}},
	}

	if orphans := search.ReachableOrphans(g); len(orphans) != 0 {
		t.Fatalf("unresolved legacy call did not reach its uniquely named symbol: %#v", orphans)
	}
}
