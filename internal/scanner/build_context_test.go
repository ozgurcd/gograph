package scanner_test

import (
	"go/build"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/ozgurcd/gograph/internal/buildctx"
	"github.com/ozgurcd/gograph/internal/scanner"
)

func TestWalkWithContextHonorsGoBuildSelection(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"active.go":           "package fixture\n",
		"ignored.go":          "//go:build ignore\n\npackage fixture\n",
		"platform_amd64.go":   "package fixture\n",
		"platform_arm64.go":   "package fixture\n",
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
				"platform_amd64.go", "platform_linux.go", "release.go", "tool_tag.go",
			},
		},
		{
			name: "ignore can be activated explicitly",
			configure: func(ctx *build.Context) {
				ctx.BuildTags = append(ctx.BuildTags, "ignore")
			},
			expected: []string{
				"active.go", "feature.go", "feature_test.go", "ignored.go", "legacy_feature.go",
				"platform_amd64.go", "platform_linux.go", "release.go", "tool_tag.go",
			},
		},
		{
			name: "cgo import is active only when cgo is enabled",
			configure: func(ctx *build.Context) {
				ctx.CgoEnabled = true
			},
			expected: []string{
				"active.go", "cgo.go", "feature.go", "feature_test.go", "legacy_feature.go",
				"platform_amd64.go", "platform_linux.go", "release.go", "tool_tag.go",
			},
		},
		{
			name: "custom and legacy tags can be inactive",
			configure: func(ctx *build.Context) {
				ctx.BuildTags = nil
			},
			expected: []string{"active.go", "platform_amd64.go", "platform_linux.go", "release.go", "tool_tag.go"},
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

func TestWalkWithContextMatchesGoWildcardDirectoryRules(t *testing.T) {
	root := filepath.Join(t.TempDir(), "_repo")
	mustWrite(t, filepath.Join(root, "root.go"), "package fixture\n")
	mustWrite(t, filepath.Join(root, "normal", "keep.go"), "package normal\n")
	mustWrite(t, filepath.Join(root, ".scratch", "hidden.go"), "package scratch\n")
	mustWrite(t, filepath.Join(root, "_scratch", "hidden.go"), "package scratch\n")
	mustWrite(t, filepath.Join(root, "testdata", "hidden.go"), "package testdata\n")

	paths, errs := scanner.WalkWithContext(root, build.Default)
	if len(errs) != 0 {
		t.Fatalf("WalkWithContext errors: %v", errs)
	}
	got := relativePaths(t, root, paths)
	want := []string{"normal/keep.go", "root.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selected files = %v, want %v", got, want)
	}
}

func TestWalkWithContextFollowsExplicitRootSymlink(t *testing.T) {
	realRoot := filepath.Join(t.TempDir(), "real")
	mustWrite(t, filepath.Join(realRoot, "root.go"), "package fixture\n")
	linkRoot := filepath.Join(t.TempDir(), "linked-root")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Skipf("create root symlink: %v", err)
	}

	paths, errs := scanner.WalkWithContext(linkRoot, build.Default)
	if len(errs) != 0 {
		t.Fatalf("WalkWithContext errors: %v", errs)
	}
	if got, want := relativePaths(t, linkRoot, paths), []string{"root.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected files = %v, want %v", got, want)
	}
}

func TestWalkWithConfigHonorsModuleIgnoreDirectives(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), `module example.com/ignore

go 1.26

ignore (
	ignored
	./anchored
)
`)
	mustWrite(t, filepath.Join(root, "root.go"), "package fixture\n")
	mustWrite(t, filepath.Join(root, "ignored", "noise.go"), "package ignored\n")
	mustWrite(t, filepath.Join(root, "deep", "ignored", "noise.go"), "package ignored\n")
	mustWrite(t, filepath.Join(root, "anchored", "noise.go"), "package anchored\n")
	mustWrite(t, filepath.Join(root, "deep", "anchored", "keep.go"), "package anchored\n")
	mustWrite(t, filepath.Join(root, "ignored2", "keep.go"), "package ignored2\n")

	paths, errs := scanner.WalkWithConfig(root, moduleConfig(root, true))
	if len(errs) != 0 {
		t.Fatalf("WalkWithContext errors: %v", errs)
	}
	got := relativePaths(t, root, paths)
	want := []string{"deep/anchored/keep.go", "ignored2/keep.go", "root.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selected files = %v, want %v", got, want)
	}
}

func TestWalkWithConfigHonorsParentModuleIgnoreFromSubdirectory(t *testing.T) {
	moduleRoot := t.TempDir()
	mustWrite(t, filepath.Join(moduleRoot, "go.mod"), "module example.com/root\n\ngo 1.26\n\nignore ignored\n")
	scanRoot := filepath.Join(moduleRoot, "pkg")
	mustWrite(t, filepath.Join(scanRoot, "keep.go"), "package pkg\n")
	mustWrite(t, filepath.Join(scanRoot, "ignored", "noise.go"), "package ignored\n")

	paths, errs := scanner.WalkWithConfig(scanRoot, moduleConfig(moduleRoot, true))
	if len(errs) != 0 {
		t.Fatalf("WalkWithConfig errors: %v", errs)
	}
	if got, want := relativePaths(t, scanRoot, paths), []string{"keep.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected files = %v, want %v", got, want)
	}
}

