package scanner_test

import (
	"go/build"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/ozgurcd/gograph/internal/scanner"
)

func TestWalkWithContextHonorsGoBuildSelection(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"active.go":           "package fixture\n",
		"ignored.go":          "//go:build ignore\n\npackage fixture\n",
		"platform_linux.go":   "package fixture\n",
		"platform_windows.go": "package fixture\n",
		"feature.go":          "//go:build issue30_feature\n\npackage fixture\n",
		"other.go":            "//go:build issue30_other\n\npackage fixture\n",
		"legacy_feature.go":   "// +build issue30_feature\n\npackage fixture\n",
		"legacy_other.go":     "// +build issue30_other\n\npackage fixture\n",
		"feature_test.go":     "//go:build issue30_feature\n\npackage fixture\nfunc TestFeature() {}\n",
		"other_test.go":       "//go:build issue30_other\n\npackage fixture\nfunc TestOther() {}\n",
		"cgo.go":              "package fixture\nimport \"C\"\n",
		"release.go":          "//go:build go1.1\n\npackage fixture\n",
		"tool_tag.go":         "//go:build issue30_tool\n\npackage fixture\n",
		"_hidden.go":          "package fixture\n",
	}
	for name, content := range files {
		mustWrite(t, filepath.Join(root, name), content)
	}

	base := build.Default
	base.GOOS = "linux"
	base.GOARCH = "amd64"
	base.CgoEnabled = false
	base.BuildTags = []string{"issue30_feature"}
	base.ToolTags = []string{"issue30_tool"}
	base.ReleaseTags = []string{"go1.1"}

	cases := []struct {
		name      string
		configure func(*build.Context)
		expected  []string
	}{
		{
			name: "inactive constraints are excluded",
			expected: []string{
				"active.go", "feature.go", "feature_test.go", "legacy_feature.go",
				"platform_linux.go", "release.go", "tool_tag.go",
			},
		},
		{
			name: "ignore can be activated explicitly",
			configure: func(ctx *build.Context) {
				ctx.BuildTags = append(ctx.BuildTags, "ignore")
			},
			expected: []string{
				"active.go", "feature.go", "feature_test.go", "ignored.go", "legacy_feature.go",
				"platform_linux.go", "release.go", "tool_tag.go",
			},
		},
		{
			name: "cgo import is active only when cgo is enabled",
			configure: func(ctx *build.Context) {
				ctx.CgoEnabled = true
			},
			expected: []string{
				"active.go", "cgo.go", "feature.go", "feature_test.go", "legacy_feature.go",
				"platform_linux.go", "release.go", "tool_tag.go",
			},
		},
		{
			name: "custom and legacy tags can be inactive",
			configure: func(ctx *build.Context) {
				ctx.BuildTags = nil
			},
			expected: []string{"active.go", "platform_linux.go", "release.go", "tool_tag.go"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := base
			ctx.BuildTags = append([]string(nil), base.BuildTags...)
			if tc.configure != nil {
				tc.configure(&ctx)
			}
			paths, errs := scanner.WalkWithContext(root, ctx)
			if len(errs) != 0 {
				t.Fatalf("WalkWithContext errors: %v", errs)
			}
			got := make([]string, 0, len(paths))
			for _, path := range paths {
				got = append(got, filepath.Base(path))
			}
			sort.Strings(got)
			sort.Strings(tc.expected)
			if !reflect.DeepEqual(got, tc.expected) {
				t.Fatalf("selected files = %v, want %v", got, tc.expected)
			}
		})
	}
}
