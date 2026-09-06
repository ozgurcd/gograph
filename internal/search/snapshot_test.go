package search

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func snapshotAttributionFixture() *graph.Graph {
	return &graph.Graph{
		Build: &graph.BuildMetadata{Precision: graph.PrecisionPrecise, TestCallResolution: graph.TestCallResolutionTyped},
		Symbols: []graph.SymbolNode{
			{ID: "example.com/app/service::TestFlow", Kind: graph.KindFunction, Name: "TestFlow", PackageName: "service", File: "service/flow_test.go", Line: 10},
			{ID: "example.com/app/other::TestFlow", Kind: graph.KindFunction, Name: "TestFlow", PackageName: "other", File: "other/flow_test.go", Line: 7},
			{ID: "example.com/app/service::Start", Kind: graph.KindFunction, Name: "Start", PackageName: "service", File: "service/flow.go", Line: 10},
			{ID: "example.com/app/service::Exact", Kind: graph.KindFunction, Name: "Exact", PackageName: "service", File: "service/flow.go", Line: 20},
			{ID: "example.com/app/service::(*Impl).Run", Kind: graph.KindMethod, Name: "Run", Receiver: "*Impl", PackageName: "service", File: "service/impl.go", Line: 30},
			{ID: "example.com/app/service::Leaf", Kind: graph.KindFunction, Name: "Leaf", PackageName: "service", File: "service/leaf.go", Line: 40},
		},
		TestEdges: []graph.TestEdge{
			{TestFunc: "TestFlow", Target: "Start", TargetSymbolID: "example.com/app/service::Start", Resolution: graph.CallResolutionStatic, File: "service/flow_test.go", Line: 11},
		},
		Calls: []graph.CallEdge{
			{CallerSymbolID: "example.com/app/service::Start", CalleeSymbolID: "example.com/app/service::Exact", Resolution: graph.CallResolutionStatic},
			{CallerSymbolID: "example.com/app/service::Exact", CalleeSymbolID: "example.com/app/service::(*Impl).Run", Resolution: graph.CallResolutionCHA},
			{CallerSymbolID: "example.com/app/service::(*Impl).Run", CalleeSymbolID: "example.com/app/service::Leaf", Resolution: graph.CallResolutionStatic},
		},
	}
}

func snapshotSQLFixture() *graph.Graph {
	g := &graph.Graph{}
	for i := 0; i < 500; i++ {
		g.SQLs = append(g.SQLs, graph.SQLEdge{Query: fmt.Sprintf("INSERT INTO accounts (id) VALUES (%d) RETURNING id", i), File: "store.go", Function: "Save", Line: i + 1})
	}
	return g
}

func TestSnapshotSQLPagesMatchAndOwnTheirTables(t *testing.T) {
	g := snapshotSQLFixture()
	snapshot := NewSnapshot(g)
	query := SQLQuery{Tables: []string{"accounts"}, Limit: 7}
	first, err := snapshot.QuerySQL(query)
	if err != nil {
		t.Fatal(err)
	}
	want, err := QuerySQL(g, query)
	if err != nil || !reflect.DeepEqual(first, want) {
		t.Fatalf("cached result differs: err=%v", err)
	}
	first.Queries[0].Tables[0] = SQLTable{}
	again, err := snapshot.QuerySQL(query)
	if err != nil || !reflect.DeepEqual(again, want) {
		t.Fatal("response mutation poisoned cached SQL classification")
	}
	query.Cursor = first.NextCursor
	second, err := snapshot.QuerySQL(query)
	if err != nil || second.Returned != 7 {
		t.Fatalf("second page: %+v %v", second, err)
	}
	changed := *g
	changed.SQLs = append([]graph.SQLEdge{}, g.SQLs...)
	changed.SQLs[0].Query = "DELETE FROM accounts"
	if _, err := NewSnapshot(&changed).QuerySQL(query); err == nil {
		t.Fatal("changed snapshot accepted old cursor")
	}
	if _, err := snapshot.QuerySQL(query); err != nil {
		t.Fatalf("old live snapshot invalidated by a new one: %v", err)
	}
}

func TestSnapshotIndexesConcurrentAndEquivalent(t *testing.T) {
	g := snapshotAttributionFixture()
	snapshot := NewSnapshot(g)
	wantTests := TransitiveTestsInPackage(g, g.Symbols[len(g.Symbols)-1].ID, "", false)
	wantUntested := Untested(g)
	wantImpact := ImpactWithOptions(g, []string{g.Symbols[len(g.Symbols)-1].ID}, "reason", ImpactOptions{IncludeTests: true})
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := snapshot.TransitiveTests(g.Symbols[len(g.Symbols)-1].ID, "", false); !reflect.DeepEqual(got, wantTests) {
				t.Error("cached test attribution differs")
			}
			if got := snapshot.Untested(); !reflect.DeepEqual(got, wantUntested) {
				t.Error("cached untested differs")
			}
			if got := snapshot.Impact([]string{g.Symbols[len(g.Symbols)-1].ID}, "reason", ImpactOptions{IncludeTests: true}); !reflect.DeepEqual(got, wantImpact) {
				t.Error("cached impact differs")
			}
		}()
	}
	wg.Wait()
}

func TestSnapshotRepeatedSQLAvoidsReclassificationAllocations(t *testing.T) {
	g := snapshotSQLFixture()
	snapshot := NewSnapshot(g)
	query := SQLQuery{Limit: 1}
	if _, err := snapshot.QuerySQL(query); err != nil {
		t.Fatal(err)
	}
	warm := testing.AllocsPerRun(3, func() {
		if _, err := snapshot.QuerySQL(query); err != nil {
			t.Fatal(err)
		}
	})
	cold := testing.AllocsPerRun(3, func() {
		if _, err := QuerySQL(g, query); err != nil {
			t.Fatal(err)
		}
	})
	if warm >= cold/2 {
		t.Fatalf("warm allocations %.0f, cold %.0f: classification was not effectively reused", warm, cold)
	}
	t.Logf("SQL allocations: cached %.0f, uncached %.0f", warm, cold)
}

func BenchmarkSnapshotSQL(b *testing.B) {
	g := snapshotSQLFixture()
	for _, cached := range []bool{false, true} {
		b.Run(fmt.Sprintf("cached=%t", cached), func(b *testing.B) {
			snapshot := NewSnapshot(g)
			_, _ = snapshot.QuerySQL(SQLQuery{Limit: 1})
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				var err error
				if cached {
					_, err = snapshot.QuerySQL(SQLQuery{Limit: 1})
				} else {
					_, err = QuerySQL(g, SQLQuery{Limit: 1})
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
