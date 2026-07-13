package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ozgurcd/gograph/internal/mcpbundle"
)

func TestValidateServerPackages(t *testing.T) {
	t.Parallel()
	doc := validServerDocument("1.5.0")
	expected, err := validateServerPackages(doc, "1.5.0")
	if err != nil {
		t.Fatalf("validateServerPackages() error = %v", err)
	}
	if len(expected) != len(mcpbundle.Targets) {
		t.Fatalf("expected %d hashes, got %d", len(mcpbundle.Targets), len(expected))
	}

	doc.Packages[0].Transport.Type = "sse"
	if _, err := validateServerPackages(doc, "1.5.0"); err == nil {
		t.Fatal("validateServerPackages() accepted a non-stdio transport")
	}
}

func TestValidateServerPackagesFailsClosed(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*serverDocument){
		"server version mismatch":  func(doc *serverDocument) { doc.Version = "1.5.1" },
		"package version mismatch": func(doc *serverDocument) { doc.Packages[0].Version = "1.5.1" },
		"missing hash":             func(doc *serverDocument) { doc.Packages[0].FileSHA256 = "" },
		"uppercase hash":           func(doc *serverDocument) { doc.Packages[0].FileSHA256 = strings.Repeat("A", 64) },
		"mutable URL": func(doc *serverDocument) {
			doc.Packages[0].Identifier = repositoryURL + "/releases/latest/download/gograph.mcpb"
		},
		"registry base URL": func(doc *serverDocument) { doc.Packages[0].RegistryBase = json.RawMessage(`"https://github.com"`) },
		"missing package":   func(doc *serverDocument) { doc.Packages = doc.Packages[1:] },
		"duplicate package": func(doc *serverDocument) {
			doc.Packages[len(doc.Packages)-1] = doc.Packages[0]
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			doc := validServerDocument("1.5.0")
			mutate(doc)
			if _, err := validateServerPackages(doc, "1.5.0"); err == nil {
				t.Fatal("invalid publication metadata was accepted")
			}
		})
	}
}

func TestCanonicalServerShapeRejectsMissingHashAndExtraArguments(t *testing.T) {
	t.Parallel()
	doc := validServerDocument("1.5.0")
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCanonicalServerShape(raw); err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	packages := object["packages"].([]any)
	first := packages[0].(map[string]any)
	delete(first, "fileSha256")
	missingHash, _ := json.Marshal(object)
	if err := validateCanonicalServerShape(missingHash); err == nil {
		t.Fatal("server.json without fileSha256 was accepted")
	}
	first["fileSha256"] = strings.Repeat("a", 64)
	first["packageArguments"] = []any{map[string]any{"type": "positional", "value": "mcp"}}
	extraArguments, _ := json.Marshal(object)
	if err := validateCanonicalServerShape(extraArguments); err == nil {
		t.Fatal("server.json duplicating manifest arguments was accepted")
	}
}

func TestRenderServerDocumentIsDeterministicAndArgumentFree(t *testing.T) {
	t.Parallel()
	const version = "1.5.0"
	artifacts := make([]mcpbundle.Artifact, 0, len(mcpbundle.Targets))
	for index, target := range mcpbundle.Targets {
		artifacts = append(artifacts, mcpbundle.Artifact{
			Target: target,
			Name:   target.ArtifactName(version),
			SHA256: strings.Repeat(string(rune('a'+index)), 64),
		})
	}
	first, err := renderServerDocument(version, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderServerDocument(version, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("renderServerDocument() is not deterministic")
	}
	var rendered map[string]any
	if err := json.Unmarshal(first, &rendered); err != nil {
		t.Fatal(err)
	}
	if rendered["name"] != registryName || rendered["version"] != version {
		t.Fatalf("unexpected Registry identity: %v", rendered)
	}
	repository := rendered["repository"].(map[string]any)
	if repository["id"] != repositoryID {
		t.Fatalf("repository.id = %v, want %s", repository["id"], repositoryID)
	}
	packages := rendered["packages"].([]any)
	if len(packages) != len(mcpbundle.Targets) {
		t.Fatalf("rendered %d packages, want %d", len(packages), len(mcpbundle.Targets))
	}
	for _, value := range packages {
		pkg := value.(map[string]any)
		if _, duplicatedArgs := pkg["packageArguments"]; duplicatedArgs {
			t.Fatal("server.json must not duplicate manifest launch arguments")
		}
	}
}

func TestLocalMetadataMatchesPublishedAllowsRegistryExtensions(t *testing.T) {
	t.Parallel()
	local := []byte(`{
  "$schema":"https://example.test/schema.json",
  "name":"io.github.ozgurcd/gograph",
  "version":"1.5.0",
  "packages":[
    {"registryType":"mcpb","identifier":"https://example.test/b.mcpb"},
    {"registryType":"mcpb","identifier":"https://example.test/a.mcpb"}
  ]
}`)
	published := []byte(`{
  "name":"io.github.ozgurcd/gograph",
  "version":"1.5.0",
  "packages":[
    {"registryType":"mcpb","identifier":"https://example.test/a.mcpb","registryBaseUrl":"https://example.test"},
    {"registryType":"mcpb","identifier":"https://example.test/b.mcpb","registryBaseUrl":"https://example.test"}
  ],
  "_meta":{"official":{"published":true}}
}`)
	matches, err := localMetadataMatchesPublished(local, published)
	if err != nil {
		t.Fatalf("localMetadataMatchesPublished() error = %v", err)
	}
	if !matches {
		t.Fatal("metadata should match after sorting packages and ignoring Registry extensions")
	}
}

func TestRegistryState(t *testing.T) {
	t.Parallel()
	doc := validServerDocument("1.5.0")
	local, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !strings.HasSuffix(request.URL.Path, "/versions/"+doc.Version) {
			t.Errorf("exact Registry lookup path = %q", request.URL.Path)
		}
		_ = json.NewEncoder(response).Encode(map[string]any{
			"server": doc,
			"_meta": map[string]any{
				"io.modelcontextprotocol.registry/official": map[string]any{"status": "active"},
			},
		})
	}))
	defer server.Close()

	state, err := registryState(context.Background(), server.URL, local, doc.Name, doc.Version)
	if err != nil {
		t.Fatalf("registryState() error = %v", err)
	}
	if state != "matching" {
		t.Fatalf("registryState() = %q, want matching", state)
	}
}

