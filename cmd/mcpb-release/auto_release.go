package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ozgurcd/gograph/internal/mcpbundle"
)

const (
	releaseConfigFile = ".bumpversion.cfg"
	releasePluginFile = "plugin.json"
	releaseServerFile = "server.json"
	releaseRepository = "ozgurcd/gograph"
)

var stableVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type autoReleaseOptions struct {
	repositoryRoot string
	remote         string
	branch         string
	dryRun         bool
}

type releaseCommandRunner interface {
	Output(context.Context, string, string, ...string) ([]byte, error)
	Run(context.Context, string, string, ...string) error
}

type execReleaseCommandRunner struct {
	stdout io.Writer
	stderr io.Writer
}

func (r execReleaseCommandRunner) Output(ctx context.Context, directory, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = directory
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, commandFailure(name, args, stderr.Bytes(), err)
	}
	return output, nil
}

func (r execReleaseCommandRunner) Run(ctx context.Context, directory, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = directory
	command.Stdout = r.stdout
	command.Stderr = r.stderr
	if err := command.Run(); err != nil {
		return commandFailure(name, args, nil, err)
	}
	return nil
}

func commandFailure(name string, args []string, stderr []byte, err error) error {
	detail := strings.TrimSpace(string(stderr))
	if detail == "" {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, detail)
}

type autoReleaseDependencies struct {
	runner         releaseCommandRunner
	stdout         io.Writer
	build          func(context.Context, string, string, string) ([]mcpbundle.Artifact, error)
	render         func(string, []mcpbundle.Artifact) ([]byte, error)
	verify         func(context.Context, string, string, string, string) error
	githubState    func(context.Context, string, string) (string, error)
	registryState  func(context.Context, string) (string, error)
	validateRemote func(context.Context, releaseCommandRunner, string, string) error
}

func defaultAutoReleaseDependencies() autoReleaseDependencies {
	runner := execReleaseCommandRunner{stdout: os.Stdout, stderr: os.Stderr}
	return autoReleaseDependencies{
		runner: runner,
		stdout: os.Stdout,
		build:  mcpbundle.BuildAll,
		render: renderServerDocument,
		verify: func(ctx context.Context, root, version, bundles, server string) error {
			return runner.Run(ctx, root, "make", "release-verify",
				"MCPB_VERSION="+version,
				"MCPB_OUTPUT="+bundles,
				"MCPB_SERVER="+server,
			)
		},
		githubState: func(ctx context.Context, serverPath, version string) (string, error) {
			doc, raw, err := readServerDocument(serverPath)
			if err != nil {
				return "", err
			}
			expected, err := validateServerPackages(doc, version)
			if err != nil {
				return "", err
			}
			return githubReleaseState(ctx, releaseRepository, "v"+version, raw, expected)
		},
		registryState: func(ctx context.Context, serverPath string) (string, error) {
			doc, raw, err := readServerDocument(serverPath)
			if err != nil {
				return "", err
			}
			if _, err := validateServerPackages(doc, doc.Version); err != nil {
				return "", err
			}
			return registryState(ctx, defaultRegistryURL, raw, doc.Name, doc.Version)
		},
		validateRemote: requireOfficialReleaseRemote,
	}
}

