package workspace

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func dynamicHTTPFixture() (Manifest, []LoadedMember) {
	client := fakeMember("client", "example.com/client")
	client.Config.HTTPClients = []HTTPClientConfig{{Base: "cfg.API", AuthorityID: "api", PathPrefix: "/v1"}}
	client.Graph.Symbols = []graph.SymbolNode{{ID: "example.com/client::Call", Name: "Call", Kind: graph.KindFunction, File: "client.go"}}
	client.Graph.HTTPCalls = []graph.HTTPCallEdge{{FunctionName: "Call", SourceFile: "client.go", SourceLine: 4, Method: "GET", URL: "cfg.API/items", HasDynamic: true, URLBase: "cfg.API", URLSuffix: "/items", URLSuffixStatic: true}}
	server := fakeMember("server", "example.com/server")
	server.Config.Services = []ServiceConfig{{ID: "api", HTTP: HTTPServiceConfig{Authorities: []string{"api.internal"}}}}
	server.Graph.Symbols = []graph.SymbolNode{{ID: "example.com/server::Handle", Name: "Handle", Kind: graph.KindFunction, File: "server.go"}}
	server.Graph.Routes = []graph.HTTPRoute{{Method: "GET", Path: "/v1/items", Handler: "Handle", File: "server.go", Line: 5}}
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, Name: "fleet", DefaultScope: "production", Repositories: []RepositoryConfig{client.Config, server.Config}, Scopes: []ScopeConfig{{ID: "production", Repositories: []string{"client", "server"}}}}
	return manifest, []LoadedMember{client, server}
}

func TestConfiguredDynamicHTTPPathAndCertainty(t *testing.T) {
	for _, test := range []struct {
		name                    string
		request, dynamicHandler bool
	}{
		{name: "exact configured"},
		{name: "construction only", request: true},
		{name: "possible handler", dynamicHandler: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest, members := dynamicHTTPFixture()
			members[0].Graph.HTTPCalls[0].RequestOnly = test.request
			members[1].Graph.Routes[0].DynamicHandler = test.dynamicHandler
			artifact, err := Resolve(manifest, members)
			if err != nil {
				t.Fatal(err)
			}
			scope := artifact.Scopes[0]
			if len(scope.HTTPUnresolved) != 0 || len(scope.HTTPContracts) != 1 || len(scope.HTTPRelations) != 2 {
				t.Fatalf("overlay = %+v", scope)
			}
			for _, relation := range scope.HTTPRelations {
				if relation.Kind == "calls_http" && relation.EvidenceOrigin != EvidenceConfigured {
					t.Fatalf("configured provenance lost: %+v", relation)
				}
			}
			loaded := &LoadedWorkspace{Manifest: manifest, Members: members, Artifact: artifact}
			for _, includePossible := range []bool{false, true} {
				path, err := Path(loaded, scope, "client:Call", "server:Handle", includePossible)
				want := includePossible || (!test.request && !test.dynamicHandler)
				if err != nil || path.Found != want {
					t.Fatalf("includePossible=%t: path=%+v, err=%v", includePossible, path, err)
				}
			}
		})
	}
}

