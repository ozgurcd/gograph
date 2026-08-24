package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

func TestLoadArtifactRejectsOversizedJSONBeforeDecode(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ArtifactFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(graph.MaxArtifactBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadArtifact(root); !errors.Is(err, sourcefs.ErrFileTooLarge) {
		t.Fatalf("oversized workspace artifact error = %v, want ErrFileTooLarge", err)
	}
}

func TestSelectScopeRequiresExplicitChoiceUnlessDefaultOrSingle(t *testing.T) {
	scopes := []ScopeOverlay{{ID: "oss"}, {ID: "ce"}}
	loaded := &LoadedWorkspace{Artifact: &Artifact{Scopes: scopes}}
	if _, err := SelectScope(loaded, ""); err == nil || !strings.Contains(err.Error(), "--scope is required") {
		t.Fatalf("multiple scopes without default error = %v", err)
	}
	loaded.Artifact.DefaultScope = "ce"
	if scope, err := SelectScope(loaded, ""); err != nil || scope.ID != "ce" {
		t.Fatalf("default scope = %+v, %v", scope, err)
	}
	if scope, err := SelectScope(loaded, "oss"); err != nil || scope.ID != "oss" {
		t.Fatalf("explicit scope = %+v, %v", scope, err)
	}
	if _, err := SelectScope(loaded, "missing"); err == nil || !strings.Contains(err.Error(), "unknown workspace scope") {
		t.Fatalf("unknown scope error = %v", err)
	}
	loaded.Artifact.DefaultScope = ""
	loaded.Artifact.Scopes = scopes[:1]
	if scope, err := SelectScope(loaded, ""); err != nil || scope.ID != "oss" {
		t.Fatalf("implicit sole scope = %+v, %v", scope, err)
	}
}

func TestStatusCannotEvaluateWhenNoMemberGraphIsAvailable(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "repo"))
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		Name:          "fleet",
		Repositories:  []RepositoryConfig{{ID: "repo", Path: "repo"}},
	}
	status := InspectStatus(context.Background(), root, manifest)
	if status.AggregateState != StateCannotEvaluate || status.Members[0].Available || status.Overlay.Present {
		t.Fatalf("status without member artifacts = %+v", status)
	}
}

func TestRepositoryRevisionDisablesConfiguredFSMonitor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	root := t.TempDir()
	runGit := func(arguments ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", arguments, err, output)
		}
	}
	runGit("init", "-q")
	runGit("config", "user.email", "gograph@example.invalid")
	runGit("config", "user.name", "Gograph Test")
	mustWrite(t, filepath.Join(root, "tracked.txt"), "tracked\n")
	runGit("add", "tracked.txt")
	runGit("commit", "-qm", "fixture")
	marker := filepath.Join(root, "fsmonitor-ran")
	hook := filepath.Join(root, "fsmonitor-hook")
	t.Setenv("GOGRAPH_FSMONITOR_MARKER", marker)
	mustWrite(t, hook, "#!/bin/sh\nprintf ran > \"$GOGRAPH_FSMONITOR_MARKER\"\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit("config", "core.fsmonitor", hook)

	revision, dirty := repositoryRevision(context.Background(), root)
	if revision == "" || dirty {
		t.Fatalf("repository revision = %q, dirty = %v", revision, dirty)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("repository-configured fsmonitor executed during status: %v", err)
	}
}

