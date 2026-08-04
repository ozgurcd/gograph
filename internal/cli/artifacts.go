package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/report"
	"github.com/ozgurcd/gograph/internal/search"
)

const (
	artifactLockFile    = ".artifacts.lock"
	artifactLockTimeout = 30 * time.Second
	artifactLockRetry   = 25 * time.Millisecond
	graphReportCount    = 9
)

// graphPublication describes the graph that should remain active after a
// publication attempt. Graph may be an already-persisted graph that won a
// concurrent publication race. Published reports whether candidate was
// committed by this call.
type graphPublication struct {
	Graph     *graph.Graph
	Published bool
}

type artifactPublicationMode uint8

const (
	manualArtifactPublication artifactPublicationMode = iota
	refreshArtifactPublication
)

type artifactPayload struct {
	relPath string
	data    []byte
}

type stagedArtifact struct {
	finalPath string
	tempPath  string
}

// publishGraphArtifacts serializes every gograph writer, re-checks the source
// and persisted graph under that lock, then stages a complete graph/report
// bundle. Reports are replaced first and graph.json is replaced last, making
// graph.json the publication commit marker.
//
// Background MCP refreshes adopt a fresh graph of equal or richer quality that
// another process already published. An explicit manual build remains
// authoritative and may intentionally replace a precise graph with AST output,
// preserving the established `gograph build` behavior. The one exception is a
// failed manual precise retry: it cannot replace a still-fresh precise graph
// with precise_fallback metadata.
func publishGraphArtifacts(root string, candidate *graph.Graph, mode artifactPublicationMode) (result graphPublication, err error) {
	if candidate == nil {
		return graphPublication{}, fmt.Errorf("cannot publish a nil graph")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return graphPublication{}, fmt.Errorf("resolving publication root: %w", err)
	}
	candidate.Root = absRoot

	outDir := filepath.Join(absRoot, outputDir)
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return graphPublication{}, fmt.Errorf("creating artifact directory: %w", err)
	}

	lock := flock.New(filepath.Join(outDir, artifactLockFile), flock.SetPermissions(0o640))
	ctx, cancel := context.WithTimeout(context.Background(), artifactLockTimeout)
	defer cancel()
	locked, lockErr := lock.TryLockContext(ctx, artifactLockRetry)
	if lockErr != nil {
		return graphPublication{}, fmt.Errorf("acquiring artifact publication lock: %w", lockErr)
	}
	if !locked {
		return graphPublication{}, fmt.Errorf("acquiring artifact publication lock: %w", ctx.Err())
	}
	defer func() {
		if unlockErr := lock.Unlock(); unlockErr != nil && err == nil {
			err = fmt.Errorf("releasing artifact publication lock: %w", unlockErr)
		}
	}()

	if stale := search.Stale(candidate, absRoot); stale.IsStale {
		return graphPublication{}, fmt.Errorf("refusing to publish stale graph (%d freshness changes)", stale.ChangeCount())
	}

	previous, loadErr := loadGraph(absRoot)
	if loadErr != nil {
		previous = nil
	}
	if previous != nil && !search.Stale(previous, absRoot).IsStale {
		quality := compareGraphPublicationQuality(previous, candidate)
		keepForRefresh := mode == refreshArtifactPublication && quality >= 0
		keepPreciseAfterFailedManualRetry := mode == manualArtifactPublication &&
			previous.Build.EffectivePrecision() == graph.PrecisionPrecise &&
			candidate.Build.EffectivePrecision() == graph.PrecisionFallback
		if keepForRefresh || keepPreciseAfterFailedManualRetry {
			return graphPublication{Graph: previous}, nil
		}
	}

	candidate.Baseline = graphBaseline(previous)
	payloads, err := renderGraphArtifacts(candidate)
	if err != nil {
		return graphPublication{}, err
	}
	staged, err := stageGraphArtifacts(absRoot, payloads)
	if err != nil {
		return graphPublication{}, err
	}
	defer cleanupStagedArtifacts(staged)

	// Sources can change while reports are rendered and staged. Do not make a
	// known-stale candidate visible; the caller can rebuild and retry.
	if stale := search.Stale(candidate, absRoot); stale.IsStale {
		return graphPublication{}, fmt.Errorf("source changed while staging graph artifacts (%d freshness changes)", stale.ChangeCount())
	}
	for _, artifact := range staged {
		if err := os.Rename(artifact.tempPath, artifact.finalPath); err != nil {
			return graphPublication{}, fmt.Errorf("committing %s: %w", filepath.Base(artifact.finalPath), err)
		}
		artifact.tempPath = ""
	}
	if err := syncDirectory(outDir); err != nil {
		return graphPublication{}, fmt.Errorf("syncing artifact directory: %w", err)
	}
	return graphPublication{Graph: candidate, Published: true}, nil
}

