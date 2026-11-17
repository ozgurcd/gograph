package search_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

func writeFlowPolicy(t *testing.T, root, policy string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gograph", "flow.json"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
}

// relayFunction reads agent output and writes it as argument 1 of an MCP
// response, optionally through a return or argument sanitizer.
func relayFunction(name string, line int, sanitize string) graph.FlowFunction {
	read := graph.FlowFact{Kind: "call", Target: "$call:read", Callee: "agent.ReadAgentOutput", Arguments: [][]string{{"path"}}, Inputs: []string{"path"}, Detail: "agent.ReadAgentOutput(path)", Line: line}
	assign := graph.FlowFact{Kind: "transfer", Target: "out", Inputs: []string{"$call:read"}, Detail: "out := agent.ReadAgentOutput(path)", Line: line}
	facts := []graph.FlowFact{read, assign}
	value := "out"
	switch sanitize {
	case "return":
		facts = append(facts,
			graph.FlowFact{Kind: "call", Target: "$call:escape", Callee: "mcpio.Escape", Arguments: [][]string{{"out"}}, Inputs: []string{"out"}, Detail: "mcpio.Escape(out)", Line: line + 1},
			graph.FlowFact{Kind: "transfer", Target: "safe", Inputs: []string{"$call:escape"}, Detail: "safe := mcpio.Escape(out)", Line: line + 1})
		value = "safe"
	case "argument":
		facts = append(facts, graph.FlowFact{Kind: "call", Target: "$call:validate", Callee: "mcpio.Validate", Arguments: [][]string{{"out"}}, Inputs: []string{"out"}, Detail: "mcpio.Validate(out)", Line: line + 1})
	}
	facts = append(facts,
		graph.FlowFact{Kind: "call", Target: "$call:write", Callee: "mcpio.WriteMCPResponse", Arguments: [][]string{{"id"}, {value}}, Inputs: []string{"id", value}, Detail: "mcpio.WriteMCPResponse(id, " + value + ")", Line: line + 2},
		graph.FlowFact{Kind: "call", Target: "$call:write2", Callee: "mcpio.WriteMCPResponse", Arguments: [][]string{{value}, {"fixed"}}, Inputs: []string{value}, Detail: "mcpio.WriteMCPResponse(" + value + ", fixed)", Line: line + 3})
	return graph.FlowFunction{ID: "example.com/app/tools::" + name, Name: name, File: "tools/tools.go", Facts: facts}
}

func TestFlowCustomSourcesSinksAndSanitizers(t *testing.T) {
	root := t.TempDir()
	writeFlowPolicy(t, root, `{
	  "sources": [{"function": "agent.ReadAgentOutput", "kind": "agent_output", "return": 0}],
	  "sinks": [{"function": "mcpio.WriteMCPResponse", "kind": "mcp_response", "argument": 1}],
	  "sanitizers": [
	    {"function": "mcpio.Escape", "for": ["mcp_response"]},
	    {"function": "mcpio.Validate", "for": ["mcp_response"], "argument": 0}
	  ]
	}`)
	g := &graph.Graph{Root: root, FlowFunctions: []graph.FlowFunction{
		relayFunction("Relay", 10, ""),
		relayFunction("RelayEscaped", 20, "return"),
		relayFunction("RelayValidated", 30, "argument"),
	}}
	results, err := search.Flow(g, search.FlowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("want exactly the unsanitized Relay path, got %+v", results)
	}
	got := results[0]
	if got.Source.Kind != "agent_output" || got.Sink.Kind != "mcp_response" || got.Sink.Function != "Relay" || got.Sink.Line != 12 || got.Severity != "high" {
		t.Fatalf("finding = %+v", got)
	}
	if _, err := search.Flow(g, search.FlowOptions{Source: "agent_output", Sink: "mcp_response"}); err != nil {
		t.Fatalf("custom kinds rejected as filters: %v", err)
	}
	if _, err := search.Flow(g, search.FlowOptions{Sink: "unknown_kind"}); err == nil || !strings.Contains(err.Error(), "mcp_response") {
		t.Fatalf("unknown sink error should list custom kinds, got %v", err)
	}

	// Without a policy the same graph has no findings: defaults are unchanged.
	if err := os.Remove(filepath.Join(root, ".gograph", "flow.json")); err != nil {
		t.Fatal(err)
	}
	if results, err := search.Flow(g, search.FlowOptions{}); err != nil || len(results) != 0 {
		t.Fatalf("default findings = %+v, %v", results, err)
	}
}

func TestFlowPolicyValidation(t *testing.T) {
	for _, policy := range []string{
		`{"sources":[{"function":"a.B","kind":"Bad-Kind"}]}`,
		`{"sinks":[{"function":"","kind":"x"}]}`,
		`{"sinks":[{"function":"a.B","kind":"x","argument":-1}]}`,
		`{"sinks":[{"function":"a.B","kind":"x","severity":"critical"}]}`,
		`{"sanitizers":[{"function":"a.B","for":["not_a_sink"]}]}`,
		`{"sanitizers":[{"function":"a.B","argument":-2}]}`,
	} {
		root := t.TempDir()
		writeFlowPolicy(t, root, policy)
		g := &graph.Graph{Root: root, FlowFunctions: []graph.FlowFunction{relayFunction("Relay", 1, "")}}
		if _, err := search.Flow(g, search.FlowOptions{}); err == nil {
			t.Errorf("accepted invalid policy %s", policy)
		}
	}
}

func TestFlowPackageScopeTopWorkAndParameterOnly(t *testing.T) {
	source := graph.FlowFact{Kind: "source", Target: "request", SourceKind: "http_request", Detail: "request", Line: 1}
	g := &graph.Graph{Root: t.TempDir(), FlowFunctions: []graph.FlowFunction{
		{ID: "example.com/app/api::Git", Name: "Git", File: "api/git.go", Facts: []graph.FlowFact{source,
			{Kind: "sink", Inputs: []string{"request"}, SinkKind: "process_execution", ParameterOnly: true, Detail: `exec.Command("git", request)`, Line: 2}}},
		{ID: "example.com/app/internal/run::Shell", Name: "Shell", File: "internal/run/shell.go", Facts: []graph.FlowFact{source,
			{Kind: "sink", Inputs: []string{"request"}, SinkKind: "process_execution", Detail: `exec.Command("sh", "-c", request)`, Line: 2}}},
	}}
	results, err := search.Flow(g, search.FlowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	severities := map[string]string{}
	for _, result := range results {
		severities[result.Sink.Function] = result.Severity
		if (result.Sink.Function == "Git") != result.ParameterOnly {
			t.Fatalf("parameter_only = %v for %s", result.ParameterOnly, result.Sink.Function)
		}
	}
	if severities["Git"] != "medium" || severities["Shell"] != "high" {
		t.Fatalf("severities = %v", severities)
	}
	scoped, err := search.Flow(g, search.FlowOptions{Packages: []string{"example.com/app/internal/..."}})
	if err != nil || len(scoped) != 1 || scoped[0].Sink.Function != "Shell" {
		t.Fatalf("package scope = %+v, %v", scoped, err)
	}
	report, err := search.FlowWithReport(g, search.FlowOptions{MaxWork: 1})
	if err != nil || report.Completeness.Complete || len(report.Completeness.TopFunctions) == 0 {
		t.Fatalf("exhausted report = %+v, %v", report.Completeness, err)
	}
	shares := report.Completeness.TopFunctions
	if !sort.SliceIsSorted(shares, func(i, j int) bool { return shares[i].Work > shares[j].Work }) {
		t.Fatalf("top functions not ordered by work: %+v", shares)
	}
}
