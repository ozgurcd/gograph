package scanner_test

import (
	"errors"
	"go/build"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"

	"github.com/ozgurcd/gograph/internal/buildctx"
	"github.com/ozgurcd/gograph/internal/scanner"
)

func TestWalkWithContextExcludesGoSourceSymlinksBeforeImportDir(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	mustWrite(t, filepath.Join(root, "main.go"), "package main\nfunc main() {}\n")
	outside := filepath.Join(base, "outside.go")
	mustWrite(t, outside, "package outside\nfunc OutsideOnly() {}\n")
	linked := filepath.Join(root, "linked.go")
	if err := os.Symlink(outside, linked); err != nil {
		t.Skipf("create source symlink: %v", err)
	}
	dangling := filepath.Join(root, "dangling.go")
	if err := os.Symlink(filepath.Join(base, "missing.go"), dangling); err != nil {
		t.Skipf("create dangling source symlink: %v", err)
	}

	paths, errs := scanner.WalkWithContext(root, build.Default)
	if got, want := relativePaths(t, root, paths), []string{"main.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected files = %v, want %v", got, want)
	}
	if len(errs) != 2 {
		t.Fatalf("walk errors = %v, want two unsafe-source warnings", errs)
	}
	for _, err := range errs {
		var unsafe *scanner.UnsafeSourceFileError
		if !errors.As(err, &unsafe) {
			t.Fatalf("walk error = %v, want UnsafeSourceFileError", err)
		}
	}
	if err := scanner.ValidateNoSourceLinks(root); err == nil || !scanner.IsUnsafeSourceFileError(err) {
		t.Fatalf("ValidateNoSourceLinks error = %v, want unsafe source", err)
	}
}

func TestWalkWithContextConfinesNonGoBuildInputs(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	mustWrite(t, filepath.Join(root, "main.go"), "package main\nfunc main() {}\n")
	mustWrite(t, filepath.Join(root, "regular.s"), "// regular assembly input\n")
	outside := filepath.Join(base, "outside.s")
	mustWrite(t, outside, "//go:build (\n")
	if err := os.Symlink(outside, filepath.Join(root, "linked.s")); err != nil {
		t.Skipf("create assembly symlink: %v", err)
	}

	ctx := build.Default
	var opened []string
	ctx.OpenFile = func(path string) (io.ReadCloser, error) {
		opened = append(opened, filepath.Base(path))
		return os.Open(path)
	}
	paths, errs := scanner.WalkWithContext(root, ctx)
	if got, want := relativePaths(t, root, paths), []string{"main.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected files = %v, want %v", got, want)
	}
	if len(errs) != 1 || !scanner.IsUnsafeSourceFileError(errs[0]) {
		t.Fatalf("walk errors = %v, want one unsafe non-Go build input", errs)
	}
	if !containsString(opened, "regular.s") {
		t.Fatalf("regular assembly input was not inspected: opened %v", opened)
	}
	if containsString(opened, "linked.s") {
		t.Fatalf("linked assembly input was opened: %v", opened)
	}
	if err := scanner.ValidateNoSourceLinks(root); err == nil || !scanner.IsUnsafeSourceFileError(err) {
		t.Fatalf("ValidateNoSourceLinks error = %v, want unsafe non-Go build input", err)
	}
}

func TestWalkWithContextRejectsSpecialNonGoBuildInput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-domain socket fixture is unavailable on Windows")
	}
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.go"), "package main\nfunc main() {}\n")
	listener, err := net.Listen("unix", filepath.Join(root, "special.s"))
	if err != nil {
		t.Skipf("create Unix-domain socket build input: %v", err)
	}
	defer func() { _ = listener.Close() }()

	paths, errs := scanner.WalkWithContext(root, build.Default)
	if got, want := relativePaths(t, root, paths), []string{"main.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected files = %v, want %v", got, want)
	}
	if len(errs) != 1 || !scanner.IsUnsafeSourceFileError(errs[0]) {
		t.Fatalf("walk errors = %v, want one unsafe special build input", errs)
	}
	if err := scanner.ValidateNoSourceLinks(root); err == nil || !scanner.IsUnsafeSourceFileError(err) {
		t.Fatalf("ValidateNoSourceLinks error = %v, want unsafe special build input", err)
	}
}

