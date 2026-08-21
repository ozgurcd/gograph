// Package mcpbundle builds and validates the platform-specific MCP Bundles
// published for gograph.
package mcpbundle

import (
	"fmt"
	"path/filepath"
	"slices"
)

const (
	// ManifestVersion is the MCPB manifest revision used by release bundles.
	ManifestVersion = "0.4"
	// ServerName is the name reported by the bundled MCP server.
	ServerName = "gograph"
)

// Target identifies one supported release operating-system and architecture
// pair. Platform is the MCPB/Node-style platform identifier used by manifests.
type Target struct {
	GOOS     string
	GOARCH   string
	Platform string
}

// Targets is the complete, deterministic release target order.
var Targets = []Target{
	{GOOS: "darwin", GOARCH: "amd64", Platform: "darwin"},
	{GOOS: "darwin", GOARCH: "arm64", Platform: "darwin"},
	{GOOS: "linux", GOARCH: "amd64", Platform: "linux"},
	{GOOS: "linux", GOARCH: "arm64", Platform: "linux"},
	{GOOS: "windows", GOARCH: "amd64", Platform: "win32"},
	{GOOS: "windows", GOARCH: "arm64", Platform: "win32"},
}

// SupportedTargets returns a copy so callers cannot change the canonical
// release target list accidentally.
func SupportedTargets() []Target {
	return append([]Target(nil), Targets...)
}

// TargetFor returns the supported target matching goos and goarch.
func TargetFor(goos, goarch string) (Target, bool) {
	for _, target := range Targets {
		if target.GOOS == goos && target.GOARCH == goarch {
			return target, true
		}
	}
	return Target{}, false
}

// Validate rejects targets outside the six release combinations and catches
// inconsistent MCPB platform identifiers.
func (t Target) Validate() error {
	if slices.Contains(Targets, t) {
		return nil
	}
	return fmt.Errorf("unsupported MCPB target %s/%s (platform %q)", t.GOOS, t.GOARCH, t.Platform)
}

// ExecutableName is the executable filename stored in the bundle.
func (t Target) ExecutableName() string {
	if t.GOOS == "windows" {
		return "gograph.exe"
	}
	return "gograph"
}

// ServerPath is the slash-separated executable path inside a MCPB ZIP.
func (t Target) ServerPath() string {
	return "server/" + t.ExecutableName()
}

// InstalledCommand is the MCPB command template for this target.
func (t Target) InstalledCommand() string {
	return "${__dirname}/" + t.ServerPath()
}

// ArtifactName returns the canonical immutable release asset filename.
func (t Target) ArtifactName(version string) string {
	return fmt.Sprintf("gograph_%s_%s_%s.mcpb", version, t.GOOS, t.GOARCH)
}

// InstalledExecutable resolves the executable path below an extracted bundle
// directory without invoking a shell.
func (t Target) InstalledExecutable(bundleDir string) string {
	return filepath.Join(bundleDir, "server", t.ExecutableName())
}
