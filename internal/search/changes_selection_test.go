package search

import (
	"context"
	"go/build"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	sourceparser "github.com/ozgurcd/gograph/internal/parser"
	"github.com/ozgurcd/gograph/internal/repositoryfingerprint"
)

func writeChangesSource(t *testing.T, root, name, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestChangesUsesRecordedBuildSelection(t *testing.T) {
	root := t.TempDir()
	writeChangesSource(t, root, "go.mod", "module example.com/tagged\n\ngo 1.26\n")
	writeChangesSource(t, root, "base.go", "package tagged\nfunc Base() {}\n")
	writeChangesSource(t, root, "tagged_test.go", "//go:build integration\n\npackage tagged\nfunc TestTagged() { Base() }\n")
	selection := graph.CaptureBuildSelection(build.Default)
	selection.BuildTags = []string{"integration"}
	baseline, err := buildDeclarationBaseline(context.Background(), root, selection)
	if err != nil || len(baseline.Files) != 2 {
		t.Fatalf("tagged baseline = %+v, %v", baseline, err)
	}
	t.Setenv("GOFLAGS", "-tags=unrelated")
	result := Changes(baseline, root)
	if result.Evaluation != "complete" || len(result.Symbols) != 0 || len(result.ChangedFiles) != 0 {
		t.Fatalf("unchanged tagged graph changed under ambient tags: %+v", result)
	}
	writeChangesSource(t, root, "tagged_test.go", "//go:build integration\n\npackage tagged\nfunc TestTagged() { Base(); Base() }\n")
	result = Changes(baseline, root)
	if result.Evaluation != "complete" || len(result.Symbols) != 1 || result.Symbols[0].Status != ChangeModified || result.Symbols[0].Name != "TestTagged" {
		t.Fatalf("tagged modification = %+v", result)
	}
	// An explicitly empty saved selection must not inherit GOFLAGS either.
	selection.BuildTags = []string{}
	baseline, err = buildDeclarationBaseline(context.Background(), root, selection)
	if err != nil || len(baseline.Files) != 1 {
		t.Fatalf("untagged baseline = %+v, %v", baseline, err)
	}
	t.Setenv("GOFLAGS", "-tags=integration")
	if result := Changes(baseline, root); result.Evaluation != "complete" || len(result.Symbols) != 0 {
		t.Fatalf("ambient tags expanded untagged census: %+v", result)
	}
}

func TestChangesRecomputesModuleIdentityWhenGoBytesDoNotChange(t *testing.T) {
	for _, test := range []struct{ name, moduleFile, moduleSource string }{
		{"root rename", "go.mod", "module example.com/new\n\ngo 1.26\n"},
		{"nested boundary", "sub/go.mod", "module example.com/nested\n\ngo 1.26\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeChangesSource(t, root, "go.mod", "module example.com/old\n\ngo 1.26\n")
			writeChangesSource(t, root, "sub/main.go", "package sub\nfunc Keep() {}\n")
			baseline, err := buildDeclarationBaseline(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			writeChangesSource(t, root, test.moduleFile, test.moduleSource)
			result := Changes(baseline, root)
			if result.Evaluation != "complete" || len(result.Symbols) != 2 || len(result.ChangedFiles) != 1 {
				t.Fatalf("module ownership change = %+v", result)
			}
			statuses := make(map[ChangeStatus]string)
			for _, symbol := range result.Symbols {
				statuses[symbol.Status] = symbol.StableID
			}
			wantNew := "example.com/new/sub::Keep"
			if test.name == "nested boundary" {
				wantNew = "example.com/nested::Keep"
			}
			if statuses[ChangeDeleted] != "example.com/old/sub::Keep" || statuses[ChangeNew] != wantNew {
				t.Fatalf("ownership identities = %+v", statuses)
			}
		})
	}
}

func TestChangesPackageClauseChangesIdentity(t *testing.T) {
	root := t.TempDir()
	before := "package before\nfunc Keep() {}\n"
	baseline := declarationBaseline(t, root, before)
	writeChangesSource(t, root, "main.go", "package after\nfunc Keep() {}\n")
	result := Changes(baseline, root)
	if result.Evaluation != "complete" || len(result.Symbols) != 2 {
		t.Fatalf("package clause change hidden: %+v", result)
	}
	for _, symbol := range result.Symbols {
		if symbol.Status == ChangeDeleted && symbol.PackageName != "before" || symbol.Status == ChangeNew && symbol.PackageName != "after" {
			t.Fatalf("package qualifier lost: %+v", symbol)
		}
	}
}

func TestChangesKeepsRepeatedInitializers(t *testing.T) {
	root := t.TempDir()
	before := "package p\nfunc init() { first() }\nfunc init() { second() }\n"
	baseline := declarationBaseline(t, root, before)
	for _, test := range []struct {
		source string
		count  int
		status ChangeStatus
		line   int
	}{
		{"package p\nfunc init() { second() }\n", 1, ChangeDeleted, 2},
		{"package p\nfunc init() { second() }\nfunc init() { first() }\n", 2, ChangeModified, 2},
		{"package p\nfunc init() { first() }\nfunc init() { modified() }\n", 1, ChangeModified, 3},
	} {
		writeChangesSource(t, root, "main.go", test.source)
		result := Changes(baseline, root)
		if result.Evaluation != "complete" || len(result.Symbols) != test.count {
			t.Fatalf("repeated init comparison = %+v", result)
		}
		if test.count > 0 && (result.Symbols[0].Status != test.status || result.Symbols[0].Line != test.line) {
			t.Fatalf("wrong initializer attributed: %+v", result.Symbols)
		}
	}
}

func TestChangesExternalTestTwinDoesNotConflate(t *testing.T) {
	root := t.TempDir()
	writeChangesSource(t, root, "go.mod", "module example.com/twins\n\ngo 1.26\n")
	writeChangesSource(t, root, "main.go", "package twins\nfunc Helper() {}\n")
	writeChangesSource(t, root, "main_test.go", "package twins_test\nfunc Helper() {}\n")
	baseline, err := buildDeclarationBaseline(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	writeChangesSource(t, root, "main_test.go", "package twins_test\nfunc Added() {}\n")
	result := Changes(baseline, root)
	if result.Evaluation != "complete" || len(result.Symbols) != 2 {
		t.Fatalf("external test diff = %+v", result)
	}
	for _, symbol := range result.Symbols {
		if symbol.PackageName != "twins_test" || symbol.File != "main_test.go" {
			t.Fatalf("test twin conflated: %+v", symbol)
		}
	}
}

func TestChangesCanceledDoesNotReportDeletion(t *testing.T) {
	root := t.TempDir()
	baseline := declarationBaseline(t, root, "package p\nfunc Keep() {}\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := ChangesContext(ctx, baseline, root)
	if result.Evaluation == "complete" || len(result.Symbols) != 0 {
		t.Fatalf("cancellation became a clean deletion: %+v", result)
	}
}

func TestCompareDeclarationsBlankIdentifiersDoNotCollapse(t *testing.T) {
	parse := func(source string) []graph.SymbolNode {
		parsed, err := sourceparser.ParseSource(token.NewFileSet(), "main.go", []byte(source), "main.go", "example.com/blanks")
		if err != nil {
			t.Fatal(err)
		}
		return parsed.Symbols
	}
	before := parse("package p\nvar _ = first()\nvar _ = second()\n")
	after := parse("package p\nvar _ = second()\n")
	result := &ChangesResult{Evaluation: "complete"}
	compareDeclarations(result, before, after, "main.go")
	if len(result.Symbols) != 1 || result.Symbols[0].Status != ChangeDeleted || result.Symbols[0].Line != 2 {
		t.Fatalf("blank declarations collapsed: %+v", result)
	}
}

func TestChangesObservationDetectsContentSelectionAndMetadataRaces(t *testing.T) {
	for _, test := range []struct{ name, file, source string }{
		{"content", "main.go", "package p\nfunc Changed() {}\n"},
		{"new selection", "added.go", "package p\nfunc Added() {}\n"},
		{"module", "go.mod", "module example.com/renamed\n\ngo 1.26\n"},
		{"ignore", ".gitignore", "main.go\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeChangesSource(t, root, "go.mod", "module example.com/races\n\ngo 1.26\n")
			writeChangesSource(t, root, "main.go", "package p\nfunc Before() {}\n")
			config, paths, errs := changesSelection(context.Background(), root, nil)
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			before, err := repositoryfingerprint.Compute(context.Background(), root, config, paths)
			if err != nil {
				t.Fatal(err)
			}
			if err := verifyChangesObservation(context.Background(), root, nil, before.Fingerprint); err != nil {
				t.Fatal(err)
			}
			writeChangesSource(t, root, test.file, test.source)
			if err := verifyChangesObservation(context.Background(), root, nil, before.Fingerprint); err == nil {
				t.Fatal("source race accepted as a coherent declaration snapshot")
			}
		})
	}
}
