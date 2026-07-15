// Package buildctx resolves the effective Go build configuration shared by
// source scanning and type-checked package loading.
package buildctx

import (
	"context"
	"encoding/json"
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"strings"
)

// Config is an immutable snapshot of the file-selection inputs used by both
// go/build and go/packages. Callers should use Environment and Flags, which
// return defensive copies, when configuring subprocess-backed package loads.
type Config struct {
	build       build.Context
	environment []string
	buildFlags  []string
}

// Resolve asks cmd/go for its effective build context. This deliberately lets
// the Go tool interpret GOENV, GOFLAGS, toolchain selection, cgo defaults, and
// tool/release tags instead of duplicating those rules in gograph.
func Resolve(ctx context.Context, root string) (Config, error) {
	environment := append([]string(nil), os.Environ()...)
	cmd := exec.CommandContext(ctx, "go", "list", "-e", "-json=false", "-deps=false", "-test=false", "-export=false", "-find=false", "-f", goListContextTemplate, "builtin")
	cmd.Dir = root
	cmd.Env = environment
	out, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			return Config{}, fmt.Errorf("resolve Go build context: %w", err)
		}
		return Config{}, fmt.Errorf("resolve Go build context: %w: %s", err, message)
	}

	var resolved wireContext
	if err := json.Unmarshal(out, &resolved); err != nil {
		return Config{}, fmt.Errorf("decode Go build context: %w", err)
	}

	buildContext := build.Default
	buildContext.GOOS = resolved.GOOS
	buildContext.GOARCH = resolved.GOARCH
	buildContext.CgoEnabled = resolved.CgoEnabled
	buildContext.UseAllFiles = resolved.UseAllFiles
	buildContext.Compiler = resolved.Compiler
	buildContext.BuildTags = append([]string(nil), resolved.BuildTags...)
	buildContext.ToolTags = append([]string(nil), resolved.ToolTags...)
	buildContext.ReleaseTags = append([]string(nil), resolved.ReleaseTags...)
	buildContext.InstallSuffix = resolved.InstallSuffix

	return FromBuildContext(buildContext, environment), nil
}

// FromBuildContext creates a deterministic configuration from an injected
// go/build context. It is primarily useful for host-independent tests and as
// the AST fallback when cmd/go cannot resolve the target repository.
func FromBuildContext(buildContext build.Context, environment []string) Config {
	cgoEnabled := "0"
	if buildContext.CgoEnabled {
		cgoEnabled = "1"
	}
	env := append([]string(nil), environment...)
	env = append(env,
		"GOOS="+buildContext.GOOS,
		"GOARCH="+buildContext.GOARCH,
		"CGO_ENABLED="+cgoEnabled,
	)
	return Config{
		build:       cloneBuildContext(buildContext),
		environment: env,
		// An explicit flag is applied after GOFLAGS by cmd/go. Pinning the
		// already-resolved tags prevents scanner and loader interpretations
		// from diverging while preserving all other inherited GOFLAGS.
		buildFlags: []string{"-tags=" + strings.Join(buildContext.BuildTags, ",")},
	}
}

// BuildContext returns a defensive copy of the resolved go/build context.
func (c Config) BuildContext() build.Context {
	return cloneBuildContext(c.build)
}

// Environment returns a defensive copy of the environment snapshot.
func (c Config) Environment() []string {
	return append([]string(nil), c.environment...)
}

// Flags returns a defensive copy of the package-loader build flags.
func (c Config) Flags() []string {
	return append([]string(nil), c.buildFlags...)
}

func cloneBuildContext(ctx build.Context) build.Context {
	ctx.BuildTags = append([]string(nil), ctx.BuildTags...)
	ctx.ToolTags = append([]string(nil), ctx.ToolTags...)
	ctx.ReleaseTags = append([]string(nil), ctx.ReleaseTags...)
	return ctx
}

type wireContext struct {
	GOOS          string   `json:"goos"`
	GOARCH        string   `json:"goarch"`
	CgoEnabled    bool     `json:"cgo_enabled"`
	UseAllFiles   bool     `json:"use_all_files"`
	Compiler      string   `json:"compiler"`
	BuildTags     []string `json:"build_tags"`
	ToolTags      []string `json:"tool_tags"`
	ReleaseTags   []string `json:"release_tags"`
	InstallSuffix string   `json:"install_suffix"`
}

// cmd/go exposes its fully resolved build.Context to go-list templates. The
// values interpolated here are restricted Go identifiers or booleans;
// printf %q therefore produces valid JSON strings while retaining all tags.
const goListContextTemplate = `{"goos":{{printf "%q" context.GOOS}},"goarch":{{printf "%q" context.GOARCH}},"cgo_enabled":{{context.CgoEnabled}},"use_all_files":{{context.UseAllFiles}},"compiler":{{printf "%q" context.Compiler}},"build_tags":[{{range $index, $tag := context.BuildTags}}{{if $index}},{{end}}{{printf "%q" $tag}}{{end}}],"tool_tags":[{{range $index, $tag := context.ToolTags}}{{if $index}},{{end}}{{printf "%q" $tag}}{{end}}],"release_tags":[{{range $index, $tag := context.ReleaseTags}}{{if $index}},{{end}}{{printf "%q" $tag}}{{end}}],"install_suffix":{{printf "%q" context.InstallSuffix}}}`
