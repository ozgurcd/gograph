package search

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/sqlquery"
)

const (
	SQLSchemaVersion  = "gograph.sql.v1"
	DefaultSQLLimit   = 100
	MaxSQLLimit       = 200
	MaxSQLResultBytes = 64 * 1024

	maxSQLTermBytes     = 4096
	maxSQLFunctionBytes = 1024
	maxSQLModuleBytes   = 1024
	maxSQLSelectorBytes = 256
	maxSQLCursorBytes   = 1024
	maxSQLFilters       = 32
)

type SQLQuery struct {
	Term     string
	Tables   []string
	Verbs    []string
	Accesses []string
	Function string
	Module   string
	NoTests  bool
	Limit    int
	Cursor   string
}

type SQLTable struct {
	Name   string `json:"name"`
	Access string `json:"access"`
}

type SQLResult struct {
	Query          string     `json:"query"`
	Verb           string     `json:"verb,omitempty"`
	Access         string     `json:"access,omitempty"`
	Classification string     `json:"classification"`
	Tables         []SQLTable `json:"tables"`
	Function       string     `json:"function"`
	File           string     `json:"file"`
	Line           int        `json:"line"`
}

func (result SQLResult) String() string {
	return Result{
		Kind: "sql", Name: result.Query, File: result.File, Line: result.Line,
		Detail: "executed by " + result.Function,
	}.String()
}

type SQLPage struct {
	SchemaVersion string      `json:"schema_version"`
	Term          string      `json:"term,omitempty"`
	Tables        []string    `json:"tables"`
	Verbs         []string    `json:"verbs"`
	Accesses      []string    `json:"accesses"`
	Function      string      `json:"function,omitempty"`
	Module        string      `json:"module,omitempty"`
	IncludeTests  bool        `json:"include_tests"`
	Limit         int         `json:"limit"`
	Cursor        string      `json:"cursor,omitempty"`
	Total         int         `json:"total"`
	Returned      int         `json:"returned"`
	Truncated     bool        `json:"truncated"`
	NextCursor    string      `json:"next_cursor,omitempty"`
	Queries       []SQLResult `json:"queries"`
}

// QuerySQL returns one deterministic, bounded page of PostgreSQL static SQL facts.
func QuerySQL(g *graph.Graph, query SQLQuery) (SQLPage, error) {
	normalized, err := normalizeSQLQuery(query)
	if err != nil {
		return SQLPage{}, err
	}
	offset, err := decodeSQLCursor(normalized.Cursor)
	if err != nil {
		return SQLPage{}, err
	}
	moduleIndex, err := resolveRouteModule(g, normalized.Module)
	if err != nil {
		return SQLPage{}, errors.New(strings.ReplaceAll(err.Error(), "route module", "SQL module"))
	}

	term := strings.ToLower(normalized.Term)
	function := strings.ToLower(normalized.Function)
	rows := make([]SQLResult, 0, len(g.SQLs))
	for _, edge := range g.SQLs {
		if normalized.NoTests && strings.HasSuffix(strings.ToLower(filepath.Base(edge.File)), "_test.go") {
			continue
		}
		if moduleIndex >= 0 && routeModuleIndex(g, edge.File) != moduleIndex {
			continue
		}
		if term != "" && !strings.Contains(strings.ToLower(edge.Query), term) {
			continue
		}
		if function != "" && !strings.Contains(strings.ToLower(edge.Function), function) {
			continue
		}
		row := classifySQLResult(edge)
		if !sqlVerbMatches(row, normalized.Verbs) || !sqlAccessMatches(row, normalized.Accesses) || !sqlTableMatches(row, normalized.Tables) {
			continue
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		left, right := rows[i], rows[j]
		leftQuery, rightQuery := strings.ToLower(left.Query), strings.ToLower(right.Query)
		if leftQuery != rightQuery {
			return leftQuery < rightQuery
		}
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		return left.Function < right.Function
	})
	if offset > len(rows) {
		return SQLPage{}, fmt.Errorf("SQL cursor starts at %d, past the %d filtered queries; restart without cursor", offset, len(rows))
	}

	page := SQLPage{
		SchemaVersion: SQLSchemaVersion,
		Term:          normalized.Term,
		Tables:        append([]string{}, normalized.Tables...),
		Verbs:         append([]string{}, normalized.Verbs...),
		Accesses:      append([]string{}, normalized.Accesses...),
		Function:      normalized.Function,
		Module:        normalized.Module,
		IncludeTests:  !normalized.NoTests,
		Limit:         normalized.Limit,
		Cursor:        normalized.Cursor,
		Total:         len(rows),
		Queries:       []SQLResult{},
	}
	for index := offset; index < len(rows) && len(page.Queries) < normalized.Limit; index++ {
		page.Queries = append(page.Queries, rows[index])
		finalizeSQLPage(&page, offset)
		encoded, marshalErr := json.MarshalIndent(page, "", "  ")
		if marshalErr != nil {
			return SQLPage{}, fmt.Errorf("encode SQL page: %w", marshalErr)
		}
		if len(encoded) <= MaxSQLResultBytes {
			continue
		}
		page.Queries = page.Queries[:len(page.Queries)-1]
		if len(page.Queries) == 0 {
			return SQLPage{}, fmt.Errorf("one SQL result exceeds the %d-byte response budget; narrow the term, table, function, or module filter", MaxSQLResultBytes)
		}
		break
	}
	finalizeSQLPage(&page, offset)
	encoded, err := json.MarshalIndent(page, "", "  ")
	if err != nil {
		return SQLPage{}, fmt.Errorf("encode SQL page: %w", err)
	}
	if len(encoded) > MaxSQLResultBytes {
		return SQLPage{}, fmt.Errorf("SQL response exceeds the %d-byte response budget; narrow the filters", MaxSQLResultBytes)
	}
	return page, nil
}