func TestQueryAndSelectorsCoverSymbolsPackagesModulesAndContracts(t *testing.T) {
	member := fakeMember("server", "example.com/service")
	member.Graph.Packages = []graph.PackageNode{{ID: "example.com/service/api", Name: "api", ImportPathBestEffort: "example.com/service/api", Dir: "api"}}
	member.Graph.Symbols = []graph.SymbolNode{{ID: "example.com/service/api::Handle", Name: "Handle", Kind: graph.KindFunction, File: "api/handler.go", Line: 10}}
	contract := HTTPContract{ID: HTTPContractID{AuthorityID: "service-api", Method: "GET", NormalizedPath: "/items/{}"}}
	scope := ScopeOverlay{ID: "default", Repositories: []string{"server"}, HTTPContracts: []HTTPContract{contract}}
	loaded := &LoadedWorkspace{Manifest: Manifest{Name: "fleet"}, Members: []LoadedMember{member}, Artifact: &Artifact{DefaultScope: "default", Scopes: []ScopeOverlay{scope}}}

	for term, kind := range map[string]string{
		"Handle":              "symbol",
		"example.com/service": "module",
		"service-api":         "http_contract",
	} {
		response := Query(loaded, scope, term)
		found := false
		for _, result := range response.Results {
			if result.Node.Kind == kind {
				found = true
			}
		}
		if !found {
			t.Errorf("query %q did not return %s: %+v", term, kind, response.Results)
		}
	}
	for selector, kind := range map[string]string{
		"server:Handle":                  "symbol",
		"server:example.com/service/api": "package",
		"server:example.com/service":     "module",
		"GET service-api/items/{}":       "http_contract",
	} {
		ref, err := ResolveSelector(loaded, scope, selector)
		if err != nil || ref.Kind != kind {
			t.Errorf("selector %q = %+v, %v; want kind %s", selector, ref, err, kind)
		}
	}
}

func TestManifestConfinesMembersAndDefaultsScopes(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "repo"))
	mustWrite(t, filepath.Join(root, ManifestFile), `schema_version: gograph.workspace-manifest.v1
name: test
repositories:
  - id: repo
    path: ./repo
`)
	manifest, _, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.DefaultScope != "default" || len(manifest.Scopes) != 1 || manifest.Scopes[0].Repositories[0] != "repo" {
		t.Fatalf("implicit scope = %+v", manifest)
	}
}

func TestWorkspaceSchemasVersionIndependentlyAndManifestRequiresItsVersion(t *testing.T) {
	versions := []string{ManifestSchemaVersion, ArtifactSchemaVersion, QuerySchemaVersion, StatusSchemaVersion}
	seen := make(map[string]bool)
	for _, version := range versions {
		if version == "" || seen[version] {
			t.Fatalf("workspace schema versions are not independent: %v", versions)
		}
		seen[version] = true
	}
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "repo"))
	mustWrite(t, filepath.Join(root, ManifestFile), "name: test\nrepositories:\n  - id: repo\n    path: repo\n")
	if _, _, err := LoadManifest(root); err == nil || !strings.Contains(err.Error(), "unsupported workspace manifest schema") {
		t.Fatalf("missing schema error = %v", err)
	}
}

func TestManifestRejectsMemberSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "repo")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	mustWrite(t, filepath.Join(root, ManifestFile), `schema_version: gograph.workspace-manifest.v1
name: test
repositories:
  - id: repo
    path: repo
`)
	if _, _, err := LoadManifest(root); err == nil || !strings.Contains(err.Error(), "confinement") {
		t.Fatalf("LoadManifest symlink error = %v", err)
	}
}

func TestMemberConfinementIsRecheckedAfterManifestLoad(t *testing.T) {
	root := t.TempDir()
	member := filepath.Join(root, "repo")
	mustMkdir(t, member)
	mustWrite(t, filepath.Join(root, ManifestFile), `schema_version: gograph.workspace-manifest.v1
name: test
repositories:
  - id: repo
    path: repo
`)
	manifest, _, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(member); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), member); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := ResolveMemberRoot(root, manifest.Repositories[0]); err == nil || !strings.Contains(err.Error(), "confinement") {
		t.Fatalf("rechecked member root error = %v", err)
	}
}

func TestPublishRejectsLinkedWorkspaceStatePaths(t *testing.T) {
	artifact := &Artifact{SchemaVersion: ArtifactSchemaVersion, WorkspaceName: "test", ResolverVersions: map[string]string{}, Members: []Member{}, Scopes: []ScopeOverlay{}}
	for _, target := range []string{"directory", "artifact", "lock"} {
		t.Run(target, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "workspace")
			mustMkdir(t, root)
			outside := filepath.Join(base, "outside")
			mustMkdir(t, outside)
			switch target {
			case "directory":
				if err := os.Symlink(outside, filepath.Join(root, ".gograph")); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			case "artifact":
				mustMkdir(t, filepath.Join(root, ".gograph"))
				mustWrite(t, filepath.Join(outside, "workspace.json"), "KEEP")
				if err := os.Symlink(filepath.Join(outside, "workspace.json"), filepath.Join(root, ArtifactFile)); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			case "lock":
				mustMkdir(t, filepath.Join(root, ".gograph"))
				mustWrite(t, filepath.Join(outside, "lock"), "KEEP")
				if err := os.Symlink(filepath.Join(outside, "lock"), filepath.Join(root, ".gograph", workspaceLockFile)); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			if err := Publish(root, artifact); err == nil {
				t.Fatal("linked workspace state path was accepted")
			}
		})
	}
}

