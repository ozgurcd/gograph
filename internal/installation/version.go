// Package installation compares executable metadata without executing binaries.
package installation

import (
	"debug/buildinfo"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

type Status struct {
	RunningVersion   string `json:"running_version"`
	InstalledVersion string `json:"installed_version,omitempty"`
	State            string `json:"state"`
	RestartRequired  bool   `json:"restart_required"`
	Message          string `json:"message"`
}

// Inspect compares this process with the executable selected by its PATH.
// It cannot discover the versions of unrelated running MCP processes.
func Inspect(running string) Status {
	result := Status{RunningVersion: running, State: "unknown", Message: "Installed version could not be read from Go build metadata; no binary was executed. Separate running MCP processes cannot be inspected; check their capabilities and restart them after an upgrade."}
	if semver.Prerelease("v"+strings.TrimPrefix(running, "v")) != "" {
		result.Message = "Development and prerelease builds are not compared with installed releases; check the release version before deciding whether to restart."
		return result
	}
	path, err := exec.LookPath("gograph")
	if err != nil {
		return result
	}
	stat, err := os.Stat(path)
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > 512<<20 {
		return result
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil || info.Path != "github.com/ozgurcd/gograph/cmd/gograph" {
		return result
	}
	installed := strings.TrimPrefix(info.Main.Version, "v")
	for _, setting := range info.Settings {
		if setting.Key != "-ldflags" {
			continue
		}
		fields := strings.Fields(setting.Value)
		for i, field := range fields {
			if field == "-X" && i+1 < len(fields) {
				field = fields[i+1]
			} else {
				field = strings.TrimPrefix(field, "-X=")
			}
			if value, ok := strings.CutPrefix(field, "main.version="); ok {
				installed = strings.TrimPrefix(value, "v")
			}
		}
	}
	if !semver.IsValid("v"+installed) || !semver.IsValid("v"+strings.TrimPrefix(running, "v")) {
		return result
	}
	result.InstalledVersion = installed
	result.State = "current"
	result.Message = "This process is not older than the gograph binary on PATH. Separate running MCP processes cannot be inspected; check their capabilities and restart them after an upgrade."
	if semver.Compare("v"+installed, "v"+strings.TrimPrefix(running, "v")) > 0 {
		result.State = "older_than_installed"
		result.RestartRequired = true
		result.Message = "Running gograph " + running + " is older than installed gograph " + installed + " on PATH; restart the MCP server to use the installed version."
	}
	return result
}

// RunningVersion returns the version this binary reports. A release build
// sets main.version through -ldflags and that value wins. Without it (for
// example `go install github.com/ozgurcd/gograph/cmd/gograph@v1.7.20`), the
// module version recorded by the Go toolchain is used. A local build from a
// checkout reports "dev" plus its VCS revision.
func RunningVersion(linker string) string {
	info, _ := debug.ReadBuildInfo()
	return versionFromBuildInfo(linker, info)
}

func versionFromBuildInfo(linker string, info *debug.BuildInfo) string {
	if linker != "" && linker != "dev" {
		return linker
	}
	if info == nil {
		return "dev"
	}
	revision, modified := "", false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	moduleVersion := info.Main.Version
	if moduleVersion != "" && moduleVersion != "(devel)" && semver.IsValid(moduleVersion) &&
		semver.Build(moduleVersion) == "" && revision == "" {
		// A module download (go install pkg@version), not a local checkout:
		// a release tag, or a pseudo-version for an untagged commit.
		return strings.TrimPrefix(moduleVersion, "v")
	}
	if moduleVersion != "" && moduleVersion != "(devel)" && semver.IsValid(moduleVersion) &&
		!module.IsPseudoVersion(moduleVersion) && semver.Build(moduleVersion) == "" && !modified {
		// A clean local build exactly at a release tag.
		return strings.TrimPrefix(moduleVersion, "v")
	}
	if revision == "" {
		return "dev"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	version := "dev+" + revision
	if modified {
		version += ".dirty"
	}
	return version
}