// SQL preserves the original unbounded term-only search API for internal callers.
func SQL(g *graph.Graph, term string) []Result {
	var results []Result
	normalized := strings.ToLower(term)
	for _, edge := range g.SQLs {
		if term != "" && !strings.Contains(strings.ToLower(edge.Query), normalized) {
			continue
		}
		results = append(results, Result{
			Kind:   "sql",
			Name:   edge.Query,
			File:   edge.File,
			Line:   edge.Line,
			Detail: "executed by " + edge.Function,
			Score:  10,
		})
	}
	sortResults(results)
	return results
}

func classifySQLResult(edge graph.SQLEdge) SQLResult {
	classification := sqlquery.ClassifyPostgreSQL(edge.Query)
	tables := make([]SQLTable, 0, len(classification.Tables))
	for _, table := range classification.Tables {
		tables = append(tables, SQLTable{Name: table.Name, Access: table.Access})
	}
	return SQLResult{
		Query: edge.Query, Verb: classification.Verb, Access: classification.Access,
		Classification: classification.Status, Tables: tables,
		Function: edge.Function, File: edge.File, Line: edge.Line,
	}
}

func normalizeSQLQuery(query SQLQuery) (SQLQuery, error) {
	query.Term = strings.TrimSpace(query.Term)
	query.Function = strings.TrimSpace(query.Function)
	query.Module = strings.TrimSpace(query.Module)
	if len(query.Term) > maxSQLTermBytes {
		return SQLQuery{}, fmt.Errorf("SQL term must not exceed %d bytes", maxSQLTermBytes)
	}
	if len(query.Function) > maxSQLFunctionBytes {
		return SQLQuery{}, fmt.Errorf("SQL function filter must not exceed %d bytes", maxSQLFunctionBytes)
	}
	if len(query.Module) > maxSQLModuleBytes {
		return SQLQuery{}, fmt.Errorf("SQL module selector must not exceed %d bytes", maxSQLModuleBytes)
	}
	if len(query.Cursor) > maxSQLCursorBytes {
		return SQLQuery{}, fmt.Errorf("invalid SQL cursor; restart without cursor")
	}
	if query.Limit == 0 {
		query.Limit = DefaultSQLLimit
	}
	if query.Limit < 1 || query.Limit > MaxSQLLimit {
		return SQLQuery{}, fmt.Errorf("SQL limit must be between 1 and %d", MaxSQLLimit)
	}
	var err error
	if query.Tables, err = normalizeSQLFilters(query.Tables, "table", sqlquery.NormalizeTableSelector); err != nil {
		return SQLQuery{}, err
	}
	if query.Verbs, err = normalizeSQLFilters(query.Verbs, "verb", sqlquery.NormalizeVerb); err != nil {
		return SQLQuery{}, err
	}
	if query.Accesses, err = normalizeSQLFilters(query.Accesses, "access", sqlquery.NormalizeAccess); err != nil {
		return SQLQuery{}, err
	}
	return query, nil
}

func normalizeSQLFilters(values []string, kind string, normalize func(string) (string, error)) ([]string, error) {
	if len(values) > maxSQLFilters {
		return nil, fmt.Errorf("SQL %s filters must not contain more than %d values", kind, maxSQLFilters)
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]bool)
	for _, value := range values {
		if len(value) > maxSQLSelectorBytes {
			return nil, fmt.Errorf("SQL %s filter must not exceed %d bytes", kind, maxSQLSelectorBytes)
		}
		normalized, err := normalize(value)
		if err != nil {
			return nil, err
		}
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		result = append(result, normalized)
	}
	return result, nil
}

func sqlVerbMatches(row SQLResult, verbs []string) bool {
	if len(verbs) == 0 {
		return true
	}
	for _, verb := range verbs {
		if row.Verb == verb {
			return true
		}
	}
	return false
}

func sqlAccessMatches(row SQLResult, accesses []string) bool {
	if len(accesses) == 0 {
		return true
	}
	for _, access := range accesses {
		if row.Access == access {
			return true
		}
	}
	return false
}

func sqlTableMatches(row SQLResult, tables []string) bool {
	if len(tables) == 0 {
		return true
	}
	for _, table := range row.Tables {
		for _, selector := range tables {
			if sqlquery.TableMatches(table.Name, selector) {
				return true
			}
		}
	}
	return false
}

func finalizeSQLPage(page *SQLPage, offset int) {
	page.Returned = len(page.Queries)
	next := offset + page.Returned
	page.Truncated = next < page.Total
	page.NextCursor = ""
	if page.Truncated {
		page.NextCursor = encodeSQLCursor(next)
	}
}

func encodeSQLCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeSQLCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("invalid SQL cursor; restart without cursor")
	}
	offset, err := strconv.Atoi(string(decoded))
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("invalid SQL cursor; restart without cursor")
	}
	return offset, nil
}