func TestManifestRejectsTrailingDocumentsAndUnsafeDisplayText(t *testing.T) {
	for name, manifest := range map[string]string{
		"trailing document": "schema_version: gograph.workspace-manifest.v1\nname: test\nrepositories:\n  - id: repo\n    path: repo\n---\nname: second\n",
		"control character": "schema_version: gograph.workspace-manifest.v1\nname: \"bad\\u001b[31m\"\nrepositories:\n  - id: repo\n    path: repo\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			mustMkdir(t, filepath.Join(root, "repo"))
			mustWrite(t, filepath.Join(root, ManifestFile), manifest)
			if _, _, err := LoadManifest(root); err == nil {
				t.Fatal("unsafe manifest was accepted")
			}
		})
	}
}

func TestManifestHTTPLogicalAuthorityOwnershipIsScoped(t *testing.T) {
	root := t.TempDir()
	for _, repository := range []string{"oss", "ce"} {
		mustMkdir(t, filepath.Join(root, repository))
	}
	manifest := `schema_version: gograph.workspace-manifest.v1
name: fleet
repositories:
  - id: oss
    path: oss
    services:
      - id: api
        http:
          authorities: [oss.internal]
  - id: ce
    path: ce
    services:
      - id: api
        http:
          authorities: [ce.internal]
scopes:
  - id: oss
    repositories: [oss]
  - id: ce
    repositories: [ce]
`
	mustWrite(t, filepath.Join(root, ManifestFile), manifest)
	if _, _, err := LoadManifest(root); err != nil {
		t.Fatalf("mutually exclusive logical authority ownership: %v", err)
	}
	manifest = strings.Replace(manifest, "  - id: ce\n    repositories: [ce]\n", "  - id: combined\n    repositories: [oss, ce]\n", 1)
	mustWrite(t, filepath.Join(root, ManifestFile), manifest)
	if _, _, err := LoadManifest(root); err == nil || !strings.Contains(err.Error(), "logical HTTP authority") {
		t.Fatalf("same-scope logical authority collision error = %v", err)
	}
}

func TestManifestRejectsDuplicateAuthorityAndMultipleHTTPServices(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "repo"))
	mustWrite(t, filepath.Join(root, ManifestFile), `schema_version: gograph.workspace-manifest.v1
name: fleet
repositories:
  - id: repo
    path: repo
    services:
      - id: one
        http:
          authorities: [API.internal, api.internal]
`)
	if _, _, err := LoadManifest(root); err == nil || !strings.Contains(err.Error(), "repeats normalized") {
		t.Fatalf("duplicate normalized authority error = %v", err)
	}
	mustWrite(t, filepath.Join(root, ManifestFile), `schema_version: gograph.workspace-manifest.v1
name: fleet
repositories:
  - id: repo
    path: repo
    services:
      - id: one
        http:
          authorities: [one.internal]
      - id: two
        http:
          authorities: [two.internal]
`)
	if _, _, err := LoadManifest(root); err == nil || !strings.Contains(err.Error(), "one HTTP service") {
		t.Fatalf("multiple HTTP services error = %v", err)
	}
}

