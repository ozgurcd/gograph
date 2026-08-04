package search_test

import (
	"testing"
	"time"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func makeGraph() *graph.Graph {
	return &graph.Graph{
		Version:     "1",
		GeneratedAt: time.Now(),
		Root:        "/repo",
		Packages: []graph.PackageNode{
			{ID: "auth", Name: "auth", Dir: "internal/auth"},
		},
		Files: []graph.FileNode{
			{ID: "internal/auth/service.go", Path: "internal/auth/service.go", PackageName: "auth"},
		},
		Symbols: []graph.SymbolNode{
			{
				ID:   "internal/auth/service.go::(*AuthService).IssueToken",
				Kind: graph.KindMethod, Name: "IssueToken",
				Receiver: "*AuthService", PackageName: "auth",
				File: "internal/auth/service.go", Line: 42, EndLine: 60,
				Doc: "IssueToken creates a signed JWT for the given user.",
			},
			{
				ID:          "internal/auth/service.go::AuthService",
				Kind:        graph.KindStruct,
				Name:        "AuthService",
				PackageName: "auth",
				File:        "internal/auth/service.go",
				Line:        10,
				EndLine:     20,
			},
		},
		Imports: []graph.ImportEdge{
			{FromFile: "internal/auth/service.go", FromPackage: "auth", ImportPath: "crypto/rsa"},
		},
		Calls: []graph.CallEdge{
			{
				CallerSymbolID: "internal/auth/service.go::(*AuthService).IssueToken",
				CallerName:     "(*AuthService).IssueToken",
				CalleeRaw:      "jwt.Sign",
				File:           "internal/auth/service.go",
				Line:           50,
			},
			{
				CallerSymbolID: "internal/auth/service.go::(*AuthService).IssueToken",
				CallerName:     "(*AuthService).IssueToken",
				CalleeRaw:      "os.Getenv",
				File:           "internal/auth/service.go",
				Line:           44,
			},
			{
				CallerSymbolID: "internal/auth/service_test.go::TestIssueToken",
				CallerName:     "TestIssueToken",
				CalleeRaw:      "jwt.Sign",
				File:           "internal/auth/service_test.go",
				Line:           20,
			},
		},
	}
}

func TestQuery_MatchSymbolName(t *testing.T) {
	g := makeGraph()
	results := search.Query(g, []string{"IssueToken"})
	if len(results) == 0 {
		t.Fatal("expected results for IssueToken, got none")
	}
	found := false
	for _, r := range results {
		if r.Name == "(*AuthService).IssueToken" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected IssueToken method in results: %+v", results)
	}
}

func TestQuery_CaseInsensitive(t *testing.T) {
	g := makeGraph()
	results := search.Query(g, []string{"issuetoken"})
	if len(results) == 0 {
		t.Fatal("expected results for case-insensitive issuetoken")
	}
}

func TestQuery_MatchPackage(t *testing.T) {
	g := makeGraph()
	results := search.Query(g, []string{"auth"})
	found := false
	for _, r := range results {
		if r.Kind == "package" && r.Name == "auth" {
			found = true
		}
	}
	if !found {
		t.Error("expected package auth in results")
	}
}

func TestQuery_MatchImport(t *testing.T) {
	g := makeGraph()
	results := search.Query(g, []string{"crypto/rsa"})
	found := false
	for _, r := range results {
		if r.Kind == "import" {
			found = true
		}
	}
	if !found {
		t.Error("expected import result for crypto/rsa")
	}
}

func TestQuery_MatchCall(t *testing.T) {
	g := makeGraph()
	results := search.Query(g, []string{"jwt.Sign"})
	found := false
	for _, r := range results {
		if r.Kind == "call" && r.Name == "jwt.Sign" {
			found = true
		}
	}
	if !found {
		t.Error("expected call result for jwt.Sign")
	}
}

func TestCallers(t *testing.T) {
	g := makeGraph()
	// includeTests = true -> should return 2 callers for jwt.Sign
	results := search.Callers(g, "jwt.Sign", true, false)
	if len(results) != 2 {
		t.Fatalf("expected 2 callers of jwt.Sign, got %d", len(results))
	}

	// includeTests = false -> should return 1 caller (the production one)
	resultsNoTests := search.Callers(g, "jwt.Sign", false, false)
	if len(resultsNoTests) != 1 {
		t.Fatalf("expected 1 production caller of jwt.Sign, got %d", len(resultsNoTests))
	}
	if resultsNoTests[0].Name != "(*AuthService).IssueToken" {
		t.Errorf("expected IssueToken as caller, got %q", resultsNoTests[0].Name)
	}
}

func TestCallees(t *testing.T) {
	g := makeGraph()
	results := search.Callees(g, "IssueToken", true, false)
	if len(results) == 0 {
		t.Fatal("expected callees of IssueToken")
	}
	calleeNames := make(map[string]bool)
	for _, r := range results {
		calleeNames[r.Name] = true
	}
	if !calleeNames["jwt.Sign"] {
		t.Errorf("expected jwt.Sign as callee; got %v", calleeNames)
	}
	if !calleeNames["os.Getenv"] {
		t.Errorf("expected os.Getenv as callee; got %v", calleeNames)
	}
}

func TestCallQueriesPreserveWindowsSubdirectoryEdges(t *testing.T) {
	const (
		helperID    = "example.com/rootcase/sub::subHelper"
		subCallerID = "example.com/rootcase/sub::SubCaller"
		mainID      = "example.com/rootcase::main"
		windowsPath = `sub\sub.go`
	)
	g := &graph.Graph{
		Root: t.TempDir(),
		Symbols: []graph.SymbolNode{
			{ID: helperID, Kind: graph.KindFunction, Name: "subHelper", PackageName: "sub", File: windowsPath, Line: 3},
			{ID: subCallerID, Kind: graph.KindFunction, Name: "SubCaller", PackageName: "sub", File: windowsPath, Line: 4},
			{ID: mainID, Kind: graph.KindFunction, Name: "main", PackageName: "main", File: "main.go", Line: 5},
		},
		Calls: []graph.CallEdge{
			{
				CallerSymbolID: subCallerID,
				CallerName:     "SubCaller",
				CalleeSymbolID: helperID,
				CalleeRaw:      "subHelper",
				File:           windowsPath,
				Line:           4,
				Column:         31,
			},
			{
				CallerSymbolID: mainID,
				CallerName:     "main",
				CalleeSymbolID: subCallerID,
				CalleeRaw:      "sub.SubCaller",
				File:           "main.go",
				Line:           5,
				Column:         28,
			},
		},
	}

	for _, query := range []struct {
		name  string
		exact bool
	}{
		{name: "subHelper"},
		{name: "subHelper", exact: true},
		{name: "sub.subHelper", exact: true},
		{name: helperID, exact: true},
	} {
		callers := search.Callers(g, query.name, true, query.exact)
		if len(callers) != 1 || callers[0].Name != "SubCaller" {
			t.Errorf("Callers(%q, exact=%v) = %#v, want the subdirectory caller", query.name, query.exact, callers)
			continue
		}
		if callers[0].CallSiteFile != windowsPath {
			t.Errorf("Callers(%q) call-site path = %q, want %q", query.name, callers[0].CallSiteFile, windowsPath)
		}
	}

	for _, query := range []string{"SubCaller", "sub.SubCaller", subCallerID} {
		callees := search.Callees(g, query, true, true)
		if len(callees) != 1 || callees[0].Name != "subHelper" {
			t.Errorf("Callees(%q) = %#v, want the subdirectory callee", query, callees)
			continue
		}
		if callees[0].CallSiteFile != windowsPath {
			t.Errorf("Callees(%q) call-site path = %q, want %q", query, callees[0].CallSiteFile, windowsPath)
		}
	}

	impact := search.Impact(g, "subHelper", true)
	if len(impact) != 2 || impact[0].Name != "SubCaller" || impact[1].Name != "main" {
		t.Fatalf("Impact(subHelper) = %#v, want only SubCaller and main", impact)
	}
}

func TestNode_ExactMatch(t *testing.T) {
	g := makeGraph()
	results := search.Node(g, "IssueToken")
	if len(results) == 0 {
		t.Fatal("expected node result for IssueToken")
	}
}

func TestQuery_NoResults(t *testing.T) {
	g := makeGraph()
	results := search.Query(g, []string{"zzznomatch"})
	if len(results) != 0 {
		t.Errorf("expected no results, got %d", len(results))
	}
}
