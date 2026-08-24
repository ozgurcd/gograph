// Package baseline builds graph snapshots from Git refs or saved graph files.
package baseline

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

var safeGitRef = regexp.MustCompile(`^[A-Za-z0-9._/\-~^]+$`)

// BuildFunc constructs a graph from an extracted project directory.
type BuildFunc func(string) (*graph.Graph, error)

// Build loads a saved graph JSON file or extracts a Git ref into a temporary
// directory and builds a graph from the same project subtree as projectRoot.
func Build(ctx context.Context, projectRoot, ref string, buildGraph BuildFunc) (*graph.Graph, error) {
	if buildGraph == nil {
		return nil, fmt.Errorf("baseline graph builder is nil")
	}
	if strings.HasSuffix(ref, ".json") {
		return loadGraphFile(projectRoot, ref)
	}
	if ref == "" || strings.HasPrefix(ref, "-") || !safeGitRef.MatchString(ref) {
		return nil, fmt.Errorf("invalid or unsafe baseline ref: %q", ref)
	}

	projectRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}
	if resolvedRoot, resolveErr := filepath.EvalSymlinks(projectRoot); resolveErr == nil {
		projectRoot = resolvedRoot
	}
	repoRoot, projectRel, err := gitRoots(ctx, projectRoot)
	if err != nil {
		return nil, err
	}

	tmpDir, err := os.MkdirTemp("", "gograph-baseline-*")
	if err != nil {
		return nil, fmt.Errorf("create baseline temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	buildRoot, err := extractGitArchive(ctx, repoRoot, projectRel, ref, tmpDir)
	if err != nil {
		return nil, err
	}
	return buildGraph(buildRoot)
}

func loadGraphFile(projectRoot, path string) (*graph.Graph, error) {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(absRoot, candidate)
	}
	rel, err := filepath.Rel(absRoot, candidate)
	if err != nil || !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("saved baseline graph must be a regular file inside project root: %s", path)
	}
	reader, err := sourcefs.Open(absRoot)
	if err != nil {
		return nil, fmt.Errorf("open project root for baseline graph: %w", err)
	}
	defer func() { _ = reader.Close() }()
	data, err := reader.ReadRegularFileLimit(rel, graph.MaxArtifactBytes)
	if err != nil {
		return nil, fmt.Errorf("load baseline graph %s: %w", candidate, err)
	}
	var g graph.Graph
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("parse baseline graph %s: %w", candidate, err)
	}
	if !g.UsesCurrentSourcePolicy() {
		return nil, fmt.Errorf("baseline graph %s has a missing or unsupported repository source policy; rebuild or replace it", candidate)
	}
	// A serialized baseline root is metadata, not authority for later reads.
	g.Root = absRoot
	return &g, nil
}

func gitRoots(ctx context.Context, projectRoot string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", projectRoot, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("determine repository root: %w", err)
	}
	repoRoot := strings.TrimSpace(string(out))
	projectRel, err := filepath.Rel(repoRoot, projectRoot)
	if err != nil || projectRel == ".." || strings.HasPrefix(projectRel, ".."+string(os.PathSeparator)) {
		return "", "", fmt.Errorf("project root %s is outside Git repository %s", projectRoot, repoRoot)
	}
	return repoRoot, filepath.Clean(projectRel), nil
}

func extractGitArchive(parent context.Context, repoRoot, projectRel, ref, destination string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, time.Minute)
	defer cancel()

	args := []string{"-C", repoRoot, "archive", "--format=tar", ref}
	if projectRel != "." {
		args = append(args, "--", filepath.ToSlash(projectRel))
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("open git archive output: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start git archive for %q: %w", ref, err)
	}
	waited := false
	defer func() {
		if !waited {
			cancel()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait()
		}
	}()

	extractErr := extractTar(stdout, destination)
	if extractErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	waited = true
	if extractErr != nil {
		return "", fmt.Errorf("extract Git baseline %q: %w", ref, extractErr)
	}
	if waitErr != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("git archive %q: %w", ref, ctx.Err())
		}
		return "", fmt.Errorf("git archive %q: %w: %s", ref, waitErr, strings.TrimSpace(stderr.String()))
	}

	if projectRel == "." {
		return destination, nil
	}
	return filepath.Join(destination, projectRel), nil
}

func extractTar(r io.Reader, destination string) error {
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(filepath.FromSlash(header.Name))
		if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe archive path %q", header.Name)
		}
		target := filepath.Join(destination, name)
		rel, err := filepath.Rel(destination, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe archive path %q", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := header.FileInfo().Mode().Perm()
			if mode == 0 {
				mode = 0o600
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, tr)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
}