func TestDuplicateModuleOwnershipAcrossScopesSucceedsButWithinScopeFails(t *testing.T) {
	oss := fakeMember("idp-oss", "github.com/acme/idp")
	ce := fakeMember("idp-ce", "github.com/acme/idp")
	ui := fakeMember("ui", "github.com/acme/ui")
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, Name: "fleet", DefaultScope: "oss", Repositories: []RepositoryConfig{{ID: "idp-oss"}, {ID: "idp-ce"}, {ID: "ui"}}, Scopes: []ScopeConfig{{ID: "oss", Repositories: []string{"idp-oss", "ui"}}, {ID: "ce", Repositories: []string{"idp-ce", "ui"}}}}
	if _, err := Resolve(manifest, []LoadedMember{oss, ce, ui}); err != nil {
		t.Fatalf("mutually exclusive ownership: %v", err)
	}
	manifest.Scopes = []ScopeConfig{{ID: "bad", Repositories: []string{"idp-oss", "idp-ce", "ui"}}}
	manifest.DefaultScope = "bad"
	if _, err := Resolve(manifest, []LoadedMember{oss, ce, ui}); err == nil || !strings.Contains(err.Error(), "duplicate module ownership") {
		t.Fatalf("same-scope duplicate error = %v", err)
	}
}

func TestResolveRejectsUnknownScopeMemberWithoutPanicking(t *testing.T) {
	member := fakeMember("repo", "example.com/repo")
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, Name: "fleet", Repositories: []RepositoryConfig{member.Config}, Scopes: []ScopeConfig{{ID: "bad", Repositories: []string{"missing"}}}}
	if _, err := Resolve(manifest, []LoadedMember{member}); err == nil || !strings.Contains(err.Error(), "unknown repository") {
		t.Fatalf("unknown scope member error = %v", err)
	}
}

func TestOutOfScopeDecoyCannotInfluenceGoResolution(t *testing.T) {
	client := fakeMember("client", "github.com/acme/client")
	client.Graph.Packages = []graph.PackageNode{{ID: "github.com/acme/client", Files: []string{"client.go"}}}
	client.Graph.Symbols = []graph.SymbolNode{{ID: "github.com/acme/client::Call", Name: "Call", Kind: graph.KindFunction, File: "client.go"}}
	client.Graph.Imports = []graph.ImportEdge{{FromFile: "client.go", ImportPath: "github.com/acme/service/api"}}
	client.Graph.Calls = []graph.CallEdge{{CallerSymbolID: "github.com/acme/client::Call", CallerName: "Call", CalleeRaw: "api.Handle", File: "client.go", Line: 5}}
	selected := fakeMember("selected", "github.com/acme/service")
	selected.Graph.Symbols = []graph.SymbolNode{{ID: "github.com/acme/service/api::Handle", Name: "Handle", Kind: graph.KindFunction, File: "api.go"}}
	decoy := fakeMember("decoy", "github.com/acme/service")
	decoy.Graph.Symbols = []graph.SymbolNode{{ID: "github.com/acme/service/api::Handle", Name: "Handle", Kind: graph.KindFunction, File: "decoy.go"}}
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, Name: "fleet", DefaultScope: "selected", Repositories: []RepositoryConfig{{ID: "client"}, {ID: "selected"}, {ID: "decoy"}}, Scopes: []ScopeConfig{{ID: "selected", Repositories: []string{"client", "selected"}}, {ID: "decoy-only", Repositories: []string{"decoy"}}}}
	artifact, err := Resolve(manifest, []LoadedMember{client, selected, decoy})
	if err != nil {
		t.Fatal(err)
	}
	var selectedScope ScopeOverlay
	for _, scope := range artifact.Scopes {
		if scope.ID == "selected" {
			selectedScope = scope
		}
	}
	if len(selectedScope.GoCalls) != 1 || selectedScope.GoCalls[0].Targets[0].RepositoryID != "selected" {
		t.Fatalf("selected scope Go calls = %+v", selectedScope.GoCalls)
	}
}

