package search

import (
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestUncommittedDeclarationsAreNestedRootScoped(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	for _, dir := range []string{"member", "decoy"} {
		writeChangesSource(t, root, dir+"/go.mod", "module example.com/"+dir+"\n\ngo 1.26\n")
		writeChangesSource(t, root, dir+"/main.go", "package p\nfunc Keep() int { return 1 }\n")
	}
	git("init", "-q")
	git("add", ".")
	git("-c", "user.name=Gograph test", "-c", "user.email=test@example.invalid", "-c", "core.hooksPath=/dev/null", "commit", "-qm", "baseline")
	// The old suffix/hunk matcher could confuse these identically named files.
	writeChangesSource(t, root, "decoy/main.go", "package p\nfunc Keep() int { return 2 }\n")
	member := filepath.Join(root, "member")
	g, err := buildDeclarationBaseline(context.Background(), member)
	if err != nil {
		t.Fatal(err)
	}
	g.Root = member
	if ids, err := UncommittedSymbols(g); err != nil || len(ids) != 0 {
		t.Fatalf("sibling change leaked: %v, %v", ids, err)
	}
	writeChangesSource(t, member, "main.go", "package p\n// formatting is not a change\nfunc Keep() int { return 1 }\n")
	if ids, err := UncommittedSymbols(g); err != nil || len(ids) != 0 {
		t.Fatalf("comment became modification: %v, %v", ids, err)
	}
	writeChangesSource(t, member, "main.go", "package p\nfunc Keep() int { return 3 }\n")
	writeChangesSource(t, member, "new.go", "package p\nfunc Added() {}\n")
	if _, err := UncommittedSymbols(g); err == nil || !strings.Contains(err.Error(), "absent from the graph") {
		t.Fatalf("untracked declaration silently omitted: %v", err)
	}
	g, err = buildDeclarationBaseline(context.Background(), member)
	if err != nil {
		t.Fatal(err)
	}
	g.Root = member
	if ids, err := UncommittedSymbols(g); err != nil || !reflect.DeepEqual(ids, []string{"example.com/member::Added", "example.com/member::Keep"}) {
		t.Fatalf("declaration selection = %v, %v", ids, err)
	}
	writeChangesSource(t, member, "main.go", "package p\n")
	if _, err := UncommittedSymbols(g); err == nil || !strings.Contains(err.Error(), "historical caller evidence") {
		t.Fatalf("deletion silently omitted: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := UncommittedSymbolsContext(ctx, g); err == nil {
		t.Fatal("canceled selection succeeded")
	}
}

func TestCurrentChangedSymbolIDsRejectsIncompleteAndAmbiguousSelection(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{{ID: "p::Keep", Name: "Keep", PackageName: "p"}}}
	for _, tc := range []struct {
		name   string
		result ChangesResult
	}{
		{"partial", ChangesResult{Evaluation: "partial"}},
		{"missing identity", ChangesResult{Evaluation: "complete", Symbols: []ChangedSymbol{{Status: ChangeModified}}}},
		{"missing target", ChangesResult{Evaluation: "complete", Symbols: []ChangedSymbol{{StableID: "p::Gone", Status: ChangeModified}}}},
		{"deleted", ChangesResult{Evaluation: "complete", Symbols: []ChangedSymbol{{StableID: "p::Keep", Status: ChangeDeleted}}}},
		{"excluded", ChangesResult{Evaluation: "complete", Symbols: []ChangedSymbol{{StableID: "p::Keep", Status: ChangeExcluded}}}},
		{"package mismatch", ChangesResult{Evaluation: "complete", Symbols: []ChangedSymbol{{StableID: "p::Keep", PackageName: "p_test", Status: ChangeModified}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := CurrentChangedSymbolIDs(g, &tc.result); err == nil {
				t.Fatal("unsafe selection accepted")
			}
		})
	}
	g.Symbols = append(g.Symbols, g.Symbols[0])
	if _, err := CurrentChangedSymbolIDs(g, &ChangesResult{Evaluation: "complete", Symbols: []ChangedSymbol{{StableID: "p::Keep", Status: ChangeModified}}}); err == nil {
		t.Fatal("duplicate identity accepted")
	}
}
