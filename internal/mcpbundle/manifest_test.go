package mcpbundle

import (
	"bytes"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const testVersion = "1.5.0"

func TestManifestForEverySupportedTarget(t *testing.T) {
	wantNames := []string{
		"gograph_1.5.0_darwin_amd64.mcpb",
		"gograph_1.5.0_darwin_arm64.mcpb",
		"gograph_1.5.0_linux_amd64.mcpb",
		"gograph_1.5.0_linux_arm64.mcpb",
		"gograph_1.5.0_windows_amd64.mcpb",
		"gograph_1.5.0_windows_arm64.mcpb",
	}
	if len(Targets) != len(wantNames) {
		t.Fatalf("Targets has %d entries, want %d", len(Targets), len(wantNames))
	}
	for index, target := range Targets {
		t.Run(target.GOOS+"_"+target.GOARCH, func(t *testing.T) {
			manifest, err := NewManifest(testVersion, target)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateManifest(manifest, testVersion, target); err != nil {
				t.Fatal(err)
			}
			if manifest.ManifestVersion != "0.4" {
				t.Fatalf("manifest version = %q", manifest.ManifestVersion)
			}
			if manifest.Schema != manifestSchemaResource {
				t.Fatalf("schema = %q, want %q", manifest.Schema, manifestSchemaResource)
			}
			if manifest.Documentation != releaseFileURL(testVersion, "README.md") || !reflect.DeepEqual(manifest.PrivacyPolicies, []string{releaseFileURL(testVersion, "PRIVACY.md")}) {
				t.Fatalf("release documentation links are not immutable: documentation=%q privacy=%#v", manifest.Documentation, manifest.PrivacyPolicies)
			}
			if got := manifest.Meta[architectureMetaKey]["architecture"]; got != target.GOARCH {
				t.Fatalf("architecture metadata = %q, want %q", got, target.GOARCH)
			}
			if got := target.ArtifactName(testVersion); got != wantNames[index] {
				t.Fatalf("artifact name = %q, want %q", got, wantNames[index])
			}
			if got, want := manifest.Server.MCPConfig.Args, []string{"mcp", "${user_config.project_directory}"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("args = %#v, want %#v", got, want)
			}
			if manifest.Server.EntryPoint != target.ServerPath() || manifest.Server.MCPConfig.Command != target.InstalledCommand() {
				t.Fatalf("manifest executable paths do not match target: %+v", manifest.Server)
			}
			if len(manifest.Compatibility.Platforms) != 1 || manifest.Compatibility.Platforms[0] != target.Platform {
				t.Fatalf("platforms = %#v, want only %q", manifest.Compatibility.Platforms, target.Platform)
			}
			if len(manifest.Compatibility.Runtimes) != 0 {
				t.Fatalf("binary manifest declares runtimes: %#v", manifest.Compatibility.Runtimes)
			}
			data, err := MarshalManifest(manifest, testVersion, target)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeManifest(data, testVersion, target)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded, manifest) {
				t.Fatalf("manifest round-trip changed value\n got: %#v\nwant: %#v", decoded, manifest)
			}
		})
	}
}

func TestManifestCommandKeepsProjectDirectoryAsOneLiteralArgument(t *testing.T) {
	target := Targets[0]
	manifest, err := NewManifest(testVersion, target)
	if err != nil {
		t.Fatal(err)
	}
	bundleDir := filepath.Join(t.TempDir(), "bundle with spaces")
	project := filepath.Join(t.TempDir(), "project with spaces;touch-not-run")
	command, args, err := ResolveCommand(manifest, target, bundleDir, project)
	if err != nil {
		t.Fatal(err)
	}
	if command != target.InstalledExecutable(bundleDir) {
		t.Fatalf("command = %q, want %q", command, target.InstalledExecutable(bundleDir))
	}
	if got, want := args, []string{"mcp", project}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved argv = %#v, want %#v", got, want)
	}
	if strings.Contains(command, "sh") || len(args) != 2 {
		t.Fatalf("command resolution introduced a shell: %q %#v", command, args)
	}

	unsafeArgs := manifest
	unsafeArgs.Server.MCPConfig.Args = []string{"mcp ${user_config.project_directory}"}
	if err := ValidateManifest(unsafeArgs, testVersion, target); err == nil {
		t.Fatal("combined shell-like argument was accepted")
	}
	unsafeCommand := manifest
	unsafeCommand.Server.MCPConfig.Command = "sh"
	unsafeCommand.Server.MCPConfig.Args = []string{"-c", "gograph mcp ${user_config.project_directory}"}
	if err := ValidateManifest(unsafeCommand, testVersion, target); err == nil {
		t.Fatal("shell command was accepted")
	}
	if _, _, err := ResolveCommand(manifest, target, bundleDir, " "); err == nil {
		t.Fatal("empty project directory was accepted")
	}
	wrongArchitecture := manifest
	wrongArchitecture.Meta = map[string]map[string]string{architectureMetaKey: {"architecture": "wrong"}}
	if err := ValidateManifest(wrongArchitecture, testVersion, target); err == nil {
		t.Fatal("wrong architecture metadata was accepted")
	}
}