func runAutoRelease(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("auto-release", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	repositoryRoot := fs.String("repository-root", ".", "gograph repository root")
	remote := fs.String("remote", "origin", "Git remote to update")
	dryRun := fs.Bool("dry-run", false, "prepare and verify, then restore metadata without committing or pushing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("auto-release accepts no positional arguments")
	}
	return runAutoReleaseWithDependencies(ctx, autoReleaseOptions{
		repositoryRoot: *repositoryRoot,
		remote:         *remote,
		branch:         "main",
		dryRun:         *dryRun,
	}, defaultAutoReleaseDependencies())
}

func runAutoReleaseWithDependencies(ctx context.Context, options autoReleaseOptions, dependencies autoReleaseDependencies) (resultErr error) {
	if dependencies.runner == nil || dependencies.stdout == nil || dependencies.build == nil || dependencies.render == nil || dependencies.verify == nil || dependencies.githubState == nil || dependencies.registryState == nil || dependencies.validateRemote == nil {
		return errors.New("auto-release dependencies are incomplete")
	}
	root, err := filepath.Abs(options.repositoryRoot)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve repository root symlinks: %w", err)
	}
	if options.remote == "" || options.branch == "" {
		return errors.New("release remote and branch must not be empty")
	}

	sourceBranch, err := requireReleaseRepository(ctx, dependencies.runner, root)
	if err != nil {
		return err
	}
	if err := dependencies.validateRemote(ctx, dependencies.runner, root, options.remote); err != nil {
		return err
	}
	current, err := readAlignedReleaseVersion(root)
	if err != nil {
		return err
	}
	if err := fetchReleaseBranch(ctx, dependencies.runner, root, options.remote, options.branch); err != nil {
		return err
	}
	head, err := gitOutput(ctx, dependencies.runner, root, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	currentTag := "v" + current
	localCurrentTag, err := localTagCommit(ctx, dependencies.runner, root, currentTag)
	if err != nil {
		return err
	}
	remoteCurrentTag, err := lookupRemoteTag(ctx, dependencies.runner, root, options.remote, currentTag)
	if err != nil {
		return err
	}
	prepared, err := isPreparedReleaseCommit(ctx, dependencies.runner, root, current)
	if err != nil {
		return err
	}
	currentAtHead := localCurrentTag == head || remoteCurrentTag.commit == head || prepared
	if currentAtHead {
		if remoteCurrentTag.exists && remoteCurrentTag.commit != head {
			return fmt.Errorf("remote tag %s resolves to %s, not release commit %s", currentTag, remoteCurrentTag.commit, head)
		}
		if remoteCurrentTag.exists && remoteCurrentTag.commit == head {
			isPushed, err := gitIsAncestor(ctx, dependencies.runner, root, head, "refs/remotes/"+options.remote+"/"+options.branch)
			if err != nil {
				return fmt.Errorf("compare %s with %s/%s: %w", currentTag, options.remote, options.branch, err)
			}
			if isPushed {
				_, err := fmt.Fprintf(dependencies.stdout, "%s is already tagged and pushed from %s; no new patch release was created\n", currentTag, head)
				return err
			}
		}
		return resumeAutoRelease(ctx, root, current, head, sourceBranch, options, dependencies, localCurrentTag, remoteCurrentTag)
	}
	if err := requireRemoteAncestor(ctx, dependencies.runner, root, options.remote, options.branch); err != nil {
		return err
	}
	if !remoteCurrentTag.exists {
		return fmt.Errorf("current version %s has no remote baseline tag %s; refusing to skip an unpublished version", current, currentTag)
	}
	baselineAncestor, err := gitIsAncestor(ctx, dependencies.runner, root, remoteCurrentTag.commit, head)
	if err != nil {
		return fmt.Errorf("validate baseline tag %s: %w", currentTag, err)
	}
	if !baselineAncestor {
		return fmt.Errorf("current baseline tag %s at %s is not an ancestor of HEAD %s", currentTag, remoteCurrentTag.commit, head)
	}

	next, err := nextPatchVersion(current)
	if err != nil {
		return err
	}
	nextTag := "v" + next
	localNextTag, err := localTagCommit(ctx, dependencies.runner, root, nextTag)
	if err != nil {
		return err
	}
	if localNextTag != "" {
		return fmt.Errorf("local tag %s already exists at %s; refusing to reuse a release version", nextTag, localNextTag)
	}
	remoteNextTag, err := lookupRemoteTag(ctx, dependencies.runner, root, options.remote, nextTag)
	if err != nil {
		return err
	}
	if remoteNextTag.exists {
		return fmt.Errorf("remote tag %s already exists at %s; refusing to reuse a release version", nextTag, remoteNextTag.commit)
	}

	owned, err := prepareVersionFiles(root, current, next)
	if err != nil {
		return err
	}
	keepPreparedFiles := false
	defer func() {
		if keepPreparedFiles {
			return
		}
		if rollbackErr := restoreOwnedFiles(owned); rollbackErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("restore release metadata: %w", rollbackErr))
		}
	}()

	bundles, err := newReleaseWorkspace(root, "gograph-release-"+next+"-")
	if err != nil {
		return fmt.Errorf("create temporary release directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(bundles) }()
	serverPath := filepath.Join(root, releaseServerFile)
	rendered, err := prepareAndVerifyRelease(ctx, root, next, bundles, serverPath, dependencies, nil)
	if rendered != nil {
		owned[2].updated = rendered
	}
	if err != nil {
		return err
	}
	if err := requireOnlyOwnedReleaseChanges(ctx, dependencies.runner, root, owned, next); err != nil {
		return err
	}
	if err := requireUnpublishedRelease(ctx, dependencies, serverPath, next); err != nil {
		return err
	}
	if err := requireReleaseHead(ctx, dependencies.runner, root, sourceBranch, head); err != nil {
		keepPreparedFiles = true
		return fmt.Errorf("release repository changed during verification; prepared metadata was retained: %w", err)
	}
	if err := requireOnlyOwnedReleaseChanges(ctx, dependencies.runner, root, owned, next); err != nil {
		return err
	}
	if options.dryRun {
		_, err := fmt.Fprintf(dependencies.stdout, "verified patch release %s (dry run); metadata restored and nothing was committed, tagged, or pushed\n", nextTag)
		return err
	}

	message := releaseCommitMessage(current, next)
	paths := releaseOwnedPaths()
	commitArgs := []string{"commit", "-m", message, "--only", "--"}
	commitArgs = append(commitArgs, paths...)
	if err := dependencies.runner.Run(ctx, root, "git", commitArgs...); err != nil {
		after, headErr := gitOutput(ctx, dependencies.runner, root, "rev-parse", "HEAD")
		if headErr == nil && after != head {
			keepPreparedFiles = true
			return fmt.Errorf("create release commit: %w (HEAD changed to %s; prepared files were retained)", err, after)
		}
		return fmt.Errorf("create release commit: %w", err)
	}
	keepPreparedFiles = true
	commit, err := gitOutput(ctx, dependencies.runner, root, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("read release commit: %w", err)
	}
	if err := verifyCreatedReleaseCommit(ctx, dependencies.runner, root, sourceBranch, commit, head, message, owned); err != nil {
		return fmt.Errorf("validate release commit %s: %w; nothing was tagged or pushed", commit, err)
	}
	if err := dependencies.runner.Run(ctx, root, "git", "tag", "-a", nextTag, "-m", message, commit); err != nil {
		return fmt.Errorf("create annotated tag %s: %w; the release commit was retained, so rerun make release to resume", nextTag, err)
	}
	if err := pushRelease(ctx, dependencies.runner, root, options.remote, options.branch, commit, nextTag, true); err != nil {
		return fmt.Errorf("atomically push %s and %s: %w; the local release commit and tag were retained—rerun make release if %s/%s still permits a fast-forward; a divergent remote requires manual reconciliation", options.branch, nextTag, err, options.remote, options.branch)
	}
	_, err = fmt.Fprintf(dependencies.stdout, "released %s from %s; the tag-triggered workflow will publish GitHub, Homebrew, and Registry artifacts\n", nextTag, commit)
	return err
}

func resumeAutoRelease(ctx context.Context, root, version, head, sourceBranch string, options autoReleaseOptions, dependencies autoReleaseDependencies, localTag string, remoteTag remoteTagState) error {
	tag := "v" + version
	if localTag != "" && localTag != head {
		return fmt.Errorf("local tag %s resolves to %s, not release commit %s", tag, localTag, head)
	}
	remoteRef := "refs/remotes/" + options.remote + "/" + options.branch
	remoteCommit, err := gitOutput(ctx, dependencies.runner, root, "rev-parse", remoteRef)
	if err != nil {
		return fmt.Errorf("resolve %s/%s: %w", options.remote, options.branch, err)
	}
	remoteIsAncestor, err := gitIsAncestor(ctx, dependencies.runner, root, remoteCommit, head)
	if err != nil {
		return fmt.Errorf("compare %s/%s with release commit: %w", options.remote, options.branch, err)
	}
	remoteContainsRelease, err := gitIsAncestor(ctx, dependencies.runner, root, head, remoteCommit)
	if err != nil {
		return fmt.Errorf("compare release commit with %s/%s: %w", options.remote, options.branch, err)
	}
	if !remoteIsAncestor && !remoteContainsRelease {
		return fmt.Errorf("release commit %s is behind or diverged from %s/%s at %s; local release state requires manual reconciliation", head, options.remote, options.branch, remoteCommit)
	}
	mainCommit := head
	if remoteContainsRelease && !remoteIsAncestor {
		// The release commit reached main without its tag (for example through a
		// protected-branch merge). Re-push the captured remote tip unchanged as
		// an atomic compare-and-swap alongside the missing tag.
		mainCommit = remoteCommit
	}
	bundles, err := newReleaseWorkspace(root, "gograph-release-resume-"+version+"-")
	if err != nil {
		return fmt.Errorf("create temporary release directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(bundles) }()
	serverPath := filepath.Join(root, releaseServerFile)
	originalServer, err := os.ReadFile(serverPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", releaseServerFile, err)
	}
	if _, err := prepareAndVerifyRelease(ctx, root, version, bundles, serverPath, dependencies, originalServer); err != nil {
		return err
	}
	if err := requireUnpublishedRelease(ctx, dependencies, serverPath, version); err != nil {
		return err
	}
	if err := requireReleaseHead(ctx, dependencies.runner, root, sourceBranch, head); err != nil {
		return fmt.Errorf("release repository changed during resume: %w", err)
	}
	if err := requireCleanReleaseWorktree(ctx, dependencies.runner, root); err != nil {
		return fmt.Errorf("release worktree changed during resume: %w", err)
	}
	if options.dryRun {
		_, err := fmt.Fprintf(dependencies.stdout, "verified resumable release %s (dry run); nothing was tagged or pushed\n", tag)
		return err
	}
	pushTag := false
	if !remoteTag.exists {
		if localTag == "" {
			old, err := previousVersionFromReleaseCommit(ctx, dependencies.runner, root, version)
			if err != nil {
				return err
			}
			if err := dependencies.runner.Run(ctx, root, "git", "tag", "-a", tag, "-m", releaseCommitMessage(old, version), head); err != nil {
				return fmt.Errorf("create annotated tag %s: %w", tag, err)
			}
		}
		pushTag = true
	}
	if err := pushRelease(ctx, dependencies.runner, root, options.remote, options.branch, mainCommit, tag, pushTag); err != nil {
		return fmt.Errorf("resume atomic push for %s: %w; local release state was retained—retry if %s/%s still permits the verified update; a divergent remote requires manual reconciliation", tag, err, options.remote, options.branch)
	}
	_, err = fmt.Fprintf(dependencies.stdout, "released %s from %s; the tag-triggered workflow will publish GitHub, Homebrew, and Registry artifacts\n", tag, head)
	return err
}

func newReleaseWorkspace(root, prefix string) (string, error) {
	base := filepath.Join(root, ".release-work")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", fmt.Errorf("create release workspace root: %w", err)
	}
	workspace, err := os.MkdirTemp(base, prefix)
	if err != nil {
		return "", fmt.Errorf("create release workspace: %w", err)
	}
	return workspace, nil
}

func prepareAndVerifyRelease(ctx context.Context, root, version, bundles, serverPath string, dependencies autoReleaseDependencies, expectedServer []byte) ([]byte, error) {
	_, _ = fmt.Fprintf(dependencies.stdout, "Preparing and verifying patch release v%s...\n", version)
	artifacts, err := dependencies.build(ctx, root, bundles, version)
	if err != nil {
		return nil, fmt.Errorf("build MCP bundles for %s: %w", version, err)
	}
	rendered, err := dependencies.render(version, artifacts)
	if err != nil {
		return nil, fmt.Errorf("render server.json for %s: %w", version, err)
	}
	if expectedServer != nil {
		if !bytes.Equal(expectedServer, rendered) {
			return nil, fmt.Errorf("committed server.json does not match deterministic bundles for %s", version)
		}
	} else if err := atomicWriteFile(serverPath, rendered, 0o644); err != nil {
		return rendered, fmt.Errorf("write server.json for %s: %w", version, err)
	}
	if err := dependencies.verify(ctx, root, version, bundles, serverPath); err != nil {
		return rendered, fmt.Errorf("release verification for %s: %w", version, err)
	}
	return rendered, nil
}

func requireUnpublishedRelease(ctx context.Context, dependencies autoReleaseDependencies, serverPath, version string) error {
	github, err := dependencies.githubState(ctx, serverPath, version)
	if err != nil {
		return fmt.Errorf("inspect GitHub release v%s: %w", version, err)
	}
	if github != "missing" {
		return fmt.Errorf("GitHub release v%s is %s; refusing to reuse immutable release state", version, github)
	}
	registry, err := dependencies.registryState(ctx, serverPath)
	if err != nil {
		return fmt.Errorf("inspect Registry version %s: %w", version, err)
	}
	if registry != "missing" {
		return fmt.Errorf("registry version %s is %s; refusing to reuse immutable Registry state", version, registry)
	}
	return nil
}

func requireReleaseRepository(ctx context.Context, runner releaseCommandRunner, root string) (string, error) {
	top, err := gitOutput(ctx, runner, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("inspect Git repository: %w", err)
	}
	resolvedTop, err := filepath.EvalSymlinks(top)
	if err != nil {
		return "", fmt.Errorf("resolve Git repository root: %w", err)
	}
	if filepath.Clean(resolvedTop) != filepath.Clean(root) {
		return "", fmt.Errorf("--repository-root %s is not the Git repository root %s", root, resolvedTop)
	}
	currentBranch, err := currentReleaseBranch(ctx, runner, root)
	if err != nil {
		return "", err
	}
	status, err := gitOutput(ctx, runner, root, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return "", fmt.Errorf("inspect worktree: %w", err)
	}
	if status != "" {
		return "", fmt.Errorf("release requires a completely clean worktree; commit or remove these changes first:\n%s", status)
	}
	return currentBranch, nil
}

func currentReleaseBranch(ctx context.Context, runner releaseCommandRunner, root string) (string, error) {
	currentBranch, err := gitOutput(ctx, runner, root, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("release requires an attached branch: %w", err)
	}
	if currentBranch == "" {
		return "", errors.New("release requires an attached branch")
	}
	return currentBranch, nil
}

func fetchReleaseBranch(ctx context.Context, runner releaseCommandRunner, root, remote, branch string) error {
	refspec := "refs/heads/" + branch + ":refs/remotes/" + remote + "/" + branch
	if err := runner.Run(ctx, root, "git", "fetch", "--no-tags", remote, refspec); err != nil {
		return fmt.Errorf("fetch %s/%s: %w", remote, branch, err)
	}
	return nil
}

func requireRemoteAncestor(ctx context.Context, runner releaseCommandRunner, root, remote, branch string) error {
	remoteRef := "refs/remotes/" + remote + "/" + branch
	isAncestor, err := gitIsAncestor(ctx, runner, root, remoteRef, "HEAD")
	if err != nil {
		return fmt.Errorf("compare release HEAD with %s/%s: %w", remote, branch, err)
	}
	if !isAncestor {
		return fmt.Errorf("HEAD is behind or diverged from %s/%s; update the working branch before releasing", remote, branch)
	}
	return nil
}

func gitIsAncestor(ctx context.Context, runner releaseCommandRunner, root, ancestor, descendant string) (bool, error) {
	_, err := runner.Output(ctx, root, "git", "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	if commandExitedWithCode(err, 1) {
		return false, nil
	}
	return false, err
}

func pushRelease(ctx context.Context, runner releaseCommandRunner, root, remote, branch, commit, tag string, pushTag bool) error {
	args := []string{"push", "--atomic", remote, commit + ":refs/heads/" + branch}
	if pushTag {
		args = append(args, "refs/tags/"+tag+":refs/tags/"+tag)
	}
	return runner.Run(ctx, root, "git", args...)
}

func requireReleaseHead(ctx context.Context, runner releaseCommandRunner, root, branch, expected string) error {
	currentBranch, err := gitOutput(ctx, runner, root, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return err
	}
	if currentBranch != branch {
		return fmt.Errorf("current branch is %s, want %s", currentBranch, branch)
	}
	head, err := gitOutput(ctx, runner, root, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != expected {
		return fmt.Errorf("HEAD is %s, want %s", head, expected)
	}
	return nil
}

func requireCleanReleaseWorktree(ctx context.Context, runner releaseCommandRunner, root string) error {
	status, err := gitOutput(ctx, runner, root, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("worktree is no longer clean:\n%s", status)
	}
	return nil
}

func requireOfficialReleaseRemote(ctx context.Context, runner releaseCommandRunner, root, remote string) error {
	fetchURLs, err := gitRemoteURLs(ctx, runner, root, remote, false)
	if err != nil {
		return fmt.Errorf("inspect fetch URL for remote %s: %w", remote, err)
	}
	pushURLs, err := gitRemoteURLs(ctx, runner, root, remote, true)
	if err != nil {
		return fmt.Errorf("inspect push URLs for remote %s: %w", remote, err)
	}
	if len(fetchURLs) != 1 || len(pushURLs) != 1 {
		return fmt.Errorf("remote %s must have exactly one fetch URL and one effective push URL for %s; got fetch=%v push=%v", remote, releaseRepository, fetchURLs, pushURLs)
	}
	for _, candidate := range []struct {
		kind string
		url  string
	}{{kind: "fetch", url: fetchURLs[0]}, {kind: "push", url: pushURLs[0]}} {
		repository, ok := githubRepositoryFromRemoteURL(candidate.url)
		if !ok || repository != releaseRepository {
			return fmt.Errorf("remote %s %s URL %q is not the official %s repository; configure one non-fan-out official remote and pass --remote if needed", remote, candidate.kind, candidate.url, releaseRepository)
		}
	}
	return nil
}

func gitRemoteURLs(ctx context.Context, runner releaseCommandRunner, root, remote string, push bool) ([]string, error) {
	args := []string{"remote", "get-url"}
	if push {
		args = append(args, "--push")
	}
	args = append(args, "--all", remote)
	output, err := gitOutput(ctx, runner, root, args...)
	if err != nil {
		return nil, err
	}
	urls := nonemptyLines(output)
	if len(urls) == 0 {
		return nil, fmt.Errorf("remote %s has no configured URL", remote)
	}
	return urls, nil
}

func githubRepositoryFromRemoteURL(remoteURL string) (string, bool) {
	value := strings.TrimSpace(remoteURL)
	if strings.HasPrefix(value, "git@github.com:") {
		return normalizeGitHubRepository(strings.TrimPrefix(value, "git@github.com:"))
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "ssh") || !strings.EqualFold(parsed.Hostname(), "github.com") {
		return "", false
	}
	return normalizeGitHubRepository(parsed.Path)
}

func normalizeGitHubRepository(path string) (string, bool) {
	repository := strings.Trim(strings.TrimSpace(path), "/")
	repository = strings.TrimSuffix(repository, ".git")
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}

func verifyCreatedReleaseCommit(ctx context.Context, runner releaseCommandRunner, root, branch, commit, parent, message string, owned []ownedReleaseFile) error {
	if err := requireReleaseHead(ctx, runner, root, branch, commit); err != nil {
		return err
	}
	parents, err := gitOutput(ctx, runner, root, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return err
	}
	parentFields := strings.Fields(parents)
	if len(parentFields) != 2 || parentFields[0] != commit || parentFields[1] != parent {
		return fmt.Errorf("commit parents are %v, want [%s %s]", parentFields, commit, parent)
	}
	subject, err := gitOutput(ctx, runner, root, "log", "-1", "--format=%s", commit)
	if err != nil {
		return err
	}
	if subject != message {
		return fmt.Errorf("commit subject is %q, want %q", subject, message)
	}
	changed, err := gitOutput(ctx, runner, root, "diff-tree", "--no-commit-id", "--name-only", "-r", commit)
	if err != nil {
		return err
	}
	actual := nonemptyLines(changed)
	expected := releaseOwnedPaths()
	sort.Strings(actual)
	sort.Strings(expected)
	if !equalStrings(actual, expected) {
		return fmt.Errorf("commit changes %v, want exactly %v", actual, expected)
	}
	for _, file := range owned {
		relative, err := filepath.Rel(root, file.path)
		if err != nil {
			return err
		}
		contents, err := runner.Output(ctx, root, "git", "show", commit+":"+filepath.ToSlash(relative))
		if err != nil {
			return fmt.Errorf("read committed %s: %w", relative, err)
		}
		if !bytes.Equal(contents, file.updated) {
			return fmt.Errorf("committed %s differs from verified metadata", relative)
		}
		treeEntry, err := gitOutput(ctx, runner, root, "ls-tree", commit, "--", filepath.ToSlash(relative))
		if err != nil {
			return fmt.Errorf("read committed mode for %s: %w", relative, err)
		}
		fields := strings.Fields(treeEntry)
		if len(fields) < 4 || fields[1] != "blob" {
			return fmt.Errorf("unexpected tree entry for %s: %q", relative, treeEntry)
		}
		expectedMode := "100644"
		if file.mode&0o111 != 0 {
			expectedMode = "100755"
		}
		if fields[0] != expectedMode {
			return fmt.Errorf("committed %s mode is %s, want %s", relative, fields[0], expectedMode)
		}
	}
	return requireCleanReleaseWorktree(ctx, runner, root)
}

type remoteTagState struct {
	exists bool
	commit string
}

func lookupRemoteTag(ctx context.Context, runner releaseCommandRunner, root, remote, tag string) (remoteTagState, error) {
	ref := "refs/tags/" + tag
	output, err := runner.Output(ctx, root, "git", "ls-remote", "--tags", remote, ref, ref+"^{}")
	if err != nil {
		return remoteTagState{}, fmt.Errorf("inspect remote tag %s: %w", tag, err)
	}
	state := remoteTagState{}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return remoteTagState{}, fmt.Errorf("unexpected ls-remote output for %s: %q", tag, line)
		}
		switch fields[1] {
		case ref:
			state.exists = true
			if state.commit == "" {
				state.commit = fields[0]
			}
		case ref + "^{}":
			state.exists = true
			state.commit = fields[0]
		}
	}
	return state, nil
}

func localTagCommit(ctx context.Context, runner releaseCommandRunner, root, tag string) (string, error) {
	ref := "refs/tags/" + tag
	if _, err := runner.Output(ctx, root, "git", "show-ref", "--verify", "--quiet", ref); err != nil {
		if commandExitedWithCode(err, 1) {
			return "", nil
		}
		return "", fmt.Errorf("inspect local tag %s: %w", tag, err)
	}
	commit, err := gitOutput(ctx, runner, root, "rev-parse", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve local tag %s: %w", tag, err)
	}
	return commit, nil
}

func commandExitedWithCode(err error, code int) bool {
	var exitError *exec.ExitError
	return errors.As(err, &exitError) && exitError.ExitCode() == code
}

func gitOutput(ctx context.Context, runner releaseCommandRunner, root string, args ...string) (string, error) {
	output, err := runner.Output(ctx, root, "git", args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

type ownedReleaseFile struct {
	path     string
	original []byte
	updated  []byte
	mode     os.FileMode
}

func prepareVersionFiles(root, current, next string) ([]ownedReleaseFile, error) {
	paths := releaseOwnedPaths()
	owned := make([]ownedReleaseFile, 0, len(paths))
	for _, relative := range paths {
		fullPath := filepath.Join(root, relative)
		contents, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", relative, err)
		}
		info, err := os.Stat(fullPath)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", relative, err)
		}
		owned = append(owned, ownedReleaseFile{path: fullPath, original: contents, mode: info.Mode().Perm()})
	}
	config, err := replaceExactly(owned[0].original, "current_version = "+current, "current_version = "+next, releaseConfigFile)
	if err != nil {
		return nil, err
	}
	plugin, err := replaceExactly(owned[1].original, `"version": "`+current+`"`, `"version": "`+next+`"`, releasePluginFile)
	if err != nil {
		return nil, err
	}
	owned[0].updated = config
	owned[1].updated = plugin
	owned[2].updated = nil // server.json is rendered from the new bundle hashes.
	if err := atomicWriteFile(owned[0].path, config, owned[0].mode); err != nil {
		return nil, fmt.Errorf("update %s: %w", releaseConfigFile, err)
	}
	if err := atomicWriteFile(owned[1].path, plugin, owned[1].mode); err != nil {
		rollbackErr := restoreOwnedFiles(owned[:2])
		return nil, errors.Join(fmt.Errorf("update %s: %w", releasePluginFile, err), rollbackErr)
	}
	return owned, nil
}

func replaceExactly(contents []byte, old, next, filename string) ([]byte, error) {
	if count := bytes.Count(contents, []byte(old)); count != 1 {
		return nil, fmt.Errorf("%s contains %d exact %q version markers, want 1", filename, count, old)
	}
	return bytes.Replace(contents, []byte(old), []byte(next), 1), nil
}

func restoreOwnedFiles(files []ownedReleaseFile) error {
	var errs []error
	for _, file := range files {
		current, err := os.ReadFile(file.path)
		if err != nil {
			errs = append(errs, fmt.Errorf("inspect %s before restore: %w", file.path, err))
			continue
		}
		if bytes.Equal(current, file.original) {
			continue
		}
		if file.updated == nil || !bytes.Equal(current, file.updated) {
			errs = append(errs, fmt.Errorf("rollback conflict in %s: file changed outside the release coordinator and was left untouched", file.path))
			continue
		}
		if err := atomicWriteFile(file.path, file.original, file.mode); err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", file.path, err))
		}
	}
	return errors.Join(errs...)
}

