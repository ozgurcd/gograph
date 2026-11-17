// Package releasenotes parses RELEASE_NOTES.md into per-release sections.
package releasenotes

import (
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

// SchemaVersion identifies the whatsnew document.
const SchemaVersion = "gograph.whatsnew.v1"

// Section is one "## vX.Y.Z — DATE" (or "## Unreleased") block.
type Section struct {
	Version string `json:"version"`
	Date    string `json:"date,omitempty"`
	Body    string `json:"body"`
}

// Report is the whatsnew answer.
type Report struct {
	SchemaVersion  string    `json:"schema_version"`
	RunningVersion string    `json:"running_version"`
	Selection      string    `json:"selection"`
	Sections       []Section `json:"sections"`
	Note           string    `json:"note,omitempty"`
}

var heading = regexp.MustCompile(`^## (v\d+\.\d+\.\d+|Unreleased)\b(?:\s*[—-]+\s*(.*))?$`)

// Parse splits release notes into sections, newest first as written.
func Parse(text string) []Section {
	var sections []Section
	var current *Section
	var body []string
	flush := func() {
		if current != nil {
			current.Body = strings.TrimSpace(strings.Join(body, "\n"))
			sections = append(sections, *current)
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if match := heading.FindStringSubmatch(line); match != nil {
			flush()
			current = &Section{Version: match[1], Date: strings.TrimSpace(match[2])}
			body = nil
			continue
		}
		if current != nil {
			body = append(body, line)
		}
	}
	flush()
	return sections
}

func canonical(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return ""
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	if !semver.IsValid(version) {
		return ""
	}
	return version
}

// Select returns the sections for running (a release version such as
// "1.7.20", or a development version), or every release newer than since
// up to running when since is set.
func Select(sections []Section, running, since string) Report {
	report := Report{SchemaVersion: SchemaVersion, RunningVersion: running, Sections: []Section{}}
	runningVersion := canonical(running)
	release := runningVersion != "" && semver.Prerelease(runningVersion) == "" && semver.Build(runningVersion) == ""
	if since != "" {
		sinceVersion := canonical(since)
		report.Selection = "since " + since
		if sinceVersion == "" {
			report.Note = "--since needs a version such as v1.7.18"
			return report
		}
		for _, section := range sections {
			version := canonical(section.Version)
			switch {
			case section.Version == "Unreleased":
				if !release {
					report.Sections = append(report.Sections, section)
				}
			case version != "" && semver.Compare(version, sinceVersion) > 0 && (!release || semver.Compare(version, runningVersion) <= 0):
				report.Sections = append(report.Sections, section)
			}
		}
		return report
	}
	if release {
		report.Selection = "running release"
		for _, section := range sections {
			if canonical(section.Version) == runningVersion {
				report.Sections = append(report.Sections, section)
				return report
			}
		}
		report.Note = "the release notes shipped with this binary have no section for " + running
		return report
	}
	// A development, branch or dirty build: show unreleased changes and the
	// latest release they build on.
	report.Selection = "development build: unreleased changes and the latest release"
	for _, section := range sections {
		report.Sections = append(report.Sections, section)
		if section.Version != "Unreleased" {
			break
		}
	}
	return report
}
