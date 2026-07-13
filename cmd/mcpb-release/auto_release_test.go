package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/mcpbundle"
)

func TestNextPatchVersion(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		current string
		want    string
		valid   bool
	}{
		"patch":        {current: "1.5.0", want: "1.5.1", valid: true},
		"carry digits": {current: "2.19.99", want: "2.19.100", valid: true},
		"zero":         {current: "0.0.0", want: "0.0.1", valid: true},
		"v prefix":     {current: "v1.5.0"},
		"prerelease":   {current: "1.5.0-rc.1"},
		"short":        {current: "1.5"},
		"leading zero": {current: "1.5.01"},
		"overflow":     {current: "1.5.18446744073709551615"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := nextPatchVersion(test.current)
			if test.valid {
				if err != nil || got != test.want {
					t.Fatalf("nextPatchVersion(%q) = %q, %v; want %q", test.current, got, err, test.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("nextPatchVersion(%q) = %q, want error", test.current, got)
			}
		})
	}
}

func TestGitHubRepositoryFromRemoteURL(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		remote string
		want   string
		valid  bool
	}{
		"scp SSH":       {remote: "git@github.com:ozgurcd/gograph.git", want: releaseRepository, valid: true},
		"SSH URL":       {remote: "ssh://git@github.com/ozgurcd/gograph.git", want: releaseRepository, valid: true},
		"HTTPS":         {remote: "https://github.com/ozgurcd/gograph", want: releaseRepository, valid: true},
		"fork":          {remote: "https://github.com/example/gograph.git", want: "example/gograph", valid: true},
		"wrong host":    {remote: "https://gitlab.com/ozgurcd/gograph.git"},
		"local path":    {remote: "/tmp/gograph.git"},
		"nested GitHub": {remote: "https://github.com/org/team/gograph.git"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, valid := githubRepositoryFromRemoteURL(test.remote)
			if got != test.want || valid != test.valid {
				t.Fatalf("githubRepositoryFromRemoteURL(%q) = %q, %v; want %q, %v", test.remote, got, valid, test.want, test.valid)
			}
		})
	}
}

