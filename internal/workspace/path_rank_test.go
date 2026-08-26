package workspace

import (
	"slices"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func workspaceRankMember(id string, names ...string) LoadedMember {
	member := fakeMember(id, "example.com/"+id)
	module := member.Graph.Modules[0].Path
	for index, name := range names {
		member.Graph.Symbols = append(member.Graph.Symbols, graph.SymbolNode{
			ID: module + "::" + name, Name: name, Kind: graph.KindFunction,
			File: "path.go", Line: index + 1,
		})
	}
	return member
}

func workspaceRankCall(member LoadedMember, from, to, file string, resolution graph.CallResolution) graph.CallEdge {
	module := member.Graph.Modules[0].Path
	return graph.CallEdge{
		CallerSymbolID: module + "::" + from,
		CallerName:     from,
		CalleeSymbolID: module + "::" + to,
		CalleeRaw:      to,
		File:           file,
		Resolution:     resolution,
	}
}

func workspacePathNodeIDs(path PathResponse) []string {
	if !path.Found || len(path.Steps) == 0 {
		return nil
	}
	result := []string{path.Steps[0].From.NodeID}
	for _, step := range path.Steps {
		result = append(result, step.To.NodeID)
	}
	return result
}

func rankedWorkspace(t *testing.T, members []LoadedMember, scope ScopeOverlay) (*LoadedWorkspace, ScopeOverlay) {
	t.Helper()
	manifest := Manifest{Name: "ranked"}
	artifact := &Artifact{SchemaVersion: ArtifactSchemaVersion, WorkspaceName: manifest.Name, Scopes: []ScopeOverlay{scope}}
	return &LoadedWorkspace{Manifest: manifest, Members: members, Artifact: artifact}, scope
}

func TestWorkspacePathRanksExactBeforeShorterPossible(t *testing.T) {
	member := workspaceRankMember("app", "Start", "ExactMid", "End")
	member.Graph.Calls = []graph.CallEdge{
		workspaceRankCall(member, "Start", "End", "path.go", graph.CallResolutionCHA),
		workspaceRankCall(member, "Start", "ExactMid", "path.go", graph.CallResolutionStatic),
		workspaceRankCall(member, "ExactMid", "End", "path.go", graph.CallResolutionStatic),
	}
	workspace, scope := rankedWorkspace(t, []LoadedMember{member}, ScopeOverlay{ID: "default", Repositories: []string{"app"}})

	path, err := Path(workspace, scope, "app:Start", "app:End", true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"example.com/app::Start", "example.com/app::ExactMid", "example.com/app::End"}
	if got := workspacePathNodeIDs(path); !slices.Equal(got, want) {
		t.Fatalf("ranked workspace path = %v, want %v", got, want)
	}
}

func TestWorkspacePathRanksProductionThenTyped(t *testing.T) {
	t.Run("production before tests", func(t *testing.T) {
		member := workspaceRankMember("app", "Start", "TestMid", "ProductionMid", "End")
		member.Graph.Calls = []graph.CallEdge{
			workspaceRankCall(member, "Start", "TestMid", "path_test.go", graph.CallResolutionStatic),
			workspaceRankCall(member, "TestMid", "End", "path_test.go", graph.CallResolutionStatic),
			workspaceRankCall(member, "Start", "ProductionMid", "path.go", graph.CallResolutionStatic),
			workspaceRankCall(member, "ProductionMid", "End", "path.go", graph.CallResolutionStatic),
		}
		workspace, scope := rankedWorkspace(t, []LoadedMember{member}, ScopeOverlay{ID: "default", Repositories: []string{"app"}})
		path, err := Path(workspace, scope, "app:Start", "app:End", true)
		if err != nil {
			t.Fatal(err)
		}
		if got := workspacePathNodeIDs(path); len(got) != 3 || got[1] != "example.com/app::ProductionMid" {
			t.Fatalf("ranked workspace path = %v, want production route", got)
		}
	})

	t.Run("typed before heuristic", func(t *testing.T) {
		member := workspaceRankMember("app", "Start", "HeuristicMid", "TypedMid", "End")
		member.Graph.Calls = []graph.CallEdge{
			workspaceRankCall(member, "Start", "HeuristicMid", "path.go", ""),
			workspaceRankCall(member, "HeuristicMid", "End", "path.go", ""),
			workspaceRankCall(member, "Start", "TypedMid", "path.go", graph.CallResolutionStatic),
			workspaceRankCall(member, "TypedMid", "End", "path.go", graph.CallResolutionStatic),
		}
		workspace, scope := rankedWorkspace(t, []LoadedMember{member}, ScopeOverlay{ID: "default", Repositories: []string{"app"}})
		path, err := Path(workspace, scope, "app:Start", "app:End", true)
		if err != nil {
			t.Fatal(err)
		}
		if got := workspacePathNodeIDs(path); len(got) != 3 || got[1] != "example.com/app::TypedMid" {
			t.Fatalf("ranked workspace path = %v, want typed route", got)
		}
	})
}

func TestWorkspacePathRanksLocalBeforeCrossRepository(t *testing.T) {
	app := workspaceRankMember("app", "Start", "LocalMid", "End")
	bridge := workspaceRankMember("bridge", "Bridge")
	app.Graph.Calls = []graph.CallEdge{
		workspaceRankCall(app, "Start", "LocalMid", "path.go", graph.CallResolutionStatic),
		workspaceRankCall(app, "LocalMid", "End", "path.go", graph.CallResolutionStatic),
	}
	appModule := app.Graph.Modules[0].Path
	bridgeModule := bridge.Graph.Modules[0].Path
	appStart := NodeRef{RepositoryID: "app", ModuleID: appModule, NodeID: appModule + "::Start", Kind: "symbol", Language: "go"}
	appEnd := NodeRef{RepositoryID: "app", ModuleID: appModule, NodeID: appModule + "::End", Kind: "symbol", Language: "go"}
	bridgeNode := NodeRef{RepositoryID: "bridge", ModuleID: bridgeModule, NodeID: bridgeModule + "::Bridge", Kind: "symbol", Language: "go"}
	scope := ScopeOverlay{ID: "default", Repositories: []string{"app", "bridge"}, GoCalls: []GoCallResolution{
		{Source: appStart, Targets: []NodeRef{bridgeNode}, ResolutionStatus: ResolutionExact, EvidenceOrigin: EvidenceStructural},
		{Source: bridgeNode, Targets: []NodeRef{appEnd}, ResolutionStatus: ResolutionExact, EvidenceOrigin: EvidenceStructural},
	}}
	workspace, scope := rankedWorkspace(t, []LoadedMember{app, bridge}, scope)

	path, err := Path(workspace, scope, "app:Start", "app:End", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := workspacePathNodeIDs(path); len(got) != 3 || got[1] != "example.com/app::LocalMid" {
		t.Fatalf("ranked workspace path = %v, want repository-local route", got)
	}
}