func TestWalkWithConfigHonorsModuleIgnoreAtExplicitRoot(t *testing.T) {
	moduleRoot := t.TempDir()
	mustWrite(t, filepath.Join(moduleRoot, "go.mod"), "module example.com/root\n\ngo 1.26\n\nignore ./ignored\n")
	scanRoot := filepath.Join(moduleRoot, "ignored")
	mustWrite(t, filepath.Join(scanRoot, "noise.go"), "package ignored\n")

	paths, errs := scanner.WalkWithConfig(scanRoot, moduleConfig(moduleRoot, true))
	if len(errs) != 0 {
		t.Fatalf("WalkWithConfig errors: %v", errs)
	}
	if len(paths) != 0 {
		t.Fatalf("explicit ignored root selected files: %v", paths)
	}
}

func TestWalkWithConfigAlignsCanonicalModuleRootWithLexicalScanPath(t *testing.T) {
	base := t.TempDir()
	realParent := filepath.Join(base, "real")
	moduleRoot := filepath.Join(realParent, "module")
	mustWrite(t, filepath.Join(moduleRoot, "go.mod"), "module example.com/root\n\ngo 1.26\n\nignore ignored\n")
	mustWrite(t, filepath.Join(moduleRoot, "pkg", "keep.go"), "package pkg\n")
	mustWrite(t, filepath.Join(moduleRoot, "pkg", "ignored", "noise.go"), "package ignored\n")
	aliasParent := filepath.Join(base, "alias")
	if err := os.Symlink(realParent, aliasParent); err != nil {
		t.Skipf("create path alias: %v", err)
	}
	scanRoot := filepath.Join(aliasParent, "module", "pkg")

	paths, errs := scanner.WalkWithConfig(scanRoot, moduleConfig(moduleRoot, true))
	if len(errs) != 0 {
		t.Fatalf("WalkWithConfig errors: %v", errs)
	}
	if got, want := relativePaths(t, scanRoot, paths), []string{"keep.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected files = %v, want %v", got, want)
	}
}

func TestWalkWithConfigDoesNotApplyModuleIgnoreWhenModulesDisabled(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.26\n\nignore ignored\n")
	mustWrite(t, filepath.Join(root, "root.go"), "package root\n")
	mustWrite(t, filepath.Join(root, "ignored", "noise.go"), "package ignored\n")

	paths, errs := scanner.WalkWithConfig(root, moduleConfig(root, false))
	if len(errs) != 0 {
		t.Fatalf("WalkWithConfig errors: %v", errs)
	}
	want := []string{"ignored/noise.go", "root.go"}
	if got := relativePaths(t, root, paths); !reflect.DeepEqual(got, want) {
		t.Fatalf("selected files = %v, want %v", got, want)
	}
}

func TestWalkWithConfigMalformedNestedModuleFailsOpen(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.26\n")
	mustWrite(t, filepath.Join(root, "root.go"), "package root\n")
	mustWrite(t, filepath.Join(root, "nested", "go.mod"), "module\n")
	mustWrite(t, filepath.Join(root, "nested", "child.go"), "package child\n")

	paths, errs := scanner.WalkWithConfig(root, moduleConfig(root, true))
	if len(errs) != 1 {
		t.Fatalf("WalkWithContext errors = %v, want one malformed-module warning", errs)
	}
	got := relativePaths(t, root, paths)
	want := []string{"nested/child.go", "root.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selected files = %v, want %v", got, want)
	}
}

func TestSelectionFingerprintTracksModuleIdentityAndBoundaries(t *testing.T) {
	root := t.TempDir()
	modPath := filepath.Join(root, "go.mod")
	mustWrite(t, modPath, "module example.com/first\n\ngo 1.26\n")
	mustWrite(t, filepath.Join(root, "root.go"), "package root\n")
	mustWrite(t, filepath.Join(root, "nested", "child.go"), "package child\n")
	config := moduleConfig(root, true)
	_, first, errs := scanner.WalkWithConfigAndFingerprint(root, config)
	if len(errs) != 0 {
		t.Fatalf("initial walk errors: %v", errs)
	}

	mustWrite(t, modPath, "module example.com/second\n\ngo 1.26\n")
	_, second, errs := scanner.WalkWithConfigAndFingerprint(root, config)
	if len(errs) != 0 || second == first {
		t.Fatalf("module identity fingerprint = %q (errors %v), want change from %q", second, errs, first)
	}

	mustWrite(t, filepath.Join(root, "nested", "go.mod"), "module example.com/nested\n\ngo 1.26\n")
	_, third, errs := scanner.WalkWithConfigAndFingerprint(root, config)
	if len(errs) != 0 || third == second {
		t.Fatalf("module boundary fingerprint = %q (errors %v), want change from %q", third, errs, second)
	}
}

func moduleConfig(moduleRoot string, enabled bool) buildctx.Config {
	return buildctx.FromBuildContextWithModule(build.Default, nil, enabled, moduleRoot)
}

func relativePaths(t *testing.T, root string, paths []string) []string {
	t.Helper()
	relative := make([]string, 0, len(paths))
	for _, path := range paths {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		relative = append(relative, filepath.ToSlash(rel))
	}
	sort.Strings(relative)
	return relative
}