func TestAutoReleaseCreatesPatchCommitAnnotatedTagAndAtomicRemoteRefs(t *testing.T) {
	repository := newAutoReleaseRepository(t)
	dependencies, calls := fakeAutoReleaseDependencies(t, repository.root)
	var output bytes.Buffer
	dependencies.stdout = &output

	if err := runAutoReleaseWithDependencies(context.Background(), autoReleaseOptions{
		repositoryRoot: repository.root,
		remote:         "origin",
		branch:         "main",
	}, dependencies); err != nil {
		t.Fatalf("runAutoReleaseWithDependencies() error = %v", err)
	}

	if got := gitTestOutput(t, repository.root, "log", "-1", "--format=%s"); got != releaseCommitMessage("1.5.0", "1.5.1") {
		t.Fatalf("release commit subject = %q", got)
	}
	releaseCommit := gitTestOutput(t, repository.root, "rev-parse", "HEAD")
	if releaseCommit == repository.startHead {
		t.Fatal("release did not create a commit")
	}
	if got := gitTestOutput(t, repository.root, "cat-file", "-t", "refs/tags/v1.5.1"); got != "tag" {
		t.Fatalf("v1.5.1 object type = %q, want annotated tag", got)
	}
	if got := gitTestOutput(t, repository.root, "rev-parse", "refs/tags/v1.5.1^{commit}"); got != releaseCommit {
		t.Fatalf("local tag commit = %s, want %s", got, releaseCommit)
	}
	if got := gitTestOutput(t, repository.bare, "rev-parse", "refs/heads/main"); got != releaseCommit {
		t.Fatalf("remote main = %s, want %s", got, releaseCommit)
	}
	if got := gitTestOutput(t, repository.bare, "rev-parse", "refs/tags/v1.5.1^{commit}"); got != releaseCommit {
		t.Fatalf("remote tag = %s, want %s", got, releaseCommit)
	}
	if got := gitTestOutput(t, repository.root, "status", "--porcelain=v1", "--untracked-files=all"); got != "" {
		t.Fatalf("release left dirty worktree:\n%s", got)
	}
	assertReleaseVersions(t, repository.root, "1.5.1")
	if want := []string{"build", "render", "verify", "github", "registry"}; !reflect.DeepEqual(*calls, want) {
		t.Fatalf("release calls = %v, want %v", *calls, want)
	}
	if !strings.Contains(output.String(), "released v1.5.1") {
		t.Fatalf("release output = %q", output.String())
	}

	// Re-running at the exact pushed release commit must not bump to 1.5.2 or
	// rebuild anything while the tag-triggered workflow is publishing.
	noOpDependencies, noOpCalls := fakeAutoReleaseDependencies(t, repository.root)
	if err := runAutoReleaseWithDependencies(context.Background(), autoReleaseOptions{
		repositoryRoot: repository.root,
		remote:         "origin",
		branch:         "main",
	}, noOpDependencies); err != nil {
		t.Fatalf("second run error = %v", err)
	}
	if len(*noOpCalls) != 0 {
		t.Fatalf("second run performed release work: %v", *noOpCalls)
	}
	if got := gitTestOutput(t, repository.root, "rev-parse", "HEAD"); got != releaseCommit {
		t.Fatalf("second run moved HEAD to %s", got)
	}
	assertReleaseVersions(t, repository.root, "1.5.1")

	// The same tagged checkout also remains a no-op after remote main advances.
	other := filepath.Join(t.TempDir(), "other")
	gitTestCommand(t, "", "clone", repository.bare, other)
	gitTestCommand(t, other, "config", "user.email", "other@example.com")
	gitTestCommand(t, other, "config", "user.name", "Other Writer")
	gitTestCommand(t, other, "config", "commit.gpgSign", "false")
	writeTestFile(t, other, "after-release.txt", "later change\n")
	gitTestCommand(t, other, "add", "after-release.txt")
	gitTestCommand(t, other, "commit", "-m", "docs: advance main")
	gitTestCommand(t, other, "push", "origin", "main")
	advancedDependencies, advancedCalls := fakeAutoReleaseDependencies(t, repository.root)
	if err := runAutoReleaseWithDependencies(context.Background(), autoReleaseOptions{
		repositoryRoot: repository.root,
		remote:         "origin",
		branch:         "main",
	}, advancedDependencies); err != nil {
		t.Fatalf("advanced-main no-op error = %v", err)
	}
	if len(*advancedCalls) != 0 {
		t.Fatalf("advanced-main no-op performed release work: %v", *advancedCalls)
	}
}

func TestAutoReleasePushFailureResumesSameVersion(t *testing.T) {
	repository := newAutoReleaseRepository(t)
	dependencies, _ := fakeAutoReleaseDependencies(t, repository.root)
	failing := &failOncePushRunner{delegate: dependencies.runner}
	dependencies.runner = failing

	err := runAutoReleaseWithDependencies(context.Background(), autoReleaseOptions{
		repositoryRoot: repository.root,
		remote:         "origin",
		branch:         "main",
	}, dependencies)
	if err == nil || !strings.Contains(err.Error(), "retained") {
		t.Fatalf("first run error = %v, want retained-state push error", err)
	}
	localRelease := gitTestOutput(t, repository.root, "rev-parse", "HEAD")
	if localRelease == repository.startHead {
		t.Fatal("failed push did not retain the local release commit")
	}
	if got := gitTestOutput(t, repository.root, "rev-parse", "refs/tags/v1.5.1^{commit}"); got != localRelease {
		t.Fatalf("retained tag = %s, want %s", got, localRelease)
	}
	if got := gitTestOutput(t, repository.bare, "rev-parse", "refs/heads/main"); got == localRelease {
		t.Fatal("simulated failed atomic push unexpectedly updated remote main")
	}

	resumeDependencies, resumeCalls := fakeAutoReleaseDependencies(t, repository.root)
	if err := runAutoReleaseWithDependencies(context.Background(), autoReleaseOptions{
		repositoryRoot: repository.root,
		remote:         "origin",
		branch:         "main",
	}, resumeDependencies); err != nil {
		t.Fatalf("resume error = %v", err)
	}
	if want := []string{"build", "render", "verify", "github", "registry"}; !reflect.DeepEqual(*resumeCalls, want) {
		t.Fatalf("resume calls = %v, want %v", *resumeCalls, want)
	}
	if got := gitTestOutput(t, repository.root, "rev-parse", "HEAD"); got != localRelease {
		t.Fatalf("resume created another version commit %s", got)
	}
	if got := gitTestOutput(t, repository.bare, "rev-parse", "refs/heads/main"); got != localRelease {
		t.Fatalf("resumed remote main = %s, want %s", got, localRelease)
	}
	if got := gitTestOutput(t, repository.bare, "rev-parse", "refs/tags/v1.5.1^{commit}"); got != localRelease {
		t.Fatalf("resumed remote tag = %s, want %s", got, localRelease)
	}
	assertReleaseVersions(t, repository.root, "1.5.1")
}

