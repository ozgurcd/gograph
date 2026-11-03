package search_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

// flowLatticeGraph builds levels of two functions each, where every function
// calls both functions of the next level. The number of distinct call strings
// doubles per level, which made exact call-string tracking exponential.
func flowLatticeGraph(root string, levels int) *graph.Graph {
	g := &graph.Graph{Root: root}
	name := func(level, side int) string { return fmt.Sprintf("f%d_%d", level, side) }
	id := func(level, side int) string { return "example.com/app::" + name(level, side) }
	g.FlowFunctions = append(g.FlowFunctions, graph.FlowFunction{
		ID: "example.com/app::Handle", Name: "Handle", File: "handler.go",
		Facts: []graph.FlowFact{
			{Kind: "source", Target: "request", SourceKind: "http_request", Detail: "request", Line: 1},
			{Kind: "call", Target: "$call:2:1", Inputs: []string{"request"}, Arguments: [][]string{{"request"}}, Callee: name(0, 0), Detail: name(0, 0) + "(request)", Line: 2, Column: 1},
			{Kind: "call", Target: "$call:3:1", Inputs: []string{"request"}, Arguments: [][]string{{"request"}}, Callee: name(0, 1), Detail: name(0, 1) + "(request)", Line: 3, Column: 1},
		},
	})
	g.Calls = append(g.Calls,
		graph.CallEdge{CallerSymbolID: "example.com/app::Handle", CalleeRaw: name(0, 0), CalleeSymbolID: id(0, 0), File: "handler.go", Line: 2},
		graph.CallEdge{CallerSymbolID: "example.com/app::Handle", CalleeRaw: name(0, 1), CalleeSymbolID: id(0, 1), File: "handler.go", Line: 3},
	)
	for level := 0; level < levels; level++ {
		for side := 0; side < 2; side++ {
			file := name(level, side) + ".go"
			function := graph.FlowFunction{
				ID: id(level, side), Name: name(level, side), File: file,
				Params: []graph.FlowParameter{{Name: "value", Type: "string"}},
			}
			if level == levels-1 {
				function.Facts = []graph.FlowFact{{Kind: "sink", Inputs: []string{"value"}, Callee: "exec.Command", SinkKind: "process_execution", Detail: "exec.Command(value)", Line: 1}}
			} else {
				for next := 0; next < 2; next++ {
					line := 2 + next
					function.Facts = append(function.Facts, graph.FlowFact{
						Kind: "call", Target: fmt.Sprintf("$call:%d:1", line), Inputs: []string{"value"}, Arguments: [][]string{{"value"}},
						Callee: name(level+1, next), Detail: name(level+1, next) + "(value)", Line: line, Column: 1,
					})
					g.Calls = append(g.Calls, graph.CallEdge{CallerSymbolID: id(level, side), CalleeRaw: name(level+1, next), CalleeSymbolID: id(level+1, next), File: file, Line: line})
				}
			}
			g.FlowFunctions = append(g.FlowFunctions, function)
		}
	}
	return g
}

func TestFlowCompletesOnExponentialCallContexts(t *testing.T) {
	report, err := search.FlowWithReport(flowLatticeGraph(t.TempDir(), 24), search.FlowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Completeness.Complete {
		t.Fatalf("flow must reach a fixed point within the default budget: %+v", report.Completeness)
	}
	if report.Completeness.ContextDepth != search.DefaultFlowContextDepth || report.Completeness.MaxWork != search.DefaultFlowWork(flowLatticeGraph(t.TempDir(), 24)) {
		t.Fatalf("unexpected completeness defaults: %+v", report.Completeness)
	}
	if len(report.Results) == 0 {
		t.Fatal("flow through 24 nested calls must reach the sink")
	}
	for _, result := range report.Results {
		if result.Confidence != "medium" {
			t.Fatalf("a call-only chain needs no unmatched return and stays medium confidence: %+v", result)
		}
	}
}

func TestFlowReportsExhaustedBudgetAsIncomplete(t *testing.T) {
	g := flowLatticeGraph(t.TempDir(), 6)
	report, err := search.FlowWithReport(g, search.FlowOptions{MaxWork: 3})
	if err != nil {
		t.Fatal(err)
	}
	if report.Completeness.Complete || report.Completeness.MaxWork != 3 || !strings.Contains(report.Completeness.Reason, "--max-work") {
		t.Fatalf("a tiny budget must be reported as incomplete with remedy: %+v", report.Completeness)
	}
	if _, err := search.Flow(g, search.FlowOptions{MaxWork: 3}); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("Flow must not return a partial answer silently, got %v", err)
	}
	for _, options := range []search.FlowOptions{{MaxWork: -1}, {MaxWork: search.MaxFlowWork + 1}, {ContextDepth: -1}, {ContextDepth: search.MaxFlowContextDepth + 1}} {
		if _, err := search.FlowWithReport(g, options); err == nil {
			t.Fatalf("expected option error for %+v", options)
		}
	}
}

