package buildctx

import (
	"context"
	"errors"
	"go/build"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

func TestResolveUsesEffectiveGoToolContext(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/buildctx\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOENV", "off")
	t.Setenv("GOWORK", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GO111MODULE", "")
	t.Setenv("GOOS", "linux")
	t.Setenv("GOARCH", "amd64")
	t.Setenv("CGO_ENABLED", "0")
	t.Setenv("GOFLAGS", "-tags=issue30_custom,ignore")

	config, err := Resolve(context.Background(), root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	buildContext := config.BuildContext()
	if buildContext.GOOS != "linux" || buildContext.GOARCH != "amd64" || buildContext.CgoEnabled {
		t.Fatalf("resolved target = %s/%s cgo=%v, want linux/amd64 cgo=false", buildContext.GOOS, buildContext.GOARCH, buildContext.CgoEnabled)
	}
	for _, tag := range []string{"issue30_custom", "ignore"} {
		if !slices.Contains(buildContext.BuildTags, tag) {
			t.Fatalf("resolved build tags %v do not contain %q", buildContext.BuildTags, tag)
		}
	}
	if len(buildContext.ToolTags) == 0 || len(buildContext.ReleaseTags) == 0 {
		t.Fatalf("tool/release tags were not resolved: tool=%v release=%v", buildContext.ToolTags, buildContext.ReleaseTags)
	}
	if buildContext.GOROOT == "" || buildContext.GOPATH == "" {
		t.Fatalf("Go roots were not resolved: GOROOT=%q GOPATH=%q", buildContext.GOROOT, buildContext.GOPATH)
	}
	if want := []string{"-tags=issue30_custom,ignore"}; !reflect.DeepEqual(config.Flags(), want) {
		t.Fatalf("Flags() = %v, want %v", config.Flags(), want)
	}
	if got := lastEnvironmentValue(config.Environment(), "CGO_ENABLED"); got != "0" {
		t.Fatalf("CGO_ENABLED = %q, want 0", got)
	}
	resolvedRootInfo, resolvedRootErr := os.Stat(config.ModuleRoot())
	wantRootInfo, wantRootErr := os.Stat(root)
	if !config.ModulesEnabled() || resolvedRootErr != nil || wantRootErr != nil || !os.SameFile(resolvedRootInfo, wantRootInfo) {
		t.Fatalf("module context = enabled:%v root:%q, want enabled root equivalent to %q", config.ModulesEnabled(), config.ModuleRoot(), root)
	}
	if config.ModulePath() != "example.com/buildctx" {
		t.Fatalf("module path = %q, want example.com/buildctx", config.ModulePath())
	}
}

func TestResolveWithOptionsOverridesGOFLAGSBuildTags(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/explicit-tags\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOENV", "off")
	t.Setenv("GOWORK", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOFLAGS", "-tags=ambient_tag")

	config, err := ResolveWithOptions(context.Background(), root, ResolveOptions{BuildTags: []string{"second,explicit_tag", "second"}})
	if err != nil {
		t.Fatalf("ResolveWithOptions: %v", err)
	}
	if got, want := config.BuildContext().BuildTags, []string{"explicit_tag", "second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildTags = %v, want %v", got, want)
	}
	if got, want := config.Flags(), []string{"-tags=explicit_tag,second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Flags = %v, want %v", got, want)
	}
}

func TestNormalizeBuildTagsRejectsFlagsAndExpressions(t *testing.T) {
	for _, value := range []string{"", "integration,,linux", "integration linux", "integration||linux", "-mod=vendor"} {
		t.Run(value, func(t *testing.T) {
			if _, err := NormalizeBuildTags([]string{value}); err == nil {
				t.Fatalf("NormalizeBuildTags(%q) succeeded", value)
			}
		})
	}
}

func TestResolveCanonicalizesSymlinkRootIndependentlyOfAmbientPWD(t *testing.T) {
	realRoot := filepath.Join(t.TempDir(), "real")
	if err := os.MkdirAll(realRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realRoot, "go.mod"), []byte("module example.com/symlink\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkRoot := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Skipf("create root symlink: %v", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("GOENV", "off")
	t.Setenv("GOWORK", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("PWD", filepath.Dir(linkRoot))
	outside, err := Resolve(context.Background(), linkRoot)
	if err != nil {
		t.Fatalf("Resolve from outside symlink: %v", err)
	}
	t.Setenv("PWD", linkRoot)
	inside, err := Resolve(context.Background(), linkRoot)
	if err != nil {
		t.Fatalf("Resolve from inside symlink: %v", err)
	}

	if outside.Fingerprint() != inside.Fingerprint() {
		t.Fatalf("ambient PWD changed the fingerprint: outside=%s inside=%s", outside.Fingerprint(), inside.Fingerprint())
	}
	for name, config := range map[string]Config{"outside": outside, "inside": inside} {
		if config.ModuleRoot() != canonicalRoot {
			t.Fatalf("%s module root = %q, want canonical %q", name, config.ModuleRoot(), canonicalRoot)
		}
		if got := lastEnvironmentValue(config.Environment(), "PWD"); got != canonicalRoot {
			t.Fatalf("%s PWD = %q, want canonical %q", name, got, canonicalRoot)
		}
	}
}

func TestResolveRejectsSymlinkedGoMod(t *testing.T) {
	base := t.TempDir()
	repository := filepath.Join(base, "repository")
	shared := filepath.Join(base, "shared")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	sharedMod := filepath.Join(shared, "base.mod")
	if err := os.WriteFile(sharedMod, []byte("module example.com/modlink\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sharedMod, filepath.Join(repository, "go.mod")); err != nil {
		t.Skipf("create go.mod symlink: %v", err)
	}

	t.Setenv("GOENV", "off")
	t.Setenv("GOWORK", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	if _, err := Resolve(context.Background(), repository); err == nil || !strings.Contains(err.Error(), "unsafe repository source path") {
		t.Fatalf("Resolve with symlinked go.mod error = %v, want unsafe path rejection", err)
	}
}

func TestResolveRejectsSymlinkedGoWork(t *testing.T) {
	base := t.TempDir()
	repository := filepath.Join(base, "repository")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	sharedWork := filepath.Join(base, "shared.work")
	if err := os.WriteFile(sharedWork, []byte("go 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sharedWork, filepath.Join(repository, "go.work")); err != nil {
		t.Skipf("create go.work symlink: %v", err)
	}

	t.Setenv("GOENV", "off")
	t.Setenv("GOWORK", "auto")
	t.Setenv("GOTOOLCHAIN", "local")
	if _, err := Resolve(context.Background(), repository); err == nil || !strings.Contains(err.Error(), "unsafe repository source path") {
		t.Fatalf("Resolve with symlinked go.work error = %v, want unsafe path rejection", err)
	}
}

func TestValidateToolchainMetadataRejectsLinkedCompanions(t *testing.T) {
	for _, test := range []struct {
		name       string
		primary    string
		companion  string
		goWork     string
		moduleMode string
	}{
		{name: "module sum", primary: "go.mod", companion: "go.sum", goWork: "off"},
		{name: "workspace sum", primary: "go.work", companion: "go.work.sum", goWork: "auto"},
		{name: "vendor metadata", primary: "go.mod", companion: filepath.Join("vendor", "modules.txt"), goWork: "off"},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "repository")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			primaryContents := "module example.com/metadata\n\ngo 1.26\n"
			if test.primary == "go.work" {
				primaryContents = "go 1.26\n"
			}
			if err := os.WriteFile(filepath.Join(root, test.primary), []byte(primaryContents), 0o644); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(base, "outside-metadata")
			if err := os.WriteFile(outside, []byte("outside metadata\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			linked := filepath.Join(root, test.companion)
			if err := os.MkdirAll(filepath.Dir(linked), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, linked); err != nil {
				t.Skipf("create metadata symlink: %v", err)
			}
			t.Setenv("GOWORK", test.goWork)
			t.Setenv("GO111MODULE", test.moduleMode)
			if err := validateToolchainMetadata(root); err == nil || !strings.Contains(err.Error(), "unsafe repository source path") {
				t.Fatalf("validateToolchainMetadata with linked %s = %v, want unsafe path rejection", test.companion, err)
			}
		})
	}
}

func TestValidateToolchainMetadataRejectsLinkedVendorDirectory(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	outsideVendor := filepath.Join(base, "outside-vendor")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideVendor, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/vendorlink\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideVendor, "modules.txt"), []byte("outside metadata\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideVendor, filepath.Join(root, "vendor")); err != nil {
		t.Skipf("create vendor directory symlink: %v", err)
	}
	t.Setenv("GOWORK", "off")
	t.Setenv("GO111MODULE", "")
	if err := validateToolchainMetadata(root); err == nil || !strings.Contains(err.Error(), "unsafe repository source path") {
		t.Fatalf("validateToolchainMetadata with linked vendor = %v, want unsafe path rejection", err)
	}
}

func TestValidateToolchainMetadataRejectsUnsafeWorkspaceInputs(t *testing.T) {
	for _, test := range []struct {
		name      string
		workUse   string
		configure func(*testing.T, string, string)
	}{
		{
			name: "workspace vendor metadata",
			configure: func(t *testing.T, root, outside string) {
				if err := os.MkdirAll(filepath.Join(root, "vendor"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "vendor", "modules.txt")); err != nil {
					t.Skipf("create workspace vendor metadata symlink: %v", err)
				}
			},
		},
		{
			name: "workspace member directory",
			configure: func(t *testing.T, root, outside string) {
				outsideMember := filepath.Join(filepath.Dir(outside), "outside-member")
				if err := os.MkdirAll(outsideMember, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(outsideMember, "go.mod"), []byte("module example.com/outside\n\ngo 1.26\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(filepath.Join(root, "member")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outsideMember, filepath.Join(root, "member")); err != nil {
					t.Skipf("create workspace member symlink: %v", err)
				}
			},
		},
		{
			name: "workspace member module",
			configure: func(t *testing.T, root, outside string) {
				if err := os.Symlink(outside, filepath.Join(root, "member", "go.mod")); err != nil {
					t.Skipf("create workspace member go.mod symlink: %v", err)
				}
			},
		},
		{
			name: "workspace member sum",
			configure: func(t *testing.T, root, outside string) {
				if err := os.WriteFile(filepath.Join(root, "member", "go.mod"), []byte("module example.com/member\n\ngo 1.26\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "member", "go.sum")); err != nil {
					t.Skipf("create workspace member go.sum symlink: %v", err)
				}
			},
		},
		{
			name:    "outside workspace member",
			workUse: "../outside-member",
			configure: func(t *testing.T, _ string, outside string) {
				outsideMember := filepath.Join(filepath.Dir(outside), "outside-member")
				if err := os.MkdirAll(outsideMember, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(outsideMember, "go.mod"), []byte("module example.com/outside\n\ngo 1.26\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "repository")
			if err := os.MkdirAll(filepath.Join(root, "member"), 0o755); err != nil {
				t.Fatal(err)
			}
			usePath := test.workUse
			if usePath == "" {
				usePath = "./member"
			}
			if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26\n\nuse "+usePath+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(base, "outside-metadata")
			if err := os.WriteFile(outside, []byte("outside metadata\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			test.configure(t, root, outside)
			t.Setenv("GOWORK", "auto")
			t.Setenv("GO111MODULE", "")
			if err := validateToolchainMetadata(root); err == nil || !strings.Contains(err.Error(), "unsafe repository source path") {
				t.Fatalf("validateToolchainMetadata unsafe workspace error = %v, want unsafe path rejection", err)
			}
		})
	}
}

func TestValidateToolchainMetadataAcceptsConfinedWorkspaceMembers(t *testing.T) {
	root := t.TempDir()
	for _, member := range []string{"member-a", "member-b"} {
		if err := os.MkdirAll(filepath.Join(root, member), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, member, "go.mod"), []byte("module example.com/"+member+"\n\ngo 1.26\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	absoluteMember := filepath.ToSlash(filepath.Join(root, "member-b"))
	workFile := "go 1.26\n\nuse (\n\t./member-a\n\t" + absoluteMember + "\n)\n"
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte(workFile), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "member-a", "go.sum"), []byte("regular sum\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "vendor", "modules.txt"), []byte("## workspace\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", "auto")
	t.Setenv("GO111MODULE", "")
	sourceRoots, err := ToolchainSourceRoots(root)
	if err != nil {
		t.Fatalf("ToolchainSourceRoots confined workspace = %v", err)
	}
	wantRoots := []string{root, filepath.Join(root, "member-a"), filepath.Join(root, "member-b")}
	for index := range wantRoots {
		wantRoots[index], err = filepath.EvalSymlinks(wantRoots[index])
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(sourceRoots)
	sort.Strings(wantRoots)
	if !reflect.DeepEqual(sourceRoots, wantRoots) {
		t.Fatalf("ToolchainSourceRoots = %v, want %v", sourceRoots, wantRoots)
	}
}

func TestToolchainSourceRootsAllowsSiblingWorkspaceMemberWithinGitAuthority(t *testing.T) {
	base := t.TempDir()
	repository := filepath.Join(base, "repository")
	analysisRoot := filepath.Join(repository, "cust", "app")
	siblingRoot := filepath.Join(repository, "mesa2", "core")
	for _, directory := range []string{filepath.Join(repository, ".git"), analysisRoot, siblingRoot} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for directory, module := range map[string]string{
		analysisRoot: "example.com/app",
		siblingRoot:  "example.com/mesa",
	} {
		if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte("module "+module+"\n\ngo 1.26\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(analysisRoot, "go.work"), []byte("go 1.26\n\nuse (\n\t.\n\t../../mesa2/core\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", "auto")
	t.Setenv("GO111MODULE", "")

	sourceRoots, err := ToolchainSourceRoots(analysisRoot)
	if err != nil {
		t.Fatalf("ToolchainSourceRoots same-checkout sibling = %v", err)
	}
	wantRoots := []string{analysisRoot, siblingRoot}
	for index := range wantRoots {
		wantRoots[index], err = filepath.EvalSymlinks(wantRoots[index])
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(sourceRoots)
	sort.Strings(wantRoots)
	if !reflect.DeepEqual(sourceRoots, wantRoots) {
		t.Fatalf("ToolchainSourceRoots = %v, want %v", sourceRoots, wantRoots)
	}
}

func TestToolchainSourceRootsUsesNearestGitAuthority(t *testing.T) {
	base := t.TempDir()
	outer := filepath.Join(base, "outer")
	nested := filepath.Join(outer, "nested")
	analysisRoot := filepath.Join(nested, "app")
	outsideNestedCheckout := filepath.Join(outer, "sibling")
	for _, directory := range []string{
		filepath.Join(outer, ".git"),
		filepath.Join(nested, ".git"),
		analysisRoot,
		outsideNestedCheckout,
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for directory, module := range map[string]string{
		analysisRoot:          "example.com/app",
		outsideNestedCheckout: "example.com/sibling",
	} {
		if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte("module "+module+"\n\ngo 1.26\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(analysisRoot, "go.work"), []byte("go 1.26\n\nuse (\n\t.\n\t../../sibling\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", "auto")
	t.Setenv("GO111MODULE", "")

	if _, err := ToolchainSourceRoots(analysisRoot); err == nil || !strings.Contains(err.Error(), "workspace use path must stay beneath") {
		t.Fatalf("ToolchainSourceRoots nested checkout error = %v, want nearest-boundary rejection", err)
	}
}

func TestToolchainSourceRootsRejectsWorkspaceAboveNearestGitAuthority(t *testing.T) {
	base := t.TempDir()
	outer := filepath.Join(base, "outer")
	nested := filepath.Join(outer, "nested")
	analysisRoot := filepath.Join(nested, "app")
	for _, directory := range []string{filepath.Join(nested, ".git"), analysisRoot} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(analysisRoot, "go.mod"), []byte("module example.com/app\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outer, "go.work"), []byte("go 1.26\n\nuse ./nested/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", "auto")
	t.Setenv("GO111MODULE", "")

	if _, err := ToolchainSourceRoots(analysisRoot); err == nil || !strings.Contains(err.Error(), "workspace file must stay beneath repository source authority") {
		t.Fatalf("ToolchainSourceRoots parent workspace error = %v, want nearest-boundary rejection", err)
	}
}

func TestToolchainSourceRootsDoesNotTrustUnrelatedAliasParent(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "real", "workspace")
	member := filepath.Join(workspace, "member")
	validatedDecoy := filepath.Join(workspace, "outside")
	outside := filepath.Join(base, "outside")
	for _, directory := range []string{member, validatedDecoy, outside} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for directory, module := range map[string]string{
		member:         "example.com/member",
		validatedDecoy: "example.com/decoy",
		outside:        "example.com/outside",
	} {
		if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte("module "+module+"\n\ngo 1.26\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The absolute entry is beneath base, not the canonical workspace. A scan
	// alias that points directly at member must not make base a trusted alias
	// for the workspace merely because both paths are ascended once.
	workFile := "go 1.26\n\nuse " + filepath.ToSlash(outside) + "\n"
	if err := os.WriteFile(filepath.Join(workspace, "go.work"), []byte(workFile), 0o644); err != nil {
		t.Fatal(err)
	}
	rootAlias := filepath.Join(base, "link")
	if err := os.Symlink(member, rootAlias); err != nil {
		t.Skipf("create selected-root symlink: %v", err)
	}
	t.Setenv("GOWORK", "auto")
	t.Setenv("GO111MODULE", "")

	if _, err := ToolchainSourceRoots(rootAlias); err == nil || !strings.Contains(err.Error(), "workspace use path must stay beneath") {
		t.Fatalf("ToolchainSourceRoots false alias error = %v, want workspace confinement rejection", err)
	}
}

func TestValidateToolchainMetadataHonorsModuleModeOff(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "outside.work"), []byte("go 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "outside.work"), filepath.Join(root, "go.work")); err != nil {
		t.Skipf("create ignored workspace symlink: %v", err)
	}
	t.Setenv("GOWORK", "auto")
	t.Setenv("GO111MODULE", "off")
	if err := validateToolchainMetadata(root); err != nil {
		t.Fatalf("validateToolchainMetadata inspected auto workspace with module mode off: %v", err)
	}
}

func TestValidateToolchainMetadataStopsAtApplicableFiles(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/nearest\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside.mod")
	if err := os.WriteFile(outside, []byte("module example.com/outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "go.mod")); err != nil {
		t.Skipf("create outer go.mod symlink: %v", err)
	}
	t.Setenv("GOWORK", "off")
	t.Setenv("GO111MODULE", "")
	sourceRoots, err := ToolchainSourceRoots(root)
	if err != nil {
		t.Fatalf("ToolchainSourceRoots inspected an inapplicable outer go.mod: %v", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sourceRoots, []string{canonicalRoot}) {
		t.Fatalf("ToolchainSourceRoots = %v, want module root %s", sourceRoots, canonicalRoot)
	}
}

func TestConfigAccessorsReturnDefensiveCopies(t *testing.T) {
	config := FromBuildContext(buildContextFixture(), []string{"ORIGINAL=value"})
	ctx := config.BuildContext()
	ctx.BuildTags[0] = "mutated"
	env := config.Environment()
	env[0] = "MUTATED=value"
	flags := config.Flags()
	flags[0] = "-tags=mutated"

	if got := config.BuildContext().BuildTags[0]; got != "original" {
		t.Fatalf("BuildContext was mutated through accessor: %q", got)
	}
	if got := config.Environment()[0]; got != "ORIGINAL=value" {
		t.Fatalf("Environment was mutated through accessor: %q", got)
	}
	if got := config.Flags()[0]; got != "-tags=original" {
		t.Fatalf("Flags were mutated through accessor: %q", got)
	}
}

func TestResolveIgnoresSuccessfulStderr(t *testing.T) {
	config, err := resolve(context.Background(), t.TempDir(), []string{"PATH=test"}, nil, func(_ context.Context, _ string, _ []string, args ...string) ([]byte, []byte, error) {
		if args[0] == "env" {
			return []byte(testWireModuleContextJSON), []byte("go: module diagnostic\n"), nil
		}
		return []byte(testWireContextJSON), []byte("go: downloading go1.27.0\n"), nil
	})
	if err != nil {
		t.Fatalf("resolve with successful stderr: %v", err)
	}
	if got := config.BuildContext(); got.GOOS != "linux" || got.GOARCH != "amd64" {
		t.Fatalf("resolved context = %s/%s, want linux/amd64", got.GOOS, got.GOARCH)
	}
}

func TestResolveReportsCommandDiagnostics(t *testing.T) {
	commandErr := errors.New("exit status 1")
	tests := []struct {
		name   string
		stdout string
		stderr string
		want   string
	}{
		{name: "stderr preferred", stdout: "stdout detail", stderr: "stderr detail", want: "stderr detail"},
		{name: "stdout fallback", stdout: "stdout detail", want: "stdout detail"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolve(context.Background(), t.TempDir(), nil, nil, func(context.Context, string, []string, ...string) ([]byte, []byte, error) {
				return []byte(tc.stdout), []byte(tc.stderr), commandErr
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) || !errors.Is(err, commandErr) {
				t.Fatalf("resolve error = %v, want wrapped error containing %q", err, tc.want)
			}
		})
	}
}

func TestResolveReportsModuleContextDiagnostics(t *testing.T) {
	commandErr := errors.New("exit status 1")
	_, err := resolve(context.Background(), t.TempDir(), nil, nil, func(_ context.Context, _ string, _ []string, args ...string) ([]byte, []byte, error) {
		if args[0] == "list" {
			return []byte(testWireContextJSON), nil, nil
		}
		return nil, []byte("module diagnostic"), commandErr
	})
	if err == nil || !strings.Contains(err.Error(), "resolve Go module context") || !strings.Contains(err.Error(), "module diagnostic") || !errors.Is(err, commandErr) {
		t.Fatalf("resolve error = %v, want wrapped module-context diagnostic", err)
	}
}

func TestFingerprintTracksSelectionInputsAsSets(t *testing.T) {
	first := build.Default
	first.GOOS = "linux"
	first.GOARCH = "amd64"
	first.CgoEnabled = false
	first.Compiler = "gc"
	first.BuildTags = []string{"beta", "alpha"}
	first.ToolTags = []string{"amd64.v2", "amd64.v1"}
	first.ReleaseTags = []string{"go1.26", "go1.25"}

	second := first
	second.BuildTags = []string{"alpha", "beta"}
	second.ToolTags = []string{"amd64.v1", "amd64.v2"}
	second.ReleaseTags = []string{"go1.25", "go1.26"}
	if got, want := FromBuildContext(first, nil).Fingerprint(), FromBuildContext(second, nil).Fingerprint(); got != want {
		t.Fatalf("equivalent tag sets produced different fingerprints: %s != %s", got, want)
	}

	second.GOARCH = "arm64"
	if got, notWant := FromBuildContext(second, nil).Fingerprint(), FromBuildContext(first, nil).Fingerprint(); got == notWant {
		t.Fatalf("different GOARCH produced the same fingerprint: %s", got)
	}

	moduleRoot := t.TempDir()
	moduleConfig := FromBuildContextWithModule(first, nil, true, moduleRoot)
	if moduleConfig.Fingerprint() == FromBuildContext(first, nil).Fingerprint() {
		t.Fatal("module-mode state did not affect the fingerprint")
	}
	otherRootConfig := FromBuildContextWithModule(first, nil, true, filepath.Join(moduleRoot, "other"))
	if moduleConfig.Fingerprint() == otherRootConfig.Fingerprint() {
		t.Fatal("module root did not affect the fingerprint")
	}

	differentGoRoot := first
	differentGoRoot.GOROOT = filepath.Join(first.GOROOT, "other")
	if FromBuildContext(first, nil).Fingerprint() == FromBuildContext(differentGoRoot, nil).Fingerprint() {
		t.Fatal("GOROOT did not affect the fingerprint")
	}
	differentGoPath := first
	differentGoPath.GOPATH = filepath.Join(first.GOPATH, "other")
	if FromBuildContext(first, nil).Fingerprint() == FromBuildContext(differentGoPath, nil).Fingerprint() {
		t.Fatal("GOPATH did not affect the fingerprint")
	}
}

func buildContextFixture() build.Context {
	ctx := build.Default
	ctx.BuildTags = []string{"original"}
	return ctx
}

func lastEnvironmentValue(environment []string, key string) string {
	prefix := key + "="
	for i := len(environment) - 1; i >= 0; i-- {
		if len(environment[i]) >= len(prefix) && environment[i][:len(prefix)] == prefix {
			return environment[i][len(prefix):]
		}
	}
	return ""
}

const testWireContextJSON = `{"goos":"linux","goarch":"amd64","cgo_enabled":false,"use_all_files":false,"compiler":"gc","goroot":"/go","gopath":"/gopath","build_tags":["issue30"],"tool_tags":["amd64.v1"],"release_tags":["go1.26"],"install_suffix":""}`

const testWireModuleContextJSON = `{"GO111MODULE":"","GOMOD":""}`
