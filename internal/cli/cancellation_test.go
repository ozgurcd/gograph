package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ozgurcd/gograph/internal/buildctx"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/graphstate"
	"github.com/ozgurcd/gograph/internal/memorylimit"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestCanceledBuildDoesNotProduceGraphOrMarkFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := t.TempDir()
	if g, err := buildGraphWithTagsContext(ctx, root, nil); g != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("AST result=%v error=%v", g, err)
	}
	if g, err := buildPreciseGraphWithMemoryAndTagsContext(ctx, root, memorylimit.Policy{}, nil); g != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("precise result=%v error=%v", g, err)
	}
	g := &graph.Graph{}
	if err := enrichGraphPreciselyWithMemoryContext(ctx, root, g, buildctx.Config{}, nil, memorylimit.Policy{}); !errors.Is(err, context.Canceled) || g.Build != nil {
		t.Fatalf("cancellation mutated graph or became fallback: %+v %v", g, err)
	}
}

func TestCanceledRefreshDoesNotAdoptCandidateOrPublish(t *testing.T) {
	initial := &graph.Graph{Build: &graph.BuildMetadata{Precision: graph.PrecisionAST}}
	state := graphstate.ManualPersisted(initial, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	build := func(context.Context, string) (*graph.Graph, error) {
		cancel()
		return &graph.Graph{Build: &graph.BuildMetadata{Precision: graph.PrecisionFallback}}, context.Canceled
	}
	published := false
	refresh, getState := graphRefresherWithServingStateContext(initial, state, t.TempDir(), build, build,
		func(context.Context, *graph.Graph, string) search.StaleResult {
			return search.StaleResult{IsStale: true}
		}, true,
		func(context.Context, *graph.Graph) (graphPublication, error) {
			published = true
			return graphPublication{}, nil
		})
	g, err := refresh(ctx)
	if g != nil || !errors.Is(err, context.Canceled) || published || getState() != state {
		t.Fatalf("canceled refresh escaped: graph=%v err=%v published=%v state=%+v", g, err, published, getState())
	}
}

func TestCanceledStagingPreservesEveryPublishedArtifact(t *testing.T) {
	root := writeTestModule(t)
	g := buildTestGraph(t, root)
	if _, err := publishGraphArtifacts(root, g, manualArtifactPublication); err != nil {
		t.Fatal(err)
	}
	readBundle := func() map[string]string {
		result := make(map[string]string)
		entries, err := os.ReadDir(filepath.Join(root, outputDir))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			data, err := os.ReadFile(filepath.Join(root, outputDir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			result[entry.Name()] = graph.SourceDigest(data)
		}
		return result
	}
	before := readBundle()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checks := 0
	_, err := publishGraphArtifactsWithFreshnessContext(ctx, root, g, manualArtifactPublication, func(*graph.Graph, string) search.StaleResult {
		checks++
		if checks == 3 {
			cancel()
		} // Candidate, previous, then staged candidate.
		return search.StaleResult{}
	})
	if !errors.Is(err, context.Canceled) || checks != 3 {
		t.Fatalf("staging cancellation was not exercised: checks=%d err=%v", checks, err)
	}
	if after := readBundle(); !reflect.DeepEqual(before, after) {
		t.Fatalf("canceled staging changed artifacts: before=%v after=%v", before, after)
	}
	if g.Baseline != nil {
		t.Fatal("canceled staging mutated caller snapshot")
	}
}
