// Package buildctx resolves the effective Go build configuration shared by
// source scanning and type-checked package loading.
package buildctx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/sourcefs"
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
	// Cmd/go discovers module/workspace files, their sums, and vendoring
	// metadata before evaluating even otherwise repository-independent queries.
	// Reject linked or special entries before starting it so attacker-controlled
	// link targets cannot enter diagnostics or receive toolchain writes.
	if _, err := ToolchainSourceRoots(root); err != nil {
		return Config{}, fmt.Errorf("validate Go build metadata: %w", err)
	}
	// os.Getwd accepts PWD as its lexical spelling when it names the current
	// directory. Pin both cmd.Dir and PWD to the canonical root so cmd/go does
	// not discover a different GOMOD merely because gograph was invoked from
	// inside rather than outside a symlinked scan root.
	environment := setEnvironmentValue(os.Environ(), "PWD", canonicalRoot)
	return resolve(ctx, canonicalRoot, environment, runGoCommand)
}

// ValidateToolchainMetadata verifies every local metadata path that cmd/go
// discovers before it starts resolving ordinary dependencies. Repository-
// discovered workspace members must remain beneath the workspace directory.
func ValidateToolchainMetadata(root string) error {
	_, err := ToolchainSourceRoots(root)
	return err
}

// ToolchainSourceRoots validates local metadata and returns the module root,
// or the workspace root plus member roots, whose source cmd/go may inspect as
// local packages.
// Callers that expose toolchain-derived content must preflight every returned
// tree before invoking cmd/go.
func ToolchainSourceRoots(root string) ([]string, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve Go metadata root spelling: %w", err)
	}
	canonicalRoot, err := canonicalDirectory(root)
	if err != nil {
		return nil, fmt.Errorf("resolve Go metadata root: %w", err)
	}
	return validateToolchainMetadataAndRoots(canonicalRoot, filepath.Clean(absoluteRoot))
}

func validateToolchainMetadata(root string) error {
	_, err := ToolchainSourceRoots(root)
	return err
}

func validateToolchainMetadataAndRoots(root, rootAlias string) ([]string, error) {
	modulesEnabled := os.Getenv("GO111MODULE") != "off"
	modulePending := modulesEnabled
	moduleRoot := ""
	goWork := os.Getenv("GOWORK")
	workspacePending := modulesEnabled && (goWork == "" || goWork == "auto")

	// An explicit GOWORK path is operator-selected rather than discovered from
	// the repository. Its contents still select module paths, so apply the same
	// workspace-root confinement before cmd/go opens any member metadata.
	if modulesEnabled && goWork != "" && goWork != "auto" && goWork != "off" {
		if !filepath.IsAbs(goWork) {
			return nil, fmt.Errorf("GOWORK must be an absolute path, auto, or off: %q", goWork)
		}
		workspaceAlias := filepath.Clean(filepath.Dir(goWork))
		workspaceRoot, err := canonicalDirectory(workspaceAlias)
		if err != nil {
			return nil, fmt.Errorf("resolve explicit workspace root: %w", err)
		}
		files, err := sourcefs.Open(workspaceRoot)
		if err != nil {
			return nil, fmt.Errorf("open explicit workspace root: %w", err)
		}
		primary := filepath.Base(goWork)
		found, sourceRoots, validateErr := validateWorkspaceSet(files, workspaceRoot, workspaceAlias, primary)
		_ = files.Close()
		if validateErr != nil {
			return nil, validateErr
		}
		if !found {
			return nil, fmt.Errorf("inspect %s: %w", goWork, os.ErrNotExist)
		}
		return sourceRoots, nil
	}
	if !workspacePending && !modulePending {
		return nil, nil
	}

	aliasDir := filepath.Clean(rootAlias)
	for dir := filepath.Clean(root); ; dir = filepath.Dir(dir) {
		files, err := sourcefs.Open(dir)
		if err != nil {
			return nil, fmt.Errorf("open Go metadata directory %s: %w", dir, err)
		}
		if workspacePending {
			found, sourceRoots, validateErr := validateWorkspaceSet(files, dir, aliasDir, "go.work")
			if validateErr != nil {
				_ = files.Close()
				return nil, validateErr
			}
			if found {
				_ = files.Close()
				return sourceRoots, nil
			}
		}
		if modulePending {
			found, validateErr := validateMetadataSet(files, dir, "go.mod", "go.sum", filepath.Join("vendor", "modules.txt"))
			if validateErr != nil {
				_ = files.Close()
				return nil, validateErr
			}
			modulePending = !found
			if found {
				moduleRoot = dir
			}
		}
		_ = files.Close()
		if !workspacePending && !modulePending {
			return optionalRoot(moduleRoot), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return optionalRoot(moduleRoot), nil
		}
		aliasDir = filepath.Dir(aliasDir)
	}
}

