package search_test

import (
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

const (
	stateRepositoryID = "example.com/app/internal/repository::StateRepository"
	postgresDeleteID  = "example.com/app/internal/postgres::(*PostgresStateRepository).Delete"
	memoryDeleteID    = "example.com/app/internal/memory::(*MemoryStateRepository).Delete"
	deleteSignature   = "func(context.Context, string) error"
	deleteCallerID    = "example.com/app/pkg/api::Run"
)

func interfaceDeleteGraph() *graph.Graph {
	return &graph.Graph{
		Symbols: []graph.SymbolNode{
			{
				ID:               stateRepositoryID,
				Kind:             graph.KindInterface,
				Name:             "StateRepository",
				PackageName:      "repository",
				File:             "internal/repository/state.go",
				InterfaceMethods: map[string]string{"Delete": deleteSignature},
			},
			{
				ID:              postgresDeleteID,
				Kind:            graph.KindMethod,
				Name:            "Delete",
				Receiver:        "*PostgresStateRepository",
				PackageName:     "postgres",
				File:            "internal/postgres/state.go",
				MethodSignature: deleteSignature,
			},
			{
				ID:              memoryDeleteID,
				Kind:            graph.KindMethod,
				Name:            "Delete",
				Receiver:        "*MemoryStateRepository",
				PackageName:     "memory",
				File:            "internal/memory/state.go",
				MethodSignature: deleteSignature,
			},
			{
				ID:          deleteCallerID,
				Kind:        graph.KindFunction,
				Name:        "Run",
				PackageName: "api",
				File:        "pkg/api/run.go",
				Line:        10,
			},
		},
		Implements: []graph.ImplementsEdge{
			{
				Interface:   "StateRepository",
				InterfaceID: stateRepositoryID,
				Concrete:    "PostgresStateRepository",
				ConcreteID:  "example.com/app/internal/postgres::PostgresStateRepository",
			},
			{
				Interface:   "StateRepository",
				InterfaceID: stateRepositoryID,
				Concrete:    "MemoryStateRepository",
				ConcreteID:  "example.com/app/internal/memory::MemoryStateRepository",
			},
		},
		Calls: []graph.CallEdge{
			{
				CallerSymbolID: deleteCallerID,
				CallerName:     "Run",
				CalleeRaw:      "states.Delete",
				CalleeSymbolID: postgresDeleteID,
				File:           "pkg/api/run.go",
				Line:           20,
			},
			{
				CallerSymbolID: deleteCallerID,
				CallerName:     "Run",
				CalleeRaw:      "states.Delete",
				CalleeSymbolID: memoryDeleteID,
				File:           "pkg/api/run.go",
				Line:           20,
			},
		},
	}
}

func TestCallers_InterfaceMethodMatchesConcreteTargetsOnce(t *testing.T) {
	g := interfaceDeleteGraph()

	queries := []struct {
		label string
		name  string
		exact bool
	}{
		{label: "bare exact", name: "Delete", exact: true},
		{label: "receiver dot fuzzy", name: "PostgresStateRepository.Delete", exact: false},
		{label: "receiver dot exact", name: "PostgresStateRepository.Delete", exact: true},
		{label: "package receiver dot", name: "postgres.PostgresStateRepository.Delete", exact: true},
		{label: "fully qualified fuzzy", name: postgresDeleteID, exact: false},
		{label: "fully qualified exact", name: postgresDeleteID, exact: true},
		{label: "interface dot fuzzy", name: "StateRepository.Delete", exact: false},
		{label: "interface dot exact", name: "StateRepository.Delete", exact: true},
		{label: "package interface dot", name: "repository.StateRepository.Delete", exact: true},
		{label: "fully qualified interface", name: stateRepositoryID + ".Delete", exact: true},
	}

	for _, query := range queries {
		t.Run(query.label, func(t *testing.T) {
			results := search.Callers(g, query.name, false, query.exact)
			if len(results) != 1 {
				t.Fatalf("Callers(%q, exact=%v) returned %d results, want one source call site: %#v", query.name, query.exact, len(results), results)
			}
			if results[0].Name != "Run" || results[0].CallSiteFile != "pkg/api/run.go" || results[0].CallSiteLine != 20 {
				t.Fatalf("Callers(%q) returned the wrong call site: %#v", query.name, results[0])
			}
		})
	}
}

func TestCallersDepth_InterfaceMethodUsesConcreteTargetIDs(t *testing.T) {
	queries := []string{
		"Delete",
		"PostgresStateRepository.Delete",
		postgresDeleteID,
		"StateRepository.Delete",
		stateRepositoryID + ".Delete",
	}
	for _, query := range queries {
		results := search.CallersDepth(interfaceDeleteGraph(), query, 2, false, true)
		if len(results) != 1 || results[0].Name != "Run" {
			t.Fatalf("expected %q caller once, got %#v", query, results)
		}
	}
}

func TestCallers_InterfaceMethodWithoutTargetsDoesNotMatchUnrelatedMethods(t *testing.T) {
	g := interfaceDeleteGraph()
	g.Implements = nil
	if results := search.Callers(g, "StateRepository.Delete", false, true); len(results) != 0 {
		t.Fatalf("interface query without proven targets fell back to unrelated Delete methods: %#v", results)
	}
}

func TestCallers_InterfaceMethodMatchesSyntheticImplementerMethodID(t *testing.T) {
	g := interfaceDeleteGraph()
	// Promoted methods and SSA wrappers can be valid call targets without a
	// parser-emitted standalone method symbol on the outer concrete type.
	g.Symbols = []graph.SymbolNode{g.Symbols[0], g.Symbols[3]}
	results := search.Callers(g, "StateRepository.Delete", false, true)
	if len(results) != 1 || results[0].Name != "Run" {
		t.Fatalf("interface query lost call-edge IDs without method symbols: %#v", results)
	}
	for _, query := range []string{
		"Delete",
		"PostgresStateRepository.Delete",
		"postgres.PostgresStateRepository.Delete",
		postgresDeleteID,
	} {
		results := search.Callers(g, query, false, true)
		if len(results) != 1 || results[0].Name != "Run" {
			t.Errorf("synthetic concrete query %q disagrees with its FQ call-edge ID: %#v", query, results)
		}
		depthResults := search.CallersDepth(g, query, 2, false, true)
		if len(depthResults) != 1 || depthResults[0].Name != "Run" {
			t.Errorf("synthetic concrete depth query %q disagrees with direct callers: %#v", query, depthResults)
		}
	}
}

func TestCallees_DeduplicatesParallelInterfaceTargets(t *testing.T) {
	g := interfaceDeleteGraph()
	results := search.Callees(g, deleteCallerID, false, false)
	if len(results) != 1 {
		t.Fatalf("expected one user-visible callee row for one source call site, got %d: %#v", len(results), results)
	}
	if results[0].Name != "states.Delete" || results[0].CallSiteLine != 20 {
		t.Fatalf("unexpected callee result: %#v", results[0])
	}
	if len(g.Calls) != 2 {
		t.Fatalf("callee presentation dedup must preserve both reachability targets; graph has %d edges", len(g.Calls))
	}
}

func TestCalleesDepth_DeduplicatesDisplayWithoutDroppingParallelTargets(t *testing.T) {
	g := interfaceDeleteGraph()
	const (
		postgresLeafID = "example.com/app/internal/postgres::deleteSQLRow"
		memoryLeafID   = "example.com/app/internal/memory::deleteMapEntry"
	)
	g.Symbols = append(g.Symbols,
		graph.SymbolNode{ID: postgresLeafID, Kind: graph.KindFunction, Name: "deleteSQLRow", File: "internal/postgres/state.go"},
		graph.SymbolNode{ID: memoryLeafID, Kind: graph.KindFunction, Name: "deleteMapEntry", File: "internal/memory/state.go"},
	)
	g.Calls = append(g.Calls,
		graph.CallEdge{
			CallerSymbolID: postgresDeleteID,
			CallerName:     "(*PostgresStateRepository).Delete",
			CalleeRaw:      "deleteSQLRow",
			CalleeSymbolID: postgresLeafID,
			File:           "internal/postgres/state.go",
			Line:           30,
		},
		graph.CallEdge{
			CallerSymbolID: memoryDeleteID,
			CallerName:     "(*MemoryStateRepository).Delete",
			CalleeRaw:      "deleteMapEntry",
			CalleeSymbolID: memoryLeafID,
			File:           "internal/memory/state.go",
			Line:           30,
		},
	)

	results := search.CalleesDepth(g, deleteCallerID, 2, false)
	counts := make(map[string]int)
	for _, result := range results {
		counts[result.Name]++
	}
	if counts["states.Delete"] != 1 {
		t.Fatalf("parallel interface targets produced %d visible rows for their shared source site: %#v", counts["states.Delete"], results)
	}
	if counts["deleteSQLRow"] != 1 || counts["deleteMapEntry"] != 1 {
		t.Fatalf("depth traversal dropped an interface target: %#v", results)
	}
}

func TestReturnUsages_DeduplicatesParallelInterfaceTargets(t *testing.T) {
	g := interfaceDeleteGraph()
	g.Calls[0].ReturnUsage = "returned"
	g.Calls[1].ReturnUsage = "returned"
	results := search.ReturnUsages(g, "Delete")
	if len(results) != 1 || results[0].Name != "Run" || results[0].File != "pkg/api/run.go" || results[0].Line != 20 {
		t.Fatalf("expected one user-visible return-usage row, got %#v", results)
	}
}

func TestReachableOrphans_ParallelInterfaceTargetsAreReachable(t *testing.T) {
	orphans := search.ReachableOrphans(interfaceDeleteGraph())
	for _, orphan := range orphans {
		if orphan.Name == postgresDeleteID || orphan.Name == memoryDeleteID {
			t.Errorf("interface dispatch target incorrectly reported as orphan: %s", orphan.Name)
		}
	}
}

func TestCallers_InterfaceIdentityDoesNotFoldGoIdentifierCase(t *testing.T) {
	g := interfaceDeleteGraph()
	const lowerDeleteID = "example.com/app/internal/postgres::(*postgresStateRepository).delete"
	g.Symbols = append(g.Symbols, graph.SymbolNode{
		ID:              lowerDeleteID,
		Kind:            graph.KindMethod,
		Name:            "delete",
		Receiver:        "*postgresStateRepository",
		PackageName:     "postgres",
		File:            "internal/postgres/lower_state.go",
		MethodSignature: deleteSignature,
	})
	g.Calls = append(g.Calls, graph.CallEdge{
		CallerSymbolID: deleteCallerID,
		CallerName:     "Run",
		CalleeRaw:      "lower.delete",
		CalleeSymbolID: lowerDeleteID,
		File:           "pkg/api/run.go",
		Line:           21,
	})

	results := search.Callers(g, "StateRepository.Delete", false, true)
	if len(results) != 1 || results[0].CallSiteLine != 20 {
		t.Fatalf("case-distinct Go method was treated as an interface implementation: %#v", results)
	}
}

func TestCallers_InterfaceMethodCaseFoldFallbackIsDeterministic(t *testing.T) {
	g := interfaceDeleteGraph()
	g.Symbols[0].InterfaceMethods["delete"] = deleteSignature

	if results := search.Callers(g, "StateRepository.DELETE", false, true); len(results) != 0 {
		t.Fatalf("case-ambiguous interface method query selected an arbitrary method: %#v", results)
	}
	if results := search.Callers(g, "StateRepository.Delete", false, true); len(results) != 1 {
		t.Fatalf("exactly cased interface method query returned %#v, want one call site", results)
	}
}

func TestCallersMermaid_InterfaceMethodUsesSharedTargetResolver(t *testing.T) {
	g := interfaceDeleteGraph()
	queries := []string{
		"Delete",
		"StateRepository.Delete",
		stateRepositoryID + ".Delete",
	}
	var baseline string
	for _, query := range queries {
		got := search.CallersToMermaid(g, query, 1, false, false)
		if !strings.Contains(got, "Run") || !strings.Contains(got, "PostgresStateRepository") || !strings.Contains(got, "MemoryStateRepository") {
			t.Errorf("CallersToMermaid(%q) lost an interface target:\n%s", query, got)
		}
		if baseline == "" {
			baseline = got
		} else if got != baseline {
			t.Errorf("CallersToMermaid(%q) disagrees with other interface query forms:\nbaseline:\n%s\nactual:\n%s", query, baseline, got)
		}
	}

	concrete := search.CallersToMermaid(g, postgresDeleteID, 1, false, false)
	if !strings.Contains(concrete, "Run") || !strings.Contains(concrete, "PostgresStateRepository") || strings.Contains(concrete, "MemoryStateRepository") {
		t.Fatalf("concrete FQ caller diagram did not stay target-specific:\n%s", concrete)
	}

	withoutProof := interfaceDeleteGraph()
	withoutProof.Implements = nil
	if diagram := search.CallersToMermaid(withoutProof, "StateRepository.Delete", 1, false, false); strings.Contains(diagram, "Run") {
		t.Fatalf("interface caller diagram widened to unrelated Delete methods without proven targets:\n%s", diagram)
	}
	if impact := search.Impact(withoutProof, "StateRepository.Delete", false); len(impact) != 0 {
		t.Fatalf("interface impact widened to unrelated Delete methods without proven targets: %#v", impact)
	}
}

func TestPromotedForwardingIsTransparentToImpactAndMermaid(t *testing.T) {
	const (
		interfaceID = "example.com/promoted/internal/store::Store"
		wrapperID   = "example.com/promoted/internal/store::(*CompositeStore).Delete"
		declaredID  = "example.com/promoted/internal/store::(*DeletePart).Delete"
		leafID      = "example.com/promoted/internal/store::deleteRecord"
		purgeID     = "example.com/promoted/api::Purge"
		mainID      = "example.com/promoted/cmd::main"
	)
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: interfaceID, Kind: graph.KindInterface, Name: "Store", PackageName: "store", InterfaceMethods: map[string]string{"Delete": "func(string) error"}},
			{ID: "example.com/promoted/internal/store::CompositeStore", Kind: graph.KindStruct, Name: "CompositeStore", PackageName: "store", File: "internal/store/store.go"},
			{ID: declaredID, Kind: graph.KindMethod, Name: "Delete", Receiver: "*DeletePart", PackageName: "store", File: "internal/store/store.go", Line: 10},
			{ID: leafID, Kind: graph.KindFunction, Name: "deleteRecord", PackageName: "store", File: "internal/store/store.go", Line: 14},
			{ID: purgeID, Kind: graph.KindFunction, Name: "Purge", PackageName: "api", File: "api/api.go", Line: 5},
			{ID: mainID, Kind: graph.KindFunction, Name: "main", PackageName: "cmd", File: "cmd/main.go", Line: 3},
		},
		Implements: []graph.ImplementsEdge{{
			Interface:   "Store",
			InterfaceID: interfaceID,
			Concrete:    "CompositeStore",
			ConcreteID:  "example.com/promoted/internal/store::CompositeStore",
		}},
		Calls: []graph.CallEdge{
			{CallerSymbolID: mainID, CallerName: "main", CalleeRaw: "api.Purge", CalleeSymbolID: purgeID, File: "cmd/main.go", Line: 4, Column: 2},
			{CallerSymbolID: purgeID, CallerName: "Purge", CalleeRaw: "value.Delete", CalleeSymbolID: wrapperID, File: "api/api.go", Line: 6, Column: 15},
			{CallerSymbolID: wrapperID, CallerName: "(*CompositeStore).Delete", CalleeRaw: "Delete", CalleeSymbolID: declaredID, Synthetic: true},
			{CallerSymbolID: declaredID, CallerName: "(*DeletePart).Delete", CalleeRaw: "deleteRecord", CalleeSymbolID: leafID, File: "internal/store/store.go", Line: 11, Column: 9},
		},
	}

	impactQueries := []string{"Store.Delete", interfaceID + ".Delete", "CompositeStore.Delete", wrapperID, declaredID}
	for _, query := range impactQueries {
		impact := search.Impact(g, query, false)
		seen := make(map[string]bool)
		for _, result := range impact {
			seen[result.Name] = true
			if strings.Contains(result.Name, "CompositeStore") || result.File == "" {
				t.Fatalf("impact %q exposed synthetic wrapper metadata: %#v", query, impact)
			}
		}
		if !seen["Purge"] || !seen["main"] || len(seen) != 2 {
			t.Fatalf("impact %q did not cross promoted forwarding to every real caller: %#v", query, impact)
		}
	}

	callerQueries := []string{"Store.Delete", declaredID, wrapperID}
	for _, query := range callerQueries {
		diagram := search.CallersToMermaid(g, query, 2, false, false)
		if !strings.Contains(diagram, "Purge") || !strings.Contains(diagram, "main") || !strings.Contains(diagram, "DeletePart") {
			t.Errorf("caller diagram %q did not traverse promoted forwarding:\n%s", query, diagram)
		}
		if strings.Contains(diagram, "CompositeStore") {
			t.Errorf("caller diagram %q exposed traversal-only wrapper:\n%s", query, diagram)
		}
	}

	callees := search.CalleesToMermaid(g, purgeID, 2, false)
	if !strings.Contains(callees, "Purge") || !strings.Contains(callees, "DeletePart") || !strings.Contains(callees, "deleteRecord") {
		t.Fatalf("callee diagram stopped at promoted wrapper:\n%s", callees)
	}
	if strings.Contains(callees, "CompositeStore") {
		t.Fatalf("callee diagram exposed traversal-only wrapper:\n%s", callees)
	}

	impactDiagram := search.ImpactMultipleToMermaid(g, []string{declaredID}, false)
	if !strings.Contains(impactDiagram, "Purge") || !strings.Contains(impactDiagram, "main") || strings.Contains(impactDiagram, "CompositeStore") {
		t.Fatalf("multi-impact diagram did not collapse promoted forwarding:\n%s", impactDiagram)
	}
}