func TestOutOfScopeHTTPAuthorityCannotInfluenceResolution(t *testing.T) {
	client := fakeMember("client", "example.com/client")
	client.Graph.Symbols = []graph.SymbolNode{{ID: "example.com/client::Call", Name: "Call", Kind: graph.KindFunction, File: "client.go"}}
	client.Graph.HTTPCalls = []graph.HTTPCallEdge{{FunctionName: "Call", SourceFile: "client.go", Method: "GET", URL: "https://api.internal/items"}}
	selected := fakeMember("selected", "example.com/selected")
	selected.Config.Services = []ServiceConfig{{ID: "selected-api", HTTP: HTTPServiceConfig{Authorities: []string{"api.internal"}}}}
	selected.Graph.Symbols = []graph.SymbolNode{{ID: "example.com/selected::Handle", Name: "Handle", Kind: graph.KindFunction, File: "server.go"}}
	selected.Graph.Routes = []graph.HTTPRoute{{Method: "GET", Path: "/items", Handler: "Handle", File: "server.go"}}
	decoy := fakeMember("decoy", "example.com/decoy")
	decoy.Config.Services = []ServiceConfig{{ID: "decoy-api", HTTP: HTTPServiceConfig{Authorities: []string{"api.internal"}}}}
	decoy.Graph.Symbols = []graph.SymbolNode{{ID: "example.com/decoy::Handle", Name: "Handle", Kind: graph.KindFunction, File: "decoy.go"}}
	decoy.Graph.Routes = []graph.HTTPRoute{{Method: "GET", Path: "/items", Handler: "Handle", File: "decoy.go"}}
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, Name: "fleet", DefaultScope: "selected", Repositories: []RepositoryConfig{client.Config, selected.Config, decoy.Config}, Scopes: []ScopeConfig{{ID: "selected", Repositories: []string{"client", "selected"}}, {ID: "decoy", Repositories: []string{"decoy"}}}}
	artifact, err := Resolve(manifest, []LoadedMember{client, selected, decoy})
	if err != nil {
		t.Fatal(err)
	}
	for _, relation := range artifact.Scopes[1].HTTPRelations {
		if relation.Source.RepositoryID == "decoy" {
			t.Fatalf("out-of-scope HTTP decoy influenced selected resolution: %+v", artifact.Scopes[1].HTTPRelations)
		}
	}
}

func TestPossibleEdgeDoesNotTraverseOrSatisfyDerivedPathByDefault(t *testing.T) {
	client := fakeMember("client", "github.com/acme/client")
	client.Graph.Symbols = []graph.SymbolNode{{ID: "github.com/acme/client::Call", Name: "Call", Kind: graph.KindFunction}}
	server := fakeMember("server", "github.com/acme/server")
	server.Graph.Symbols = []graph.SymbolNode{{ID: "github.com/acme/server::Handle", Name: "Handle", Kind: graph.KindFunction}}
	contract := HTTPContractID{AuthorityID: "api", Method: "GET", NormalizedPath: "/items/{}"}
	artifact := &Artifact{SchemaVersion: ArtifactSchemaVersion, WorkspaceName: "fleet", DefaultScope: "default", Scopes: []ScopeOverlay{{ID: "default", Repositories: []string{"client", "server"}, HTTPRelations: []HTTPRelation{
		{Kind: "calls_http", Source: NodeRef{RepositoryID: "client", ModuleID: "github.com/acme/client", NodeID: "github.com/acme/client::Call", Kind: "symbol", Language: "go"}, Contract: contract, ResolutionStatus: ResolutionPossible, EvidenceOrigin: EvidenceDerived},
		{Kind: "serves_http", Source: NodeRef{RepositoryID: "server", ModuleID: "github.com/acme/server", NodeID: "github.com/acme/server::Handle", Kind: "symbol", Language: "go"}, Contract: contract, ResolutionStatus: ResolutionExact, EvidenceOrigin: EvidenceDerived},
	}}}}
	loaded := &LoadedWorkspace{Manifest: Manifest{Name: "fleet"}, Members: []LoadedMember{client, server}, Artifact: artifact}
	scope := artifact.Scopes[0]
	path, err := Path(loaded, scope, "client:Call", "server:Handle", false)
	if err != nil {
		t.Fatal(err)
	}
	if path.Found {
		t.Fatalf("possible input produced default traversable path: %+v", path)
	}
	path, err = Path(loaded, scope, "client:Call", "server:Handle", true)
	if err != nil || !path.Found {
		t.Fatalf("include possible path = %+v, %v", path, err)
	}
}

