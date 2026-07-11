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