func TestManifestRejectsMalformedInput(t *testing.T) {
	target := Targets[0]
	manifest, err := NewManifest(testVersion, target)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := MarshalManifest(manifest, testVersion, target)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"unknown field":   bytes.Replace(valid, []byte("{"), []byte(`{"unknown":true,`), 1),
		"duplicate field": bytes.Replace(valid, []byte("{"), []byte(`{"manifest_version":"0.4",`), 1),
		"trailing JSON":   append(append([]byte(nil), valid...), []byte(`{"second":true}`)...),
		"invalid JSON":    []byte(`{"manifest_version":`),
		"invalid UTF-8":   []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'},
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeManifest(data, testVersion, target); err == nil {
				t.Fatal("malformed manifest was accepted")
			}
		})
	}
}

func TestVendoredSchemasFailClosed(t *testing.T) {
	target := Targets[0]
	manifest, err := NewManifest(testVersion, target)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := MarshalManifest(manifest, testVersion, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateManifestSchema(raw); err != nil {
		t.Fatal(err)
	}
	invalidManifest := bytes.Replace(raw, []byte(`"type": "binary"`), []byte(`"type": "not-a-server-type"`), 1)
	if err := ValidateManifestSchema(invalidManifest); err == nil {
		t.Fatal("manifest rejected by v0.4 schema was accepted")
	}

	serverJSON := []byte(`{
  "$schema": "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json",
  "name": "io.github.ozgurcd/gograph",
  "title": "gograph",
  "description": "Local Go repository intelligence for coding agents over MCP.",
  "version": "1.5.0",
  "websiteUrl": "https://gograph.identuum.ai",
  "repository": {"url":"https://github.com/ozgurcd/gograph","source":"github","id":"1233398203"},
  "packages": [{
    "registryType": "mcpb",
    "identifier": "https://github.com/ozgurcd/gograph/releases/download/v1.5.0/gograph_1.5.0_darwin_amd64.mcpb",
    "version": "1.5.0",
    "fileSha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "transport": {"type":"stdio"}
  }]
}`)
	if err := ValidateServerJSON(serverJSON); err != nil {
		t.Fatal(err)
	}
	duplicateServerName := bytes.Replace(serverJSON, []byte("{"), []byte(`{"name":"io.github.ozgurcd/gograph",`), 1)
	if err := ValidateServerJSON(duplicateServerName); err == nil {
		t.Fatal("server.json with a duplicate object name was accepted")
	}
	invalidServer := bytes.Replace(serverJSON, []byte(strings.Repeat("a", 64)), []byte("short"), 1)
	if err := ValidateServerJSON(invalidServer); err == nil {
		t.Fatal("server.json rejected by Registry schema was accepted")
	}
}

func TestValidateVersion(t *testing.T) {
	for _, version := range []string{"0.0.0", "1.5.0", "2.0.0-rc.1", "2.0.0+build.7"} {
		t.Run("valid_"+version, func(t *testing.T) {
			if err := ValidateVersion(version); err != nil {
				t.Fatal(err)
			}
		})
	}
	for index, version := range []string{"", "v1.5.0", "1.5", "01.5.0", "1.5.0;echo", "latest", "1_5_0"} {
		t.Run(fmt.Sprintf("invalid_%d", index), func(t *testing.T) {
			if err := ValidateVersion(version); err == nil {
				t.Fatalf("invalid version %q was accepted", version)
			}
		})
	}
}
