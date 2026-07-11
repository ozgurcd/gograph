package parser_test

import (
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/ozgurcd/gograph/internal/parser"
)

func TestDomainExtractorsRequireKnownPackagesAndSyncTypes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.go")
	source := `package sample

import (
	"errors"
	"fmt"
	"os/exec"
	synch "sync"
	"testing"
	"time"
)

type Service struct {
	mu synch.Mutex
	rw synch.RWMutex
	once synch.Once
}

type factory struct{}
func (factory) New(string) {}
func (factory) Errorf(string) {}
var fake factory

func (s *Service) run(f *testing.F, cmd *exec.Cmd, now time.Time) {
	var wg synch.WaitGroup
	s.mu.Lock()
	s.mu.Unlock()
	s.rw.RLock()
	s.rw.RUnlock()
	s.once.Do(func() {})
	wg.Add(1)
	wg.Done()
	wg.Wait()
	now.Add(time.Second)
	f.Add("fuzz seed")
	_ = cmd.Wait()
	fake.New("not an error")
	fake.Errorf("also not an error")
	_ = errors.New("real error")
	_ = fmt.Errorf("formatted error")
	panic("panic error")
}
`
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := parser.ParseFile(token.NewFileSet(), path, "sample.go", "example.com/sample")
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	for _, node := range result.Concurrency {
		switch node.Detail {
		case "now.Add", "f.Add", "cmd.Wait":
			t.Errorf("non-sync call was classified as concurrency: %+v", node)
		}
	}
	wantKinds := map[string]bool{
		"mutex_lock":     false,
		"mutex_unlock":   false,
		"rwmutex_lock":   false,
		"rwmutex_unlock": false,
		"once_do":        false,
		"waitgroup_add":  false,
		"waitgroup_wait": false,
	}
	for _, node := range result.Concurrency {
		if _, ok := wantKinds[node.Kind]; ok {
			wantKinds[node.Kind] = true
		}
	}
	for kind, found := range wantKinds {
		if !found {
			t.Errorf("expected concurrency kind %q, got %+v", kind, result.Concurrency)
		}
	}

	messages := make(map[string]bool)
	for _, edge := range result.Errors {
		messages[edge.Message] = true
	}
	for _, message := range []string{"real error", "formatted error", "panic error"} {
		if !messages[message] {
			t.Errorf("expected error message %q, got %+v", message, result.Errors)
		}
	}
	for _, message := range []string{"not an error", "also not an error", "fuzz seed"} {
		if messages[message] {
			t.Errorf("unrelated constructor string was classified as an error: %q", message)
		}
	}
}

func TestMutationExtractionTracksFieldTypeAndRejectsLocalAssignments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mutations.go")
	source := `package sample

type Graph struct { Root string }
var Global int

func update(g *Graph) {
	root := "old"
	root = "new"
	g.Root = root
	Global = 1
}
`
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := parser.ParseFile(token.NewFileSet(), path, "mutations.go", "example.com/sample")
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(result.Mutations) != 2 {
		t.Fatalf("mutations = %+v, want Graph.Root and Global only", result.Mutations)
	}
	found := make(map[string]string)
	for _, mutation := range result.Mutations {
		found[mutation.Field] = mutation.TypeName
	}
	if found["Root"] != "Graph" {
		t.Fatalf("Root type = %q, want Graph", found["Root"])
	}
	if _, ok := found["Global"]; !ok {
		t.Fatalf("package global mutation missing: %+v", result.Mutations)
	}
	if _, ok := found["root"]; ok {
		t.Fatalf("local assignment was classified as a mutation: %+v", result.Mutations)
	}
}
