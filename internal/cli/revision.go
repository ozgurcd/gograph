package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ozgurcd/gograph/internal/baseline"
	"github.com/ozgurcd/gograph/internal/gitenv"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

// queryRev holds the global --rev of a graph query: the Git ref whose tree the
// command reads instead of the working tree.
var queryRev string

// revisionRoots memoizes the prepared tree per project root and ref for one
// invocation, so commands that load the graph more than once resolve once.
var revisionRoots = map[string]string{}

// revisionCacheDir is where --rev keeps one extracted tree, with its own
// .gograph graph, per commit.
const revisionCacheDir = ".gograph/revs"

// commandTakesRev reports whether --rev applies to command: the graph queries
// that take the global --tags. A command that compares the working tree with
// Git (--uncommitted, --since) cannot also read a commit.
func commandTakesRev(args []string) error {
	command := commandFromArgs(args)
	if !commandTakesQueryTags(command) {
		return fmt.Errorf("--rev selects the commit a graph query reads; %s does not take it", command)
	}
	for _, arg := range args {
		switch arg {
		case "--uncommitted", "--since":
			return fmt.Errorf("--rev cannot be combined with %s, which compares the working tree with Git", arg)
		}
	}
	return nil
}

// revisionRoot returns the directory of projectRoot's tree at ref, extracting
// it with git archive into .gograph/revs/<commit>/ and building its graph the
// first time. The tree is immutable, so later queries reuse it; the graph is
// rebuilt only when the requested build context changes.
func revisionRoot(projectRoot, ref string) (string, error) {
	key := projectRoot + "\x00" + ref
	if root, ok := revisionRoots[key]; ok {
		return root, nil
	}
	ctx := context.Background()
	commit, err := baseline.ResolveCommit(ctx, projectRoot, ref)
	if err != nil {
		return "", err
	}
	cacheRoot := filepath.Join(projectRoot, filepath.FromSlash(revisionCacheDir))
	treeDir := filepath.Join(cacheRoot, commit)
	root := ""
	if _, statErr := os.Stat(treeDir); statErr != nil {
		if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
			return "", fmt.Errorf("create %s: %w", revisionCacheDir, err)
		}
		if err := writeGitignore(projectRoot); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not update .gitignore: %v\n", err)
		}
		tmp, err := os.MkdirTemp(cacheRoot, commit+".tmp-")
		if err != nil {
			return "", fmt.Errorf("create revision directory: %w", err)
		}
		extracted, err := baseline.ExtractProject(ctx, projectRoot, commit, tmp)
		if err != nil {
			_ = os.RemoveAll(tmp)
			return "", err
		}
		rel, err := filepath.Rel(tmp, extracted)
		if err != nil {
			_ = os.RemoveAll(tmp)
			return "", err
		}
		if err := os.Rename(tmp, treeDir); err != nil {
			// Another process may have published the same commit first.
			_ = os.RemoveAll(tmp)
			if _, statErr := os.Stat(treeDir); statErr != nil {
				return "", fmt.Errorf("publish revision tree: %w", err)
			}
		}
		root = filepath.Join(treeDir, rel)
	} else {
		extracted, err := revisionProjectDir(projectRoot, treeDir)
		if err != nil {
			return "", err
		}
		root = extracted
	}
	if err := ensureStandaloneRepository(treeDir); err != nil {
		return "", err
	}

	tags, excludes := queryBuildTags, []string(nil)
	if working, err := loadGraphIgnoringRev(projectRoot); err == nil && working.Build != nil && working.Build.Selection != nil {
		excludes = working.Build.Selection.ExcludeDirs
		if len(tags) == 0 && !search.GOFLAGSSetsBuildTags(os.Getenv("GOFLAGS")) {
			tags = working.Build.Selection.BuildTags
		}
	}
	if cached, err := loadGraphIgnoringRev(root); err == nil && !search.StaleForQuery(cached, root, tags).IsStale {
		revisionRoots[key] = root
		return root, nil
	}
	if err := buildRevisionGraph(root, tags, excludes); err != nil {
		return "", fmt.Errorf("build graph for %s (%s): %w", ref, commit, err)
	}
	revisionRoots[key] = root
	return root, nil
}

// revisionProjectDir maps projectRoot to its directory inside an extracted
// tree: the tree holds the project's path relative to the Git top level.
func revisionProjectDir(projectRoot, treeDir string) (string, error) {
	extracted, err := baseline.ProjectSubdir(context.Background(), projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(treeDir, extracted), nil
}

// ensureStandaloneRepository makes treeDir its own (empty) Git repository.
// The tree lives under the project's ignored .gograph directory, and the
// scanner refuses an analysis root that an enclosing repository ignores; a
// nested repository applies only the tree's own ignore rules.
func ensureStandaloneRepository(treeDir string) error {
	if _, err := os.Stat(filepath.Join(treeDir, ".git")); err == nil {
		return nil
	}
	cmd := exec.Command("git", "init", "--quiet", treeDir)
	cmd.Env = gitenv.Clean(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init %s: %w: %s", treeDir, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func buildRevisionGraph(root string, tags, excludes []string) error {
	config, configErr := resolveBuildConfigWithTags(root, tags, excludes)
	g, err := buildGraphWithConfig(root, config, configErr)
	if err != nil {
		return err
	}
	sortGraph(g)
	if err := refreshCompleteGraphSourceFingerprint(root, g, config); err != nil {
		return err
	}
	_, err = publishGraphArtifactsWithFreshness(root, g, manualArtifactPublication, func(candidate *graph.Graph, root string) search.StaleResult {
		return search.StaleWithConfig(candidate, root, config)
	})
	return err
}
