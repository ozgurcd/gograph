// Command mcpb-release builds, validates, and smoke-tests gograph MCP bundles.
// It also provides fail-closed state checks used to make release workflow
// reruns safe without replacing published artifacts or Registry metadata.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/ozgurcd/gograph/internal/mcpbundle"
)

const defaultRegistryURL = "https://registry.modelcontextprotocol.io/v0.1/servers"

const (
	registrySchemaURL   = "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json"
	registryName        = "io.github.ozgurcd/gograph"
	registryTitle       = "gograph"
	registryDescription = "Local Go repository call graphs and structural analysis for coding agents over stdio."
	registryWebsite     = "https://gograph.identuum.ai"
	repositoryURL       = "https://github.com/ozgurcd/gograph"
	repositoryID        = "1233398203"
)

type serverDocument struct {
	Schema      string           `json:"$schema"`
	Name        string           `json:"name"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Version     string           `json:"version"`
	WebsiteURL  string           `json:"websiteUrl"`
	Repository  serverRepository `json:"repository"`
	Packages    []serverPackage  `json:"packages"`
}

type serverRepository struct {
	URL    string `json:"url"`
	Source string `json:"source"`
	ID     string `json:"id"`
}

type serverPackage struct {
	RegistryType string           `json:"registryType"`
	Identifier   string           `json:"identifier"`
	Version      string           `json:"version"`
	FileSHA256   string           `json:"fileSha256"`
	Transport    packageTransport `json:"transport"`
	RegistryBase json.RawMessage  `json:"registryBaseUrl,omitempty"`
}

type packageTransport struct {
	Type string `json:"type"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "mcpb-release: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError()
	}

	switch args[0] {
	case "auto-release":
		return runAutoRelease(ctx, args[1:])
	case "build":
		return runBuild(ctx, args[1:])
	case "verify":
		return runVerify(args[1:])
	case "render-server":
		return runRenderServer(args[1:])
	case "render-goreleaser":
		return runRenderGoReleaser(args[1:])
	case "smoke":
		return runSmoke(ctx, args[1:])
	case "github-release-state":
		return runGitHubReleaseState(ctx, args[1:])
	case "registry-state":
		return runRegistryState(ctx, args[1:])
	case "help", "-h", "--help":
		_, err := fmt.Fprintln(os.Stdout, usageText())
		return err
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usageText())
	}
}

func runBuild(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	version := fs.String("version", "", "release version without a v prefix")
	output := fs.String("output", ".release-mcpb", "bundle output directory")
	repositoryRoot := fs.String("repository-root", ".", "gograph repository root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireVersion(*version); err != nil {
		return err
	}

	artifacts, err := mcpbundle.BuildAll(ctx, *repositoryRoot, *output, *version)
	if err != nil {
		return fmt.Errorf("build bundles: %w", err)
	}
	for _, artifact := range artifacts {
		if _, err := fmt.Fprintf(os.Stdout, "%s  %s\n", artifact.SHA256, artifact.Path); err != nil {
			return err
		}
	}
	return nil
}

func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	version := fs.String("version", "", "release version without a v prefix")
	input := fs.String("input", ".release-mcpb", "bundle input directory")
	serverPath := fs.String("server", "server.json", "Registry server metadata")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireVersion(*version); err != nil {
		return err
	}

	doc, _, err := readServerDocument(*serverPath)
	if err != nil {
		return err
	}
	expected, err := validateServerPackages(doc, *version)
	if err != nil {
		return err
	}
	artifacts, err := mcpbundle.VerifyAllHashes(*input, *version, expected)
	if err != nil {
		return fmt.Errorf("verify bundles: %w", err)
	}
	for _, artifact := range artifacts {
		if _, err := fmt.Fprintf(os.Stdout, "verified %s\n", artifact.Name); err != nil {
			return err
		}
	}
	return nil
}

