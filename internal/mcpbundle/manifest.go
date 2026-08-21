package mcpbundle

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	manifestDisplayName = "gograph — Go Repository Intelligence"
	manifestDescription = "Local Go repository intelligence for coding agents over MCP."
	repositoryURL       = "https://github.com/ozgurcd/gograph"
	homepageURL         = "https://gograph.identuum.ai"
	supportURL          = "https://github.com/ozgurcd/gograph/issues"
	projectDirectoryKey = "project_directory"
	projectDirectoryArg = "${user_config.project_directory}"
	architectureMetaKey = "io.github.ozgurcd.gograph"
)

var semverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// Manifest is the strict subset of MCPB manifest v0.4 used by gograph binary
// bundles. Keeping this typed prevents release scripts from silently emitting
// misspelled or unsupported fields.
type Manifest struct {
	Schema          string                       `json:"$schema"`
	ManifestVersion string                       `json:"manifest_version"`
	Name            string                       `json:"name"`
	DisplayName     string                       `json:"display_name"`
	Version         string                       `json:"version"`
	Description     string                       `json:"description"`
	LongDescription string                       `json:"long_description,omitempty"`
	Author          Author                       `json:"author"`
	Repository      Repository                   `json:"repository"`
	Homepage        string                       `json:"homepage"`
	Documentation   string                       `json:"documentation"`
	Support         string                       `json:"support"`
	Server          Server                       `json:"server"`
	ToolsGenerated  bool                         `json:"tools_generated"`
	Keywords        []string                     `json:"keywords"`
	License         string                       `json:"license"`
	PrivacyPolicies []string                     `json:"privacy_policies"`
	Compatibility   Compatibility                `json:"compatibility"`
	UserConfig      map[string]UserConfig        `json:"user_config"`
	Meta            map[string]map[string]string `json:"_meta"`
}

type Author struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

type Repository struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type Server struct {
	Type       string    `json:"type"`
	EntryPoint string    `json:"entry_point"`
	MCPConfig  MCPConfig `json:"mcp_config"`
}

type MCPConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

type Compatibility struct {
	Platforms []string          `json:"platforms"`
	Runtimes  map[string]string `json:"runtimes,omitempty"`
}

type UserConfig struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Multiple    bool   `json:"multiple,omitempty"`
}

