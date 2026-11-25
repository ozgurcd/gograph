package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/graphstate"
	"github.com/ozgurcd/gograph/internal/search"
)

// jsonMode is set to true when --json is present in the CLI args.
// It is package-level (single-threaded CLI) so run* functions can read it
// without threading a parameter through every call.
var jsonMode bool
var filesOnlyMode bool
var mermaidMode bool

// resetOutputModes restores the default text output.
func resetOutputModes() {
	jsonMode = false
	filesOnlyMode = false
	mermaidMode = false
}

var outputGraph struct {
	sync.RWMutex
	graph *graph.Graph
	// stale memoizes the freshness check of graph for this invocation so the
	// JSON graph_state and the stale warning never hash the sources twice.
	stale *search.StaleResult
}

// SchemaVersion is the stable JSON output schema version. Bump when the
// envelope shape changes in a backwards-incompatible way.
const SchemaVersion = "1"

// Envelope is the top-level JSON wrapper for all --json output.
// schema_version lets agents pin to a known schema and detect changes.
type Envelope struct {
	Warning       string `json:"warning,omitempty"`
	SchemaVersion string `json:"schema_version"`
	Command       string `json:"command"`
	Query         string `json:"query,omitempty"`
	// Status is "ok" (results found), "empty" (no results, symbol/query valid),
	// or "error" (hard failure — graph missing, parse error, etc.).
	Status  string      `json:"status"`
	Count   int         `json:"count"`
	Results interface{} `json:"results,omitempty"`
	Error   string      `json:"error,omitempty"`
	// Route-page metadata is mirrored at the envelope level so callers can
	// detect and continue a bounded routes result without knowing the nested
	// results representation. Other commands leave these fields nil/empty.
	Total      *int    `json:"total,omitempty"`
	Limit      int     `json:"limit,omitempty"`
	Offset     *int    `json:"offset,omitempty"`
	Returned   *int    `json:"returned,omitempty"`
	Truncated  *bool   `json:"truncated,omitempty"`
	NextCursor *string `json:"next_cursor,omitempty"`
	// Completeness is additive evidence that a bounded analysis stopped
	// before a fixed point. Only commands with such a bound set it.
	Completeness interface{} `json:"completeness,omitempty"`
	// Summary is additive per-command accounting, such as what untested
	// skipped; commands without one leave it nil.
	Summary interface{} `json:"summary,omitempty"`
	// GraphState is additive request provenance for repository graph-backed
	// JSON commands. It is omitted for host, lifecycle, and hard-error results.
	GraphState *graphstate.State `json:"graph_state,omitempty"`
}

// PrintJSON serialises env to stdout as indented JSON and always exits
// cleanly (exit 0) unless Status is "error", in which case it exits 1.
// This function never returns.
func PrintJSON(env Envelope) int {
	isRead := env.Command == "source" || env.Command == "context"
	if env.GraphState == nil && (env.Status != "error" || isRead) && commandReportsGraphState(env.Command) {
		env.GraphState = currentOutputGraphState()
	}
	if isRead && env.GraphState != nil {
		env.GraphState.ReadDiagnostic = env.Error
		if payload, ok := env.Results.(search.ContextPayload); ok {
			env.GraphState.ReadDiagnostic = payload.SourceError
		}
	}
	if env.Status != "error" && env.GraphState != nil {
		env.Warning = search.EmptyAnswerWarning(env.Command, string(env.GraphState.Precision), env.Count)
		if env.GraphState.Freshness == graphstate.FreshnessStale && commandWarnsWhenStale(env.Command) {
			outputGraph.RLock()
			g := outputGraph.graph
			outputGraph.RUnlock()
			if g != nil {
				env.Warning = strings.TrimSpace(staleGraphWarning(loadedGraphStale(g)) + " " + env.Warning)
			}
		}
	}
	if autoRefresh.note != "" {
		env.Warning = strings.TrimSpace(autoRefresh.note + " " + env.Warning)
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "json marshal error: %v\n", err)
		return 1
	}
	fmt.Println(string(data))
	if env.Status == "error" {
		return 1
	}
	return 0
}

func currentOutputGraphState() *graphstate.State {
	outputGraph.RLock()
	g := outputGraph.graph
	outputGraph.RUnlock()
	if g == nil {
		return nil
	}
	stale := loadedGraphStale(g)
	state := graphstate.ManualPersisted(g, stale.IsStale)
	return &state
}

// loadedGraphStale returns the freshness of the graph loaded for this
// invocation, computing it at most once.
func loadedGraphStale(g *graph.Graph) search.StaleResult {
	outputGraph.Lock()
	defer outputGraph.Unlock()
	if outputGraph.stale != nil && outputGraph.graph == g {
		return *outputGraph.stale
	}
	stale := search.StaleForQuery(g, graphRoot(g), queryBuildTags)
	if outputGraph.graph == g {
		outputGraph.stale = &stale
	}
	return stale
}

func resetOutputGraph() {
	outputGraph.Lock()
	outputGraph.graph = nil
	outputGraph.stale = nil
	outputGraph.Unlock()
}

func rememberOutputGraph(g *graph.Graph) {
	outputGraph.Lock()
	outputGraph.graph = g
	outputGraph.stale = nil
	outputGraph.Unlock()
}

func commandReportsGraphState(command string) bool {
	switch command {
	case "query", "explore", "focus", "node", "source", "public", "fields", "embeds", "imports",
		"callers", "callees", "impact", "implementers", "envs", "interfaces", "concurrency", "tests",
		"coverage", "identity", "routes", "sql", "errors", "errorflow", "trace", "flow", "path",
		"stale", "stats", "summary", "untested", "orphans", "godobj", "skeleton", "mutate", "arity",
		"complexity", "diagram", "coupling", "context", "hotspot", "deps", "dependents", "changes",
		"constructors", "literals", "usages", "returnusage", "schema", "globals", "mocks", "fixtures",
		"boundaries", "endpoint", "explain", "plan", "review", "risk", "api", "contract", "check",
		"httpcalls", "strings":
		return true
	default:
		return false
	}
}

// okEnvelope builds a standard "ok" envelope for slice results.
func okEnvelope(cmd, query string, results interface{}, count int) Envelope {
	status := "ok"
	if count == 0 {
		status = "empty"
		results = normalizeEmptyResults(results)
	}
	return Envelope{
		SchemaVersion: SchemaVersion,
		Command:       cmd,
		Query:         query,
		Status:        status,
		Count:         count,
		Results:       results,
	}
}

// normalizeEmptyResults keeps successful empty collection responses
// structurally useful to JSON consumers. An interface holding a nil slice is
// not itself nil, so handle both forms explicitly while preserving the slice's
// concrete type when possible.
func normalizeEmptyResults(results interface{}) interface{} {
	if results == nil {
		return []any{}
	}
	value := reflect.ValueOf(results)
	if value.Kind() == reflect.Slice && value.IsNil() {
		return reflect.MakeSlice(value.Type(), 0, 0).Interface()
	}
	return results
}

// errEnvelope builds an error envelope (graph not found, parse failure, etc.).
func errEnvelope(cmd, msg string) Envelope {
	return Envelope{
		SchemaVersion: SchemaVersion,
		Command:       cmd,
		Status:        "error",
		Error:         msg,
	}
}
