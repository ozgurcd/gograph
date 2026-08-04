package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
)

// jsonMode is set to true when --json is present in the CLI args.
// It is package-level (single-threaded CLI) so run* functions can read it
// without threading a parameter through every call.
var jsonMode bool
var filesOnlyMode bool
var mermaidMode bool

// SchemaVersion is the stable JSON output schema version. Bump when the
// envelope shape changes in a backwards-incompatible way.
const SchemaVersion = "1"

// Envelope is the top-level JSON wrapper for all --json output.
// schema_version lets agents pin to a known schema and detect changes.
type Envelope struct {
	SchemaVersion string `json:"schema_version"`
	Command       string `json:"command"`
	Query         string `json:"query,omitempty"`
	// Status is "ok" (results found), "empty" (no results, symbol/query valid),
	// or "error" (hard failure — graph missing, parse error, etc.).
	Status  string      `json:"status"`
	Count   int         `json:"count"`
	Results interface{} `json:"results,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// PrintJSON serialises env to stdout as indented JSON and always exits
// cleanly (exit 0) unless Status is "error", in which case it exits 1.
// This function never returns.
func PrintJSON(env Envelope) int {
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