func TestAutoReleaseTagFailureResumesPreparedCommit(t *testing.T) {
	repository := newAutoReleaseRepository(t)
	dependencies, _ := fakeAutoReleaseDependencies(t, repository.root)
	failing := &failOnceTagRunner{delegate: dependencies.runner}
	dependencies.runner = failing

	err := runAutoReleaseWithDependencies(context.Background(), autoReleaseOptions{
		repositoryRoot: repository.root,
		remote:         "origin",
		branch:         "main",
	}, dependencies)
	if err == nil || !strings.Contains(err.Error(), "release commit was retained") {
		t.Fatalf("first run error = %v, want retained-commit tag error", err)
	}
	preparedCommit := gitTestOutput(t, repository.root, "rev-parse", "HEAD")
	if preparedCommit == repository.startHead {
		t.Fatal("tag failure did not retain the prepared release commit")
	}
	if gitTestRun(repository.root, "show-ref", "--verify", "--quiet", "refs/tags/v1.5.1") == nil {
		t.Fatal("injected tag failure still created v1.5.1")
	}

	resumeDependencies, _ := fakeAutoReleaseDependencies(t, repository.root)
	if err := runAutoReleaseWithDependencies(context.Background(), autoReleaseOptions{
		repositoryRoot: repository.root,
		remote:         "origin",
		branch:         "main",
	}, resumeDependencies); err != nil {
		t.Fatalf("resume error = %v", err)
	}
	if got := gitTestOutput(t, repository.root, "rev-parse", "HEAD"); got != preparedCommit {
		t.Fatalf("resume created another version commit %s", got)
	}
	if got := gitTestOutput(t, repository.bare, "rev-parse", "refs/tags/v1.5.1^{commit}"); got != preparedCommit {
		t.Fatalf("resumed remote tag = %s, want %s", got, preparedCommit)
	}
	assertReleaseVersions(t, repository.root, "1.5.1")
}