func TestValidateNoSourceLinksScansExplicitDocDirectories(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	mustWrite(t, filepath.Join(root, "main.go"), "package main\n")
	mustWrite(t, filepath.Join(base, "outside.go"), "package outside\n")
	if err := os.Symlink(filepath.Join(base, "outside.go"), filepath.Join(root, "testdata", "linked.go")); err != nil {
		// Ensure the parent exists before deciding whether symlinks are supported.
		if err := os.MkdirAll(filepath.Join(root, "testdata"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(base, "outside.go"), filepath.Join(root, "testdata", "linked.go")); err != nil {
			t.Skipf("create testdata source symlink: %v", err)
		}
	}
	if err := scanner.ValidateNoSourceLinks(root); err == nil || !scanner.IsUnsafeSourceFileError(err) {
		t.Fatalf("ValidateNoSourceLinks testdata error = %v, want unsafe source", err)
	}
}

func TestValidateNoSourceLinksRejectsLinkedToolchainMetadata(t *testing.T) {
	for _, relative := range []string{"go.sum", "go.work.sum", filepath.Join("vendor", "modules.txt")} {
		t.Run(filepath.ToSlash(relative), func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "repository")
			mustWrite(t, filepath.Join(root, "main.go"), "package main\n")
			outside := filepath.Join(base, "outside-metadata")
			mustWrite(t, outside, "outside metadata\n")
			linked := filepath.Join(root, relative)
			if err := os.MkdirAll(filepath.Dir(linked), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, linked); err != nil {
				t.Skipf("create metadata symlink: %v", err)
			}
			if err := scanner.ValidateNoSourceLinks(root); err == nil || !scanner.IsUnsafeSourceFileError(err) {
				t.Fatalf("ValidateNoSourceLinks(%s) error = %v, want unsafe metadata", relative, err)
			}
		})
	}
}

func TestValidateNoSourceLinksRejectsSpecialToolchainMetadata(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-domain socket fixture is unavailable on Windows")
	}
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.go"), "package main\n")
	listener, err := net.Listen("unix", filepath.Join(root, "go.sum"))
	if err != nil {
		t.Skipf("create Unix-domain socket metadata: %v", err)
	}
	defer func() { _ = listener.Close() }()

	if err := scanner.ValidateNoSourceLinks(root); err == nil || !scanner.IsUnsafeSourceFileError(err) {
		t.Fatalf("ValidateNoSourceLinks special metadata error = %v, want unsafe metadata", err)
	}
}

func TestValidateNoSourceLinksRejectsDescendantDirectorySymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	mustWrite(t, filepath.Join(root, "main.go"), "package main\n")
	outside := filepath.Join(base, "outside")
	mustWrite(t, filepath.Join(outside, "source.go"), "package outside\n")
	if err := os.Symlink(outside, filepath.Join(root, "linked-package")); err != nil {
		t.Skipf("create directory symlink: %v", err)
	}
	if err := scanner.ValidateNoSourceLinks(root); err == nil || !scanner.IsUnsafeSourceFileError(err) {
		t.Fatalf("ValidateNoSourceLinks directory error = %v, want unsafe source", err)
	}
}

func TestValidateNoSourceLinksRejectsUnrecognizedFileSymlinkWithoutFollowingIt(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	mustWrite(t, filepath.Join(root, "main.go"), "package main\n")
	// A dangling non-Go link proves validation relies only on the repository
	// entry itself and does not need to stat or open its target.
	if err := os.Symlink(filepath.Join(base, "missing-target"), filepath.Join(root, "notes.txt")); err != nil {
		t.Skipf("create unrelated file symlink: %v", err)
	}
	if err := scanner.ValidateNoSourceLinks(root); err == nil || !scanner.IsUnsafeSourceFileError(err) {
		t.Fatalf("ValidateNoSourceLinks unrelated link error = %v, want unsafe source", err)
	}
}

