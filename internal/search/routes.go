package search

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

const (
	RoutesSchemaVersion  = "gograph.routes.v1"
	DefaultRoutesLimit   = 100
	MaxRoutesLimit       = 200
	MaxRoutesResultBytes = 64 * 1024
	maxRouteTermBytes    = 1024
	maxRouteModuleBytes  = 1024
	maxRouteCursorBytes  = 128
	maxModuleErrorBytes  = 2048
)

// RouteQuery selects one deterministic, bounded page from the route census.
// IncludeTests is deliberately opt-in so the default matches Endpoint.
type RouteQuery struct {
	Term         string
	Module       string
	IncludeTests bool
	Limit        int
	Cursor       string
}

// RoutePage is shared by CLI JSON and MCP route queries.
type RoutePage struct {
	SchemaVersion string   `json:"schema_version"`
	Term          string   `json:"term,omitempty"`
	Module        string   `json:"module,omitempty"`
	IncludeTests  bool     `json:"include_tests"`
	Limit         int      `json:"limit"`
	Cursor        string   `json:"cursor,omitempty"`
	Total         int      `json:"total"`
	Returned      int      `json:"returned"`
	Truncated     bool     `json:"truncated"`
	NextCursor    string   `json:"next_cursor,omitempty"`
	Routes        []Result `json:"routes"`
}

// QueryRoutes filters the full route inventory before applying a cursor. Pages
// are bounded by both row count and serialized size so MCP clients never need
// an out-of-band spill file to consume an ordinary route census.
func QueryRoutes(g *graph.Graph, query RouteQuery) (RoutePage, error) {
	return NewSnapshot(g).QueryRoutes(query)
}

func (snapshot *Snapshot) QueryRoutes(query RouteQuery) (RoutePage, error) {
	g := snapshot.g
	if len(query.Term) > maxRouteTermBytes {
		return RoutePage{}, fmt.Errorf("route term must not exceed %d bytes", maxRouteTermBytes)
	}
	if len(query.Module) > maxRouteModuleBytes {
		return RoutePage{}, fmt.Errorf("route module selector must not exceed %d bytes", maxRouteModuleBytes)
	}
	if len(query.Cursor) > maxRouteCursorBytes {
		return RoutePage{}, fmt.Errorf("invalid route cursor; restart without cursor")
	}
	limit := query.Limit
	if limit == 0 {
		limit = DefaultRoutesLimit
	}
	if limit < 1 || limit > MaxRoutesLimit {
		return RoutePage{}, fmt.Errorf("route limit must be between 1 and %d", MaxRoutesLimit)
	}

	selection := query
	selection.Cursor, selection.Limit = "", 0
	selection.Term = strings.ToLower(strings.TrimSpace(selection.Term))
	selection.Module = strings.TrimSpace(selection.Module)
	binding, err := snapshot.binding(RoutesSchemaVersion, selection)
	if err != nil {
		return RoutePage{}, err
	}
	encodedOffset, err := cursorOffset(query.Cursor, binding, "route")
	if err != nil {
		return RoutePage{}, err
	}
	offset, err := decodeRouteCursor(encodedOffset)
	if err != nil {
		return RoutePage{}, err
	}
	moduleIndex, err := resolveRouteModule(g, strings.TrimSpace(query.Module))
	if err != nil {
		return RoutePage{}, err
	}

	term := strings.ToLower(strings.TrimSpace(query.Term))
	all := snapshot.routeRows()
	filtered := make([]Result, 0, len(all))
	for _, result := range all {
		if !query.IncludeTests && strings.HasSuffix(strings.ToLower(filepath.Base(result.File)), "_test.go") {
			continue
		}
		if moduleIndex >= 0 && routeModuleIndex(g, result.File) != moduleIndex {
			continue
		}
		if term != "" && !routeResultContains(result, term) {
			continue
		}
		filtered = append(filtered, result)
	}

	if offset > len(filtered) {
		return RoutePage{}, fmt.Errorf("route cursor starts at %d, past the %d filtered routes; restart without cursor", offset, len(filtered))
	}

	page := RoutePage{
		SchemaVersion: RoutesSchemaVersion,
		Term:          strings.TrimSpace(query.Term),
		Module:        strings.TrimSpace(query.Module),
		IncludeTests:  query.IncludeTests,
		Limit:         limit,
		Cursor:        query.Cursor,
		Total:         len(filtered),
		Routes:        []Result{},
	}
	for index := offset; index < len(filtered) && len(page.Routes) < limit; index++ {
		page.Routes = append(page.Routes, filtered[index])
		finalizeRoutePage(&page, offset, binding)
		encoded, marshalErr := json.Marshal(page)
		if marshalErr != nil {
			return RoutePage{}, fmt.Errorf("encode route page: %w", marshalErr)
		}
		if len(encoded) <= MaxRoutesResultBytes {
			continue
		}
		page.Routes = page.Routes[:len(page.Routes)-1]
		if len(page.Routes) == 0 {
			return RoutePage{}, fmt.Errorf("one route result exceeds the %d-byte response budget; narrow the term or module filter", MaxRoutesResultBytes)
		}
		break
	}
	finalizeRoutePage(&page, offset, binding)
	encoded, err := json.Marshal(page)
	if err != nil {
		return RoutePage{}, fmt.Errorf("encode route page: %w", err)
	}
	if len(encoded) > MaxRoutesResultBytes {
		return RoutePage{}, fmt.Errorf("route response exceeds the %d-byte response budget; narrow the term or module filter", MaxRoutesResultBytes)
	}
	return page, nil
}

