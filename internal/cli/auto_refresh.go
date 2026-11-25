package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
	"github.com/ozgurcd/gograph/internal/session"
)

// autoRefresh is the per-invocation state of the CLI's stale-graph refresh:
// a graph query whose persisted graph no longer matches the working tree
// rebuilds it once, with the recorded precision, build tags and excluded
// directories, before answering.
var autoRefresh struct {
	enabled bool
	done    bool
	// note reports the refresh in JSON output, whose stderr stays empty.
	note string
}

// quietBuildProgress drops the build's progress lines during an automatic
// refresh; warnings still print.
var quietBuildProgress bool

// commandAutoRefreshes reports whether command answers from the persisted
// graph and may rebuild it first. Commands that report freshness, compare
// the tree with the graph, build, serve or guard hooks never do, nor do the
// enforcement commands (gate, check, boundaries): they judge the persisted
// graph and fail closed when it is stale.
func commandAutoRefreshes(command string) bool {
	if !commandWarnsWhenStale(command) {
		return false
	}
	switch command {
	case "workspace", "doctor", "validate", "add-claude-plugin", "version", "--version", "-v",
		"help", "--help", "-h", "capabilities", "wiki", "doc", "api", "gate", "check", "boundaries":
		return false
	}
	return true
}

// autoRefreshDisabledByEnv reports GOGRAPH_NO_REFRESH set to anything but
// "", "0" or "false".
func autoRefreshDisabledByEnv() bool {
	value := strings.TrimSpace(os.Getenv("GOGRAPH_NO_REFRESH"))
	return value != "" && value != "0" && !strings.EqualFold(value, "false")
}

func resetAutoRefresh() {
	autoRefresh.enabled, autoRefresh.done, autoRefresh.note = false, false, ""
}

// reportAutoRefresh prints a refresh message on stderr, or keeps it for the
// JSON envelope's warning.
func reportAutoRefresh(message string) {
	if jsonMode {
		autoRefresh.note = message
		return
	}
	fmt.Fprintln(os.Stderr, "gograph: "+message)
}

// refreshStaleLoadedGraph rebuilds a stale graph once per invocation and
// returns the reloaded graph, or g unchanged when refresh is off, the graph
// is current, its build context changed (other tags or GOOS), another
// caller's session refuses writes, or the rebuild fails. A graph that is
// still stale afterwards keeps the usual stale warning.
func refreshStaleLoadedGraph(g *graph.Graph) *graph.Graph {
	if !autoRefresh.enabled || autoRefresh.done || g == nil || queryRev != "" {
		return g
	}
	autoRefresh.done = true
	stale := loadedGraphStale(g)
	if !stale.IsStale || stale.BuildContextChanged {
		return g
	}
	root := graphRoot(g)
	if err := session.GuardCallerMutationAt(root, callerSessionID); err != nil {
		return g
	}
	if code := runQuietBuild(autoRefreshBuildArgs(g, root)); code != exitSuccess {
		reportAutoRefresh("the graph is stale and the automatic rebuild failed; answering from the stale graph")
		return g
	}
	refreshed, err := loadGraphIgnoringRev(root)
	if err != nil {
		return g
	}
	files := "the Go build context"
	if count := len(stale.ChangedFiles); count > 0 {
		files = fmt.Sprintf("%d changed file(s)", count)
	}
	reportAutoRefresh(fmt.Sprintf("rebuilt the stale graph (%s) before answering; --no-refresh or GOGRAPH_NO_REFRESH=1 turns this off", files))
	return refreshed
}

// autoRefreshBuildArgs repeats the recorded build: precise when the graph
// asked for precision, the recorded tags unless --tags or GOFLAGS selects
// them, and the recorded excluded directories.
func autoRefreshBuildArgs(g *graph.Graph, root string) []string {
	args := []string{root}
	if g.Build == nil {
		return args
	}
	if g.Build.PreciseRequested() {
		args = append(args, "--precise")
	}
	tags := queryBuildTags
	if len(tags) == 0 && g.Build.Selection != nil && !search.GOFLAGSSetsBuildTags(os.Getenv("GOFLAGS")) {
		tags = g.Build.Selection.BuildTags
	}
	if len(tags) > 0 {
		args = append(args, "--tags="+strings.Join(tags, ","))
	}
	if g.Build.Selection != nil && len(g.Build.Selection.ExcludeDirs) > 0 {
		args = append(args, "--exclude-dirs="+strings.Join(g.Build.Selection.ExcludeDirs, ","))
	}
	return args
}

// runQuietBuild runs the build command with its progress output discarded,
// so the query's own stdout (text or JSON) stays unchanged. Build warnings
// still reach stderr, except under --json, whose stderr stays empty. The
// query's invocation status is preserved.
func runQuietBuild(args []string) int {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return 1
	}
	defer func() { _ = devNull.Close() }()
	savedStdout, savedStderr, savedJSON, savedInvocationError := os.Stdout, os.Stderr, jsonMode, invocationError
	os.Stdout = devNull
	if jsonMode {
		os.Stderr = devNull
	}
	jsonMode = false
	defer func() {
		os.Stdout, os.Stderr, jsonMode, invocationError = savedStdout, savedStderr, savedJSON, savedInvocationError
	}()
	enabled := autoRefresh.enabled
	autoRefresh.enabled, quietBuildProgress = false, true
	defer func() { autoRefresh.enabled, quietBuildProgress = enabled, false }()
	return runBuild(args)
}