func runRenderServer(args []string) error {
	fs := flag.NewFlagSet("render-server", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	version := fs.String("version", "", "release version without a v prefix")
	input := fs.String("input", ".release-mcpb", "bundle input directory")
	output := fs.String("output", "server.json", "server.json output path, or - for stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireVersion(*version); err != nil {
		return err
	}
	artifacts, err := mcpbundle.VerifyAll(*input, *version)
	if err != nil {
		return fmt.Errorf("verify bundles before rendering server.json: %w", err)
	}
	rendered, err := renderServerDocument(*version, artifacts)
	if err != nil {
		return err
	}
	if *output == "-" {
		_, err = os.Stdout.Write(rendered)
		return err
	}
	if err := atomicWriteFile(*output, rendered, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", *output, err)
	}
	return nil
}

func runSmoke(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("smoke", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	version := fs.String("version", "", "release version without a v prefix")
	input := fs.String("input", ".release-mcpb", "bundle input directory")
	project := fs.String("project", "", "Go project used for the smoke test")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireVersion(*version); err != nil {
		return err
	}

	projectDir := *project
	if projectDir == "" {
		temporary, err := os.MkdirTemp("", "gograph-mcpb-smoke-")
		if err != nil {
			return fmt.Errorf("create smoke project: %w", err)
		}
		defer func() { _ = os.RemoveAll(temporary) }()
		projectDir = temporary
		if err := os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module example.com/gograph-mcpb-smoke\n\ngo 1.27.0\n"), 0o644); err != nil {
			return fmt.Errorf("write smoke go.mod: %w", err)
		}
		if err := os.WriteFile(filepath.Join(projectDir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
			return fmt.Errorf("write smoke source: %w", err)
		}
	}

	smokeCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result, err := mcpbundle.SmokeNative(smokeCtx, *input, projectDir, *version)
	if err != nil {
		return fmt.Errorf("smoke-test native bundle: %w", err)
	}
	if len(result.ToolNames) == 0 {
		return errors.New("native MCP server returned no tools")
	}
	_, err = fmt.Fprintf(os.Stdout, "initialized %s %s with %d tools\n", result.ServerName, result.ServerVersion, len(result.ToolNames))
	return err
}

func runGitHubReleaseState(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("github-release-state", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	repository := fs.String("repository", "", "GitHub owner/repository")
	tag := fs.String("tag", "", "release tag")
	serverPath := fs.String("server", "server.json", "Registry server metadata")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *repository == "" || strings.Count(*repository, "/") != 1 {
		return errors.New("--repository must be owner/name")
	}
	if *tag == "" {
		return errors.New("--tag is required")
	}

	doc, serverBytes, err := readServerDocument(*serverPath)
	if err != nil {
		return err
	}
	expectedMCPB, err := validateServerPackages(doc, strings.TrimPrefix(*tag, "v"))
	if err != nil {
		return err
	}
	state, err := githubReleaseState(ctx, *repository, *tag, serverBytes, expectedMCPB)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, state)
	return err
}

func runRegistryState(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("registry-state", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	serverPath := fs.String("server", "server.json", "Registry server metadata")
	registryURL := fs.String("registry-url", defaultRegistryURL, "Registry servers endpoint")
	wait := fs.Duration("wait", 0, "wait up to this duration for a matching entry")
	if err := fs.Parse(args); err != nil {
		return err
	}

	doc, raw, err := readServerDocument(*serverPath)
	if err != nil {
		return err
	}
	if _, err := validateServerPackages(doc, doc.Version); err != nil {
		return err
	}
	deadline := time.Now().Add(*wait)
	for {
		state, err := registryState(ctx, *registryURL, raw, doc.Name, doc.Version)
		if err != nil {
			return err
		}
		if state == "matching" || *wait == 0 || time.Now().After(deadline) {
			_, err = fmt.Fprintln(os.Stdout, state)
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func requireVersion(version string) error {
	if version == "" {
		return errors.New("--version is required")
	}
	if strings.HasPrefix(version, "v") || strings.ContainsAny(version, "/\\") {
		return fmt.Errorf("invalid release version %q", version)
	}
	return nil
}

func readServerDocument(filename string) (*serverDocument, []byte, error) {
	raw, err := os.ReadFile(filename)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", filename, err)
	}
	if err := mcpbundle.ValidateServerJSON(raw); err != nil {
		return nil, nil, fmt.Errorf("validate %s against the vendored Registry schema: %w", filename, err)
	}
	if err := validateCanonicalServerShape(raw); err != nil {
		return nil, nil, fmt.Errorf("validate canonical %s: %w", filename, err)
	}
	var doc serverDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, fmt.Errorf("decode %s: %w", filename, err)
	}
	if doc.Name == "" || doc.Version == "" {
		return nil, nil, fmt.Errorf("%s is missing name or version", filename)
	}
	return &doc, raw, nil
}

func validateServerPackages(doc *serverDocument, version string) (map[string]string, error) {
	if doc.Schema != registrySchemaURL || doc.Name != registryName || doc.Title != registryTitle || doc.Description != registryDescription || len(doc.Description) > 100 {
		return nil, fmt.Errorf("server.json identity or description metadata is not canonical")
	}
	if doc.WebsiteURL != registryWebsite {
		return nil, fmt.Errorf("server.json websiteUrl = %q, want %q", doc.WebsiteURL, registryWebsite)
	}
	wantRepository := serverRepository{URL: repositoryURL, Source: "github", ID: repositoryID}
	if doc.Repository != wantRepository {
		return nil, fmt.Errorf("server.json repository = %+v, want %+v", doc.Repository, wantRepository)
	}
	if doc.Version != version {
		return nil, fmt.Errorf("server version %q does not match release %q", doc.Version, version)
	}
	wanted := make(map[string]struct{}, len(mcpbundle.Targets))
	for _, target := range mcpbundle.Targets {
		wanted[target.ArtifactName(version)] = struct{}{}
	}
	if len(doc.Packages) != len(wanted) {
		return nil, fmt.Errorf("server.json has %d packages, want %d", len(doc.Packages), len(wanted))
	}

	expected := make(map[string]string, len(wanted))
	for _, pkg := range doc.Packages {
		if pkg.RegistryType != "mcpb" {
			return nil, fmt.Errorf("package %q has registryType %q, want mcpb", pkg.Identifier, pkg.RegistryType)
		}
		if len(pkg.RegistryBase) != 0 {
			return nil, fmt.Errorf("package %q must not set registryBaseUrl", pkg.Identifier)
		}
		if pkg.Version != version {
			return nil, fmt.Errorf("package %q version %q does not match %q", pkg.Identifier, pkg.Version, version)
		}
		if pkg.Transport.Type != "stdio" {
			return nil, fmt.Errorf("package %q transport is %q, want stdio", pkg.Identifier, pkg.Transport.Type)
		}
		if len(pkg.FileSHA256) != 64 || strings.ToLower(pkg.FileSHA256) != pkg.FileSHA256 {
			return nil, fmt.Errorf("package %q has invalid fileSha256", pkg.Identifier)
		}
		name := path.Base(pkg.Identifier)
		if _, ok := wanted[name]; !ok {
			return nil, fmt.Errorf("unexpected MCPB artifact %q", name)
		}
		expectedURL := repositoryURL + "/releases/download/v" + version + "/" + name
		if pkg.Identifier != expectedURL {
			return nil, fmt.Errorf("package identifier = %q, want %q", pkg.Identifier, expectedURL)
		}
		if _, duplicate := expected[name]; duplicate {
			return nil, fmt.Errorf("duplicate MCPB package %q", name)
		}
		expected[name] = pkg.FileSHA256
	}
	for name := range wanted {
		if _, ok := expected[name]; !ok {
			return nil, fmt.Errorf("server.json is missing package %q", name)
		}
	}
	return expected, nil
}

func usageError() error {
	return errors.New(usageText())
}

func usageText() string {
	return `usage: mcpb-release <command> [options]

commands:
  auto-release           bump, verify, commit, tag, and atomically push a patch release
  build                  build all six deterministic MCP bundles
  verify                 validate bundle layout, targets, versions, and hashes
  render-server          render deterministic Registry metadata from bundle hashes
  render-goreleaser      render a safe temporary GoReleaser snapshot configuration
  smoke                  initialize the native bundle and request tools/list
  github-release-state   print missing or matching; fail on divergent assets
  registry-state         print missing or matching; fail on divergent metadata`
}

func renderServerDocument(version string, artifacts []mcpbundle.Artifact) ([]byte, error) {
	if len(artifacts) != len(mcpbundle.Targets) {
		return nil, fmt.Errorf("render server.json from %d artifacts, want %d", len(artifacts), len(mcpbundle.Targets))
	}
	doc := serverDocument{
		Schema:      registrySchemaURL,
		Name:        registryName,
		Title:       registryTitle,
		Description: registryDescription,
		Version:     version,
		WebsiteURL:  registryWebsite,
		Repository:  serverRepository{URL: repositoryURL, Source: "github", ID: repositoryID},
		Packages:    make([]serverPackage, 0, len(artifacts)),
	}
	byName := make(map[string]mcpbundle.Artifact, len(artifacts))
	for _, artifact := range artifacts {
		if _, duplicate := byName[artifact.Name]; duplicate {
			return nil, fmt.Errorf("duplicate artifact %q", artifact.Name)
		}
		byName[artifact.Name] = artifact
	}
	for _, target := range mcpbundle.Targets {
		name := target.ArtifactName(version)
		artifact, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("missing artifact %q", name)
		}
		doc.Packages = append(doc.Packages, serverPackage{
			RegistryType: "mcpb",
			Identifier:   repositoryURL + "/releases/download/v" + version + "/" + name,
			Version:      version,
			FileSHA256:   artifact.SHA256,
			Transport:    packageTransport{Type: "stdio"},
		})
	}
	rendered, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode server.json: %w", err)
	}
	rendered = append(rendered, '\n')
	if err := mcpbundle.ValidateServerJSON(rendered); err != nil {
		return nil, fmt.Errorf("validate rendered server.json against the vendored Registry schema: %w", err)
	}
	if err := validateCanonicalServerShape(rendered); err != nil {
		return nil, fmt.Errorf("validate rendered canonical server.json: %w", err)
	}
	return rendered, nil
}

func validateCanonicalServerShape(raw []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	if err := requireExactKeys(root, "$schema", "name", "title", "description", "version", "websiteUrl", "repository", "packages"); err != nil {
		return err
	}
	var repository map[string]json.RawMessage
	if err := json.Unmarshal(root["repository"], &repository); err != nil {
		return fmt.Errorf("decode repository: %w", err)
	}
	if err := requireExactKeys(repository, "url", "source", "id"); err != nil {
		return fmt.Errorf("repository: %w", err)
	}
	var packages []map[string]json.RawMessage
	if err := json.Unmarshal(root["packages"], &packages); err != nil {
		return fmt.Errorf("decode packages: %w", err)
	}
	for index, pkg := range packages {
		if err := requireExactKeys(pkg, "registryType", "identifier", "version", "fileSha256", "transport"); err != nil {
			return fmt.Errorf("packages[%d]: %w", index, err)
		}
		var transport map[string]json.RawMessage
		if err := json.Unmarshal(pkg["transport"], &transport); err != nil {
			return fmt.Errorf("packages[%d].transport: %w", index, err)
		}
		if err := requireExactKeys(transport, "type"); err != nil {
			return fmt.Errorf("packages[%d].transport: %w", index, err)
		}
	}
	return nil
}

func requireExactKeys(object map[string]json.RawMessage, keys ...string) error {
	if len(object) != len(keys) {
		return fmt.Errorf("has %d properties, want exactly %v", len(object), keys)
	}
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			return fmt.Errorf("missing property %q", key)
		}
	}
	return nil
}

func atomicWriteFile(filename string, contents []byte, mode os.FileMode) error {
	directory := filepath.Dir(filename)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".server.json-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, filename)
}

func normalizeForRegistry(raw []byte) (map[string]any, error) {
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	delete(value, "$schema")
	delete(value, "_meta")
	sortRegistryPackages(value)
	return value, nil
}

func localMetadataMatchesPublished(local, published []byte) (bool, error) {
	want, err := normalizeForRegistry(local)
	if err != nil {
		return false, fmt.Errorf("normalize local Registry metadata: %w", err)
	}
	got, err := normalizeForRegistry(published)
	if err != nil {
		return false, fmt.Errorf("normalize published Registry metadata: %w", err)
	}
	return isJSONSubset(want, got), nil
}

func isJSONSubset(want, got any) bool {
	switch wanted := want.(type) {
	case map[string]any:
		actual, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range wanted {
			actualValue, exists := actual[key]
			if !exists || !isJSONSubset(value, actualValue) {
				return false
			}
		}
		return true
	case []any:
		actual, ok := got.([]any)
		if !ok || len(wanted) != len(actual) {
			return false
		}
		for index := range wanted {
			if !isJSONSubset(wanted[index], actual[index]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(want, got)
	}
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
