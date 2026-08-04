package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestBuildGraphExcludesRepositorySourceSymlink(t *testing.T) {
	root, sentinel := writeSymlinkSecurityModule(t)
	g, err := BuildGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	if !g.UsesCurrentSourcePolicy() {
		t.Fatalf("build source policy = %+v, want current", g.Build)
	}
	if len(g.Files) != 1 || g.Files[0].Path != "main.go" {
		t.Fatalf("indexed files = %+v, want only main.go", g.Files)
	}
	if g.Build.Complete || len(g.Build.Warnings) == 0 {
		t.Fatalf("build metadata = %+v, want visible excluded-source warning", g.Build)
	}
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), sentinel) || strings.Contains(string(data), "OutsideOnly") {
		t.Fatalf("safe graph contains external source data: %s", data)
	}

	config, configErr := resolveBuildConfig(root)
	if configErr != nil {
		t.Fatal(configErr)
	}
	if err := enrichGraphPreciselyWithConfig(root, g, config, nil); err == nil {
		t.Fatal("precise enrichment accepted repository source symlink")
	}
	data, err = json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), sentinel) || strings.Contains(string(data), "OutsideOnly") {
		t.Fatalf("precise fallback contains external source data: %s", data)
	}
}

func TestBuildGraphDoesNotTrustLinkedModuleMetadata(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const sentinel = "BENIGN-LINKED-MODULE-SENTINEL"
	outsideMod := filepath.Join(base, "outside.mod")
	contents := "module example.com/" + sentinel + "\n\nrequire example.com/" + sentinel + "/dependency v9.9.9\n"
	if err := os.WriteFile(outsideMod, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideMod, filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("create module symlink: %v", err)
	}

	g, err := BuildGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), sentinel) || len(g.Dependencies) != 0 {
		t.Fatalf("linked module metadata leaked into graph: %s", payload)
	}
	if g.Build.Complete || len(g.Build.Warnings) == 0 {
		t.Fatalf("linked module metadata was not reported: %+v", g.Build)
	}
}

func TestBuildAndPreciseRefuseLinkedToolchainCompanions(t *testing.T) {
	for _, test := range []struct {
		name     string
		relative string
		work     bool
	}{
		{name: "module sum", relative: "go.sum"},
		{name: "workspace sum", relative: "go.work.sum", work: true},
		{name: "vendor metadata", relative: filepath.Join("vendor", "modules.txt")},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "repository")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/metadata\n\ngo 1.26\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package metadata\nfunc Safe() {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if test.work {
				if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26\n\nuse .\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Setenv("GOWORK", "auto")
			} else {
				t.Setenv("GOWORK", "off")
			}
			t.Setenv("GOENV", "off")
			t.Setenv("GOTOOLCHAIN", "local")

			const sentinel = "BENIGN-LINKED-TOOLCHAIN-METADATA"
			outside := filepath.Join(base, "outside-metadata")
			if err := os.WriteFile(outside, []byte(sentinel+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			linked := filepath.Join(root, test.relative)
			if err := os.MkdirAll(filepath.Dir(linked), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, linked); err != nil {
				t.Skipf("create metadata symlink: %v", err)
			}

			g, err := BuildGraph(root)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(g)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(payload), sentinel) || g.Build.Complete || len(g.Build.Warnings) == 0 {
				t.Fatalf("linked %s was trusted or not reported: %s", test.relative, payload)
			}

			config, configErr := resolveBuildConfig(root)
			if configErr == nil {
				t.Fatalf("build context accepted linked %s", test.relative)
			}
			if err := enrichGraphPreciselyWithConfig(root, g, config, configErr); err == nil {
				t.Fatalf("precise analysis accepted linked %s", test.relative)
			}
		})
	}
}

func TestBuildPreciseAndDocRefuseLinkedWorkspaceMember(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	outsideMember := filepath.Join(base, "outside-member")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideMember, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26\n\nuse ./member\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package workspace\nfunc Safe() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const sentinel = "BENIGN-OUTSIDE-WORKSPACE-MEMBER"
	if err := os.WriteFile(filepath.Join(outsideMember, "go.mod"), []byte("module example.com/"+sentinel+"\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideMember, filepath.Join(root, "member")); err != nil {
		t.Skipf("create workspace member symlink: %v", err)
	}
	t.Setenv("GOENV", "off")
	t.Setenv("GOWORK", "auto")
	t.Setenv("GOTOOLCHAIN", "local")

	g, err := BuildGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), sentinel) || g.Build.Complete || len(g.Build.Warnings) == 0 {
		t.Fatalf("linked workspace member was trusted or not reported: %s", payload)
	}
	config, configErr := resolveBuildConfig(root)
	if configErr == nil {
		t.Fatal("build context accepted linked workspace member")
	}
	if err := enrichGraphPreciselyWithConfig(root, g, config, configErr); err == nil {
		t.Fatal("precise analysis accepted linked workspace member")
	}

	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(original) }()
	if code := runDoc([]string{"fmt.Errorf"}); code != exitError {
		t.Fatalf("runDoc exit = %d, want workspace-member refusal", code)
	}
}

