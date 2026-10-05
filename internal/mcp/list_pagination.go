package mcp

import (
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/search"
)

type pendingResults struct {
	Graph *graph.Graph
	Rows  []search.Result
}

func finishListResult(command string, request mcp.CallToolRequest, pending pendingResults, snapshot *search.Snapshot) *mcp.CallToolResult {
	warning := ""
	if pending.Graph != nil && pending.Graph.Build != nil {
		warning = search.EmptyAnswerWarning(command, string(pending.Graph.Build.Precision), len(pending.Rows))
	}
	if !search.SupportsListPagination(command) {
		if warning != "" {
			result := mcp.NewToolResultText("No results found.\n" + warning)
			result.StructuredContent = map[string]any{"schema_version": "gograph.mcp-result.v1", "count": 0, "results": []search.Result{}, "warning": warning}
			return result
		}
		return formatUnpagedResults(pending.Rows)
	}
	args, _ := request.Params.Arguments.(map[string]any)
	limit, err := integerArg(args, "limit", search.DefaultResultsLimit)
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	if limit < 1 || limit > search.MaxResultsLimit {
		return mcp.NewToolResultError(fmt.Sprintf("limit must be between 1 and %d", search.MaxResultsLimit))
	}
	cursor := ""
	if value, exists := args["cursor"]; exists {
		var ok bool
		cursor, ok = value.(string)
		if !ok {
			return mcp.NewToolResultError("cursor must be a string")
		}
	}
	fingerprint, err := snapshot.Fingerprint()
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	page, err := search.PageResults(fingerprint, command, pending.Rows, limit, cursor)
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	page.Warning = warning
	return nativeResult(page)
}
