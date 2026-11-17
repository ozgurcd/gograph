package search

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

// bruteForceUnresolvedTargetIDs is the original full-scan resolver. The
// indexed resolver must return exactly the same IDs for every input.
func bruteForceUnresolvedTargetIDs(g *graph.Graph, raw, callerPackage string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	name := raw
	if index := strings.LastIndex(name, "."); index >= 0 {
		name = name[index+1:]
	}
	if index := strings.Index(name, "["); index >= 0 {
		name = name[:index]
	}
	seen := make(map[string]bool)
	var ids []string
	for _, symbol := range g.Symbols {
		if symbol.Kind != graph.KindFunction && symbol.Kind != graph.KindMethod {
			continue
		}
		matched := matchSymbolExact(symbol, raw)
		if !matched && symbol.Name == name {
			matched = strings.Contains(raw, ".") || symbol.PackageName == callerPackage
		}
		if matched && !seen[symbol.ID] {
			seen[symbol.ID] = true
			ids = append(ids, symbol.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func TestUnresolvedTargetIndexMatchesFullScan(t *testing.T) {
	g := &graph.Graph{Symbols: []graph.SymbolNode{
		{ID: "example.com/app/service::Run", Kind: graph.KindFunction, Name: "Run", PackageName: "service", File: "service/run.go"},
		{ID: "example.com/app/service::(*Impl).Run", Kind: graph.KindMethod, Name: "Run", Receiver: "*Impl", PackageName: "service", File: "service/impl.go"},
		{ID: "example.com/app/service::Value.Run", Kind: graph.KindMethod, Name: "Run", Receiver: "Value", PackageName: "service", File: "service/value.go"},
		{ID: "example.com/app/other::Run", Kind: graph.KindFunction, Name: "Run", PackageName: "other", File: "other/run.go"},
		{ID: "example.com/app/other::run", Kind: graph.KindFunction, Name: "run", PackageName: "other", File: "other/run.go"},
		{ID: "example.com/app/generic::Map", Kind: graph.KindFunction, Name: "Map", PackageName: "generic", File: "generic/map.go"},
		{ID: "example.com/app/generic::(*List[T]).Push", Kind: graph.KindMethod, Name: "Push", Receiver: "*List[T]", PackageName: "generic", File: "generic/list.go"},
		{ID: "example.com/app/service::Config", Kind: graph.KindStruct, Name: "Config", PackageName: "service", File: "service/config.go"},
		{ID: "example.com/app/service::Error", Kind: graph.KindMethod, Name: "Error", Receiver: "ErrX", PackageName: "service", File: "service/err.go"},
	}}
	raws := []string{
		"", " ", "Run", "run", "RUN", " Run ", "Impl.Run", "(*Impl).Run", "*Impl.Run", "Value.Run",
		"service.Run", "service.Impl.Run", "service.(*Impl).Run", "other.Run", "other.run", "x.Run",
		"example.com/app/service::Run", "example.com/app/service::(*Impl).Run", "EXAMPLE.COM/app/service::run",
		"example.com/app/service.Run", "example.com/app/service.(*Impl).Run", "example.com/app/service.Impl.Run",
		"service/impl.go:Impl.Run", "service/impl.go:Run", "service/run.go:Run", "example.com/app/service/impl.go:Impl.Run",
		"Map", "Map[int]", "generic.Map[int, string]", "List.Push", "(*List[T]).Push", "generic.(*List[int]).Push",
		"Config", "service.Config", "Error", "err.Error", "fmt.Errorf", "Ru n", "service . Run", "(Run)", "Missing",
	}
	index := newUnresolvedTargetIndex(g)
	for _, raw := range raws {
		for _, pkg := range []string{"", "service", "other", "generic"} {
			want := bruteForceUnresolvedTargetIDs(g, raw, pkg)
			got := index.targetIDs(raw, pkg)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("targetIDs(%q, %q) = %v, want %v", raw, pkg, got, want)
			}
		}
	}
}

func TestUnresolvedTargetIndexNilGraph(t *testing.T) {
	if got := newUnresolvedTargetIndex(nil).targetIDs("Run", ""); got != nil {
		t.Fatalf("nil graph targets = %v", got)
	}
}