func TestPreciseAndDocRefuseLinkedSourceInSiblingWorkspaceMember(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	selected := filepath.Join(workspace, "selected")
	sibling := filepath.Join(workspace, "sibling")
	for _, directory := range []string{selected, sibling} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "go.work"), []byte("go 1.26\n\nuse (\n\t./selected\n\t./sibling\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(selected, "go.mod"), []byte("module example.com/selected\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(selected, "main.go"), []byte("package selected\nfunc Safe() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "go.mod"), []byte("module example.com/sibling\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const sentinel = "BENIGN-SIBLING-WORKSPACE-SOURCE"
	outside := filepath.Join(base, "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n// "+sentinel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(sibling, "linked.go")); err != nil {
		t.Skipf("create sibling source symlink: %v", err)
	}
	t.Setenv("GOENV", "off")
	t.Setenv("GOWORK", "auto")
	t.Setenv("GOTOOLCHAIN", "local")

	g, err := BuildGraph(selected)
	if err != nil {
		t.Fatal(err)
	}
	config, configErr := resolveBuildConfig(selected)
	if configErr != nil {
		t.Fatalf("safe workspace metadata resolution failed: %v", configErr)
	}
	if err := enrichGraphPreciselyWithConfig(selected, g, config, configErr); err == nil || strings.Contains(err.Error(), sentinel) {
		t.Fatalf("precise sibling-source refusal = %v", err)
	}

	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(selected); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(original) }()
	if code := runDoc([]string{"fmt.Errorf"}); code != exitError {
		t.Fatalf("runDoc exit = %d, want sibling-source refusal", code)
	}
}

func TestLegacyGraphIsRebuiltInsteadOfRetained(t *testing.T) {
	root, sentinel := writeSymlinkSecurityModule(t)
	legacy := &graph.Graph{
		Version:     graph.Version,
		Root:        root,
		GeneratedAt: time.Now().Add(time.Hour),
		Build:       &graph.BuildMetadata{Complete: true, Precision: graph.PrecisionPrecise},
		Symbols: []graph.SymbolNode{{
			ID: "example.com/security::OutsideOnly", Name: "OutsideOnly", Kind: graph.KindFunction,
			File: "linked.go", Line: 2, EndLine: 2, Doc: sentinel,
		}},
	}
	if err := os.MkdirAll(filepath.Join(root, outputDir), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(root, graphFile), legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := loadGraph(root); err == nil || !strings.Contains(err.Error(), "rebuild") {
		t.Fatalf("loadGraph legacy error = %v, want rebuild-required", err)
	}

	g, gotRoot, err := prepareMCPGraph(mcpOptions{Root: root, PersistRefresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if gotRoot != root || !g.UsesCurrentSourcePolicy() {
		t.Fatalf("prepared graph/root = %+v/%q", g.Build, gotRoot)
	}
	persisted, err := loadGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(persisted)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), sentinel) || strings.Contains(string(data), "OutsideOnly") {
		t.Fatalf("legacy precise graph survived safe publication: %s", data)
	}
}

func TestMCPFallbackFromSubdirectoryUsesDiscoveredProjectRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/rooted\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package rooted\nfunc RootSymbol() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, outputDir), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := &graph.Graph{Version: graph.Version, Root: root, Build: &graph.BuildMetadata{Precision: graph.PrecisionAST}}
	if err := writeJSON(filepath.Join(root, graphFile), legacy); err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(root, "nested", "package")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(subdir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(original) }()

	g, gotRoot, err := prepareMCPGraph(mcpOptions{Root: "."})
	if err != nil {
		t.Fatal(err)
	}
	if !sameDirectory(gotRoot, root) || !sameDirectory(g.Root, root) {
		t.Fatalf("MCP fallback root = %q/%q, want %q", gotRoot, g.Root, root)
	}
	found := false
	for _, symbol := range g.Symbols {
		if symbol.Name == "RootSymbol" {
			found = true
		}
	}
	if !found {
		t.Fatalf("MCP fallback indexed %+v, want project-root symbol", g.Symbols)
	}
}

func TestSourceRejectsCurrentPolicyGraphPointingAtSymlink(t *testing.T) {
	root, sentinel := writeSymlinkSecurityModule(t)
	poisoned := currentPolicyGraph(&graph.Graph{
		Version: graph.Version,
		Root:    root,
		Symbols: []graph.SymbolNode{{
			ID: "example.com/security::OutsideOnly", Name: "OutsideOnly", Kind: graph.KindFunction,
			File: "linked.go", Line: 3, EndLine: 3, Doc: sentinel,
		}},
	})
	if err := os.MkdirAll(filepath.Join(root, outputDir), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(root, graphFile), poisoned); err != nil {
		t.Fatal(err)
	}
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(original) }()
	if code := runSource([]string{"OutsideOnly"}); code != exitError {
		t.Fatalf("runSource exit = %d, want confined-read failure", code)
	}
}