func TestPreciseCrossRepositoryCallResolvesExactly(t *testing.T) {
	client := fakeMember("client", "example.com/client")
	client.Graph.Symbols = []graph.SymbolNode{{ID: "example.com/client::Call", Name: "Call", Kind: graph.KindFunction}}
	client.Graph.Calls = []graph.CallEdge{{CallerSymbolID: "example.com/client::Call", CalleeSymbolID: "example.com/service/api::Handle", CalleeRaw: "api.Handle", Resolution: graph.CallResolutionStatic}}
	server := fakeMember("server", "example.com/service")
	server.Graph.Symbols = []graph.SymbolNode{{ID: "example.com/service/api::Handle", Name: "Handle", Kind: graph.KindFunction}}
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, Name: "fleet", Repositories: []RepositoryConfig{client.Config, server.Config}, Scopes: []ScopeConfig{{ID: "default", Repositories: []string{"client", "server"}}}}
	artifact, err := Resolve(manifest, []LoadedMember{client, server})
	if err != nil {
		t.Fatal(err)
	}
	resolved := artifact.Scopes[0].GoCalls
	if len(resolved) != 1 || resolved[0].ResolutionStatus != ResolutionExact || resolved[0].Targets[0].RepositoryID != "server" {
		t.Fatalf("precise resolutions = %+v", resolved)
	}
}

func TestVirtualCallsPreserveSyntheticAndExcludeCHAPossibilitiesByDefault(t *testing.T) {
	member := fakeMember("repo", "example.com/repo")
	member.Graph.Symbols = []graph.SymbolNode{
		{ID: "example.com/repo::Caller", Name: "Caller", Kind: graph.KindFunction},
		{ID: "example.com/repo::Target", Name: "Target", Kind: graph.KindFunction},
	}
	member.Graph.Calls = []graph.CallEdge{
		{CallerSymbolID: "example.com/repo::Caller", CalleeSymbolID: "example.com/repo::Target", Resolution: graph.CallResolutionCHA},
		{CallerSymbolID: "example.com/repo::Target", CalleeSymbolID: "example.com/repo::Caller", Resolution: graph.CallResolutionSynthetic, Synthetic: true},
	}
	loaded := &LoadedWorkspace{Members: []LoadedMember{member}}
	scope := ScopeOverlay{ID: "default", Repositories: []string{"repo"}}
	edges := VirtualEdges(loaded, scope, false)
	if len(edges) != 1 || edges[0].ResolutionStatus != ResolutionExact || edges[0].From.NodeID != "example.com/repo::Target" {
		t.Fatalf("default virtual calls = %+v", edges)
	}
	edges = VirtualEdges(loaded, scope, true)
	if len(edges) != 2 {
		t.Fatalf("possible virtual calls = %+v", edges)
	}
}

func TestModuleImportsMaterializeAsVirtualEdges(t *testing.T) {
	member := fakeMember("client", "example.com/client")
	loaded := &LoadedWorkspace{Members: []LoadedMember{member}}
	scope := ScopeOverlay{ID: "default", Repositories: []string{"client", "server"}, Imports: []ModuleImportResolution{{
		Source:           NodeRef{RepositoryID: "client", NodeID: "example.com/client", Kind: "package", Language: "go"},
		Target:           NodeRef{RepositoryID: "server", NodeID: "example.com/server", Kind: "module", Language: "go"},
		ResolutionStatus: ResolutionExact, EvidenceOrigin: EvidenceStructural,
	}}}
	edges := VirtualEdges(loaded, scope, false)
	if len(edges) != 1 || edges[0].Kind != "imports_module" {
		t.Fatalf("module import edges = %+v", edges)
	}
}

func TestDynamicHTTPHandlerDegradesServingRelation(t *testing.T) {
	server := fakeMember("server", "example.com/server")
	server.Config.Services = []ServiceConfig{{ID: "api", HTTP: HTTPServiceConfig{Authorities: []string{"api.internal"}}}}
	server.Graph.Routes = []graph.HTTPRoute{{Method: "GET", Path: "/items", Handler: "Factory", DynamicHandler: true}}
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, Name: "fleet", Repositories: []RepositoryConfig{server.Config}, Scopes: []ScopeConfig{{ID: "default", Repositories: []string{"server"}}}}
	artifact, err := Resolve(manifest, []LoadedMember{server})
	if err != nil {
		t.Fatal(err)
	}
	relations := artifact.Scopes[0].HTTPRelations
	if len(relations) != 1 || relations[0].ResolutionStatus != ResolutionPossible {
		t.Fatalf("dynamic handler relations = %+v", relations)
	}
}