func TestRegistryStatePendingAndMissing(t *testing.T) {
	t.Parallel()
	doc := validServerDocument("1.5.0")
	local, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	pending := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(response).Encode(map[string]any{
			"server": doc,
			"_meta": map[string]any{
				"io.modelcontextprotocol.registry/official": map[string]any{"status": "pending"},
			},
		})
	}))
	defer pending.Close()
	state, err := registryState(context.Background(), pending.URL, local, doc.Name, doc.Version)
	if err != nil || state != "pending" {
		t.Fatalf("pending registryState() = %q, %v", state, err)
	}

	missing := httptest.NewServer(http.NotFoundHandler())
	defer missing.Close()
	state, err = registryState(context.Background(), missing.URL, local, doc.Name, doc.Version)
	if err != nil || state != "missing" {
		t.Fatalf("missing registryState() = %q, %v", state, err)
	}
}

func TestGitHubReleaseStateMissing(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	t.Setenv("GITHUB_API_URL", server.URL)

	state, err := githubReleaseState(context.Background(), "ozgurcd/gograph", "v1.5.0", []byte("{}"), map[string]string{})
	if err != nil {
		t.Fatalf("githubReleaseState() error = %v", err)
	}
	if state != "missing" {
		t.Fatalf("githubReleaseState() = %q, want missing", state)
	}
}

func TestGitHubReleaseStateRejectsIncompleteRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(response).Encode(map[string]any{
			"tag_name":   "v1.5.0",
			"draft":      false,
			"prerelease": false,
			"assets":     []any{},
		})
	}))
	defer server.Close()
	t.Setenv("GITHUB_API_URL", server.URL)

	if _, err := githubReleaseState(context.Background(), "ozgurcd/gograph", "v1.5.0", []byte("{}"), map[string]string{}); err == nil {
		t.Fatal("incomplete GitHub release was accepted")
	}
}

func TestGitHubReleaseStateVerifiesDownloadedMCPBs(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-compiles the six published MCPB fixtures")
	}
	const version = "1.5.0"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	fixture := newPublishedReleaseFixture(t, ctx, version)

	t.Run("complete matching release", func(t *testing.T) {
		server, requests := fixture.server(t, nil)
		defer server.Close()
		t.Setenv("GITHUB_API_URL", server.URL)

		state, err := githubReleaseState(ctx, "ozgurcd/gograph", "v"+version, fixture.serverJSON, fixture.expectedMCPB)
		if err != nil {
			t.Fatalf("githubReleaseState() error = %v", err)
		}
		if state != "matching" {
			t.Fatalf("githubReleaseState() = %q, want matching", state)
		}
		for _, target := range mcpbundle.Targets {
			name := target.ArtifactName(version)
			if got := requests.count(name); got != 1 {
				t.Errorf("download count for %s = %d, want 1", name, got)
			}
		}
	})

	t.Run("corrupted remote MCPB", func(t *testing.T) {
		name := mcpbundle.Targets[0].ArtifactName(version)
		corrupted := append([]byte(nil), fixture.contents[name]...)
		corrupted[len(corrupted)/2] ^= 0xff
		server, requests := fixture.server(t, map[string][]byte{name: corrupted})
		defer server.Close()
		t.Setenv("GITHUB_API_URL", server.URL)

		if _, err := githubReleaseState(ctx, "ozgurcd/gograph", "v"+version, fixture.serverJSON, fixture.expectedMCPB); err == nil {
			t.Fatal("githubReleaseState() accepted a corrupted downloaded MCPB")
		} else if !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "computed") {
			t.Fatalf("githubReleaseState() corruption error = %v", err)
		}
		if got := requests.count(name); got != 1 {
			t.Fatalf("download count for corrupted %s = %d, want 1", name, got)
		}
	})
}

