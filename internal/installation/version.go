// Package installation compares executable metadata without executing binaries.
package installation

import (
	"debug/buildinfo"
	"os"
	"os/exec"
	"strings"

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