func TestHTTPCatchAllMatchesRemainderAndModulePrefixUsesBoundary(t *testing.T) {
	if !httpPathMatches("/files/*", "/files/a/b/c") {
		t.Fatal("catch-all route did not match remaining segments")
	}
	member := fakeMember("repo", "example.com/foo")
	if got := moduleForNode(member, "example.com/foobar::Wrong"); got != "" {
		t.Fatalf("non-segment module prefix matched %q", got)
	}
}

func TestHTTPSchemeIsQualifierNotContractIdentity(t *testing.T) {
	client := fakeMember("client", "github.com/acme/client")
	client.Config.Services = nil
	client.Graph.Symbols = []graph.SymbolNode{{ID: "github.com/acme/client::Call", Name: "Call", Kind: graph.KindFunction, File: "client.go"}}
	client.Graph.HTTPCalls = []graph.HTTPCallEdge{
		{FunctionName: "Call", SourceFile: "client.go", Method: "GET", URL: "http://api.internal/items"},
		{FunctionName: "Call", SourceFile: "client.go", Method: "GET", URL: "https://api.internal/items"},
	}
	server := fakeMember("server", "github.com/acme/server")
	server.Config.Services = []ServiceConfig{{ID: "api", HTTP: HTTPServiceConfig{Authorities: []string{"api.internal"}}}}
	server.Graph.Symbols = []graph.SymbolNode{{ID: "github.com/acme/server::Handle", Name: "Handle", Kind: graph.KindFunction, File: "server.go"}}
	server.Graph.Routes = []graph.HTTPRoute{{Method: "GET", Path: "/items", Handler: "Handle", File: "server.go"}}
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, Name: "fleet", DefaultScope: "default", Repositories: []RepositoryConfig{client.Config, server.Config}, Scopes: []ScopeConfig{{ID: "default", Repositories: []string{"client", "server"}}}}
	artifact, err := Resolve(manifest, []LoadedMember{client, server})
	if err != nil {
		t.Fatal(err)
	}
	contracts := artifact.Scopes[0].HTTPContracts
	if len(contracts) != 1 || len(contracts[0].Qualifiers) != 2 {
		t.Fatalf("contracts = %+v", contracts)
	}
}

func TestHTTPAuthorityCollisionRequiresExplicitSharedOwnership(t *testing.T) {
	first := fakeMember("first", "example.com/first")
	second := fakeMember("second", "example.com/second")
	first.Config.Services = []ServiceConfig{{ID: "first-api", HTTP: HTTPServiceConfig{Authorities: []string{"api.internal"}}}}
	second.Config.Services = []ServiceConfig{{ID: "second-api", HTTP: HTTPServiceConfig{Authorities: []string{"api.internal"}}}}
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, Name: "fleet", Repositories: []RepositoryConfig{first.Config, second.Config}, Scopes: []ScopeConfig{{ID: "default", Repositories: []string{"first", "second"}}}}
	if _, err := Resolve(manifest, []LoadedMember{first, second}); err == nil || !strings.Contains(err.Error(), "multiple owners") {
		t.Fatalf("HTTP collision error = %v", err)
	}
	first.Config.Services[0].HTTP.SharedAuthority = true
	second.Config.Services[0].HTTP.SharedAuthority = true
	manifest.Repositories = []RepositoryConfig{first.Config, second.Config}
	if _, err := Resolve(manifest, []LoadedMember{first, second}); err != nil {
		t.Fatalf("explicit shared authority: %v", err)
	}
}

func fakeMember(id, module string) LoadedMember {
	config := RepositoryConfig{ID: id, Path: id, Precision: "ast"}
	moduleNode := graph.ModuleNode{ID: module, Path: module, Dir: "."}
	g := &graph.Graph{Version: graph.Version, Root: "/" + id, Build: &graph.BuildMetadata{Complete: true, WorkspaceFactsVersion: graph.CurrentWorkspaceFactsVersion}, Modules: []graph.ModuleNode{moduleNode}}
	return LoadedMember{Config: config, Root: "/" + id, Graph: g, Record: Member{RepositoryID: id, Path: id, ArtifactFingerprint: id, Modules: []graph.ModuleNode{moduleNode}}}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