func requireOnlyOwnedReleaseChanges(ctx context.Context, runner releaseCommandRunner, root string, owned []ownedReleaseFile, expectedVersion string) error {
	current, err := readAlignedReleaseVersion(root)
	if err != nil {
		return err
	}
	if current != expectedVersion {
		return fmt.Errorf("prepared release version is %s, want %s", current, expectedVersion)
	}
	for index := range owned {
		contents, err := os.ReadFile(owned[index].path)
		if err != nil {
			return fmt.Errorf("read prepared release file: %w", err)
		}
		if !bytes.Equal(contents, owned[index].updated) {
			return fmt.Errorf("%s changed unexpectedly during release verification", filepath.Base(owned[index].path))
		}
		info, err := os.Stat(owned[index].path)
		if err != nil {
			return fmt.Errorf("stat prepared release file: %w", err)
		}
		if info.Mode().Perm() != owned[index].mode {
			return fmt.Errorf("%s mode changed unexpectedly during release verification: got %04o, want %04o", filepath.Base(owned[index].path), info.Mode().Perm(), owned[index].mode)
		}
	}
	cached, err := gitOutput(ctx, runner, root, "diff", "--cached", "--name-only", "--diff-filter=ACDMRTUXB")
	if err != nil {
		return fmt.Errorf("inspect staged changes: %w", err)
	}
	if cached != "" {
		return fmt.Errorf("release verification staged unexpected changes:\n%s", cached)
	}
	untracked, err := gitOutput(ctx, runner, root, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return fmt.Errorf("inspect untracked files: %w", err)
	}
	if untracked != "" {
		return fmt.Errorf("release verification created unexpected untracked files:\n%s", untracked)
	}
	changed, err := gitOutput(ctx, runner, root, "diff", "--name-only", "--diff-filter=ACDMRTUXB")
	if err != nil {
		return fmt.Errorf("inspect release changes: %w", err)
	}
	actual := nonemptyLines(changed)
	expected := releaseOwnedPaths()
	sort.Strings(actual)
	sort.Strings(expected)
	if !equalStrings(actual, expected) {
		return fmt.Errorf("release changed %v, want exactly %v", actual, expected)
	}
	return nil
}

