package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

const workspaceLockFile = ".workspace-artifacts.lock"

func EncodeArtifact(artifact *Artifact) ([]byte, error) {
	if artifact == nil {
		return nil, fmt.Errorf("cannot encode nil workspace artifact")
	}
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode workspace artifact: %w", err)
	}
	return append(data, '\n'), nil
}

// Publish atomically replaces only the workspace overlay. Member repository
// artifacts are outside this publication transaction.
func Publish(root string, artifact *Artifact) error {
	data, err := EncodeArtifact(artifact)
	if err != nil {
		return err
	}
	if int64(len(data)) > graph.MaxArtifactBytes {
		return fmt.Errorf("workspace artifact size %d bytes exceeds safety limit %d bytes", len(data), graph.MaxArtifactBytes)
	}
	reader, err := sourcefs.Open(root)
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = reader.Close() }()
	if err := reader.EnsureRealDirectory(".gograph", 0o750); err != nil {
		return fmt.Errorf("prepare workspace artifact directory: %w", err)
	}
	outDir := filepath.Join(root, ".gograph")
	lockPath := filepath.Join(outDir, workspaceLockFile)
	if info, statErr := os.Lstat(lockPath); statErr == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return fmt.Errorf("unsafe workspace artifact lock %s", lockPath)
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("inspect workspace artifact lock: %w", statErr)
	}
	lock := flock.New(lockPath, flock.SetPermissions(0o640))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	locked, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil || !locked {
		if err == nil {
			err = fmt.Errorf("timed out")
		}
		return fmt.Errorf("acquire workspace artifact lock: %w", err)
	}
	defer func() { _ = lock.Unlock() }()
	if err := reader.AtomicReplaceRegularFile(ArtifactFile, data, 0o640); err != nil {
		return fmt.Errorf("publish workspace artifact: %w", err)
	}
	return nil
}
