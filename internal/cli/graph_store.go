package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/ozgurcd/gograph/internal/buildctx"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/graphstore"
	"github.com/ozgurcd/gograph/internal/search"
)

// preciseFromStore returns the precise graph for ast's exact selected source
// and build context from the shared graph store when another checkout already
// produced it, and otherwise runs enrich on ast and stores the result. Checkouts
// building the same content at the same time build it once. A stored graph is
// used only after it is verified against this checkout's files.
func preciseFromStore(absRoot string, ast *graph.Graph, config buildctx.Config, configErr error, enrich func() error) (*graph.Graph, bool, error) {
	store, ok := graphstore.Open()
	if !ok || configErr != nil {
		return ast, false, enrich()
	}
	if err := refreshCompleteGraphSourceFingerprint(absRoot, ast, config); err != nil {
		return ast, false, enrich()
	}
	analyzer := graphstore.AnalyzerIdentity(Version)
	key, ok := graphstore.Key(ast, graph.PrecisionPrecise, analyzer)
	if !ok {
		return ast, false, enrich()
	}
	if stored, ok := loadStoredGraph(store, key, absRoot, ast, config); ok {
		return stored, true, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), buildWaitLimit())
	defer cancel()
	unlock, waited, lockErr := store.Lock(ctx, key)
	if lockErr == nil {
		defer unlock()
		if waited {
			if stored, ok := loadStoredGraph(store, key, absRoot, ast, config); ok {
				return stored, true, nil
			}
		}
	}
	if err := enrich(); err != nil {
		return ast, false, err
	}
	if ast.Build == nil || ast.Build.EffectivePrecision() != graph.PrecisionPrecise {
		return ast, false, nil
	}
	sortGraph(ast)
	if err := refreshCompleteGraphSourceFingerprint(absRoot, ast, config); err != nil {
		return ast, false, nil
	}
	if storedKey, ok := graphstore.Key(ast, graph.PrecisionPrecise, analyzer); ok && storedKey == key {
		if data, err := json.Marshal(ast); err == nil {
			if err := store.Save(key, data); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not store the precise graph for other checkouts: %v\n", err)
			}
		}
	}
	return ast, false, nil
}

// loadStoredGraph loads key and accepts it only when it is a complete, current
// policy precise graph of the same source and build context as ast, and is
// fresh against this checkout's files.
func loadStoredGraph(store *graphstore.Store, key, absRoot string, ast *graph.Graph, config buildctx.Config) (*graph.Graph, bool) {
	data, ok := store.Load(key)
	if !ok {
		return nil, false
	}
	var stored graph.Graph
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, false
	}
	if !stored.UsesCurrentSourcePolicy() || stored.Build == nil || !stored.Build.Complete ||
		stored.Build.EffectivePrecision() != graph.PrecisionPrecise ||
		stored.Build.SourceFingerprint != ast.Build.SourceFingerprint {
		return nil, false
	}
	// The same selected source and selection inputs; adopt this checkout's
	// location-bound context so freshness is judged against this checkout.
	stored.Root = absRoot
	stored.Build.BuildContextFingerprint = ast.Build.BuildContextFingerprint
	stored.Build.Selection = ast.Build.Selection
	if search.StaleWithConfig(&stored, absRoot, config).IsStale {
		return nil, false
	}
	return &stored, true
}
