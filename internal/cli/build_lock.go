package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/ozgurcd/gograph/internal/graph"
)

const (
	// buildLockFile is held for a whole build of one checkout. A second build
	// waits for it and then reuses the graph if it already fits; a query that
	// finds no graph waits for it instead of failing.
	buildLockFile = ".build.lock"
	// defaultBuildWait bounds every wait for a running build.
	defaultBuildWait = 10 * time.Minute
	buildWaitPoll    = 100 * time.Millisecond
	// buildWaitEnv overrides defaultBuildWait with a Go duration; 0 disables
	// waiting, so the caller fails or builds at once as before.
	buildWaitEnv = "GOGRAPH_BUILD_WAIT"
)

// heldBuildLocks names the checkouts whose build lock this process holds. A
// build reads its own previous graph; on Linux a second flock of the same file
// from this process would block on itself.
var heldBuildLocks = map[string]int{}

func buildWaitLimit() time.Duration {
	value := strings.TrimSpace(os.Getenv(buildWaitEnv))
	if value == "" {
		return defaultBuildWait
	}
	limit, err := time.ParseDuration(value)
	if err != nil || limit < 0 {
		return defaultBuildWait
	}
	return limit
}

func buildLockPath(absRoot string) string {
	return filepath.Join(absRoot, outputDir, buildLockFile)
}

// acquireBuildLock takes the checkout's build lock, waiting up to the build
// wait limit for a build already running. waited reports that another build
// held it, so the caller should check whether that build's graph already fits.
func acquireBuildLock(absRoot string) (unlock func(), waited bool, err error) {
	if err := ensureArtifactDirectory(filepath.Join(absRoot, outputDir)); err != nil {
		return nil, false, err
	}
	path := buildLockPath(absRoot)
	if err := validateArtifactLock(path); err != nil {
		return nil, false, err
	}
	if heldBuildLocks[absRoot] > 0 {
		// Nested build in this process (a query's quiet refresh inside a
		// build): the outer build already serializes this checkout.
		return func() {}, false, nil
	}
	lock := flock.New(path, flock.SetPermissions(0o640))
	locked, err := lock.TryLock()
	if err != nil {
		return nil, false, fmt.Errorf("acquiring build lock: %w", err)
	}
	if !locked {
		limit := buildWaitLimit()
		if limit == 0 {
			return nil, false, fmt.Errorf("another gograph build is running in %s (%s=0 disables waiting)", absRoot, buildWaitEnv)
		}
		fmt.Fprintf(os.Stderr, "gograph build: waiting up to %s for the build already running in %s\n", limit, absRoot)
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		defer cancel()
		locked, err = lock.TryLockContext(ctx, buildWaitPoll)
		if err != nil || !locked {
			return nil, true, fmt.Errorf("another gograph build in %s did not finish within %s (set %s to wait longer)", absRoot, limit, buildWaitEnv)
		}
		waited = true
	}
	heldBuildLocks[absRoot]++
	return func() {
		if heldBuildLocks[absRoot]--; heldBuildLocks[absRoot] <= 0 {
			delete(heldBuildLocks, absRoot)
		}
		_ = lock.Unlock()
	}, waited, nil
}

// waitForRunningBuild waits, within the build wait limit, for a build of
// absRoot that another process is running. It reports whether one was
// running and has finished, so the caller should read the graph again.
func waitForRunningBuild(absRoot string) bool {
	if heldBuildLocks[absRoot] > 0 {
		return false
	}
	path := buildLockPath(absRoot)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	lock := flock.New(path, flock.SetPermissions(0o640))
	locked, err := lock.TryRLock()
	if err != nil {
		return false
	}
	if locked {
		_ = lock.Unlock()
		return false
	}
	limit := buildWaitLimit()
	if limit == 0 {
		return false
	}
	fmt.Fprintf(os.Stderr, "gograph: waiting up to %s for the graph build running in %s\n", limit, absRoot)
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	locked, err = lock.TryRLockContext(ctx, buildWaitPoll)
	if err != nil || !locked {
		return false
	}
	_ = lock.Unlock()
	return true
}

// concurrentBuildFits reports whether the graph another build just published
// already answers this build request: fresh for the same build context, and
// at least as precise as requested.
func concurrentBuildFits(previous *graph.Graph, precise bool, stale bool) bool {
	if previous == nil || previous.Build == nil || stale {
		return false
	}
	if precise && previous.Build.EffectivePrecision() != graph.PrecisionPrecise {
		return false
	}
	return true
}
