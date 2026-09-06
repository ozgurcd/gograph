package search

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
)

const (
	ResultsSchemaVersion = "gograph.results.v1"
	DefaultResultsLimit  = 100
	MaxResultsLimit      = 200
	// Native MCP replies include both JSON text and structuredContent. Reserve
	// space for both copies, provenance and transport framing under a 64 KiB cap.
	MaxResultsBytes = 16 * 1024
)

// ListPaginationCommands is the shared CLI/MCP inventory, not a second set of
// independently maintained transport contracts.
var ListPaginationCommands = []string{
	"query", "focus", "node", "callers", "callees", "implementers", "mocks",
	"fields", "envs", "interfaces", "concurrency", "dependents", "public",
	"embeds", "imports", "impact", "errors", "httpcalls", "mutate",
	"constructors", "usages", "returnusage", "literals", "schema", "globals",
	"fixtures", "orphans", "boundaries",
}

func SupportsListPagination(command string) bool {
	for _, name := range ListPaginationCommands {
		if command == name {
			return true
		}
	}
	return false
}

type ResultPage struct {
	SchemaVersion string            `json:"schema_version"`
	Command       string            `json:"command"`
	Status        string            `json:"status"`
	Limit         int               `json:"limit"`
	Offset        int               `json:"offset"`
	Count         int               `json:"count"`
	Total         int               `json:"total"`
	Returned      int               `json:"returned"`
	Truncated     bool              `json:"truncated"`
	NextCursor    string            `json:"next_cursor"`
	Results       []json.RawMessage `json:"results"`
}

// PageResults binds continuation to an immutable graph fingerprint, operation,
// and exact filtered result selection. Equivalent filters yielding the same
// selection can share cursors across CLI/MCP. Rows are encoded individually to
// avoid allocating an unbounded complete serialized census.
func PageResults(snapshot, command string, rows any, limit int, cursor string) (ResultPage, error) {
	page := ResultPage{SchemaVersion: ResultsSchemaVersion, Command: command, Status: "ok", Results: []json.RawMessage{}}
	if limit == 0 {
		limit = DefaultResultsLimit
	}
	if limit < 1 || limit > MaxResultsLimit {
		return page, fmt.Errorf("limit must be between 1 and %d", MaxResultsLimit)
	}
	if len(cursor) > 128 {
		return page, fmt.Errorf("invalid result cursor; restart without cursor")
	}
	value := reflect.ValueOf(rows)
	if !value.IsValid() {
		value = reflect.ValueOf([]Result{})
	}
	if value.Kind() != reflect.Slice {
		return page, fmt.Errorf("result pagination requires a list")
	}
	h := sha256.New()
	encoder := json.NewEncoder(h)
	for _, header := range []string{ResultsSchemaVersion, snapshot, command} {
		if err := encoder.Encode(header); err != nil {
			return page, err
		}
	}
	for i := 0; i < value.Len(); i++ {
		if err := encoder.Encode(value.Index(i).Interface()); err != nil {
			return page, fmt.Errorf("encode result identity: %w", err)
		}
	}
	binding := hex.EncodeToString(h.Sum(nil))
	encodedOffset, err := cursorOffset(cursor, binding, "result")
	if err != nil {
		return page, err
	}
	offset, err := decodeRouteCursor(encodedOffset)
	if err != nil {
		return page, err
	}
	if offset > value.Len() {
		return page, fmt.Errorf("result cursor is past the selected rows; restart without cursor")
	}
	page.Limit, page.Offset, page.Total = limit, offset, value.Len()
	if page.Total == 0 {
		page.Status = "empty"
	}
	bytesUsed := 0
	for i := offset; i < value.Len() && len(page.Results) < limit; i++ {
		row, err := json.Marshal(value.Index(i).Interface())
		if err != nil {
			return page, err
		}
		// 1024 reserves fixed metadata and a maximum-length continuation token.
		if bytesUsed+len(row)+1 > MaxResultsBytes-1024 {
			if len(page.Results) == 0 {
				return page, fmt.Errorf("one result exceeds the %d-byte response budget; narrow the query or inspect its source directly", MaxResultsBytes)
			}
			break
		}
		page.Results = append(page.Results, row)
		bytesUsed += len(row) + 1
	}
	page.Returned = len(page.Results)
	page.Count = page.Returned
	page.Truncated = offset+page.Returned < page.Total
	if page.Truncated {
		page.NextCursor = boundCursor(binding, encodeRouteCursor(offset+page.Returned))
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		return page, err
	}
	if len(encoded) > MaxResultsBytes {
		return page, fmt.Errorf("result page exceeds response budget; narrow the query")
	}
	return page, nil
}
