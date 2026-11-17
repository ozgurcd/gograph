package releasenotes

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph"
)

const notes = `# Release Notes

## Unreleased

- new thing

## v1.2.3 — 2026-01-03

Third.

## v1.2.2 - 2026-01-02

Second.

## v1.2.1 — 2026-01-01

First.
`

func versions(report Report) []string {
	out := []string{}
	for _, section := range report.Sections {
		out = append(out, section.Version)
	}
	return out
}

func TestParseAndSelect(t *testing.T) {
	sections := Parse(notes)
	if got := versions(Report{Sections: sections}); !reflect.DeepEqual(got, []string{"Unreleased", "v1.2.3", "v1.2.2", "v1.2.1"}) {
		t.Fatalf("parsed %v", got)
	}
	if sections[2].Date != "2026-01-02" || sections[2].Body != "Second." {
		t.Fatalf("section = %+v", sections[2])
	}
	for name, testCase := range map[string]struct {
		running, since string
		want           []string
	}{
		"release":               {"1.2.2", "", []string{"v1.2.2"}},
		"release with v prefix": {"v1.2.3", "", []string{"v1.2.3"}},
		"development build":     {"dev+abc.dirty", "", []string{"Unreleased", "v1.2.3"}},
		"branch pseudo-version": {"1.2.4-0.20260105-abcdef", "", []string{"Unreleased", "v1.2.3"}},
		"since on a release":    {"1.2.3", "v1.2.1", []string{"v1.2.3", "v1.2.2"}},
		"since on a dev build":  {"dev", "1.2.2", []string{"Unreleased", "v1.2.3"}},
		"unknown release":       {"1.9.0", "", []string{}},
	} {
		if got := versions(Select(sections, testCase.running, testCase.since)); !reflect.DeepEqual(got, testCase.want) {
			t.Errorf("%s: got %v, want %v", name, got, testCase.want)
		}
	}
	if report := Select(sections, "1.9.0", ""); !strings.Contains(report.Note, "1.9.0") {
		t.Errorf("missing note for unknown release: %+v", report)
	}
	if report := Select(sections, "1.2.3", "latest"); report.Note == "" {
		t.Error("invalid --since accepted")
	}
}

func TestEmbeddedReleaseNotesParse(t *testing.T) {
	sections := Parse(gograph.ReleaseNotes)
	if len(sections) < 10 || canonical(sections[len(sections)-1].Version) == "" {
		t.Fatalf("embedded release notes parsed into %d sections", len(sections))
	}
}
