package graph

import "go/build"

// BuildSelection records only language/platform selection, never local paths.
type BuildSelection struct {
	GOOS        string   `json:"goos"`
	GOARCH      string   `json:"goarch"`
	Compiler    string   `json:"compiler"`
	CgoEnabled  bool     `json:"cgo_enabled"`
	BuildTags   []string `json:"build_tags"`
	ToolTags    []string `json:"tool_tags"`
	ReleaseTags []string `json:"release_tags"`
}

func CaptureBuildSelection(c build.Context) *BuildSelection {
	return &BuildSelection{GOOS: c.GOOS, GOARCH: c.GOARCH, Compiler: c.Compiler, CgoEnabled: c.CgoEnabled,
		BuildTags: append([]string{}, c.BuildTags...), ToolTags: append([]string{}, c.ToolTags...), ReleaseTags: append([]string{}, c.ReleaseTags...)}
}

func (s BuildSelection) Apply(c build.Context) build.Context {
	c.GOOS, c.GOARCH, c.Compiler, c.CgoEnabled = s.GOOS, s.GOARCH, s.Compiler, s.CgoEnabled
	c.BuildTags = append([]string{}, s.BuildTags...)
	c.ToolTags = append([]string{}, s.ToolTags...)
	c.ReleaseTags = append([]string{}, s.ReleaseTags...)
	return c
}
