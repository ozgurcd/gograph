package mcp

import (
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
)

// nativeResult exposes the same value as machine-readable structured content
// and JSON text for clients that have not adopted structuredContent yet.
func nativeResult(value any) *mcp.CallToolResult {
	data, err := json.Marshal(value)
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	result := mcp.NewToolResultText(string(data))
	result.StructuredContent = value
	return result
}
