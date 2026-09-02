package search_test

import (
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func TestPreciseImplementationQueriesUseQualifiedTypeIDs(t *testing.T) {
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: "example.com/a::Reader", Name: "Reader", Kind: graph.KindInterface, InterfaceMethods: map[string]string{"Read": "func()"}, File: "a/interface.go"},
			{ID: "example.com/b::Reader", Name: "Reader", Kind: graph.KindInterface, InterfaceMethods: map[string]string{"Read": "func()"}, File: "b/interface.go"},
			{ID: "example.com/a::Store", Name: "Store", Kind: graph.KindStruct, File: "a/store.go"},
			{ID: "example.com/b::Store", Name: "Store", Kind: graph.KindStruct, File: "b/store.go"},
		},
		Implements: []graph.ImplementsEdge{
			{Interface: "Reader", Concrete: "Store", InterfaceID: "example.com/a::Reader", ConcreteID: "example.com/a::Store"},
			{Interface: "Reader", Concrete: "Store", InterfaceID: "example.com/b::Reader", ConcreteID: "example.com/b::Store"},
		},
	}

	implementers := search.Implementers(g, "example.com/a::Reader")
	if len(implementers) != 1 || implementers[0].File != "a/store.go" {
		t.Fatalf("qualified interface matched the wrong concrete type: %+v", implementers)
	}
	interfaces := search.Interfaces(g, "example.com/b::Store")
	if len(interfaces) != 1 || interfaces[0].File != "b/interface.go" {
		t.Fatalf("qualified concrete matched the wrong interface: %+v", interfaces)
	}
}

func TestPreciseImplementersMergeTestOnlyFakes(t *testing.T) {
	g := &graph.Graph{
		Symbols: []graph.SymbolNode{
			{ID: "example.com/a::Reader", Name: "Reader", Kind: graph.KindInterface, InterfaceMethods: map[string]string{"Read": "func()"}, File: "a/interface.go"},
			{ID: "example.com/a::Store", Name: "Store", Kind: graph.KindStruct, File: "a/store.go"},
			{ID: "example.com/a::FakeReader", Name: "FakeReader", Kind: graph.KindStruct, PackageName: "a", File: "a/store_test.go"},
			{ID: "example.com/a::(*FakeReader).Read", Name: "Read", Kind: graph.KindMethod, Receiver: "*FakeReader", PackageName: "a", MethodSignature: "func()", File: "a/store_test.go"},
			{ID: "example.com/b::FakeReader", Name: "FakeReader", Kind: graph.KindStruct, PackageName: "b", File: "b/store_test.go"},
			{ID: "example.com/c::(*FakeReader).Read", Name: "Read", Kind: graph.KindMethod, Receiver: "*FakeReader", PackageName: "c", MethodSignature: "func()", File: "c/store_test.go"},
		},
		Implements: []graph.ImplementsEdge{
			{Interface: "Reader", Concrete: "Store", InterfaceID: "example.com/a::Reader", ConcreteID: "example.com/a::Store"},
		},
	}

	implementers := search.Implementers(g, "example.com/a::Reader")
	if len(implementers) != 2 {
		t.Fatalf("implementers = %+v, want production type plus test fake", implementers)
	}
	files := map[string]bool{}
	for _, result := range implementers {
		files[result.File] = true
	}
	if !files["a/store.go"] || !files["a/store_test.go"] || files["b/store_test.go"] {
		t.Fatalf("implementers = %+v, want only package-qualified implementations", implementers)
	}

	mocks := search.Mocks(g, "example.com/a::Reader")
	if len(mocks) != 1 || mocks[0].File != "a/store_test.go" {
		t.Fatalf("test-only implementers = %+v, want a/store_test.go", mocks)
	}
}