// validateWorkspaceSet reads and parses the applicable go.work through the
// rooted filesystem boundary. Cmd/go unconditionally expands every use entry,
// reads each member's go.mod and optional go.sum, and may inspect workspace
// vendor/modules.txt before handling the requested package. Those local paths
// must therefore be validated before the first cmd/go invocation.
func validateWorkspaceSet(files *sourcefs.Reader, directory, directoryAlias, primary string) (bool, []string, error) {
	data, err := files.ReadRegularFile(primary)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("inspect %s: %w", filepath.Join(directory, primary), err)
	}
	for _, companion := range []string{primary + ".sum", filepath.Join("vendor", "modules.txt")} {
		if err := files.ValidateRegularFile(companion); err != nil && !errors.Is(err, os.ErrNotExist) {
			return true, nil, fmt.Errorf("inspect %s: %w", filepath.Join(directory, companion), err)
		}
	}

	workFilePath := filepath.Join(directory, primary)
	workFile, err := modfile.ParseWork(workFilePath, data, nil)
	if err != nil {
		return true, nil, fmt.Errorf("parse %s: %w", workFilePath, err)
	}
	memberRoots := make([]string, 0, len(workFile.Use))
	for _, use := range workFile.Use {
		member, err := workspaceMemberName(directory, directoryAlias, use.Path)
		if err != nil {
			return true, nil, err
		}
		if err := files.ValidateDirectory(member); err != nil {
			return true, nil, fmt.Errorf("inspect workspace member %s: %w", filepath.Join(directory, member), err)
		}
		moduleFile := filepath.Join(member, "go.mod")
		if err := files.ValidateRegularFile(moduleFile); err != nil {
			return true, nil, fmt.Errorf("inspect workspace member metadata %s: %w", filepath.Join(directory, moduleFile), err)
		}
		sumFile := filepath.Join(member, "go.sum")
		if err := files.ValidateRegularFile(sumFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return true, nil, fmt.Errorf("inspect workspace member metadata %s: %w", filepath.Join(directory, sumFile), err)
		}
		memberRoots = append(memberRoots, filepath.Clean(filepath.Join(directory, member)))
	}
	// Workspace vendoring is rooted beside go.work, so cmd/go may inspect
	// source below directory/vendor even when analysis starts in one member.
	// Include the workspace root as well as its already-confined members.
	sourceRoots := append([]string{directory}, memberRoots...)
	return true, uniquePaths(sourceRoots), nil
}

func workspaceMemberName(directory, directoryAlias, usePath string) (string, error) {
	member := filepath.Clean(filepath.FromSlash(usePath))
	if filepath.IsAbs(member) {
		bases := []string{directory}
		if sameDirectory(directory, directoryAlias) {
			bases = append(bases, directoryAlias)
		}
		for _, base := range uniquePaths(bases) {
			relative, err := filepath.Rel(base, member)
			if err != nil {
				continue
			}
			relative = filepath.Clean(relative)
			if filepath.IsLocal(relative) {
				return relative, nil
			}
		}
		return "", fmt.Errorf("%w %q: workspace use path must stay beneath %s", sourcefs.ErrUnsafeSourcePath, usePath, directory)
	}
	if !filepath.IsLocal(member) {
		return "", fmt.Errorf("%w %q: workspace use path must stay beneath %s", sourcefs.ErrUnsafeSourcePath, usePath, directory)
	}
	return member, nil
}

func sameDirectory(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	leftInfo, err := os.Stat(left)
	if err != nil || !leftInfo.IsDir() {
		return false
	}
	rightInfo, err := os.Stat(right)
	return err == nil && rightInfo.IsDir() && os.SameFile(leftInfo, rightInfo)
}

func optionalRoot(root string) []string {
	if root == "" {
		return nil
	}
	return []string{filepath.Clean(root)}
}

func uniquePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		clean := filepath.Clean(path)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		result = append(result, clean)
	}
	return result
}

// validateMetadataSet validates the primary file and, only when that primary
// exists, each companion file that cmd/go may read or update. Missing
// companions are valid. sourcefs checks every component, so a linked vendor
// directory is rejected before vendor/modules.txt can be opened.
func validateMetadataSet(files *sourcefs.Reader, directory, primary string, companions ...string) (bool, error) {
	if err := files.ValidateRegularFile(primary); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("inspect %s: %w", filepath.Join(directory, primary), err)
	}
	for _, companion := range companions {
		if err := files.ValidateRegularFile(companion); err != nil && !errors.Is(err, os.ErrNotExist) {
			return true, fmt.Errorf("inspect %s: %w", filepath.Join(directory, companion), err)
		}
	}
	return true, nil
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
		moduleFiles, openErr := sourcefs.Open(canonicalModuleRoot)
		if openErr != nil {
			return Config{}, fmt.Errorf("open main module root: %w", openErr)
		}
		data, readErr := moduleFiles.ReadRegularFile(filepath.Base(moduleContext.GOMOD))
		_ = moduleFiles.Close()
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
	if moduleRoot != "" {
		if moduleFiles, err := sourcefs.Open(moduleRoot); err == nil {
			if data, readErr := moduleFiles.ReadRegularFile("go.mod"); readErr == nil {
				modulePath = modfile.ModulePath(data)
			}
			_ = moduleFiles.Close()
		}
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
