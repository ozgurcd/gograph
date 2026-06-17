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