func graphBaseline(previous *graph.Graph) *graph.GraphBaseline {
	if previous == nil {
		return nil
	}
	return &graph.GraphBaseline{
		OrphanCount:   len(search.ReachableOrphans(previous)),
		CouplingEdges: len(previous.Imports),
	}
}

// compareGraphPublicationQuality returns a positive value when left is richer,
// a negative value when right is richer, and zero when their durable analysis
// quality is equivalent.
func compareGraphPublicationQuality(left, right *graph.Graph) int {
	leftPrecision := graphPrecisionRank(left)
	rightPrecision := graphPrecisionRank(right)
	if leftPrecision != rightPrecision {
		return leftPrecision - rightPrecision
	}
	leftComplete, rightComplete := 0, 0
	leftParsed, rightParsed := 0, 0
	if left != nil && left.Build != nil {
		if left.Build.Complete {
			leftComplete = 1
		}
		leftParsed = left.Build.ParsedFiles
	}
	if right != nil && right.Build != nil {
		if right.Build.Complete {
			rightComplete = 1
		}
		rightParsed = right.Build.ParsedFiles
	}
	if leftComplete != rightComplete {
		return leftComplete - rightComplete
	}
	return leftParsed - rightParsed
}

func graphPrecisionRank(g *graph.Graph) int {
	if g == nil {
		return 0
	}
	switch g.Build.EffectivePrecision() {
	case graph.PrecisionPrecise:
		return 3
	case graph.PrecisionFallback:
		return 2
	default:
		return 1
	}
}

func renderGraphArtifacts(g *graph.Graph) ([]artifactPayload, error) {
	graphJSON, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding graph.json: %w", err)
	}
	// Keep graph.json last: consumers use it as the commit marker for the
	// preceding derived reports.
	return []artifactPayload{
		{relPath: reportFile, data: []byte(report.GenerateIndex(g))},
		{relPath: symFile, data: []byte(report.GenerateSymbols(g))},
		{relPath: depsFile, data: []byte(report.GenerateDeps(g))},
		{relPath: routesFile, data: []byte(report.GenerateRoutes(g))},
		{relPath: sqlFile, data: []byte(report.GenerateSQL(g))},
		{relPath: errorsFile, data: []byte(report.GenerateErrors(g))},
		{relPath: configFile, data: []byte(report.GenerateConfig(g))},
		{relPath: concFile, data: []byte(report.GenerateConcurrency(g))},
		{relPath: testsFile, data: []byte(report.GenerateTests(g))},
		{relPath: graphFile, data: graphJSON},
	}, nil
}

func stageGraphArtifacts(root string, payloads []artifactPayload) ([]stagedArtifact, error) {
	staged := make([]stagedArtifact, 0, len(payloads))
	for _, payload := range payloads {
		finalPath := filepath.Join(root, payload.relPath)
		tempPath, err := stageBytes(finalPath, payload.data, 0o640)
		if err != nil {
			cleanupStagedArtifacts(staged)
			return nil, fmt.Errorf("staging %s: %w", filepath.Base(finalPath), err)
		}
		staged = append(staged, stagedArtifact{finalPath: finalPath, tempPath: tempPath})
	}
	return staged, nil
}

func cleanupStagedArtifacts(staged []stagedArtifact) {
	for _, artifact := range staged {
		if artifact.tempPath != "" {
			_ = os.Remove(artifact.tempPath)
		}
	}
}

func stageBytes(path string, data []byte, mode os.FileMode) (string, error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	failed := true
	defer func() {
		_ = tmp.Close()
		if failed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	failed = false
	return tmpPath, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = directory.Close() }()
	// Directory fsync is not supported by every target filesystem (notably
	// Windows). The individual files were already synced before rename, so keep
	// this as the same best-effort durability step writeJSON historically used.
	_ = directory.Sync()
	return nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tempPath, err := stageBytes(path, data, 0o640)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tempPath) }()
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
