package buildctx

import (
	"context"
	"errors"
	"go/build"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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

func TestResolveKeepsSymlinkedGoModAtItsContainingModuleRoot(t *testing.T) {
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
	config, err := Resolve(context.Background(), repository)
	if err != nil {
		t.Fatalf("Resolve with symlinked go.mod: %v", err)
	}
	canonicalRepository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	if config.ModuleRoot() != canonicalRepository {
		t.Fatalf("module root = %q, want containing repository %q", config.ModuleRoot(), canonicalRepository)
	}
	if config.ModulePath() != "example.com/modlink" {
		t.Fatalf("module path = %q, want example.com/modlink", config.ModulePath())
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
	config, err := resolve(context.Background(), t.TempDir(), []string{"PATH=test"}, func(_ context.Context, _ string, _ []string, args ...string) ([]byte, []byte, error) {
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
			_, err := resolve(context.Background(), t.TempDir(), nil, func(context.Context, string, []string, ...string) ([]byte, []byte, error) {
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
	_, err := resolve(context.Background(), t.TempDir(), nil, func(_ context.Context, _ string, _ []string, args ...string) ([]byte, []byte, error) {
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
