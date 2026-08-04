package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/report"
	"github.com/ozgurcd/gograph/internal/search"
)

func writeTestModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/persist\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func buildTestGraph(t *testing.T, root string) *graph.Graph {
	t.Helper()
	g, err := BuildGraph(root)
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	return g
}

func TestPublishGraphArtifactsWritesCompleteBundleAndBaseline(t *testing.T) {
	root := writeTestModule(t)
	first := buildTestGraph(t, root)
	first.Imports = append(first.Imports,
		graph.ImportEdge{FromPackage: "example.com/persist", ImportPath: "fmt"},
		graph.ImportEdge{FromPackage: "example.com/persist", ImportPath: "os"},
	)
	publication, err := publishGraphArtifacts(root, first, manualArtifactPublication)
	if err != nil {
		t.Fatal(err)
	}
	if !publication.Published || publication.Graph != first {
		t.Fatalf("first publication = %+v, want candidate published", publication)
	}
	if first.Baseline != nil {
		t.Fatalf("first graph baseline = %+v, want nil", first.Baseline)
	}
	if _, err := os.Stat(filepath.Join(root, ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf("background-capable publisher changed .gitignore: %v", err)
	}

	previous, err := loadGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	wantBaseline := graphBaseline(previous)
	source := filepath.Join(root, "main.go")
	if err := os.WriteFile(source, []byte("package main\nfunc main() { helper() }\nfunc helper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changedAt := time.Now().Add(10 * time.Millisecond)
	if err := os.Chtimes(source, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(changedAt) + time.Millisecond)
	second := buildTestGraph(t, root)
	publication, err = publishGraphArtifacts(root, second, manualArtifactPublication)
	if err != nil {
		t.Fatal(err)
	}
	if !publication.Published {
		t.Fatal("second graph was not published")
	}
	persisted, err := loadGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Baseline == nil || *persisted.Baseline != *wantBaseline {
		t.Fatalf("persisted baseline = %+v, want %+v", persisted.Baseline, wantBaseline)
	}

	reports := map[string]string{
		reportFile: report.GenerateIndex(persisted),
		symFile:    report.GenerateSymbols(persisted),
		depsFile:   report.GenerateDeps(persisted),
		routesFile: report.GenerateRoutes(persisted),
		sqlFile:    report.GenerateSQL(persisted),
		errorsFile: report.GenerateErrors(persisted),
		configFile: report.GenerateConfig(persisted),
		concFile:   report.GenerateConcurrency(persisted),
		testsFile:  report.GenerateTests(persisted),
	}
	for relPath, want := range reports {
		path := filepath.Join(root, relPath)
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", relPath, err)
		}
		if string(got) != want {
			t.Errorf("report %s does not match persisted graph", relPath)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if gotMode := info.Mode().Perm(); runtime.GOOS != "windows" && gotMode != 0o640 {
			t.Errorf("report %s mode = %o, want 640", relPath, gotMode)
		}
	}
	info, err := os.Stat(filepath.Join(root, graphFile))
	if err != nil {
		t.Fatal(err)
	}
	if gotMode := info.Mode().Perm(); runtime.GOOS != "windows" && gotMode != 0o640 {
		t.Errorf("graph.json mode = %o, want 640", gotMode)
	}
}

func TestPublishGraphArtifactsKeepsFreshPreciseGraphOverAST(t *testing.T) {
	root := writeTestModule(t)
	precise := buildTestGraph(t, root)
	precise.Build.Precision = graph.PrecisionPrecise
	if _, err := publishGraphArtifacts(root, precise, refreshArtifactPublication); err != nil {
		t.Fatal(err)
	}

	ast := buildTestGraph(t, root)
	publication, err := publishGraphArtifacts(root, ast, refreshArtifactPublication)
	if err != nil {
		t.Fatal(err)
	}
	if publication.Published {
		t.Fatal("AST graph downgraded a fresh precise artifact")
	}
	if got := publication.Graph.Build.EffectivePrecision(); got != graph.PrecisionPrecise {
		t.Fatalf("winning precision = %q, want %q", got, graph.PrecisionPrecise)
	}
	persisted, err := loadGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.Build.EffectivePrecision(); got != graph.PrecisionPrecise {
		t.Fatalf("persisted precision = %q, want %q", got, graph.PrecisionPrecise)
	}
}

func TestManualGraphArtifactPublicationCanDowngradePrecision(t *testing.T) {
	root := writeTestModule(t)
	precise := buildTestGraph(t, root)
	precise.Build.Precision = graph.PrecisionPrecise
	if _, err := publishGraphArtifacts(root, precise, manualArtifactPublication); err != nil {
		t.Fatal(err)
	}

	ast := buildTestGraph(t, root)
	publication, err := publishGraphArtifacts(root, ast, manualArtifactPublication)
	if err != nil {
		t.Fatal(err)
	}
	if !publication.Published {
		t.Fatal("explicit manual AST build was not published")
	}
	persisted, err := loadGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.Build.EffectivePrecision(); got != graph.PrecisionAST {
		t.Fatalf("manual build precision = %q, want %q", got, graph.PrecisionAST)
	}
}

func TestManualPreciseFallbackKeepsFreshPreciseArtifact(t *testing.T) {
	root := writeTestModule(t)
	precise := buildTestGraph(t, root)
	precise.Build.Precision = graph.PrecisionPrecise
	if _, err := publishGraphArtifacts(root, precise, manualArtifactPublication); err != nil {
		t.Fatal(err)
	}

	fallback := buildTestGraph(t, root)
	fallback.Build.Precision = graph.PrecisionFallback
	fallback.Build.Warnings = append(fallback.Build.Warnings, "precise enrichment failed: transient loader failure")
	publication, err := publishGraphArtifacts(root, fallback, manualArtifactPublication)
	if err != nil {
		t.Fatal(err)
	}
	if publication.Published {
		t.Fatal("failed precise retry replaced a fresh precise artifact")
	}
	if got := publication.Graph.Build.EffectivePrecision(); got != graph.PrecisionPrecise {
		t.Fatalf("retained precision = %q, want %q", got, graph.PrecisionPrecise)
	}
	persisted, err := loadGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.Build.EffectivePrecision(); got != graph.PrecisionPrecise {
		t.Fatalf("persisted precision = %q, want %q", got, graph.PrecisionPrecise)
	}
}

func TestConcurrentGraphArtifactPublishersConvergeOnPrecise(t *testing.T) {
	root := writeTestModule(t)
	ast := buildTestGraph(t, root)
	preciseValue := *ast
	preciseBuild := *ast.Build
	preciseBuild.Precision = graph.PrecisionPrecise
	preciseValue.Build = &preciseBuild
	precise := &preciseValue

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, candidate := range []*graph.Graph{ast, precise} {
		wg.Add(1)
		go func(g *graph.Graph) {
			defer wg.Done()
			<-start
			_, err := publishGraphArtifacts(root, g, refreshArtifactPublication)
			errs <- err
		}(candidate)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent publication: %v", err)
		}
	}
	persisted, err := loadGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.Build.EffectivePrecision(); got != graph.PrecisionPrecise {
		t.Fatalf("final precision = %q, want %q", got, graph.PrecisionPrecise)
	}
}

func TestCrossProcessGraphArtifactPublishersConvergeOnPrecise(t *testing.T) {
	root := writeTestModule(t)
	gate := filepath.Join(root, "publication.start")

	type childProcess struct {
		name    string
		ready   string
		command *exec.Cmd
		output  *bytes.Buffer
	}
	children := make([]childProcess, 0, 2)
	for _, precision := range []string{string(graph.PrecisionAST), string(graph.PrecisionPrecise)} {
		ready := filepath.Join(root, ".publication-"+precision+".ready")
		command := exec.Command(os.Args[0], "-test.run=^TestGraphArtifactPublicationProcessHelper$")
		command.Env = append(os.Environ(),
			"GOGRAPH_ARTIFACT_PROCESS_HELPER=1",
			"GOGRAPH_ARTIFACT_ROOT="+root,
			"GOGRAPH_ARTIFACT_GATE="+gate,
			"GOGRAPH_ARTIFACT_READY="+ready,
			"GOGRAPH_ARTIFACT_PRECISION="+precision,
		)
		output := &bytes.Buffer{}
		command.Stdout = output
		command.Stderr = output
		if err := command.Start(); err != nil {
			t.Fatalf("start %s publisher: %v", precision, err)
		}
		children = append(children, childProcess{name: precision, ready: ready, command: command, output: output})
	}
	t.Cleanup(func() {
		for _, child := range children {
			if child.command.Process != nil {
				_ = child.command.Process.Kill()
			}
		}
	})

	deadline := time.Now().Add(10 * time.Second)
	for _, child := range children {
		for {
			if _, err := os.Stat(child.ready); err == nil {
				break
			} else if !os.IsNotExist(err) {
				t.Fatalf("check %s publisher readiness: %v", child.name, err)
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s publisher", child.name)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if err := os.WriteFile(gate, []byte("start"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, child := range children {
		if err := child.command.Wait(); err != nil {
			t.Fatalf("%s publisher failed: %v\n%s", child.name, err, child.output.String())
		}
	}

	persisted, err := loadGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.Build.EffectivePrecision(); got != graph.PrecisionPrecise {
		t.Fatalf("cross-process final precision = %q, want %q", got, graph.PrecisionPrecise)
	}
}

func TestGraphArtifactPublicationProcessHelper(t *testing.T) {
	if os.Getenv("GOGRAPH_ARTIFACT_PROCESS_HELPER") != "1" {
		return
	}
	root := os.Getenv("GOGRAPH_ARTIFACT_ROOT")
	gate := os.Getenv("GOGRAPH_ARTIFACT_GATE")
	ready := os.Getenv("GOGRAPH_ARTIFACT_READY")
	precision := graph.PrecisionMode(os.Getenv("GOGRAPH_ARTIFACT_PRECISION"))
	if root == "" || gate == "" || ready == "" {
		t.Fatal("artifact publication helper environment is incomplete")
	}
	candidate := buildTestGraph(t, root)
	candidate.Build.Precision = precision
	if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(gate); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for publication gate")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := publishGraphArtifacts(root, candidate, refreshArtifactPublication); err != nil {
		t.Fatal(err)
	}
}

func TestPublishGraphArtifactsRefusesKnownStaleCandidate(t *testing.T) {
	root := writeTestModule(t)
	candidate := buildTestGraph(t, root)
	source := filepath.Join(root, "main.go")
	if err := os.WriteFile(source, []byte("package main\nfunc changed() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changedAt := candidate.GeneratedAt.Add(2 * time.Second)
	if err := os.Chtimes(source, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := publishGraphArtifacts(root, candidate, manualArtifactPublication); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale publication error = %v, want stale rejection", err)
	}
	if _, err := os.Stat(filepath.Join(root, graphFile)); !os.IsNotExist(err) {
		t.Fatalf("stale candidate created graph.json: %v", err)
	}
}

func TestPublishGraphArtifactsRejectsLinkedArtifactDirectory(t *testing.T) {
	root := writeTestModule(t)
	outside := filepath.Join(t.TempDir(), "outside-artifacts")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinelPath := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinelPath, []byte("unchanged"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, outputDir)); err != nil {
		t.Skipf("create artifact directory symlink: %v", err)
	}

	candidate := buildTestGraph(t, root)
	if _, err := publishGraphArtifacts(root, candidate, manualArtifactPublication); err == nil || !strings.Contains(err.Error(), "unsafe artifact directory") {
		t.Fatalf("publication error = %v, want unsafe artifact directory refusal", err)
	}
	data, err := os.ReadFile(sentinelPath)
	if err != nil || string(data) != "unchanged" {
		t.Fatalf("outside sentinel = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "graph.json")); !os.IsNotExist(err) {
		t.Fatalf("publication wrote through linked artifact directory: %v", err)
	}
}

func TestPublishGraphArtifactsRejectsLinkedArtifactLock(t *testing.T) {
	root := writeTestModule(t)
	artifactDir := filepath.Join(root, outputDir)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside-lock")
	if err := os.WriteFile(outside, []byte("unchanged"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(artifactDir, artifactLockFile)); err != nil {
		t.Skipf("create artifact lock symlink: %v", err)
	}

	candidate := buildTestGraph(t, root)
	if _, err := publishGraphArtifacts(root, candidate, manualArtifactPublication); err == nil || !strings.Contains(err.Error(), "unsafe artifact lock") {
		t.Fatalf("publication error = %v, want unsafe artifact lock refusal", err)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "unchanged" {
		t.Fatalf("outside lock target = %q, %v", data, err)
	}
}

func TestRenderGraphArtifactsKeepsGraphJSONLast(t *testing.T) {
	payloads, err := renderGraphArtifacts(&graph.Graph{Version: graph.Version})
	if err != nil {
		t.Fatal(err)
	}
	if len(payloads) != graphReportCount+1 {
		t.Fatalf("artifact count = %d, want %d", len(payloads), graphReportCount+1)
	}
	if got := payloads[len(payloads)-1].relPath; got != graphFile {
		t.Fatalf("last artifact = %q, want %q", got, graphFile)
	}
}

func TestStageGraphArtifactsCleansTempsAfterFailure(t *testing.T) {
	root := t.TempDir()
	if _, err := stageGraphArtifacts(root, []artifactPayload{
		{relPath: "first.txt", data: []byte("first")},
		{relPath: filepath.Join("missing", "second.txt"), data: []byte("second")},
	}); err == nil {
		t.Fatal("stageGraphArtifacts succeeded with a missing directory")
	}
	temps, err := filepath.Glob(filepath.Join(root, ".first.txt-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 0 {
		t.Fatalf("temporary artifacts remain after staging failure: %v", temps)
	}
}

func TestPrepareMCPGraphPersistsOnlyWhenEnabled(t *testing.T) {
	for _, tt := range []struct {
		name    string
		persist bool
	}{
		{name: "disabled"},
		{name: "enabled", persist: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := writeTestModule(t)
			g, gotRoot, err := prepareMCPGraph(mcpOptions{Root: root, PersistRefresh: tt.persist})
			if err != nil {
				t.Fatal(err)
			}
			if g == nil || gotRoot != root {
				t.Fatalf("prepared graph/root = %v/%q, want graph/%q", g, gotRoot, root)
			}
			_, statErr := os.Stat(filepath.Join(root, graphFile))
			if tt.persist && statErr != nil {
				t.Fatalf("enabled persistence did not create graph.json: %v", statErr)
			}
			if !tt.persist && !os.IsNotExist(statErr) {
				t.Fatalf("disabled persistence changed graph.json: %v", statErr)
			}
			if _, err := os.Stat(filepath.Join(root, ".gitignore")); !os.IsNotExist(err) {
				t.Fatalf("MCP startup changed .gitignore: %v", err)
			}
		})
	}
}

func TestGraphRefresherPublishesOnlyFinalFreshRetry(t *testing.T) {
	root := writeTestModule(t)
	source := filepath.Join(root, "main.go")
	initial := buildTestGraph(t, root)
	changedAt := initial.GeneratedAt.Add(5 * time.Second)
	if err := os.Chtimes(source, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}

	builds := 0
	publishes := 0
	var published *graph.Graph
	refresh := graphRefresher(
		initial,
		root,
		func(string) (*graph.Graph, error) {
			builds++
			built := *initial
			built.GeneratedAt = changedAt.Add(-time.Second)
			if builds == 2 {
				built.GeneratedAt = changedAt.Add(time.Second)
			}
			return &built, nil
		},
		func(string) (*graph.Graph, error) { return nil, errors.New("unexpected precise build") },
		func(g *graph.Graph) (graphPublication, error) {
			publishes++
			published = g
			return graphPublication{Graph: g, Published: true}, nil
		},
	)
	got, err := refresh()
	if err != nil {
		t.Fatal(err)
	}
	if builds != 2 || publishes != 1 || got != published || !got.GeneratedAt.After(changedAt) {
		t.Fatalf("refresh builds=%d publishes=%d got=%p published=%p generated=%s", builds, publishes, got, published, got.GeneratedAt)
	}
}

func TestGraphRefresherRetriesFailedPublicationWithoutRebuild(t *testing.T) {
	root := writeTestModule(t)
	source := filepath.Join(root, "main.go")
	initial := buildTestGraph(t, root)
	changedAt := initial.GeneratedAt.Add(5 * time.Second)
	if err := os.Chtimes(source, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}

	builds := 0
	publishes := 0
	refresh := graphRefresher(
		initial,
		root,
		func(string) (*graph.Graph, error) {
			builds++
			built := *initial
			built.GeneratedAt = changedAt.Add(time.Second)
			return &built, nil
		},
		func(string) (*graph.Graph, error) { return nil, errors.New("unexpected precise build") },
		func(g *graph.Graph) (graphPublication, error) {
			publishes++
			if publishes == 1 {
				return graphPublication{}, errors.New("disk unavailable")
			}
			return graphPublication{Graph: g, Published: true}, nil
		},
	)
	if got, err := refresh(); got != nil || err == nil || !strings.Contains(err.Error(), "disk unavailable") {
		t.Fatalf("first refresh graph=%v err=%v, want visible publication failure", got, err)
	}
	got, err := refresh()
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || builds != 1 || publishes != 2 {
		t.Fatalf("retry graph=%v builds=%d publishes=%d, want graph/1/2", got, builds, publishes)
	}
}

func TestGraphRefresherDoesNotRepublishOwnArtifact(t *testing.T) {
	root := writeTestModule(t)
	source := filepath.Join(root, "main.go")
	initial := buildTestGraph(t, root)
	changedAt := initial.GeneratedAt.Add(5 * time.Second)
	if err := os.Chtimes(source, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}

	builds := 0
	publishes := 0
	refresh := graphRefresher(
		initial,
		root,
		func(string) (*graph.Graph, error) {
			builds++
			built := *initial
			built.GeneratedAt = changedAt.Add(time.Second)
			return &built, nil
		},
		func(string) (*graph.Graph, error) { return nil, errors.New("unexpected precise build") },
		func(g *graph.Graph) (graphPublication, error) {
			publishes++
			if err := os.MkdirAll(filepath.Join(root, outputDir), 0o750); err != nil {
				return graphPublication{}, err
			}
			if err := writeJSON(filepath.Join(root, graphFile), g); err != nil {
				return graphPublication{}, err
			}
			return graphPublication{Graph: g, Published: true}, nil
		},
	)
	first, err := refresh()
	if err != nil {
		t.Fatal(err)
	}
	second, err := refresh()
	if err != nil {
		t.Fatal(err)
	}
	if builds != 1 || publishes != 1 || first != second {
		t.Fatalf("self-publication builds=%d publishes=%d first=%p second=%p", builds, publishes, first, second)
	}
}

func TestGraphRefresherNeverPublishesPreciseFallback(t *testing.T) {
	root := writeTestModule(t)
	source := filepath.Join(root, "main.go")
	initial := buildTestGraph(t, root)
	initial.Build.Precision = graph.PrecisionPrecise
	changedAt := initial.GeneratedAt.Add(5 * time.Second)
	if err := os.Chtimes(source, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}

	publishes := 0
	refresh := graphRefresher(
		initial,
		root,
		func(string) (*graph.Graph, error) { return nil, errors.New("unexpected AST build") },
		func(string) (*graph.Graph, error) {
			fallback := *initial
			fallback.GeneratedAt = changedAt.Add(time.Second)
			fallbackBuild := *initial.Build
			fallbackBuild.Precision = graph.PrecisionFallback
			fallbackBuild.Warnings = []string{"precise enrichment failed: unresolved symbol"}
			fallback.Build = &fallbackBuild
			return &fallback, errors.New("precise MCP refresh failed; graph is precise_fallback")
		},
		func(g *graph.Graph) (graphPublication, error) {
			publishes++
			return graphPublication{Graph: g, Published: true}, nil
		},
	)
	if got, err := refresh(); got != nil || err == nil || !strings.Contains(err.Error(), "precise_fallback") {
		t.Fatalf("fallback refresh graph=%v err=%v", got, err)
	}
	if publishes != 0 {
		t.Fatalf("precise fallback published %d time(s)", publishes)
	}
}

func TestGraphBaselineMatchesPreviousMetrics(t *testing.T) {
	previous := &graph.Graph{
		Imports: []graph.ImportEdge{{FromPackage: "a", ImportPath: "b"}, {FromPackage: "b", ImportPath: "c"}},
		Symbols: []graph.SymbolNode{{Name: "main", Kind: graph.KindFunction}},
	}
	got := graphBaseline(previous)
	if got == nil {
		t.Fatal("graphBaseline returned nil")
	}
	if got.CouplingEdges != len(previous.Imports) || got.OrphanCount != len(search.ReachableOrphans(previous)) {
		t.Fatalf("baseline = %+v, want imports=%d orphans=%d", got, len(previous.Imports), len(search.ReachableOrphans(previous)))
	}
}
