// Package buildctx resolves the effective Go build configuration shared by
// source scanning and type-checked package loading.
package buildctx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

// Config is an immutable snapshot of the file-selection inputs used by both
// go/build and go/packages. Callers should use Environment and Flags, which
// return defensive copies, when configuring subprocess-backed package loads.
type Config struct {
	build          build.Context
	environment    []string
	buildFlags     []string
	modulesEnabled bool
	moduleRoot     string
	modulePath     string
}

// Resolve asks cmd/go for its effective build context. This deliberately lets
// the Go tool interpret GOENV, GOFLAGS, toolchain selection, cgo defaults, and
// tool/release tags instead of duplicating those rules in gograph.
func Resolve(ctx context.Context, root string) (Config, error) {
	canonicalRoot, err := canonicalDirectory(root)
	if err != nil {
		return Config{}, fmt.Errorf("resolve Go build root: %w", err)
	}
	// os.Getwd accepts PWD as its lexical spelling when it names the current
	// directory. Pin both cmd.Dir and PWD to the canonical root so cmd/go does
	// not discover a different GOMOD merely because gograph was invoked from
	// inside rather than outside a symlinked scan root.
	environment := setEnvironmentValue(os.Environ(), "PWD", canonicalRoot)
	return resolve(ctx, canonicalRoot, environment, runGoCommand)
}

// ResolveOrDefault returns the effective Go context when cmd/go is available,
// or a usable build.Default snapshot together with the resolution error. AST
// callers can remain tolerant while precise callers retain the error.
func ResolveOrDefault(ctx context.Context, root string) (Config, error) {
	config, err := Resolve(ctx, root)
	if err == nil {
		return config, nil
	}
	return FromBuildContext(build.Default, os.Environ()), err
}

type goCommandRunner func(context.Context, string, []string, ...string) (stdout, stderr []byte, err error)

func runGoCommand(ctx context.Context, root string, environment []string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = root
	cmd.Env = environment
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	return stdout, stderr.Bytes(), err
}

func resolve(ctx context.Context, root string, environment []string, run goCommandRunner) (Config, error) {
	stdout, stderr, err := run(ctx, root, environment,
		"list", "-e", "-json=false", "-deps=false", "-test=false", "-export=false", "-find=false", "-f", goListContextTemplate, "builtin",
	)
	if err != nil {
		return Config{}, commandError("resolve Go build context", stdout, stderr, err)
	}

	var resolved wireContext
	if err := json.Unmarshal(stdout, &resolved); err != nil {
		return Config{}, fmt.Errorf("decode Go build context: %w", err)
	}
	if resolved.GOOS == "" || resolved.GOARCH == "" || resolved.Compiler == "" {
		return Config{}, fmt.Errorf("decode Go build context: missing GOOS, GOARCH, or compiler")
	}

	moduleStdout, moduleStderr, err := run(ctx, root, environment, "env", "-json", "GO111MODULE", "GOMOD")
	if err != nil {
		return Config{}, commandError("resolve Go module context", moduleStdout, moduleStderr, err)
	}
	var moduleContext wireModuleContext
	if err := json.Unmarshal(moduleStdout, &moduleContext); err != nil {
		return Config{}, fmt.Errorf("decode Go module context: %w", err)
	}
	moduleRoot := ""
	modulePath := ""
	if moduleContext.GOMOD != "" && filepath.Clean(moduleContext.GOMOD) != filepath.Clean(os.DevNull) {
		// Cmd/go defines the module root from the directory entry named by
		// GOMOD, even when go.mod itself is a symlink to a file elsewhere.
		// Canonicalize directory aliases without replacing that final entry.
		canonicalModuleRoot, canonicalErr := canonicalDirectory(filepath.Dir(moduleContext.GOMOD))
		if canonicalErr != nil {
			return Config{}, fmt.Errorf("resolve main module identity: %w", canonicalErr)
		}
		moduleRoot = canonicalModuleRoot
		data, readErr := os.ReadFile(moduleContext.GOMOD)
		if readErr != nil {
			return Config{}, fmt.Errorf("read main module identity: %w", readErr)
		}
		modulePath = modfile.ModulePath(data)
		if modulePath == "" {
			return Config{}, fmt.Errorf("read main module identity: %s has no module path", moduleContext.GOMOD)
		}
	}

	buildContext := build.Default
	buildContext.GOOS = resolved.GOOS
	buildContext.GOARCH = resolved.GOARCH
	buildContext.CgoEnabled = resolved.CgoEnabled
	buildContext.UseAllFiles = resolved.UseAllFiles
	buildContext.Compiler = resolved.Compiler
	buildContext.GOROOT = resolved.GOROOT
	buildContext.GOPATH = resolved.GOPATH
	buildContext.BuildTags = append([]string(nil), resolved.BuildTags...)
	buildContext.ToolTags = append([]string(nil), resolved.ToolTags...)
	buildContext.ReleaseTags = append([]string(nil), resolved.ReleaseTags...)
	buildContext.InstallSuffix = resolved.InstallSuffix

	return fromBuildContext(buildContext, environment, moduleContext.GO111MODULE != "off", moduleRoot, modulePath), nil
}

