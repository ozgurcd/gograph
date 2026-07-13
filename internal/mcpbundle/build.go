package mcpbundle

import (
	"context"
	"crypto/subtle"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Artifact describes one on-disk MCPB release asset.
type Artifact struct {
	Target Target
	Name   string
	Path   string
	SHA256 string
	Size   int64
}

// BuildAll cross-compiles, packages, validates, and publishes to outputDir all
// six supported MCPB assets. It stages every artifact before changing the
// output directory and refuses to replace a different existing asset.
func BuildAll(ctx context.Context, repositoryRoot, outputDir, version string) ([]Artifact, error) {
	if err := ValidateVersion(version); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root: %w", err)
	}
	if info, err := os.Stat(filepath.Join(root, "go.mod")); err != nil || info.IsDir() {
		return nil, fmt.Errorf("repository root %q does not contain go.mod", root)
	}
	license, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		return nil, fmt.Errorf("read LICENSE: %w", err)
	}
	if err := validateLicense(license); err != nil {
		return nil, err
	}
	out, err := filepath.Abs(outputDir)
	if err != nil {
		return nil, fmt.Errorf("resolve output directory: %w", err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}
	stage, err := os.MkdirTemp(out, ".mcpb-stage-")
	if err != nil {
		return nil, fmt.Errorf("create MCPB staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	binDir := filepath.Join(stage, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return nil, fmt.Errorf("create binary staging directory: %w", err)
	}

	for _, target := range Targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		binaryPath := filepath.Join(binDir, target.GOOS+"_"+target.GOARCH, target.ExecutableName())
		if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
			return nil, fmt.Errorf("create %s/%s build directory: %w", target.GOOS, target.GOARCH, err)
		}
		ldflags := "-s -w -X main.version=" + version + " -X main.releaseVersionMarker=gograph-release-version=/" + version + "/"
		command := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-trimpath", "-ldflags="+ldflags, "-o", binaryPath, "./cmd/gograph")
		command.Dir = root
		command.Env = targetBuildEnvironment(target)
		output, err := command.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("build gograph for %s/%s: %w\n%s", target.GOOS, target.GOARCH, err, output)
		}
		binary, err := os.ReadFile(binaryPath)
		if err != nil {
			return nil, fmt.Errorf("read %s/%s binary: %w", target.GOOS, target.GOARCH, err)
		}
		manifest, err := NewManifest(version, target)
		if err != nil {
			return nil, err
		}
		bundle, _, err := BuildBundle(manifest, binary, license)
		if err != nil {
			return nil, fmt.Errorf("package %s/%s: %w", target.GOOS, target.GOARCH, err)
		}
		name := target.ArtifactName(version)
		if err := os.WriteFile(filepath.Join(stage, name), bundle, 0o644); err != nil {
			return nil, fmt.Errorf("stage %s: %w", name, err)
		}
	}

	staged, err := VerifyAll(stage, version)
	if err != nil {
		return nil, fmt.Errorf("verify staged MCPB assets: %w", err)
	}
	for _, artifact := range staged {
		destination := filepath.Join(out, artifact.Name)
		existing, err := os.ReadFile(destination)
		switch {
		case err == nil:
			stagedData, readErr := os.ReadFile(artifact.Path)
			if readErr != nil {
				return nil, fmt.Errorf("read staged %s: %w", artifact.Name, readErr)
			}
			if subtle.ConstantTimeCompare(existing, stagedData) != 1 {
				return nil, fmt.Errorf("refusing to replace different existing asset %s", destination)
			}
		case os.IsNotExist(err):
			// Preflight only. The files are installed after every destination passes.
		default:
			return nil, fmt.Errorf("inspect existing asset %s: %w", destination, err)
		}
	}
	for _, artifact := range staged {
		destination := filepath.Join(out, artifact.Name)
		if _, err := os.Stat(destination); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("inspect asset destination %s: %w", destination, err)
		}
		if err := os.Rename(artifact.Path, destination); err != nil {
			return nil, fmt.Errorf("install asset %s: %w", artifact.Name, err)
		}
	}
	return VerifyAll(out, version)
}

// VerifyAll requires exactly one valid canonical MCPB for every target. Other
// non-MCPB release files in the same directory are ignored.
func VerifyAll(inputDir, version string) ([]Artifact, error) {
	return VerifyAllHashes(inputDir, version, nil)
}

// VerifyAllHashes also compares every asset with a caller-provided SHA-256
// map. When expected is non-nil it must contain exactly the six asset names.
func VerifyAllHashes(inputDir, version string, expected map[string]string) ([]Artifact, error) {
	if err := ValidateVersion(version); err != nil {
		return nil, err
	}
	directory, err := filepath.Abs(inputDir)
	if err != nil {
		return nil, fmt.Errorf("resolve MCPB input directory: %w", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read MCPB input directory: %w", err)
	}
	wanted := make(map[string]Target, len(Targets))
	for _, target := range Targets {
		wanted[target.ArtifactName(version)] = target
	}
	seen := make(map[string]bool, len(wanted))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".mcpb") {
			continue
		}
		if _, ok := wanted[entry.Name()]; !ok {
			return nil, fmt.Errorf("unexpected MCPB asset %q", entry.Name())
		}
		seen[entry.Name()] = true
	}
	for name := range wanted {
		if !seen[name] {
			return nil, fmt.Errorf("missing MCPB asset %q", name)
		}
	}
	if expected != nil {
		if len(expected) != len(wanted) {
			return nil, fmt.Errorf("expected SHA-256 map has %d entries, want %d", len(expected), len(wanted))
		}
		for name := range expected {
			if _, ok := wanted[name]; !ok {
				return nil, fmt.Errorf("expected SHA-256 map contains unexpected asset %q", name)
			}
		}
	}

	artifacts := make([]Artifact, 0, len(Targets))
	for _, target := range Targets {
		name := target.ArtifactName(version)
		assetPath := filepath.Join(directory, name)
		data, err := os.ReadFile(assetPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		wantHash := ""
		if expected != nil {
			wantHash = expected[name]
		}
		verification, err := VerifyBundle(data, target, version, wantHash)
		if err != nil {
			return nil, fmt.Errorf("verify %s: %w", name, err)
		}
		artifacts = append(artifacts, Artifact{
			Target: target,
			Name:   name,
			Path:   assetPath,
			SHA256: verification.SHA256,
			Size:   verification.Size,
		})
	}
	return artifacts, nil
}

func targetBuildEnvironment(target Target) []string {
	environment := make([]string, 0, len(os.Environ())+3)
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		switch key {
		case "GOOS", "GOARCH", "CGO_ENABLED", "GOAMD64", "GOARM64", "GOEXPERIMENT", "GOFLAGS", "GOTOOLCHAIN":
			continue
		default:
			environment = append(environment, value)
		}
	}
	environment = append(environment,
		"GOOS="+target.GOOS,
		"GOARCH="+target.GOARCH,
		"CGO_ENABLED=0",
		"GOEXPERIMENT=",
		"GOFLAGS=",
		"GOTOOLCHAIN=local",
	)
	if target.GOARCH == "amd64" {
		environment = append(environment, "GOAMD64=v1")
	} else {
		environment = append(environment, "GOARM64=v8.0")
	}
	sort.Strings(environment)
	return environment
}