func TestValidateToolchainSourceInputsRejectsLinkedSiblingWorkspaceSource(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	selected := filepath.Join(workspace, "selected")
	sibling := filepath.Join(workspace, "sibling")
	for _, directory := range []string{selected, sibling} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(workspace, "go.work"), "go 1.26\n\nuse (\n\t./selected\n\t./sibling\n)\n")
	mustWrite(t, filepath.Join(selected, "go.mod"), "module example.com/selected\n\ngo 1.26\n")
	mustWrite(t, filepath.Join(selected, "main.go"), "package selected\n")
	mustWrite(t, filepath.Join(sibling, "go.mod"), "module example.com/sibling\n\ngo 1.26\n")
	outside := filepath.Join(base, "outside.go")
	mustWrite(t, outside, "package outside\n")
	if err := os.Symlink(outside, filepath.Join(sibling, "linked.go")); err != nil {
		t.Skipf("create sibling source symlink: %v", err)
	}
	t.Setenv("GOWORK", "auto")
	t.Setenv("GO111MODULE", "")

	if err := scanner.ValidateToolchainSourceInputs(selected); err == nil || !scanner.IsUnsafeSourceFileError(err) {
		t.Fatalf("ValidateToolchainSourceInputs linked sibling error = %v, want unsafe source", err)
	}
}

func TestValidateToolchainSourceInputsRejectsLinkedWorkspaceVendorSource(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	selected := filepath.Join(workspace, "selected")
	vendorPackage := filepath.Join(workspace, "vendor", "example.com", "dependency")
	for _, directory := range []string{selected, vendorPackage} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(workspace, "go.work"), "go 1.26\n\nuse ./selected\n")
	mustWrite(t, filepath.Join(workspace, "vendor", "modules.txt"), "## workspace\n")
	mustWrite(t, filepath.Join(selected, "go.mod"), "module example.com/selected\n\ngo 1.26\n")
	mustWrite(t, filepath.Join(selected, "main.go"), "package selected\n")
	outside := filepath.Join(base, "outside.go")
	mustWrite(t, outside, "package dependency\n")
	if err := os.Symlink(outside, filepath.Join(vendorPackage, "linked.go")); err != nil {
		t.Skipf("create workspace vendor source symlink: %v", err)
	}
	t.Setenv("GOWORK", "auto")
	t.Setenv("GO111MODULE", "")

	if err := scanner.ValidateToolchainSourceInputs(selected); err == nil || !scanner.IsUnsafeSourceFileError(err) {
		t.Fatalf("ValidateToolchainSourceInputs linked workspace vendor error = %v, want unsafe source", err)
	}
}

func TestValidateNoSourceLinksAllowsExplicitRootSymlink(t *testing.T) {
	realRoot := filepath.Join(t.TempDir(), "real")
	mustWrite(t, filepath.Join(realRoot, "main.go"), "package main\n")
	linkRoot := filepath.Join(t.TempDir(), "linked-root")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Skipf("create root symlink: %v", err)
	}
	if err := scanner.ValidateNoSourceLinks(linkRoot); err != nil {
		t.Fatalf("ValidateNoSourceLinks explicit root link = %v", err)
	}
}