func TestLoadGraphRejectsLinkedPersistedArtifact(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	if err := os.MkdirAll(filepath.Join(root, outputDir), 0o755); err != nil {
		t.Fatal(err)
	}
	outsideGraph := filepath.Join(base, "outside-graph.json")
	poisoned := currentPolicyGraph(&graph.Graph{
		Version: graph.Version,
		Root:    root,
		Symbols: []graph.SymbolNode{{Name: "ExternalOnly", Doc: "BENIGN-GRAPH-SENTINEL"}},
	})
	if err := writeJSON(outsideGraph, poisoned); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideGraph, filepath.Join(root, graphFile)); err != nil {
		t.Skipf("create graph artifact symlink: %v", err)
	}
	if _, err := loadGraph(root); err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Fatalf("loadGraph linked artifact error = %v, want confined read failure", err)
	}
}

func TestLoadGraphReplacesUntrustedSerializedRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	outsideRoot := filepath.Join(base, "outside")
	if err := os.MkdirAll(filepath.Join(root, outputDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	const sentinel = "BENIGN-UNTRUSTED-ROOT-SENTINEL"
	if err := os.WriteFile(filepath.Join(outsideRoot, "outside.go"), []byte("package outside\nfunc OutsideOnly() string { return \""+sentinel+"\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	poisoned := currentPolicyGraph(&graph.Graph{
		Version: graph.Version,
		Root:    ".",
		Symbols: []graph.SymbolNode{{
			ID: "example.com/outside::OutsideOnly", Name: "OutsideOnly", Kind: graph.KindFunction,
			File: "outside.go", Line: 2, EndLine: 2,
		}},
	})
	if err := writeJSON(filepath.Join(root, graphFile), poisoned); err != nil {
		t.Fatal(err)
	}

	loaded, err := loadGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(loaded.Root) || filepath.Clean(loaded.Root) != filepath.Clean(root) {
		t.Fatalf("loaded graph root = %q, want trusted load root %q", loaded.Root, root)
	}
	source, err := search.Source(loaded, loaded.Root, "OutsideOnly")
	if err == nil {
		t.Fatalf("source followed serialized graph root: %q", source)
	}
	if strings.Contains(source, sentinel) || strings.Contains(err.Error(), sentinel) {
		t.Fatalf("source disclosed untrusted-root sentinel: source=%q err=%v", source, err)
	}
}

func TestDocRefusesRepositorySourceSymlink(t *testing.T) {
	root, _ := writeSymlinkSecurityModule(t)
	subdir := filepath.Join(root, "nested", "package")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(subdir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(original) }()
	if code := runDoc([]string{"fmt.Errorf"}); code != exitError {
		t.Fatalf("runDoc exit = %d, want refusal", code)
	}
}

func TestDocRefusesLinkedToolchainMetadata(t *testing.T) {
	for _, relative := range []string{"go.sum", "go.work.sum", filepath.Join("vendor", "modules.txt")} {
		t.Run(filepath.ToSlash(relative), func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "repository")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/docmetadata\n\ngo 1.26\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package docmetadata\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if relative == "go.work.sum" {
				if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26\n\nuse .\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			outside := filepath.Join(base, "outside-metadata")
			if err := os.WriteFile(outside, []byte("outside metadata\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			linked := filepath.Join(root, relative)
			if err := os.MkdirAll(filepath.Dir(linked), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, linked); err != nil {
				t.Skipf("create metadata symlink: %v", err)
			}

			original, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chdir(root); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.Chdir(original) }()
			if code := runDoc([]string{"fmt.Errorf"}); code != exitError {
				t.Fatalf("runDoc with linked %s exit = %d, want refusal", relative, code)
			}
		})
	}
}

func TestDocRejectsFilesystemQuery(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/doc\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(original) }()
	if code := runDoc([]string{"../outside.go"}); code != exitError {
		t.Fatalf("runDoc filesystem query exit = %d, want refusal", code)
	}
}

func TestWriteGitignoreRejectsRepositorySymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside-ignore")
	if err := os.WriteFile(outside, []byte("KEEP\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".gitignore")); err != nil {
		t.Skipf("create .gitignore symlink: %v", err)
	}
	if err := writeGitignore(root); err == nil {
		t.Fatal("writeGitignore accepted repository symlink")
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "KEEP\n" {
		t.Fatalf("outside .gitignore target = %q, %v", data, err)
	}
}

func writeSymlinkSecurityModule(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/security\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const sentinel = "BENIGN-EXTERNAL-SENTINEL"
	outside := filepath.Join(base, "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n// "+sentinel+"\nfunc OutsideOnly() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.go")); err != nil {
		t.Skipf("create source symlink: %v", err)
	}
	return root, sentinel
}