// flowWrapperGraph passes a request value through depth identity wrappers and
// back. An unrelated caller also calls the outermost wrapper with a constant.
func flowWrapperGraph(root string, depth int) *graph.Graph {
	g := &graph.Graph{Root: root}
	wrapper := func(level int) string { return fmt.Sprintf("wrap%d", level) }
	g.FlowFunctions = append(g.FlowFunctions,
		graph.FlowFunction{
			ID: "example.com/app::Handle", Name: "Handle", File: "handler.go",
			Facts: []graph.FlowFact{
				{Kind: "source", Target: "request", SourceKind: "http_request", Detail: "request", Line: 1},
				{Kind: "call", Target: "$call:2:1", Inputs: []string{"request"}, Arguments: [][]string{{"request"}}, Callee: wrapper(0), Detail: "wrap0(request)", Line: 2, Column: 1},
				{Kind: "transfer", Target: "path", Inputs: []string{"$call:2:1"}, Detail: "path := wrap0(request)", Line: 2},
				{Kind: "sink", Inputs: []string{"path"}, Callee: "os.ReadFile", SinkKind: "filesystem", Detail: "os.ReadFile(path)", Line: 3},
			},
		},
		graph.FlowFunction{
			ID: "example.com/app::Safe", Name: "Safe", File: "safe.go",
			Facts: []graph.FlowFact{
				{Kind: "call", Target: "$call:2:1", Callee: wrapper(0), Detail: "wrap0(\"safe\")", Line: 2, Column: 1},
				{Kind: "transfer", Target: "path", Inputs: []string{"$call:2:1"}, Detail: "path := wrap0(\"safe\")", Line: 2},
				{Kind: "sink", Inputs: []string{"path"}, Callee: "os.ReadFile", SinkKind: "filesystem", Detail: "os.ReadFile(path)", Line: 3},
			},
		},
	)
	g.Calls = append(g.Calls,
		graph.CallEdge{CallerSymbolID: "example.com/app::Handle", CalleeRaw: wrapper(0), CalleeSymbolID: "example.com/app::" + wrapper(0), File: "handler.go", Line: 2},
		graph.CallEdge{CallerSymbolID: "example.com/app::Safe", CalleeRaw: wrapper(0), CalleeSymbolID: "example.com/app::" + wrapper(0), File: "safe.go", Line: 2},
	)
	for level := 0; level < depth; level++ {
		file := wrapper(level) + ".go"
		function := graph.FlowFunction{
			ID: "example.com/app::" + wrapper(level), Name: wrapper(level), File: file,
			Params: []graph.FlowParameter{{Name: "value", Type: "string"}},
		}
		if level == depth-1 {
			function.Facts = []graph.FlowFact{{Kind: "return", Target: "$return:0", Inputs: []string{"value"}, Detail: "return value", Line: 1}}
		} else {
			function.Facts = []graph.FlowFact{
				{Kind: "call", Target: "$call:1:1", Inputs: []string{"value"}, Arguments: [][]string{{"value"}}, Callee: wrapper(level + 1), Detail: wrapper(level+1) + "(value)", Line: 1, Column: 1},
				{Kind: "return", Target: "$return:0", Inputs: []string{"$call:1:1"}, Detail: "return " + wrapper(level+1) + "(value)", Line: 1},
			}
			g.Calls = append(g.Calls, graph.CallEdge{CallerSymbolID: function.ID, CalleeRaw: wrapper(level + 1), CalleeSymbolID: "example.com/app::" + wrapper(level+1), File: file, Line: 1})
		}
		g.FlowFunctions = append(g.FlowFunctions, function)
	}
	return g
}

func TestFlowFollowsReturnsBeyondContextDepth(t *testing.T) {
	g := flowWrapperGraph(t.TempDir(), 6)

	deep, err := search.Flow(g, search.FlowOptions{ContextDepth: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(deep) != 1 || deep[0].Sink.Function != "Handle" || deep[0].Confidence != "medium" {
		t.Fatalf("a context deeper than the chain must match every return exactly: %+v", deep)
	}

	shallow, err := search.Flow(g, search.FlowOptions{ContextDepth: 2})
	if err != nil {
		t.Fatal(err)
	}
	var handle bool
	for _, result := range shallow {
		if result.Confidence != "low" {
			t.Fatalf("returns past a dropped frame must lower confidence: %+v", result)
		}
		handle = handle || result.Sink.Function == "Handle"
	}
	if !handle {
		t.Fatalf("a chain deeper than the context must still reach the sink: %+v", shallow)
	}
}