func TestParseChecksumsRejectsDuplicates(t *testing.T) {
	t.Parallel()
	hash := strings.Repeat("a", 64)
	if _, err := parseChecksums([]byte(hash + "  artifact\n" + hash + "  artifact\n")); err == nil {
		t.Fatal("parseChecksums() accepted duplicate artifact names")
	}
}

func validServerDocument(version string) *serverDocument {
	doc := &serverDocument{
		Schema:      registrySchemaURL,
		Name:        registryName,
		Title:       registryTitle,
		Description: registryDescription,
		Version:     version,
		WebsiteURL:  registryWebsite,
		Repository:  serverRepository{URL: repositoryURL, Source: "github", ID: repositoryID},
	}
	for index, target := range mcpbundle.Targets {
		name := target.ArtifactName(version)
		doc.Packages = append(doc.Packages, serverPackage{
			RegistryType: "mcpb",
			Identifier:   "https://github.com/ozgurcd/gograph/releases/download/v" + version + "/" + name,
			Version:      version,
			FileSHA256:   strings.Repeat(string(rune('a'+index)), 64),
			Transport:    packageTransport{Type: "stdio"},
		})
	}
	return doc
}

type publishedReleaseFixture struct {
	version      string
	serverJSON   []byte
	expectedMCPB map[string]string
	contents     map[string][]byte
	assetNames   []string
}

func newPublishedReleaseFixture(t *testing.T, ctx context.Context, version string) *publishedReleaseFixture {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := mcpbundle.BuildAll(ctx, root, t.TempDir(), version)
	if err != nil {
		t.Fatalf("build published MCPB fixtures: %v", err)
	}
	serverJSON, err := renderServerDocument(version, artifacts)
	if err != nil {
		t.Fatalf("render fixture server.json: %v", err)
	}

	fixture := &publishedReleaseFixture{
		version:      version,
		serverJSON:   serverJSON,
		expectedMCPB: make(map[string]string, len(artifacts)),
		contents:     map[string][]byte{"server.json": serverJSON},
	}
	checksumNames := ordinaryReleaseAssets()
	for _, name := range ordinaryReleaseAssets() {
		fixture.contents[name] = []byte("ordinary release fixture for " + name)
	}
	for _, artifact := range artifacts {
		contents, readErr := os.ReadFile(artifact.Path)
		if readErr != nil {
			t.Fatalf("read fixture %s: %v", artifact.Name, readErr)
		}
		fixture.contents[artifact.Name] = contents
		fixture.expectedMCPB[artifact.Name] = artifact.SHA256
		checksumNames = append(checksumNames, artifact.Name)
	}
	sort.Strings(checksumNames)
	var checksums strings.Builder
	for _, name := range checksumNames {
		_, _ = fmt.Fprintf(&checksums, "%s  %s\n", sha256Hex(fixture.contents[name]), name)
	}
	fixture.contents["checksums.txt"] = []byte(checksums.String())
	for name := range fixture.contents {
		fixture.assetNames = append(fixture.assetNames, name)
	}
	sort.Strings(fixture.assetNames)
	return fixture
}

type assetRequestCounts struct {
	mu     sync.Mutex
	counts map[string]int
}

func (c *assetRequestCounts) count(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[name]
}

func (f *publishedReleaseFixture) server(t *testing.T, overrides map[string][]byte) (*httptest.Server, *assetRequestCounts) {
	t.Helper()
	requests := &assetRequestCounts{counts: make(map[string]int)}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/repos/ozgurcd/gograph/releases/tags/") {
			assets := make([]githubAsset, 0, len(f.assetNames))
			for _, name := range f.assetNames {
				assets = append(assets, githubAsset{
					Name:               name,
					Digest:             "sha256:" + sha256Hex(f.contents[name]),
					BrowserDownloadURL: server.URL + "/assets/" + url.PathEscape(name),
				})
			}
			_ = json.NewEncoder(response).Encode(githubRelease{
				TagName: "v" + f.version,
				Assets:  assets,
			})
			return
		}
		if !strings.HasPrefix(request.URL.Path, "/assets/") {
			http.NotFound(response, request)
			return
		}
		name, err := url.PathUnescape(strings.TrimPrefix(request.URL.Path, "/assets/"))
		if err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)
			return
		}
		contents, ok := f.contents[name]
		if replacement, replaced := overrides[name]; replaced {
			contents, ok = replacement, true
		}
		if !ok {
			http.NotFound(response, request)
			return
		}
		requests.mu.Lock()
		requests.counts[name]++
		requests.mu.Unlock()
		_, _ = response.Write(contents)
	}))
	return server, requests
}

func sha256Hex(contents []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(contents))
}