func commandError(prefix string, stdout, stderr []byte, err error) error {
	message := strings.TrimSpace(string(stderr))
	if message == "" {
		message = strings.TrimSpace(string(stdout))
	}
	if message == "" {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	return fmt.Errorf("%s: %w: %s", prefix, err, message)
}

// FromBuildContext creates a deterministic configuration from an injected
// go/build context. It is primarily useful for host-independent tests and as
// the AST fallback when cmd/go cannot resolve the target repository.
func FromBuildContext(buildContext build.Context, environment []string) Config {
	return fromBuildContext(buildContext, environment, false, "", "")
}

// FromBuildContextWithModule creates an injected configuration with explicit
// module-mode state. It is intended for deterministic scanner tests.
func FromBuildContextWithModule(buildContext build.Context, environment []string, modulesEnabled bool, moduleRoot string) Config {
	modulePath := ""
	if data, err := os.ReadFile(filepath.Join(moduleRoot, "go.mod")); err == nil {
		modulePath = modfile.ModulePath(data)
	}
	return fromBuildContext(buildContext, environment, modulesEnabled, moduleRoot, modulePath)
}

func fromBuildContext(buildContext build.Context, environment []string, modulesEnabled bool, moduleRoot, modulePath string) Config {
	cgoEnabled := "0"
	if buildContext.CgoEnabled {
		cgoEnabled = "1"
	}
	env := append([]string(nil), environment...)
	env = append(env,
		"GOOS="+buildContext.GOOS,
		"GOARCH="+buildContext.GOARCH,
		"CGO_ENABLED="+cgoEnabled,
		"GOROOT="+buildContext.GOROOT,
		"GOPATH="+buildContext.GOPATH,
	)
	return Config{
		build:          cloneBuildContext(buildContext),
		environment:    env,
		modulesEnabled: modulesEnabled,
		moduleRoot:     cleanOptionalPath(moduleRoot),
		modulePath:     modulePath,
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

// ModulesEnabled reports whether cmd/go is operating in module-aware mode.
func (c Config) ModulesEnabled() bool {
	return c.modulesEnabled
}

// ModuleRoot returns the effective main-module root, or an empty string when
// the build target is not inside a main module.
func (c Config) ModuleRoot() string {
	return c.moduleRoot
}

// ModulePath returns the main module's declared import path, or an empty
// string when the build target is outside module mode.
func (c Config) ModulePath() string {
	return c.modulePath
}

// Fingerprint identifies the effective inputs that can change Go source-file
// selection. It deliberately excludes the full environment because that can
// contain secrets and unrelated volatile values.
func (c Config) Fingerprint() string {
	ctx := c.BuildContext()
	buildTags := sortedCopy(ctx.BuildTags)
	toolTags := sortedCopy(ctx.ToolTags)
	releaseTags := sortedCopy(ctx.ReleaseTags)
	payload, err := json.Marshal(struct {
		Version        int      `json:"version"`
		GOOS           string   `json:"goos"`
		GOARCH         string   `json:"goarch"`
		CgoEnabled     bool     `json:"cgo_enabled"`
		UseAllFiles    bool     `json:"use_all_files"`
		Compiler       string   `json:"compiler"`
		GOROOT         string   `json:"goroot"`
		GOPATH         string   `json:"gopath"`
		InstallSuffix  string   `json:"install_suffix"`
		BuildTags      []string `json:"build_tags"`
		ToolTags       []string `json:"tool_tags"`
		ReleaseTags    []string `json:"release_tags"`
		ModulesEnabled bool     `json:"modules_enabled"`
		ModuleRoot     string   `json:"module_root"`
		ModulePath     string   `json:"module_path"`
	}{
		Version:        4,
		GOOS:           ctx.GOOS,
		GOARCH:         ctx.GOARCH,
		CgoEnabled:     ctx.CgoEnabled,
		UseAllFiles:    ctx.UseAllFiles,
		Compiler:       ctx.Compiler,
		GOROOT:         ctx.GOROOT,
		GOPATH:         ctx.GOPATH,
		InstallSuffix:  ctx.InstallSuffix,
		BuildTags:      buildTags,
		ToolTags:       toolTags,
		ReleaseTags:    releaseTags,
		ModulesEnabled: c.ModulesEnabled(),
		ModuleRoot:     c.ModuleRoot(),
		ModulePath:     c.ModulePath(),
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func sortedCopy(values []string) []string {
	copyOfValues := make([]string, len(values))
	copy(copyOfValues, values)
	sort.Strings(copyOfValues)
	return copyOfValues
}

func cleanOptionalPath(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func canonicalDirectory(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", path)
	}
	return filepath.Clean(resolved), nil
}

func setEnvironmentValue(environment []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
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
	GOROOT        string   `json:"goroot"`
	GOPATH        string   `json:"gopath"`
	BuildTags     []string `json:"build_tags"`
	ToolTags      []string `json:"tool_tags"`
	ReleaseTags   []string `json:"release_tags"`
	InstallSuffix string   `json:"install_suffix"`
}

type wireModuleContext struct {
	GO111MODULE string `json:"GO111MODULE"`
	GOMOD       string `json:"GOMOD"`
}

// cmd/go exposes its fully resolved build.Context to go-list templates. The
// values interpolated here are restricted Go identifiers or booleans;
// printf %q therefore produces valid JSON strings while retaining all tags.
const goListContextTemplate = `{"goos":{{printf "%q" context.GOOS}},"goarch":{{printf "%q" context.GOARCH}},"cgo_enabled":{{context.CgoEnabled}},"use_all_files":{{context.UseAllFiles}},"compiler":{{printf "%q" context.Compiler}},"goroot":{{printf "%q" context.GOROOT}},"gopath":{{printf "%q" context.GOPATH}},"build_tags":[{{range $index, $tag := context.BuildTags}}{{if $index}},{{end}}{{printf "%q" $tag}}{{end}}],"tool_tags":[{{range $index, $tag := context.ToolTags}}{{if $index}},{{end}}{{printf "%q" $tag}}{{end}}],"release_tags":[{{range $index, $tag := context.ReleaseTags}}{{if $index}},{{end}}{{printf "%q" $tag}}{{end}}],"install_suffix":{{printf "%q" context.InstallSuffix}}}`