func TestSourceValidationRootPrefersEnclosingModule(t *testing.T) {
	moduleRoot := filepath.Join(t.TempDir(), "module")
	mustWrite(t, filepath.Join(moduleRoot, "go.mod"), "module example.com/module\n")
	start := filepath.Join(moduleRoot, "nested", "package")
	if err := os.MkdirAll(filepath.Join(moduleRoot, "nested", ".gograph"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := scanner.SourceValidationRoot(start)
	if err != nil {
		t.Fatal(err)
	}
	if got != moduleRoot {
		t.Fatalf("SourceValidationRoot = %q, want module root %q", got, moduleRoot)
	}
}

func TestSourceValidationRootUsesRealArtifactFallback(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "repository")
	start := filepath.Join(repository, "nested")
	if err := os.MkdirAll(filepath.Join(repository, ".gograph"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := scanner.SourceValidationRoot(start)
	if err != nil {
		t.Fatal(err)
	}
	if got != repository {
		t.Fatalf("SourceValidationRoot = %q, want artifact root %q", got, repository)
	}
}

func TestSourceValidationRootRejectsLinkedModuleFile(t *testing.T) {
	base := t.TempDir()
	repository := filepath.Join(base, "repository")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(base, "outside.mod"), "module example.com/outside\n")
	if err := os.Symlink(filepath.Join(base, "outside.mod"), filepath.Join(repository, "go.mod")); err != nil {
		t.Skipf("create go.mod symlink: %v", err)
	}
	if _, err := scanner.SourceValidationRoot(repository); err == nil {
		t.Fatal("SourceValidationRoot accepted linked go.mod")
	}
}

func TestSourceValidationRootPrefersEnclosingWorkspace(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	moduleRoot := filepath.Join(workspaceRoot, "module")
	mustWrite(t, filepath.Join(workspaceRoot, "go.work"), "go 1.26\n\nuse ./module\n")
	mustWrite(t, filepath.Join(moduleRoot, "go.mod"), "module example.com/module\n")
	start := filepath.Join(moduleRoot, "nested")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := scanner.SourceValidationRoot(start)
	if err != nil {
		t.Fatal(err)
	}
	if got != workspaceRoot {
		t.Fatalf("SourceValidationRoot = %q, want workspace root %q", got, workspaceRoot)
	}
}

func TestSourceValidationRootHonorsDisabledWorkspace(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	moduleRoot := filepath.Join(workspaceRoot, "module")
	start := filepath.Join(moduleRoot, "nested")
	mustWrite(t, filepath.Join(workspaceRoot, "go.work"), "go 1.26\n\nuse ./module\n")
	mustWrite(t, filepath.Join(moduleRoot, "go.mod"), "module example.com/module\n")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", "off")
	t.Setenv("GO111MODULE", "")

	got, err := scanner.SourceValidationRoot(start)
	if err != nil {
		t.Fatal(err)
	}
	if got != moduleRoot {
		t.Fatalf("SourceValidationRoot with GOWORK=off = %q, want module root %q", got, moduleRoot)
	}
}

func TestSourceValidationRootRejectsLinkedWorkspaceFile(t *testing.T) {
	base := t.TempDir()
	repository := filepath.Join(base, "repository")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(base, "outside.work"), "go 1.26\n")
	if err := os.Symlink(filepath.Join(base, "outside.work"), filepath.Join(repository, "go.work")); err != nil {
		t.Skipf("create go.work symlink: %v", err)
	}
	if _, err := scanner.SourceValidationRoot(repository); err == nil {
		t.Fatal("SourceValidationRoot accepted linked go.work")
	}
}

func TestValidateGoDocQuery(t *testing.T) {
	for _, query := range []string{"fmt", "fmt.Errorf", "net/http.HandleFunc", "github.com/jackc/pgx/v5.Conn.QueryRow", "github.com/user/foo.go"} {
		if err := scanner.ValidateGoDocQuery(query); err != nil {
			t.Errorf("ValidateGoDocQuery(%q) = %v, want accepted", query, err)
		}
	}
	for _, query := range []string{"", "./local", "../outside", "/tmp/secret.go", "C:\\secret.go", "-all", "~/.cache", "pkg/../outside", "module/.gograph/source", "net//http", "fmt Errorf"} {
		if err := scanner.ValidateGoDocQuery(query); err == nil {
			t.Errorf("ValidateGoDocQuery(%q) succeeded, want rejection", query)
		}
	}
}

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
	mustWrite(t, filepath.Join(realRoot, "root.s"), "// regular assembly input\n")
	linkRoot := filepath.Join(t.TempDir(), "linked-root")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Skipf("create root symlink: %v", err)
	}

	ctx := build.Default
	var opened []string
	ctx.OpenFile = func(path string) (io.ReadCloser, error) {
		opened = append(opened, filepath.Base(path))
		return os.Open(path)
	}
	paths, errs := scanner.WalkWithContext(linkRoot, ctx)
	if len(errs) != 0 {
		t.Fatalf("WalkWithContext errors: %v", errs)
	}
	if got, want := relativePaths(t, linkRoot, paths), []string{"root.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected files = %v, want %v", got, want)
	}
	if !containsString(opened, "root.s") {
		t.Fatalf("regular assembly input beneath explicit root symlink was not inspected: %v", opened)
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

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