func TestAutoReleaseVerificationFailureRestoresMetadataAndRefs(t *testing.T) {
	repository := newAutoReleaseRepository(t)
	originals := readReleaseFiles(t, repository.root)
	dependencies, _ := fakeAutoReleaseDependencies(t, repository.root)
	dependencies.verify = func(context.Context, string, string, string, string) error {
		return errors.New("injected verification failure")
	}

	err := runAutoReleaseWithDependencies(context.Background(), autoReleaseOptions{
		repositoryRoot: repository.root,
		remote:         "origin",
		branch:         "main",
	}, dependencies)
	if err == nil || !strings.Contains(err.Error(), "injected verification failure") {
		t.Fatalf("run error = %v", err)
	}
	if got := gitTestOutput(t, repository.root, "rev-parse", "HEAD"); got != repository.startHead {
		t.Fatalf("failed verification moved HEAD to %s", got)
	}
	for name, want := range originals {
		got, readErr := os.ReadFile(filepath.Join(repository.root, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s was not restored", name)
		}
	}
	if got := gitTestOutput(t, repository.root, "status", "--porcelain=v1", "--untracked-files=all"); got != "" {
		t.Fatalf("rollback left dirty worktree:\n%s", got)
	}
	if gitTestRun(repository.root, "show-ref", "--verify", "--quiet", "refs/tags/v1.5.1") == nil {
		t.Fatal("failed verification created v1.5.1")
	}
}

func TestAutoReleaseRollbackDoesNotOverwriteConcurrentMetadataEdit(t *testing.T) {
	repository := newAutoReleaseRepository(t)
	originals := readReleaseFiles(t, repository.root)
	dependencies, _ := fakeAutoReleaseDependencies(t, repository.root)
	concurrentPlugin := "{\n  \"name\": \"gograph\",\n  \"version\": \"1.5.1\",\n  \"user_edit\": true\n}\n"
	dependencies.verify = func(context.Context, string, string, string, string) error {
		writeTestFile(t, repository.root, releasePluginFile, concurrentPlugin)
		return errors.New("verification noticed a concurrent edit")
	}

	err := runAutoReleaseWithDependencies(context.Background(), autoReleaseOptions{
		repositoryRoot: repository.root,
		remote:         "origin",
		branch:         "main",
	}, dependencies)
	if err == nil || !strings.Contains(err.Error(), "rollback conflict") {
		t.Fatalf("error = %v, want rollback conflict", err)
	}
	plugin, readErr := os.ReadFile(filepath.Join(repository.root, releasePluginFile))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(plugin) != concurrentPlugin {
		t.Fatalf("concurrent plugin edit was overwritten:\n%s", plugin)
	}
	for _, filename := range []string{releaseConfigFile, releaseServerFile} {
		got, readErr := os.ReadFile(filepath.Join(repository.root, filename))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(got, originals[filename]) {
			t.Fatalf("coordinator-owned %s was not restored", filename)
		}
	}
}

func TestAutoReleaseDryRunRestoresWithoutCreatingRefs(t *testing.T) {
	repository := newAutoReleaseRepository(t)
	dependencies, calls := fakeAutoReleaseDependencies(t, repository.root)
	if err := runAutoReleaseWithDependencies(context.Background(), autoReleaseOptions{
		repositoryRoot: repository.root,
		remote:         "origin",
		branch:         "main",
		dryRun:         true,
	}, dependencies); err != nil {
		t.Fatalf("dry run error = %v", err)
	}
	if want := []string{"build", "render", "verify", "github", "registry"}; !reflect.DeepEqual(*calls, want) {
		t.Fatalf("dry-run calls = %v, want %v", *calls, want)
	}
	if got := gitTestOutput(t, repository.root, "rev-parse", "HEAD"); got != repository.startHead {
		t.Fatalf("dry run moved HEAD to %s", got)
	}
	assertReleaseVersions(t, repository.root, "1.5.0")
	if got := gitTestOutput(t, repository.root, "status", "--porcelain=v1", "--untracked-files=all"); got != "" {
		t.Fatalf("dry run left dirty worktree:\n%s", got)
	}
	if gitTestRun(repository.root, "show-ref", "--verify", "--quiet", "refs/tags/v1.5.1") == nil {
		t.Fatal("dry run created v1.5.1")
	}
}

func TestAutoReleaseRejectsExistingImmutablePublicationState(t *testing.T) {
	tests := map[string]func(*autoReleaseDependencies){
		"GitHub release": func(dependencies *autoReleaseDependencies) {
			dependencies.githubState = func(context.Context, string, string) (string, error) {
				return "matching", nil
			}
		},
		"Registry version": func(dependencies *autoReleaseDependencies) {
			dependencies.registryState = func(context.Context, string) (string, error) {
				return "pending", nil
			}
		},
	}
	for name, configure := range tests {
		t.Run(name, func(t *testing.T) {
			repository := newAutoReleaseRepository(t)
			originals := readReleaseFiles(t, repository.root)
			dependencies, _ := fakeAutoReleaseDependencies(t, repository.root)
			configure(&dependencies)
			err := runAutoReleaseWithDependencies(context.Background(), autoReleaseOptions{
				repositoryRoot: repository.root,
				remote:         "origin",
				branch:         "main",
			}, dependencies)
			if err == nil || !strings.Contains(err.Error(), "refusing to reuse") {
				t.Fatalf("error = %v, want immutable-state rejection", err)
			}
			if got := gitTestOutput(t, repository.root, "rev-parse", "HEAD"); got != repository.startHead {
				t.Fatalf("immutable-state rejection moved HEAD to %s", got)
			}
			for filename, want := range originals {
				got, readErr := os.ReadFile(filepath.Join(repository.root, filename))
				if readErr != nil {
					t.Fatal(readErr)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("%s was not restored", filename)
				}
			}
		})
	}
}

func TestAutoReleasePreflightFailsClosed(t *testing.T) {
	t.Run("dirty worktree", func(t *testing.T) {
		repository := newAutoReleaseRepository(t)
		if err := os.WriteFile(filepath.Join(repository.root, "untracked.txt"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		dependencies, calls := fakeAutoReleaseDependencies(t, repository.root)
		err := runAutoReleaseWithDependencies(context.Background(), autoReleaseOptions{repositoryRoot: repository.root, remote: "origin", branch: "main"}, dependencies)
		if err == nil || !strings.Contains(err.Error(), "clean worktree") {
			t.Fatalf("error = %v", err)
		}
		if len(*calls) != 0 {
			t.Fatalf("dirty preflight called release operations: %v", *calls)
		}
	})

	t.Run("wrong branch", func(t *testing.T) {
		repository := newAutoReleaseRepository(t)
		gitTestCommand(t, repository.root, "checkout", "-b", "feature")
		dependencies, calls := fakeAutoReleaseDependencies(t, repository.root)
		err := runAutoReleaseWithDependencies(context.Background(), autoReleaseOptions{repositoryRoot: repository.root, remote: "origin", branch: "main"}, dependencies)
		if err == nil || !strings.Contains(err.Error(), "branch main") {
			t.Fatalf("error = %v", err)
		}
		if len(*calls) != 0 {
			t.Fatalf("wrong-branch preflight called release operations: %v", *calls)
		}
	})

	t.Run("metadata mismatch", func(t *testing.T) {
		repository := newAutoReleaseRepository(t)
		writeTestFile(t, repository.root, releasePluginFile, "{\n  \"version\": \"9.9.9\"\n}\n")
		gitTestCommand(t, repository.root, "add", releasePluginFile)
		gitTestCommand(t, repository.root, "commit", "-m", "mismatched metadata")
		dependencies, calls := fakeAutoReleaseDependencies(t, repository.root)
		err := runAutoReleaseWithDependencies(context.Background(), autoReleaseOptions{repositoryRoot: repository.root, remote: "origin", branch: "main"}, dependencies)
		if err == nil || !strings.Contains(err.Error(), "not aligned") {
			t.Fatalf("error = %v", err)
		}
		if len(*calls) != 0 {
			t.Fatalf("version preflight called release operations: %v", *calls)
		}
	})

	t.Run("remote next tag collision", func(t *testing.T) {
		repository := newAutoReleaseRepository(t)
		gitTestCommand(t, repository.root, "tag", "-a", "v1.5.1", "-m", "collision")
		gitTestCommand(t, repository.root, "push", "origin", "refs/tags/v1.5.1:refs/tags/v1.5.1")
		gitTestCommand(t, repository.root, "tag", "-d", "v1.5.1")
		dependencies, calls := fakeAutoReleaseDependencies(t, repository.root)
		err := runAutoReleaseWithDependencies(context.Background(), autoReleaseOptions{repositoryRoot: repository.root, remote: "origin", branch: "main"}, dependencies)
		if err == nil || !strings.Contains(err.Error(), "remote tag v1.5.1 already exists") {
			t.Fatalf("error = %v", err)
		}
		if len(*calls) != 0 {
			t.Fatalf("tag-collision preflight called release operations: %v", *calls)
		}
	})

	t.Run("manual version bump cannot skip a patch", func(t *testing.T) {
		repository := newAutoReleaseRepository(t)
		writeTestFile(t, repository.root, releaseConfigFile, "[bumpversion]\ncurrent_version = 1.5.1\ncommit = False\ntag = False\n")
		writeTestFile(t, repository.root, releasePluginFile, "{\n  \"name\": \"gograph\",\n  \"version\": \"1.5.1\"\n}\n")
		writeTestFile(t, repository.root, releaseServerFile, renderedTestServer("1.5.1"))
		gitTestCommand(t, repository.root, "add", releaseConfigFile, releasePluginFile, releaseServerFile)
		gitTestCommand(t, repository.root, "commit", "-m", "manually set a future version")
		dependencies, calls := fakeAutoReleaseDependencies(t, repository.root)
		err := runAutoReleaseWithDependencies(context.Background(), autoReleaseOptions{repositoryRoot: repository.root, remote: "origin", branch: "main"}, dependencies)
		if err == nil || !strings.Contains(err.Error(), "no remote baseline tag v1.5.1") {
			t.Fatalf("error = %v", err)
		}
		if len(*calls) != 0 {
			t.Fatalf("manual bump called release operations: %v", *calls)
		}
	})

	t.Run("diverged remote main", func(t *testing.T) {
		repository := newAutoReleaseRepository(t)
		clone := filepath.Join(t.TempDir(), "other")
		gitTestCommand(t, "", "clone", repository.bare, clone)
		gitTestCommand(t, clone, "config", "user.email", "other@example.com")
		gitTestCommand(t, clone, "config", "user.name", "Other Writer")
		writeTestFile(t, clone, "remote.txt", "remote-only\n")
		gitTestCommand(t, clone, "add", "remote.txt")
		gitTestCommand(t, clone, "commit", "-m", "remote divergence")
		gitTestCommand(t, clone, "push", "origin", "main")

		dependencies, calls := fakeAutoReleaseDependencies(t, repository.root)
		err := runAutoReleaseWithDependencies(context.Background(), autoReleaseOptions{repositoryRoot: repository.root, remote: "origin", branch: "main"}, dependencies)
		if err == nil || !strings.Contains(err.Error(), "behind or diverged") {
			t.Fatalf("error = %v", err)
		}
		if len(*calls) != 0 {
			t.Fatalf("divergence preflight called release operations: %v", *calls)
		}
	})
}

type autoReleaseTestRepository struct {
	root      string
	bare      string
	startHead string
}

func newAutoReleaseRepository(t *testing.T) autoReleaseTestRepository {
	t.Helper()
	root := filepath.Join(t.TempDir(), "work")
	bare := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, "", "init", "--bare", bare)
	gitTestCommand(t, "", "init", "-b", "main", root)
	gitTestCommand(t, root, "config", "user.email", "release-test@example.com")
	gitTestCommand(t, root, "config", "user.name", "Release Test")
	gitTestCommand(t, root, "config", "commit.gpgSign", "false")
	gitTestCommand(t, root, "config", "tag.gpgSign", "false")
	writeTestFile(t, root, releaseConfigFile, "[bumpversion]\ncurrent_version = 1.5.0\ncommit = False\ntag = False\n")
	writeTestFile(t, root, releasePluginFile, "{\n  \"name\": \"gograph\",\n  \"version\": \"1.5.0\"\n}\n")
	writeTestFile(t, root, releaseServerFile, renderedTestServer("1.5.0"))
	writeTestFile(t, root, "feature.txt", "initial\n")
	gitTestCommand(t, root, "add", ".")
	gitTestCommand(t, root, "commit", "-m", "initial")
	gitTestCommand(t, root, "remote", "add", "origin", bare)
	gitTestCommand(t, root, "tag", "-a", "v1.5.0", "-m", releaseCommitMessage("1.4.99", "1.5.0"))
	gitTestCommand(t, root, "push", "--atomic", "-u", "origin", "main", "refs/tags/v1.5.0:refs/tags/v1.5.0")
	gitTestCommand(t, bare, "symbolic-ref", "HEAD", "refs/heads/main")
	writeTestFile(t, root, "feature.txt", "initial\nfeature\n")
	gitTestCommand(t, root, "add", "feature.txt")
	gitTestCommand(t, root, "commit", "-m", "feat: prepare release")
	return autoReleaseTestRepository{root: root, bare: bare, startHead: gitTestOutput(t, root, "rev-parse", "HEAD")}
}

func fakeAutoReleaseDependencies(t *testing.T, root string) (autoReleaseDependencies, *[]string) {
	t.Helper()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	runner := execReleaseCommandRunner{stdout: io.Discard, stderr: io.Discard}
	dependencies := autoReleaseDependencies{
		runner: runner,
		stdout: io.Discard,
		build: func(_ context.Context, gotRoot, _ string, version string) ([]mcpbundle.Artifact, error) {
			calls = append(calls, "build")
			if gotRoot != canonicalRoot || version == "" {
				return nil, fmt.Errorf("unexpected build root/version %q %q", gotRoot, version)
			}
			return []mcpbundle.Artifact{{Name: version}}, nil
		},
		render: func(version string, _ []mcpbundle.Artifact) ([]byte, error) {
			calls = append(calls, "render")
			return []byte(renderedTestServer(version)), nil
		},
		verify: func(_ context.Context, gotRoot, version, _, server string) error {
			calls = append(calls, "verify")
			if gotRoot != canonicalRoot || server != filepath.Join(canonicalRoot, releaseServerFile) {
				return fmt.Errorf("unexpected verification paths %q %q", gotRoot, server)
			}
			got, err := readAlignedReleaseVersion(canonicalRoot)
			if err != nil {
				return err
			}
			if got != version {
				return fmt.Errorf("verification version = %s, want %s", got, version)
			}
			return nil
		},
		githubState: func(context.Context, string, string) (string, error) {
			calls = append(calls, "github")
			return "missing", nil
		},
		registryState: func(context.Context, string) (string, error) {
			calls = append(calls, "registry")
			return "missing", nil
		},
		validateRemote: func(context.Context, releaseCommandRunner, string, string) error {
			return nil
		},
	}
	return dependencies, &calls
}

type failOncePushRunner struct {
	delegate releaseCommandRunner
	failed   bool
}

type failOnceTagRunner struct {
	delegate releaseCommandRunner
	failed   bool
}

func (r *failOnceTagRunner) Output(ctx context.Context, directory, name string, args ...string) ([]byte, error) {
	return r.delegate.Output(ctx, directory, name, args...)
}

func (r *failOnceTagRunner) Run(ctx context.Context, directory, name string, args ...string) error {
	if !r.failed && name == "git" && len(args) > 0 && args[0] == "tag" {
		r.failed = true
		return errors.New("injected tag failure")
	}
	return r.delegate.Run(ctx, directory, name, args...)
}

func (r *failOncePushRunner) Output(ctx context.Context, directory, name string, args ...string) ([]byte, error) {
	return r.delegate.Output(ctx, directory, name, args...)
}

func (r *failOncePushRunner) Run(ctx context.Context, directory, name string, args ...string) error {
	if !r.failed && name == "git" && len(args) > 0 && args[0] == "push" {
		r.failed = true
		return errors.New("injected push failure")
	}
	return r.delegate.Run(ctx, directory, name, args...)
}

func renderedTestServer(version string) string {
	return fmt.Sprintf("{\n  \"version\": %q,\n  \"artifact\": %q\n}\n", version, "gograph_"+version+"_darwin_arm64.mcpb")
}

func assertReleaseVersions(t *testing.T, root, version string) {
	t.Helper()
	got, err := readAlignedReleaseVersion(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != version {
		t.Fatalf("aligned version = %q, want %q", got, version)
	}
}

func readReleaseFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	for _, name := range releaseOwnedPaths() {
		contents, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		result[name] = contents
	}
	return result
}

func writeTestFile(t *testing.T, root, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitTestCommand(t *testing.T, directory string, args ...string) {
	t.Helper()
	if output, err := gitTestCombinedOutput(directory, args...); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func gitTestOutput(t *testing.T, directory string, args ...string) string {
	t.Helper()
	output, err := gitTestCombinedOutput(directory, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func gitTestRun(directory string, args ...string) error {
	_, err := gitTestCombinedOutput(directory, args...)
	return err
}

func gitTestCombinedOutput(directory string, args ...string) ([]byte, error) {
	command := exec.Command("git", args...)
	command.Dir = directory
	return command.CombinedOutput()
}