func TestDynamicHTTPUnresolvedEvidenceAndScopeIsolation(t *testing.T) {
	for _, test := range []struct{ name, reason string }{
		{"unconfigured", "unconfigured_base"},
		{"dynamic tail", "dynamic_url_not_bounded"},
		{"out of scope", "authority_not_in_scope"},
		{"authority injection", "unsupported_url_suffix"},
		{"absolute URL injection", "unsupported_url_suffix"},
		{"unknown literal host", "unconfigured_host"},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest, members := dynamicHTTPFixture()
			switch test.name {
			case "unconfigured":
				manifest.Repositories[0].HTTPClients = nil
			case "dynamic tail":
				members[0].Graph.HTTPCalls[0].URLSuffixStatic = false
			case "out of scope":
				manifest.Scopes[0].Repositories = []string{"client"}
				manifest.Scopes = append(manifest.Scopes, ScopeConfig{ID: "decoy", Repositories: []string{"server"}})
			case "authority injection":
				manifest.Repositories[0].HTTPClients[0].PathPrefix = ""
				members[0].Graph.HTTPCalls[0].URLSuffix = "//evil.invalid/items"
			case "absolute URL injection":
				members[0].Graph.HTTPCalls[0].URLSuffix = "https://evil.invalid/items"
			case "unknown literal host":
				members[0].Graph.HTTPCalls[0].HasDynamic = false
				members[0].Graph.HTTPCalls[0].URL = "https://unknown.invalid/items"
			}
			artifact, err := Resolve(manifest, members)
			if err != nil {
				t.Fatal(err)
			}
			loaded := &LoadedWorkspace{Manifest: manifest, Members: members, Artifact: artifact}
			scope, err := SelectScope(loaded, "production")
			if err != nil || len(scope.HTTPUnresolved) != 1 || scope.HTTPUnresolved[0].Reason != test.reason {
				t.Fatalf("scope = %+v, %v", scope, err)
			}
			for _, edge := range VirtualEdges(loaded, scope, true) {
				if edge.Kind == "calls_http" {
					t.Fatalf("unresolved fact materialized into an edge: %+v", edge)
				}
			}
			query := Query(loaded, scope, test.reason)
			if len(query.HTTPUnresolved) != 1 || query.HTTPUnresolved[0].Source.RepositoryID != "client" {
				t.Fatalf("diagnostic not queryable: %+v", query)
			}
			if len(Query(loaded, scope, "no-match").HTTPUnresolved) != 0 {
				t.Fatal("diagnostics ignored query filter")
			}
		})
	}
}

func TestDynamicHTTPManifestValidationAndFingerprint(t *testing.T) {
	for _, test := range []struct {
		name    string
		clients []HTTPClientConfig
	}{
		{"duplicate", []HTTPClientConfig{{Base: "cfg.API", AuthorityID: "api"}, {Base: "cfg.API", AuthorityID: "other"}}},
		{"expression", []HTTPClientConfig{{Base: "lookup()", AuthorityID: "api"}}},
		{"empty", []HTTPClientConfig{{Base: "env:", AuthorityID: "api"}}},
		{"invalid authority", []HTTPClientConfig{{Base: "cfg.API", AuthorityID: "../api"}}},
		{"path authority", []HTTPClientConfig{{Base: "cfg.API", AuthorityID: "api", PathPrefix: "//evil.invalid"}}},
		{"path traversal", []HTTPClientConfig{{Base: "cfg.API", AuthorityID: "api", PathPrefix: "/../admin"}}},
		{"encoded traversal", []HTTPClientConfig{{Base: "cfg.API", AuthorityID: "api", PathPrefix: "/%2e%2e/admin"}}},
		{"query prefix", []HTTPClientConfig{{Base: "cfg.API", AuthorityID: "api", PathPrefix: "/v1?key=secret"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := RepositoryConfig{ID: "client", HTTPClients: test.clients}
			if err := validateHTTPClients(&repo); err == nil {
				t.Fatalf("accepted %+v", test.clients)
			}
		})
	}
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "client"))
	mustWrite(t, filepath.Join(root, ManifestFile), `schema_version: gograph.workspace-manifest.v1
name: configured
repositories:
  - id: client
    path: client
    http_clients:
      - base: env:API_URL
        authority_id: api
        path_prefix: /v1
`)
	loaded, _, err := LoadManifest(root)
	if err != nil || len(loaded.Repositories[0].HTTPClients) != 1 {
		t.Fatalf("load HTTP client manifest: %+v, %v", loaded, err)
	}
	manifest, members := dynamicHTTPFixture()
	first, err := Resolve(manifest, members)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, _ := EncodeArtifact(first)
	for range 10 {
		again, err := Resolve(manifest, []LoadedMember{members[1], members[0]})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := EncodeArtifact(again)
		if !bytes.Equal(firstBytes, encoded) {
			t.Fatal("HTTP resolver is not deterministic")
		}
	}
	manifest.Repositories[0].HTTPClients[0].PathPrefix = "/v2"
	second, err := Resolve(manifest, members)
	if err != nil || second.InputFingerprint == first.InputFingerprint {
		t.Fatalf("configuration change did not invalidate fingerprint: %+v, %v", second, err)
	}
	if strings.Contains(string(firstBytes), "secret.invalid") {
		t.Fatal("runtime environment leaked into workspace artifact")
	}
}
