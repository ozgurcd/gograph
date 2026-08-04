package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpprotocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
	mcpserver "github.com/ozgurcd/gograph/internal/mcp"
)

func skipCoverageCacheFallback(t *testing.T, g *graph.Graph) {
	t.Helper()
	if testing.CoverMode() == "" || g == nil || g.Build.EffectivePrecision() != graph.PrecisionFallback {
		return
	}
	for _, warning := range g.Build.Warnings {
		if strings.Contains(warning, "reading srcfiles list: cache entry not found") {
			t.Skipf("local Go coverage toolchain cannot provide compiled source metadata to packages.Load: %s", warning)
		}
	}
}

func TestGraphRefresherPreservesPreciseGraphUntilSourceChanges(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	if err := os.WriteFile(source, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initial := &graph.Graph{
		Root:        root,
		GeneratedAt: time.Now().Add(time.Second),
		Build:       &graph.BuildMetadata{Precision: graph.PrecisionPrecise},
		Implements:  []graph.ImplementsEdge{{Interface: "Runner", Concrete: "Service"}},
	}
	astBuilds, preciseBuilds := 0, 0
	refresh := graphRefresher(
		initial,
		root,
		func(string) (*graph.Graph, error) {
			astBuilds++
			return &graph.Graph{Root: root, Build: &graph.BuildMetadata{Precision: graph.PrecisionAST}}, nil
		},
		func(string) (*graph.Graph, error) {
			preciseBuilds++
			return &graph.Graph{
				Root:        root,
				GeneratedAt: time.Now().Add(time.Hour),
				Build:       &graph.BuildMetadata{Precision: graph.PrecisionPrecise},
			}, nil
		},
	)

	got, err := refresh()
	if err != nil {
		t.Fatal(err)
	}
	if got != initial || len(got.Implements) != 1 || astBuilds != 0 || preciseBuilds != 0 {
		t.Fatalf("unchanged source discarded initial precision: graph=%p ast=%d precise=%d implements=%v", got, astBuilds, preciseBuilds, got.Implements)
	}

	newer := initial.GeneratedAt.Add(time.Second)
	if err := os.Chtimes(source, newer, newer); err != nil {
		t.Fatal(err)
	}
	got, err = refresh()
	if err != nil {
		t.Fatal(err)
	}
	if got == initial || astBuilds != 0 || preciseBuilds != 1 {
		t.Fatalf("changed source did not rebuild precisely once: graph=%p ast=%d precise=%d", got, astBuilds, preciseBuilds)
	}
	if _, err := refresh(); err != nil {
		t.Fatal(err)
	}
	if astBuilds != 0 || preciseBuilds != 1 {
		t.Fatalf("unchanged refreshed graph rebuilt again: ast=%d precise=%d", astBuilds, preciseBuilds)
	}
}

func TestGraphRefresherRecomputesPreciseAnalysisAfterSourceEdit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/refresh\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "store.go")
	const before = `package refresh

type Store interface { Delete(string) error }
type MemoryStore struct{}
func (*MemoryStore) Delete(string) error { return nil }
type SQLStore struct{}
func (*SQLStore) Delete(string) error { return nil }
func Purge(store Store) error { return store.Delete("key") }
`
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	initial, err := buildPreciseGraph(root)
	if err != nil {
		skipCoverageCacheFallback(t, initial)
		t.Fatal(err)
	}
	skipCoverageCacheFallback(t, initial)
	if initial.Build.EffectivePrecision() != graph.PrecisionPrecise {
		t.Fatalf("initial precision = %q", initial.Build.EffectivePrecision())
	}

	refresh := graphRefresher(initial, root, BuildGraph, buildPreciseGraph)
	const after = before + "\nfunc AddedAfterRefresh() {}\n"
	if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		t.Fatal(err)
	}
	changedAt := time.Now().UTC()
	if !changedAt.After(initial.GeneratedAt) {
		changedAt = initial.GeneratedAt.Add(time.Nanosecond)
	}
	if err := os.Chtimes(path, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}

	refreshed, err := refresh()
	if err != nil {
		t.Fatal(err)
	}
	if refreshed == initial || refreshed.Build.EffectivePrecision() != graph.PrecisionPrecise {
		t.Fatalf("source refresh lost precise mode: graph=%p initial=%p precision=%q", refreshed, initial, refreshed.Build.EffectivePrecision())
	}
	foundAdded := false
	targets := make(map[string]bool)
	for _, symbol := range refreshed.Symbols {
		if symbol.Name == "AddedAfterRefresh" {
			foundAdded = true
		}
	}
	for _, call := range refreshed.Calls {
		if call.CallerName == "Purge" && call.CalleeRaw == "store.Delete" {
			targets[call.CalleeSymbolID] = true
		}
	}
	if !foundAdded || len(targets) != 2 {
		t.Fatalf("precise refresh did not re-index edit and both targets: added=%v targets=%v", foundAdded, targets)
	}
}

