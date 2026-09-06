package search

import (
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ozgurcd/gograph/internal/graph"
	sourceparser "github.com/ozgurcd/gograph/internal/parser"
)

func declarationBaseline(t *testing.T, root, source string) *graph.Graph {
	t.Helper()
	parsed, err := sourceparser.ParseSource(token.NewFileSet(), "main.go", []byte(source), "main.go", "example.com/diff")
	if err != nil {
		t.Fatal(err)
	}
	parsed.File.ContentDigest = graph.SourceDigest([]byte(source))
	return &graph.Graph{Root: root, GeneratedAt: time.Now(), Files: []graph.FileNode{parsed.File}, Symbols: parsed.Symbols}
}

func TestChangesComparesDeclarationsNotFiles(t *testing.T) {
	root := t.TempDir()
	before := "package example\nfunc Keep() {}\nfunc Remove() {}\nfunc Edit() int { return 1 }\ntype A struct{}\ntype B struct{}\nfunc (A) Same() {}\nfunc (B) Same() {}\n"
	after := "package example\n\nfunc Keep() {}\nfunc Edit() int { return 2 }\nfunc Added() {}\ntype A struct{}\ntype B struct{}\nfunc (B) Same() {}\n"
	g := declarationBaseline(t, root, before)
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(after), 0600); err != nil {
		t.Fatal(err)
	}
	result := Changes(g, root)
	if result.Evaluation != "complete" {
		t.Fatalf("incomplete: %+v", result)
	}
	want := map[string]ChangeStatus{"example.com/diff::Remove": ChangeDeleted, "example.com/diff::Edit": ChangeModified, "example.com/diff::Added": ChangeNew, "example.com/diff::(A).Same": ChangeDeleted}
	if len(result.Symbols) != len(want) {
		t.Fatalf("unexpected changed declarations: %+v", result.Symbols)
	}
	for _, s := range result.Symbols {
		if want[s.StableID] != s.Status {
			t.Fatalf("wrong change: %+v", s)
		}
	}
}

func TestChangesFormattingDoesNotModifyDeclarations(t *testing.T) {
	root := t.TempDir()
	g := declarationBaseline(t, root, "package example\nfunc Keep() int { return 1 }\n")
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package example\n\n// A comment.\nfunc Keep() int {\n return 1\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r := Changes(g, root)
	if r.Evaluation != "complete" || len(r.Symbols) != 0 || len(r.ChangedFiles) != 1 {
		t.Fatalf("formatting reported as symbol edit: %+v", r)
	}
}

func TestChangesParseFailureDoesNotClaimDeletions(t *testing.T) {
	root := t.TempDir()
	g := declarationBaseline(t, root, "package example\nfunc Keep() {}\n")
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package example\nfunc broken("), 0600); err != nil {
		t.Fatal(err)
	}
	r := Changes(g, root)
	if r.Evaluation == "complete" || len(r.Diagnostics) == 0 || len(r.Symbols) != 0 {
		t.Fatalf("parse failure hidden: %+v", r)
	}
}

func TestChangesRejectsUnconfinedBaselinePaths(t *testing.T) {
	r := Changes(&graph.Graph{Symbols: []graph.SymbolNode{{ID: "bad", Name: "Bad", File: "../outside.go"}}}, t.TempDir())
	if r.Evaluation == "complete" || len(r.Diagnostics) == 0 || len(r.Symbols) != 0 {
		t.Fatalf("unsafe baseline accepted: %+v", r)
	}
}

func TestChangesByGitRefIncludesAddedDeletedAndEditedDeclarations(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=/dev/null", "-c", "user.name=Gograph Test", "-c", "user.email=gograph-test@example.invalid"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	write := func(name, source string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/history\n\ngo 1.26\n")
	write("main.go", "package example\nfunc Keep() {}\nfunc Remove() {}\nfunc Edit() int {return 1}\n")
	run("init", "--quiet")
	run("add", "go.mod", "main.go")
	run("commit", "--quiet", "-m", "baseline")
	write("main.go", "package example\nfunc Keep() {}\nfunc Edit() int {return 2}\nfunc Added() {}\n")
	r, err := ChangesByGitRef(&graph.Graph{}, root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]ChangeStatus{"Remove": ChangeDeleted, "Edit": ChangeModified, "Added": ChangeNew}
	if r.Evaluation != "complete" || len(r.Symbols) != len(want) {
		t.Fatalf("incomplete historical diff: %+v", r)
	}
	for _, s := range r.Symbols {
		if want[s.Name] != s.Status {
			t.Fatalf("wrong historical change: %+v", s)
		}
	}
}