func finalizeRoutePage(page *RoutePage, offset int, binding string) {
	page.Returned = len(page.Routes)
	next := offset + page.Returned
	page.Truncated = next < page.Total
	page.NextCursor = ""
	if page.Truncated {
		page.NextCursor = boundCursor(binding, encodeRouteCursor(next))
	}
}

func routeResultContains(result Result, term string) bool {
	return strings.Contains(strings.ToLower(result.Name), term) ||
		strings.Contains(strings.ToLower(result.Detail), term) ||
		strings.Contains(strings.ToLower(result.File), term)
}

func encodeRouteCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeRouteCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("invalid route cursor; restart without cursor")
	}
	offset, err := strconv.Atoi(string(decoded))
	if err != nil || offset < 0 || strconv.Itoa(offset) != string(decoded) {
		return 0, fmt.Errorf("invalid route cursor; restart without cursor")
	}
	return offset, nil
}

func resolveRouteModule(g *graph.Graph, selector string) (int, error) {
	if selector == "" {
		return -1, nil
	}
	matches := []int{}
	for index, module := range g.Modules {
		for _, alias := range routeModuleAliases(g, module) {
			if selector == alias {
				matches = append(matches, index)
				break
			}
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		var candidates []string
		for _, index := range matches {
			candidates = append(candidates, routeModuleDescription(g.Modules[index]))
		}
		return -1, fmt.Errorf("route module selector %q is ambiguous: %s", selector, boundedRouteModuleList(candidates))
	}

	available := make([]string, 0, len(g.Modules))
	for _, module := range g.Modules {
		available = append(available, routeModuleDescription(module))
	}
	if len(available) == 0 {
		return -1, fmt.Errorf("route module filter requires module inventory; rebuild the graph")
	}
	return -1, fmt.Errorf("route module %q not found; available modules: %s", selector, boundedRouteModuleList(available))
}

func boundedRouteModuleList(candidates []string) string {
	sort.Strings(candidates)
	listed := make([]string, 0, len(candidates))
	used := 0
	for _, candidate := range candidates {
		additional := len(candidate)
		if len(listed) > 0 {
			additional += 2
		}
		if used+additional > maxModuleErrorBytes {
			break
		}
		listed = append(listed, candidate)
		used += additional
	}
	if len(listed) == len(candidates) {
		return strings.Join(listed, ", ")
	}
	return fmt.Sprintf("%s ... (%d more)", strings.Join(listed, ", "), len(candidates)-len(listed))
}

func routeModuleAliases(g *graph.Graph, module graph.ModuleNode) []string {
	dir := normalizeRouteModuleDir(module.Dir)
	aliases := []string{module.ID, module.Path, dir}
	if dir == "." {
		repositoryName := filepath.Base(filepath.Clean(g.Root))
		if repositoryName != "" && repositoryName != "." && repositoryName != string(filepath.Separator) {
			aliases = append(aliases, repositoryName)
		}
	} else {
		aliases = append(aliases, path.Base(dir))
	}
	return aliases
}

func routeModuleDescription(module graph.ModuleNode) string {
	dir := normalizeRouteModuleDir(module.Dir)
	if module.Path != "" {
		return fmt.Sprintf("%s (%s)", module.Path, dir)
	}
	if module.ID != "" {
		return fmt.Sprintf("%s (%s)", module.ID, dir)
	}
	return dir
}

func routeModuleIndex(g *graph.Graph, file string) int {
	normalizedFile := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(file)), "./")
	bestIndex := -1
	bestLength := -1
	for index, module := range g.Modules {
		dir := normalizeRouteModuleDir(module.Dir)
		if !routeFileWithinModule(normalizedFile, dir) {
			continue
		}
		if len(dir) > bestLength {
			bestIndex = index
			bestLength = len(dir)
		}
	}
	return bestIndex
}

func normalizeRouteModuleDir(dir string) string {
	normalized := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(dir)), "./")
	if normalized == "" || normalized == "." {
		return "."
	}
	return path.Clean(normalized)
}

func routeFileWithinModule(file, dir string) bool {
	if dir == "." {
		return true
	}
	return file == dir || strings.HasPrefix(file, dir+"/")
}