func TestGraphRefresherRebuildsWhenIndexedFileBecomesInactive(t *testing.T) {
	setDeterministicBuildEnvironment(t, "")
	root := t.TempDir()
	writeBuildContextFixture(t, root, "go.mod", "module example.com/inactive-refresh\n\ngo 1.26\n")
	writeBuildContextFixture(t, root, "keep.go", "package refresh\nfunc KeepAfterRefresh() {}\n")
	toggledPath := writeBuildContextFixture(t, root, "toggled.go", "package refresh\nfunc RemovedAfterConstraint() {}\n")

	initial, err := buildPreciseGraph(root)
	if err != nil {
		skipCoverageCacheFallback(t, initial)
		t.Fatal(err)
	}
	preciseBuilds := 0
	refresh := graphRefresher(
		initial,
		root,
		func(string) (*graph.Graph, error) { t.Fatal("unexpected AST build"); return nil, nil },
		func(root string) (*graph.Graph, error) {
			preciseBuilds++
			return buildPreciseGraph(root)
		},
	)

	if err := os.WriteFile(toggledPath, []byte("//go:build ignore\n\npackage refresh\nfunc RemovedAfterConstraint() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	older := initial.GeneratedAt.Add(-time.Minute)
	if err := os.Chtimes(toggledPath, older, older); err != nil {
		t.Fatal(err)
	}

	refreshed, err := refresh()
	if err != nil {
		t.Fatal(err)
	}
	if preciseBuilds != 1 || refreshed.Build.EffectivePrecision() != graph.PrecisionPrecise {
		t.Fatalf("active-to-inactive change did not rebuild precisely once: builds=%d precision=%s", preciseBuilds, refreshed.Build.EffectivePrecision())
	}
	for _, symbol := range refreshed.Symbols {
		if symbol.Name == "RemovedAfterConstraint" {
			t.Fatalf("inactive symbol remained in refreshed graph: %+v", symbol)
		}
	}
	if _, err := refresh(); err != nil {
		t.Fatal(err)
	}
	if preciseBuilds != 1 {
		t.Fatalf("stable refreshed graph rebuilt again: builds=%d", preciseBuilds)
	}
}

func TestGraphRefresherRetriesRequestedPrecisionAfterFallback(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	if err := os.WriteFile(source, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	generated := time.Now().Add(time.Second)
	initial := &graph.Graph{
		Root:        root,
		GeneratedAt: generated,
		Build:       &graph.BuildMetadata{Precision: graph.PrecisionFallback},
	}
	newer := generated.Add(time.Second)
	if err := os.Chtimes(source, newer, newer); err != nil {
		t.Fatal(err)
	}

	astBuilds, preciseBuilds := 0, 0
	refresh := graphRefresher(
		initial,
		root,
		func(string) (*graph.Graph, error) {
			astBuilds++
			return nil, nil
		},
		func(string) (*graph.Graph, error) {
			preciseBuilds++
			return &graph.Graph{
				Root:        root,
				GeneratedAt: newer.Add(time.Second),
				Build:       &graph.BuildMetadata{Precision: graph.PrecisionPrecise},
			}, nil
		},
	)

	got, err := refresh()
	if err != nil {
		t.Fatal(err)
	}
	if astBuilds != 0 || preciseBuilds != 1 {
		t.Fatalf("fallback did not retry precise analysis: ast=%d precise=%d", astBuilds, preciseBuilds)
	}
	if got.Build.EffectivePrecision() != graph.PrecisionPrecise {
		t.Fatalf("retry precision = %q, want %q", got.Build.EffectivePrecision(), graph.PrecisionPrecise)
	}
}

func TestGraphRefresherSurfacesPreciseFallback(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	if err := os.WriteFile(source, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initial := &graph.Graph{
		Root:        root,
		GeneratedAt: time.Now().Add(time.Second),
		Build: &graph.BuildMetadata{
			Complete:  true,
			Precision: graph.PrecisionFallback,
			Warnings:  []string{"precise enrichment failed: unresolved symbol"},
		},
	}
	builds := 0
	refresh := graphRefresher(
		initial,
		root,
		func(string) (*graph.Graph, error) { builds++; return nil, nil },
		func(string) (*graph.Graph, error) { builds++; return nil, nil },
	)

	got, err := refresh()
	if err == nil || !strings.Contains(err.Error(), "precise_fallback") || !strings.Contains(err.Error(), "unresolved symbol") {
		t.Fatalf("fallback error = %v, want visible precision failure", err)
	}
	if got != nil || builds != 0 {
		t.Fatalf("non-stale fallback unexpectedly rebuilt or returned: graph=%v builds=%d", got, builds)
	}
}

func TestGraphRefresherSurfacesFailedPreciseRebuild(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	if err := os.WriteFile(source, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	generated := time.Now().Add(time.Second)
	changedAt := generated.Add(time.Second)
	if err := os.Chtimes(source, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}
	initial := &graph.Graph{
		Root:        root,
		GeneratedAt: generated,
		Build:       &graph.BuildMetadata{Precision: graph.PrecisionPrecise},
	}
	builds := 0
	refresh := graphRefresher(
		initial,
		root,
		func(string) (*graph.Graph, error) { t.Fatal("unexpected AST build"); return nil, nil },
		func(string) (*graph.Graph, error) {
			builds++
			fallback := &graph.Graph{
				Root:        root,
				GeneratedAt: changedAt.Add(time.Second),
				Build: &graph.BuildMetadata{
					Complete:  true,
					Precision: graph.PrecisionFallback,
					Warnings:  []string{"precise enrichment failed: undefined symbol"},
				},
			}
			return fallback, errors.New("precise MCP refresh failed; graph is precise_fallback")
		},
	)

	if got, err := refresh(); got != nil || err == nil || !strings.Contains(err.Error(), "precise_fallback") {
		t.Fatalf("failed precise rebuild was not surfaced: graph=%v err=%v", got, err)
	}
	if got, err := refresh(); got != nil || err == nil || !strings.Contains(err.Error(), "undefined symbol") {
		t.Fatalf("retained fallback was not surfaced: graph=%v err=%v", got, err)
	}
	if builds != 1 {
		t.Fatalf("non-stale retained fallback rebuilt unexpectedly: builds=%d", builds)
	}
}

func TestGraphRefresherRetriesWhenRebuiltGraphIsAlreadyStale(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	if err := os.WriteFile(source, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	generated := time.Now().Add(time.Second)
	changedAt := generated.Add(10 * time.Second)
	if err := os.Chtimes(source, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}
	initial := &graph.Graph{
		Root:        root,
		GeneratedAt: generated,
		Build:       &graph.BuildMetadata{Precision: graph.PrecisionAST},
	}
	builds := 0
	refresh := graphRefresher(
		initial,
		root,
		func(string) (*graph.Graph, error) {
			builds++
			builtAt := changedAt.Add(-time.Second)
			if builds == 2 {
				builtAt = changedAt.Add(time.Second)
			}
			return &graph.Graph{
				Root:        root,
				GeneratedAt: builtAt,
				Build:       &graph.BuildMetadata{Precision: graph.PrecisionAST},
			}, nil
		},
		func(string) (*graph.Graph, error) { t.Fatal("unexpected precise build"); return nil, nil },
	)

	got, err := refresh()
	if err != nil {
		t.Fatal(err)
	}
	if builds != 2 || !got.GeneratedAt.After(changedAt) {
		t.Fatalf("stale rebuild was not retried once: builds=%d graph=%+v", builds, got)
	}
}

func TestGraphRefresherKeepsExplicitASTModeAfterSourceChanges(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	if err := os.WriteFile(source, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	generated := time.Now().Add(time.Second)
	initial := &graph.Graph{
		Root:        root,
		GeneratedAt: generated,
		Build:       &graph.BuildMetadata{Precision: graph.PrecisionAST},
	}
	newer := generated.Add(time.Second)
	if err := os.Chtimes(source, newer, newer); err != nil {
		t.Fatal(err)
	}

	astBuilds, preciseBuilds := 0, 0
	refresh := graphRefresher(
		initial,
		root,
		func(string) (*graph.Graph, error) {
			astBuilds++
			return &graph.Graph{
				Root:        root,
				GeneratedAt: newer.Add(time.Second),
				Build:       &graph.BuildMetadata{Precision: graph.PrecisionAST},
			}, nil
		},
		func(string) (*graph.Graph, error) {
			preciseBuilds++
			return nil, nil
		},
	)

	got, err := refresh()
	if err != nil {
		t.Fatal(err)
	}
	if astBuilds != 1 || preciseBuilds != 0 {
		t.Fatalf("explicit AST graph used wrong refresh policy: ast=%d precise=%d", astBuilds, preciseBuilds)
	}
	if got.Build.EffectivePrecision() != graph.PrecisionAST {
		t.Fatalf("refreshed precision = %q, want %q", got.Build.EffectivePrecision(), graph.PrecisionAST)
	}
}

func TestGraphRefresherAdoptsNewerPersistedPreciseGraph(t *testing.T) {
	root := t.TempDir()
	generated := time.Now().Add(time.Second)
	initial := &graph.Graph{
		Root:        root,
		GeneratedAt: generated,
		Build:       &graph.BuildMetadata{Precision: graph.PrecisionAST},
	}
	builds := 0
	refresh := graphRefresher(
		initial,
		root,
		func(string) (*graph.Graph, error) { builds++; return nil, nil },
		func(string) (*graph.Graph, error) { builds++; return nil, nil },
	)

	if err := os.MkdirAll(filepath.Join(root, outputDir), 0o755); err != nil {
		t.Fatal(err)
	}
	persisted := &graph.Graph{
		Version:     graph.Version,
		Root:        root,
		GeneratedAt: generated.Add(time.Hour),
		Build:       &graph.BuildMetadata{Precision: graph.PrecisionPrecise},
		Implements:  []graph.ImplementsEdge{{Interface: "Runner", Concrete: "Service"}},
	}
	if err := writeJSON(filepath.Join(root, graphFile), currentPolicyGraph(persisted)); err != nil {
		t.Fatal(err)
	}

	got, err := refresh()
	if err != nil {
		t.Fatal(err)
	}
	if builds != 0 {
		t.Fatalf("persisted precise graph triggered rebuild: builds=%d", builds)
	}
	if !got.GeneratedAt.Equal(persisted.GeneratedAt) || got.Build.EffectivePrecision() != graph.PrecisionPrecise || len(got.Implements) != 1 {
		t.Fatalf("newer persisted precise graph not adopted: %+v", got)
	}
}

func TestGraphRefresherAdoptsLaterPublishedPreciseGraphWithEarlierGeneratedAt(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, outputDir), 0o755); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(root, graphFile)
	generated := time.Now().Add(time.Hour)
	initial := &graph.Graph{
		Version:     graph.Version,
		Root:        root,
		GeneratedAt: generated,
		Build: &graph.BuildMetadata{
			Complete:  true,
			Precision: graph.PrecisionFallback,
			Warnings:  []string{"precise enrichment failed: overlapping in-memory build"},
		},
	}
	if err := writeJSON(artifactPath, currentPolicyGraph(initial)); err != nil {
		t.Fatal(err)
	}

	builds := 0
	refresh := graphRefresher(
		initial,
		root,
		func(string) (*graph.Graph, error) { builds++; return nil, errors.New("unexpected AST rebuild") },
		func(string) (*graph.Graph, error) { builds++; return nil, errors.New("unexpected precise rebuild") },
	)

	// GeneratedAt records build start, not publication. This precise build
	// started earlier but completed and atomically replaced graph.json later.
	persisted := &graph.Graph{
		Version:     graph.Version,
		Root:        root,
		GeneratedAt: generated.Add(-time.Hour),
		Build:       &graph.BuildMetadata{Complete: true, Precision: graph.PrecisionPrecise},
		Implements:  []graph.ImplementsEdge{{Interface: "Runner", Concrete: "Service"}},
	}
	if err := writeJSON(artifactPath, currentPolicyGraph(persisted)); err != nil {
		t.Fatal(err)
	}

	got, err := refresh()
	if err != nil {
		t.Fatalf("later precise publication was ignored: %v", err)
	}
	if builds != 0 {
		t.Fatalf("later precise publication triggered %d in-memory build(s), want 0", builds)
	}
	if !got.GeneratedAt.Equal(persisted.GeneratedAt) || got.Build.EffectivePrecision() != graph.PrecisionPrecise || len(got.Implements) != 1 {
		t.Fatalf("later precise publication with earlier GeneratedAt was not adopted: %+v", got)
	}
}

func TestRunningMCPHandlerAdoptsNewerPersistedPreciseGraph(t *testing.T) {
	root := t.TempDir()
	generated := time.Now().Add(-time.Hour)
	initial := &graph.Graph{
		Version:     graph.Version,
		Root:        root,
		GeneratedAt: generated,
		Build:       &graph.BuildMetadata{Complete: true, Precision: graph.PrecisionAST},
	}
	builds := 0
	refresh := graphRefresher(
		initial,
		root,
		func(string) (*graph.Graph, error) {
			builds++
			return nil, errors.New("unexpected AST rebuild")
		},
		func(string) (*graph.Graph, error) {
			builds++
			return nil, errors.New("unexpected precise rebuild")
		},
	)
	handlers := exposeMCPRefreshHandlers(t, initial, refresh)

	if err := os.MkdirAll(filepath.Join(root, outputDir), 0o755); err != nil {
		t.Fatal(err)
	}
	const (
		interfaceID = "example.com/app/repository::StateRepository"
		memoryID    = "example.com/app/repository::(*MemoryStateRepository).Delete"
		sqlID       = "example.com/app/repository::(*SQLStateRepository).Delete"
		callerID    = "example.com/app/service::Purge"
	)
	persisted := &graph.Graph{
		Version:     graph.Version,
		Root:        root,
		GeneratedAt: generated.Add(30 * time.Minute),
		Build:       &graph.BuildMetadata{Complete: true, Precision: graph.PrecisionPrecise},
		Symbols: []graph.SymbolNode{
			{ID: interfaceID, Kind: graph.KindInterface, Name: "StateRepository", PackageName: "repository", InterfaceMethods: map[string]string{"Delete": "func(string) error"}},
			{ID: memoryID, Kind: graph.KindMethod, Name: "Delete", Receiver: "*MemoryStateRepository", PackageName: "repository"},
			{ID: sqlID, Kind: graph.KindMethod, Name: "Delete", Receiver: "*SQLStateRepository", PackageName: "repository"},
			{ID: callerID, Kind: graph.KindFunction, Name: "Purge", PackageName: "service", File: "service/purge.go", Line: 10},
		},
		Implements: []graph.ImplementsEdge{
			{Interface: "StateRepository", InterfaceID: interfaceID, Concrete: "MemoryStateRepository", ConcreteID: "example.com/app/repository::MemoryStateRepository"},
			{Interface: "StateRepository", InterfaceID: interfaceID, Concrete: "SQLStateRepository", ConcreteID: "example.com/app/repository::SQLStateRepository"},
		},
		Calls: []graph.CallEdge{
			{CallerSymbolID: callerID, CallerName: "Purge", CalleeRaw: "states.Delete", CalleeSymbolID: memoryID, File: "service/purge.go", Line: 12, Column: 20},
			{CallerSymbolID: callerID, CallerName: "Purge", CalleeRaw: "states.Delete", CalleeSymbolID: sqlID, File: "service/purge.go", Line: 12, Column: 20},
		},
	}
	if err := writeJSON(filepath.Join(root, graphFile), currentPolicyGraph(persisted)); err != nil {
		t.Fatal(err)
	}

	request := mcpprotocol.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"function": "StateRepository.Delete",
		"exact":    true,
	}
	handler := handlers["gograph_callers"]
	if handler == nil {
		t.Fatal("gograph_callers handler was not registered")
	}
	result, err := handler(context.Background(), request)
	if err != nil {
		t.Fatalf("gograph_callers handler: %v", err)
	}
	if result.IsError {
		t.Fatalf("gograph_callers returned an error result: %s", mcpResultText(t, result))
	}
	text := mcpResultText(t, result)
	if count := strings.Count(text, "[caller]"); count != 1 || !strings.Contains(text, "Purge") || !strings.Contains(text, "service/purge.go:12:20") {
		t.Fatalf("running handler did not adopt the precise interface graph once: callers=%d\n%s", count, text)
	}
	if builds != 0 {
		t.Fatalf("newer precise artifact triggered %d in-memory build(s), want 0", builds)
	}
}

func TestRunningMCPHandlerSurfacesPreciseFallback(t *testing.T) {
	root := t.TempDir()
	initial := &graph.Graph{
		Version:     graph.Version,
		Root:        root,
		GeneratedAt: time.Now(),
		Build: &graph.BuildMetadata{
			Complete:  true,
			Precision: graph.PrecisionFallback,
			Warnings:  []string{"precise enrichment failed: unresolved interface target"},
		},
	}
	builds := 0
	refresh := graphRefresher(
		initial,
		root,
		func(string) (*graph.Graph, error) { builds++; return nil, nil },
		func(string) (*graph.Graph, error) { builds++; return nil, nil },
	)
	handlers := exposeMCPRefreshHandlers(t, initial, refresh)

	request := mcpprotocol.CallToolRequest{}
	request.Params.Arguments = map[string]any{"function": "StateRepository.Delete", "exact": true}
	handler := handlers["gograph_callers"]
	if handler == nil {
		t.Fatal("gograph_callers handler was not registered")
	}
	result, err := handler(context.Background(), request)
	if err != nil {
		t.Fatalf("gograph_callers handler: %v", err)
	}
	text := mcpResultText(t, result)
	if !result.IsError || !strings.Contains(text, "precise_fallback") || !strings.Contains(text, "unresolved interface target") {
		t.Fatalf("precise fallback was not visible to the MCP client: is_error=%v text=%q", result.IsError, text)
	}
	if builds != 0 {
		t.Fatalf("non-stale fallback unexpectedly triggered %d build(s)", builds)
	}
}

func exposeMCPRefreshHandlers(
	t *testing.T,
	initial *graph.Graph,
	refresh func() (*graph.Graph, error),
) map[string]func(context.Context, mcpprotocol.CallToolRequest) (*mcpprotocol.CallToolResult, error) {
	t.Helper()
	previous := mcpserver.ExposeToolsForTesting
	handlers := make(map[string]func(context.Context, mcpprotocol.CallToolRequest) (*mcpprotocol.CallToolResult, error))
	mcpserver.ExposeToolsForTesting = handlers
	t.Cleanup(func() { mcpserver.ExposeToolsForTesting = previous })
	mcpserver.NewServer(
		initial,
		refresh,
		func(string) (*graph.Graph, error) {
			t.Fatal("unexpected directory graph build")
			return nil, nil
		},
		func(context.Context, string) (*graph.Graph, error) {
			t.Fatal("unexpected baseline graph build")
			return nil, nil
		},
		"test",
	)
	return handlers
}

func mcpResultText(t *testing.T, result *mcpprotocol.CallToolResult) string {
	t.Helper()
	if result == nil || len(result.Content) == 0 {
		t.Fatal("MCP result contained no text content")
	}
	content, ok := result.Content[0].(mcpprotocol.TextContent)
	if !ok {
		t.Fatalf("MCP result content type = %T, want TextContent", result.Content[0])
	}
	return content.Text
}

func TestGraphRefresherRetriesArtifactAfterLoadFailure(t *testing.T) {
	root := t.TempDir()
	generated := time.Now().Add(time.Second)
	initial := &graph.Graph{
		Root:        root,
		GeneratedAt: generated,
		Build:       &graph.BuildMetadata{Precision: graph.PrecisionAST},
	}
	refresh := graphRefresher(
		initial,
		root,
		func(string) (*graph.Graph, error) { t.Fatal("unexpected AST rebuild"); return nil, nil },
		func(string) (*graph.Graph, error) { t.Fatal("unexpected precise rebuild"); return nil, nil },
	)

	if err := os.MkdirAll(filepath.Join(root, outputDir), 0o755); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(root, graphFile)
	if err := os.WriteFile(artifactPath, []byte("not json"), 0o640); err != nil {
		t.Fatal(err)
	}
	got, err := refresh()
	if err != nil {
		t.Fatal(err)
	}
	if got != initial {
		t.Fatalf("invalid artifact replaced current graph: got=%p want=%p", got, initial)
	}

	persisted := &graph.Graph{
		Version:     graph.Version,
		Root:        root,
		GeneratedAt: generated.Add(time.Hour),
		Build:       &graph.BuildMetadata{Precision: graph.PrecisionPrecise},
	}
	if err := writeJSON(artifactPath, currentPolicyGraph(persisted)); err != nil {
		t.Fatal(err)
	}
	got, err = refresh()
	if err != nil {
		t.Fatal(err)
	}
	if !got.GeneratedAt.Equal(persisted.GeneratedAt) || got.Build.EffectivePrecision() != graph.PrecisionPrecise {
		t.Fatalf("valid replacement was not retried and adopted: %+v", got)
	}
}

func TestGraphRefresherDoesNotAdoptNewerASTOverPreciseGraph(t *testing.T) {
	root := t.TempDir()
	generated := time.Now().Add(time.Second)
	initial := &graph.Graph{
		Root:        root,
		GeneratedAt: generated,
		Build:       &graph.BuildMetadata{Precision: graph.PrecisionPrecise},
		Implements:  []graph.ImplementsEdge{{Interface: "Runner", Concrete: "Service"}},
	}
	refresh := graphRefresher(
		initial,
		root,
		func(string) (*graph.Graph, error) { t.Fatal("unexpected AST rebuild"); return nil, nil },
		func(string) (*graph.Graph, error) { t.Fatal("unexpected precise rebuild"); return nil, nil },
	)

	if err := os.MkdirAll(filepath.Join(root, outputDir), 0o755); err != nil {
		t.Fatal(err)
	}
	persisted := &graph.Graph{
		Version:     graph.Version,
		Root:        root,
		GeneratedAt: generated.Add(time.Hour),
		Build:       &graph.BuildMetadata{Precision: graph.PrecisionAST},
	}
	if err := writeJSON(filepath.Join(root, graphFile), currentPolicyGraph(persisted)); err != nil {
		t.Fatal(err)
	}

	got, err := refresh()
	if err != nil {
		t.Fatal(err)
	}
	if got != initial {
		t.Fatalf("newer AST artifact downgraded precise graph: got=%p want=%p", got, initial)
	}
}

func TestGraphArtifactChangedDetectsEqualSizeAtomicReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "graph.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous := graphArtifactInfo(path)
	if previous == nil {
		t.Fatal("missing initial artifact info")
	}

	replacement := filepath.Join(root, "replacement.json")
	if err := os.WriteFile(replacement, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, previous.ModTime(), previous.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}

	current := graphArtifactInfo(path)
	if !graphArtifactChanged(previous, current) {
		t.Fatal("equal-size, equal-modtime atomic replacement was not detected")
	}
}

func TestGraphArtifactInfoRejectsLinkedArtifactEntries(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	outsideDirectory := filepath.Join(base, "outside-artifacts")
	if err := os.MkdirAll(filepath.Join(root, outputDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideGraph := filepath.Join(outsideDirectory, "graph.json")
	if err := os.WriteFile(outsideGraph, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(root, graphFile)
	if err := os.Symlink(outsideGraph, artifactPath); err != nil {
		t.Skipf("create linked graph artifact: %v", err)
	}
	if info := graphArtifactInfo(artifactPath); info != nil {
		t.Fatalf("linked graph artifact returned info: %+v", info)
	}
	if err := os.Remove(artifactPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, outputDir)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDirectory, filepath.Join(root, outputDir)); err != nil {
		t.Skipf("create linked artifact directory: %v", err)
	}
	if info := graphArtifactInfo(artifactPath); info != nil {
		t.Fatalf("graph below linked artifact directory returned info: %+v", info)
	}
}

func TestRunBuildPersistsPrecisionStatus(t *testing.T) {
	tests := []struct {
		name         string
		source       string
		want         graph.PrecisionMode
		wantWarning  bool
		wantComplete bool
	}{
		{
			name:         "precise success",
			source:       "package main\nfunc main() {}\n",
			want:         graph.PrecisionPrecise,
			wantComplete: true,
		},
		{
			name:         "precise fallback",
			source:       "package main\nfunc main() { missingSymbol() }\n",
			want:         graph.PrecisionFallback,
			wantWarning:  true,
			wantComplete: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/precisiontest\n\ngo 1.26\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(tt.source), 0o644); err != nil {
				t.Fatal(err)
			}

			if code := runBuild([]string{root, "--precise"}); code != 0 {
				t.Fatalf("runBuild exit code = %d, want 0", code)
			}
			persisted, err := loadGraph(root)
			if err != nil {
				t.Fatal(err)
			}
			if tt.want == graph.PrecisionPrecise {
				skipCoverageCacheFallback(t, persisted)
			}
			if got := persisted.Build.EffectivePrecision(); got != tt.want {
				t.Fatalf("persisted precision = %q, want %q", got, tt.want)
			}
			if persisted.Build.Complete != tt.wantComplete {
				t.Fatalf("persisted complete = %v, want %v", persisted.Build.Complete, tt.wantComplete)
			}
			hasPrecisionWarning := false
			for _, warning := range persisted.Build.Warnings {
				if strings.Contains(warning, "precise enrichment failed") {
					hasPrecisionWarning = true
					break
				}
			}
			if hasPrecisionWarning != tt.wantWarning {
				t.Fatalf("precision warning present = %v, want %v; warnings=%v", hasPrecisionWarning, tt.wantWarning, persisted.Build.Warnings)
			}
		})
	}
}
