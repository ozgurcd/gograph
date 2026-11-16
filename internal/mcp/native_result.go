package mcp

import (
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
)

// nativeResult returns the value once, as compact JSON text.
func nativeResult(value any) *mcp.CallToolResult {
	data, err := json.Marshal(value)
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	return mcp.NewToolResultText(string(data))
}