// NewManifest returns the canonical manifest for a release target.
func NewManifest(version string, target Target) (Manifest, error) {
	if err := ValidateVersion(version); err != nil {
		return Manifest{}, err
	}
	if err := target.Validate(); err != nil {
		return Manifest{}, err
	}

	manifest := Manifest{
		Schema:          manifestSchemaResource,
		ManifestVersion: ManifestVersion,
		Name:            ServerName,
		DisplayName:     manifestDisplayName,
		Version:         version,
		Description:     manifestDescription,
		LongDescription: "Analyze Go repository structure, calls, interfaces, impact, policies, and workflow evidence locally through MCP over stdio.",
		Author:          Author{Name: "ozgurcd", URL: "https://github.com/ozgurcd"},
		Repository:      Repository{Type: "git", URL: repositoryURL},
		Homepage:        homepageURL,
		Documentation:   releaseFileURL(version, "README.md"),
		Support:         supportURL,
		Server: Server{
			Type:       "binary",
			EntryPoint: target.ServerPath(),
			MCPConfig: MCPConfig{
				Command: target.InstalledCommand(),
				Args:    []string{"mcp", projectDirectoryArg},
				Env:     map[string]string{},
			},
		},
		ToolsGenerated:  true,
		Keywords:        []string{"go", "golang", "code-analysis", "call-graph", "mcp"},
		License:         "MIT",
		PrivacyPolicies: []string{releaseFileURL(version, "PRIVACY.md")},
		Compatibility:   Compatibility{Platforms: []string{target.Platform}},
		UserConfig: map[string]UserConfig{
			projectDirectoryKey: {
				Type:        "directory",
				Title:       "Go project directory",
				Description: "Select the Go repository that gograph should analyze locally.",
				Required:    true,
			},
		},
		Meta: map[string]map[string]string{
			architectureMetaKey: {"architecture": target.GOARCH},
		},
	}
	if err := ValidateManifest(manifest, version, target); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// ValidateVersion requires a semantic version without a leading v. This also
// makes it safe to pass the version as one ldflag argument during builds.
func ValidateVersion(version string) error {
	if !semverPattern.MatchString(version) {
		return fmt.Errorf("version %q is not a canonical semantic version", version)
	}
	return nil
}

// ValidateManifest enforces both the MCPB shape and gograph's release policy.
func ValidateManifest(manifest Manifest, version string, target Target) error {
	if err := ValidateVersion(version); err != nil {
		return err
	}
	if err := target.Validate(); err != nil {
		return err
	}
	schemaValue, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal manifest for schema validation: %w", err)
	}
	if err := ValidateManifestSchema(schemaValue); err != nil {
		return err
	}
	if manifest.Schema != manifestSchemaResource {
		return fmt.Errorf("manifest $schema = %q, want immutable %q", manifest.Schema, manifestSchemaResource)
	}
	if manifest.ManifestVersion != ManifestVersion {
		return fmt.Errorf("manifest_version = %q, want %q", manifest.ManifestVersion, ManifestVersion)
	}
	if manifest.Name != ServerName {
		return fmt.Errorf("manifest name = %q, want %q", manifest.Name, ServerName)
	}
	if manifest.DisplayName != manifestDisplayName {
		return fmt.Errorf("manifest display_name = %q, want %q", manifest.DisplayName, manifestDisplayName)
	}
	if manifest.Version != version {
		return fmt.Errorf("manifest version = %q, want %q", manifest.Version, version)
	}
	if manifest.Description != manifestDescription || manifest.LongDescription == "" {
		return fmt.Errorf("manifest description metadata is not canonical")
	}
	if manifest.Author != (Author{Name: "ozgurcd", URL: "https://github.com/ozgurcd"}) {
		return fmt.Errorf("manifest author metadata is not canonical")
	}
	if manifest.Repository != (Repository{Type: "git", URL: repositoryURL}) {
		return fmt.Errorf("manifest repository metadata is not canonical")
	}
	if manifest.Homepage != homepageURL || manifest.Documentation != releaseFileURL(version, "README.md") || manifest.Support != supportURL {
		return fmt.Errorf("manifest project URLs are not canonical")
	}
	if manifest.Server.Type != "binary" {
		return fmt.Errorf("manifest server type = %q, want binary", manifest.Server.Type)
	}
	if manifest.Server.EntryPoint != target.ServerPath() {
		return fmt.Errorf("manifest entry point = %q, want %q", manifest.Server.EntryPoint, target.ServerPath())
	}
	if manifest.Server.MCPConfig.Command != target.InstalledCommand() {
		return fmt.Errorf("manifest command = %q, want %q", manifest.Server.MCPConfig.Command, target.InstalledCommand())
	}
	if !slices.Equal(manifest.Server.MCPConfig.Args, []string{"mcp", projectDirectoryArg}) {
		return fmt.Errorf("manifest args must be separate literal mcp and project-directory arguments")
	}
	if len(manifest.Server.MCPConfig.Env) != 0 {
		return fmt.Errorf("manifest must not inject environment variables")
	}
	if !manifest.ToolsGenerated {
		return fmt.Errorf("manifest must advertise runtime-generated tools")
	}
	if !slices.Equal(manifest.Keywords, []string{"go", "golang", "code-analysis", "call-graph", "mcp"}) {
		return fmt.Errorf("manifest keywords are not canonical")
	}
	if manifest.License != "MIT" || !slices.Equal(manifest.PrivacyPolicies, []string{releaseFileURL(version, "PRIVACY.md")}) {
		return fmt.Errorf("manifest license or privacy metadata is not canonical")
	}
	if !slices.Equal(manifest.Compatibility.Platforms, []string{target.Platform}) {
		return fmt.Errorf("manifest platforms = %v, want only %q", manifest.Compatibility.Platforms, target.Platform)
	}
	if len(manifest.Compatibility.Runtimes) != 0 {
		return fmt.Errorf("binary manifest must not declare a language runtime")
	}
	if len(manifest.UserConfig) != 1 {
		return fmt.Errorf("manifest must declare exactly one user configuration field")
	}
	wantProject := UserConfig{
		Type:        "directory",
		Title:       "Go project directory",
		Description: "Select the Go repository that gograph should analyze locally.",
		Required:    true,
	}
	if project, ok := manifest.UserConfig[projectDirectoryKey]; !ok || project != wantProject {
		return fmt.Errorf("manifest project_directory configuration is missing or unsafe")
	}
	if len(manifest.Meta) != 1 || len(manifest.Meta[architectureMetaKey]) != 1 || manifest.Meta[architectureMetaKey]["architecture"] != target.GOARCH {
		return fmt.Errorf("manifest architecture metadata must identify only %s", target.GOARCH)
	}
	return nil
}

// DecodeManifest strictly decodes a manifest and rejects unknown fields or
// trailing JSON before applying target-aware semantic validation.
func DecodeManifest(data []byte, version string, target Target) (Manifest, error) {
	if err := ValidateManifestSchema(data); err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := jsonv2.Unmarshal(data, &manifest, jsonv2.RejectUnknownMembers(true)); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if err := ValidateManifest(manifest, version, target); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// MarshalManifest produces stable, human-readable JSON with a final newline.
func MarshalManifest(manifest Manifest, version string, target Target) ([]byte, error) {
	if err := ValidateManifest(manifest, version, target); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	data = append(data, '\n')
	if err := ValidateManifestSchema(data); err != nil {
		return nil, err
	}
	return data, nil
}

// ResolveCommand resolves only the two MCPB variables emitted by NewManifest.
// It returns command and argv separately; it never constructs a shell string.
func ResolveCommand(manifest Manifest, target Target, bundleDir, projectDirectory string) (string, []string, error) {
	if err := ValidateManifest(manifest, manifest.Version, target); err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(bundleDir) == "" {
		return "", nil, fmt.Errorf("bundle directory is required")
	}
	if strings.TrimSpace(projectDirectory) == "" {
		return "", nil, fmt.Errorf("project directory is required")
	}
	command := target.InstalledExecutable(bundleDir)
	args := []string{"mcp", projectDirectory}
	return command, args, nil
}

func releaseFileURL(version, name string) string {
	return repositoryURL + "/blob/v" + version + "/" + name
}