func releaseOwnedPaths() []string {
	return []string{releaseConfigFile, releasePluginFile, releaseServerFile}
}

func nonemptyLines(value string) []string {
	var result []string
	for _, line := range strings.Split(value, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func readAlignedReleaseVersion(root string) (string, error) {
	config, err := os.ReadFile(filepath.Join(root, releaseConfigFile))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", releaseConfigFile, err)
	}
	configVersion, err := configCurrentVersion(config)
	if err != nil {
		return "", err
	}
	plugin, err := os.ReadFile(filepath.Join(root, releasePluginFile))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", releasePluginFile, err)
	}
	server, err := os.ReadFile(filepath.Join(root, releaseServerFile))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", releaseServerFile, err)
	}
	var pluginIdentity, serverIdentity struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(plugin, &pluginIdentity); err != nil {
		return "", fmt.Errorf("decode %s: %w", releasePluginFile, err)
	}
	if err := json.Unmarshal(server, &serverIdentity); err != nil {
		return "", fmt.Errorf("decode %s: %w", releaseServerFile, err)
	}
	if pluginIdentity.Version != configVersion || serverIdentity.Version != configVersion {
		return "", fmt.Errorf("release versions are not aligned: %s=%s, %s=%s, %s=%s", releaseConfigFile, configVersion, releasePluginFile, pluginIdentity.Version, releaseServerFile, serverIdentity.Version)
	}
	if _, err := nextPatchVersion(configVersion); err != nil {
		return "", fmt.Errorf("invalid current release version: %w", err)
	}
	return configVersion, nil
}

