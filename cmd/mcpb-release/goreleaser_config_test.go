package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderGoReleaserConfigUsesTemporaryAbsolutePaths(t *testing.T) {
	t.Parallel()
	source := []byte(`version: 2
project_name: gograph
checksum:
  extra_files:
    - glob: ./.release-mcpb/*.mcpb
release:
  extra_files:
    - glob: ./.release-mcpb/*.mcpb
`)
	repository := t.TempDir()
	mcpb := filepath.Join(repository, ".release-work", "mcp bundles")
	dist := filepath.Join(mcpb, "release dist")
	rendered, err := renderGoReleaserConfig(source, repository, mcpb, dist)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(rendered, source) {
		t.Fatal("configuration was not rendered")
	}
	wantDist := `dist: "` + filepath.ToSlash(dist) + `"`
	relativeMCPB, err := filepath.Rel(repository, mcpb)
	if err != nil {
		t.Fatal(err)
	}
	wantGlob := `    - glob: "` + filepath.ToSlash(relativeMCPB) + `/*.mcpb"`
	if !strings.Contains(string(rendered), wantDist) {
		t.Fatalf("rendered config is missing %q:\n%s", wantDist, rendered)
	}
	if count := strings.Count(string(rendered), wantGlob); count != 2 {
		t.Fatalf("rendered config has %d temporary MCPB globs, want 2:\n%s", count, rendered)
	}
	if strings.Contains(string(rendered), "./.release-mcpb") {
		t.Fatalf("rendered config retained repository MCPB path:\n%s", rendered)
	}
}

func TestRenderGoReleaserConfigFailsClosed(t *testing.T) {
	t.Parallel()
	tests := map[string][]byte{
		"wrong schema":  []byte("version: 1\n"),
		"existing dist": []byte("version: 2\ndist: ./dist\n"),
		"missing globs": []byte("version: 2\n"),
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := renderGoReleaserConfig(source, t.TempDir(), t.TempDir(), t.TempDir()); err == nil {
				t.Fatal("invalid GoReleaser configuration was accepted")
			}
		})
	}

	t.Run("external MCPB path", func(t *testing.T) {
		repository := t.TempDir()
		external := t.TempDir()
		if _, err := renderGoReleaserConfig([]byte("version: 2\n    - glob: ./.release-mcpb/*.mcpb\n    - glob: ./.release-mcpb/*.mcpb\n"), repository, external, filepath.Join(external, "dist")); err == nil {
			t.Fatal("external MCPB output was accepted")
		}
	})
}
