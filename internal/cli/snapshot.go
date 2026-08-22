package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/rootfind"
	"github.com/ozgurcd/gograph/internal/search"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

type Snapshot struct {
	Name           string   `json:"name"`
	Timestamp      string   `json:"timestamp"`
	TotalSymbols   int      `json:"total_symbols"`
	OrphanCount    int      `json:"orphan_count"`
	GodObjects     []string `json:"god_objects"`
	MaxComplexity  int      `json:"max_complexity"`
	AvgInstability float64  `json:"avg_instability"`
	CouplingEdges  int      `json:"coupling_edges"`
}

const snapshotDirectory = ".gograph/snapshots"

func snapshotPath(name string) string {
	return filepath.Join(snapshotDirectory, name+".json")
}

func openSnapshotStore(root string) (*sourcefs.Reader, error) {
	if root == "" {
		root = rootfind.FindRepositoryRoot()
	}
	store, err := sourcefs.Open(root)
	if err != nil {
		return nil, fmt.Errorf("open snapshot repository: %w", err)
	}
	return store, nil
}

func runSnapshot(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gograph snapshot <save|diff|list|drop> [name]")
		return 1
	}

	cmd := args[0]
	if cmd == "save" || cmd == "diff" || cmd == "drop" {
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: gograph snapshot %s <name>\n", cmd)
			return 1
		}
		name := args[1]
		validName := regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)
		if !validName.MatchString(name) {
			fmt.Fprintf(os.Stderr, "error: invalid snapshot name %q (must be alphanumeric, dash, or underscore)\n", name)
			return 1
		}
	}

	switch cmd {
	case "save":
		return runSnapshotSave(args[1])
	case "diff":
		return runSnapshotDiff(args[1])
	case "list":
		return runSnapshotList()
	case "drop":
		return runSnapshotDrop(args[1])
	default:
		fmt.Fprintf(os.Stderr, "unknown snapshot command: %s\n", cmd)
		return 1
	}
}

func calculateSnapshot(g *graph.Graph, name string) *Snapshot {
	s := &Snapshot{
		Name:          name,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		TotalSymbols:  len(g.Symbols),
		OrphanCount:   len(search.ReachableOrphans(g)),
		CouplingEdges: len(g.Imports),
	}

	// MaxComplexity
	for _, result := range search.Complexity(g, "") {
		if result.Score > s.MaxComplexity {
			s.MaxComplexity = result.Score
		}
	}

	// GodObjects (using default thresholds)
	p := search.DefaultGodObjectParams()
	godObjs := search.GodObjects(g, p)
	for _, goObj := range godObjs {
		s.GodObjects = append(s.GodObjects, goObj.Name)
	}

	// AvgInstability
	coupling := search.Coupling(g, "", search.CouplingOptions{IncludeStdlib: true})
	if len(coupling) > 0 {
		var total float64
		for _, c := range coupling {
			total += c.Instability
		}
		s.AvgInstability = total / float64(len(coupling))
	}

	return s
}

func runSnapshotSave(name string) int {
	g, err := loadGraph(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading graph: %v\n", err)
		return 1
	}

	s := calculateSnapshot(g, name)

	store, err := openSnapshotStore(g.Root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening snapshot store: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()
	if err := store.EnsureRealDirectory(snapshotDirectory, 0o750); err != nil {
		fmt.Fprintf(os.Stderr, "error creating snapshot dir: %v\n", err)
		return 1
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error marshaling snapshot: %v\n", err)
		return 1
	}

	if err := store.WriteRegularFile(snapshotPath(name), data, 0o640, false); err != nil {
		fmt.Fprintf(os.Stderr, "error writing snapshot: %v\n", err)
		return 1
	}

	fmt.Printf("Saved snapshot %q at %s\n", name, s.Timestamp)
	return 0
}

func runSnapshotDiff(name string) int {
	store, err := openSnapshotStore("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening snapshot store: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()
	data, err := store.ReadRegularFile(snapshotPath(name))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading snapshot %q: %v\n", name, err)
		return 1
	}

	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		fmt.Fprintf(os.Stderr, "error parsing snapshot: %v\n", err)
		return 1
	}

	g, err := loadGraph(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading graph: %v\n", err)
		return 1
	}

	curr := calculateSnapshot(g, "current")

	fmt.Printf("%-17s %8d → %-8d %+-4d   (symbols added/removed)\n", "total_symbols", snap.TotalSymbols, curr.TotalSymbols, curr.TotalSymbols-snap.TotalSymbols)

	diffInt := func(metric string, oldV, newV int) {
		delta := newV - oldV
		if delta < 0 {
			fmt.Printf("%-17s %8d → %-8d %+-4d   improved\n", metric, oldV, newV, delta)
		} else if delta > 0 {
			fmt.Printf("%-17s %8d → %-8d %+-4d   WORSE\n", metric, oldV, newV, delta)
		} else {
			fmt.Printf("%-17s %8d → %-8d %+-4d   unchanged\n", metric, oldV, newV, delta)
		}
	}

	diffFloat := func(metric string, oldV, newV float64) {
		delta := newV - oldV
		if delta < -0.005 {
			fmt.Printf("%-17s %8.2f → %-8.2f %+-5.2f  improved\n", metric, oldV, newV, delta)
		} else if delta > 0.005 {
			fmt.Printf("%-17s %8.2f → %-8.2f %+-5.2f  WORSE\n", metric, oldV, newV, delta)
		} else {
			fmt.Printf("%-17s %8.2f → %-8.2f %+-5.2f  unchanged\n", metric, oldV, newV, delta)
		}
	}

	diffInt("orphan_count", snap.OrphanCount, curr.OrphanCount)
	diffInt("god_objects", len(snap.GodObjects), len(curr.GodObjects))
	diffInt("max_complexity", snap.MaxComplexity, curr.MaxComplexity)
	diffFloat("avg_instability", snap.AvgInstability, curr.AvgInstability)
	diffInt("coupling_edges", snap.CouplingEdges, curr.CouplingEdges)

	return 0
}

func runSnapshotList() int {
	store, err := openSnapshotStore("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening snapshot store: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()
	entries, err := store.ReadDirectory(snapshotDirectory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Println("No snapshots found.")
			return 0
		}
		fmt.Fprintf(os.Stderr, "error reading snapshots directory: %v\n", err)
		return 1
	}

	var snaps []Snapshot
	for _, e := range entries {
		info, infoErr := e.Info()
		if infoErr != nil || !info.Mode().IsRegular() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := store.ReadRegularFile(filepath.Join(snapshotDirectory, e.Name()))
		if err != nil {
			continue
		}
		var s Snapshot
		if err := json.Unmarshal(data, &s); err == nil {
			snaps = append(snaps, s)
		}
	}

	if len(snaps) == 0 {
		fmt.Println("No snapshots found.")
		return 0
	}

	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].Timestamp > snaps[j].Timestamp
	})

	fmt.Printf("%-20s %-25s %s\n", "NAME", "TIMESTAMP", "SYMBOLS")
	fmt.Println(strings.Repeat("-", 60))
	for _, s := range snaps {
		fmt.Printf("%-20s %-25s %d\n", s.Name, s.Timestamp, s.TotalSymbols)
	}

	return 0
}

func runSnapshotDrop(name string) int {
	store, err := openSnapshotStore("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening snapshot store: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()
	if err := store.RemoveRegularFile(snapshotPath(name)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "error: snapshot %q does not exist\n", name)
			return 1
		}
		fmt.Fprintf(os.Stderr, "error deleting snapshot: %v\n", err)
		return 1
	}

	fmt.Printf("Deleted snapshot %q\n", name)
	return 0
}