func configCurrentVersion(contents []byte) (string, error) {
	var version string
	for _, line := range strings.Split(string(contents), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "current_version") {
			continue
		}
		key, value, found := strings.Cut(trimmed, "=")
		if !found || strings.TrimSpace(key) != "current_version" || strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("invalid current_version in %s", releaseConfigFile)
		}
		if version != "" {
			return "", fmt.Errorf("multiple current_version values in %s", releaseConfigFile)
		}
		version = strings.TrimSpace(value)
	}
	if version == "" {
		return "", fmt.Errorf("%s has no current_version", releaseConfigFile)
	}
	return version, nil
}

func nextPatchVersion(current string) (string, error) {
	parts := stableVersionPattern.FindStringSubmatch(current)
	if parts == nil {
		return "", fmt.Errorf("version %q is not a stable semantic version X.Y.Z", current)
	}
	patch, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil || patch == math.MaxUint64 {
		return "", fmt.Errorf("patch component in %q cannot be incremented", current)
	}
	return parts[1] + "." + parts[2] + "." + strconv.FormatUint(patch+1, 10), nil
}

func releaseCommitMessage(old, next string) string {
	return "Bump version: " + old + " → " + next
}

func isPreparedReleaseCommit(ctx context.Context, runner releaseCommandRunner, root, current string) (bool, error) {
	subject, err := gitOutput(ctx, runner, root, "log", "-1", "--format=%s")
	if err != nil {
		return false, err
	}
	if !strings.HasPrefix(subject, "Bump version: ") {
		return false, nil
	}
	old, err := previousVersionFromReleaseCommit(ctx, runner, root, current)
	if err != nil {
		return false, fmt.Errorf("validate release-looking commit: %w", err)
	}
	if subject != releaseCommitMessage(old, current) {
		return false, fmt.Errorf("release-looking commit subject %q does not match %q", subject, releaseCommitMessage(old, current))
	}
	changed, err := gitOutput(ctx, runner, root, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD")
	if err != nil {
		return false, err
	}
	actual := nonemptyLines(changed)
	expected := releaseOwnedPaths()
	sort.Strings(actual)
	sort.Strings(expected)
	if !equalStrings(actual, expected) {
		return false, fmt.Errorf("release-looking commit changes %v, want exactly %v", actual, expected)
	}
	return true, nil
}

func previousVersionFromReleaseCommit(ctx context.Context, runner releaseCommandRunner, root, current string) (string, error) {
	previousConfig, err := runner.Output(ctx, root, "git", "show", "HEAD^:"+releaseConfigFile)
	if err != nil {
		return "", fmt.Errorf("read previous release version: %w", err)
	}
	old, err := configCurrentVersion(previousConfig)
	if err != nil {
		return "", err
	}
	next, err := nextPatchVersion(old)
	if err != nil {
		return "", err
	}
	if next != current {
		return "", fmt.Errorf("release commit advances %s to %s, not current version %s", old, next, current)
	}
	return old, nil
}
