package buildctx

import (
	"context"
	"go/build"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	if want := []string{"-tags=issue30_custom,ignore"}; !reflect.DeepEqual(config.Flags(), want) {
		t.Fatalf("Flags() = %v, want %v", config.Flags(), want)
	}
	if got := lastEnvironmentValue(config.Environment(), "CGO_ENABLED"); got != "0" {
		t.Fatalf("CGO_ENABLED = %q, want 0", got)
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
