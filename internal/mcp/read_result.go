package mcp

import (
	"encoding/json"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// attachReadResult classifies a read's answer (or refusal) so the refusal
// reason reaches _meta.gograph_graph_state.read_diagnostic. It must run
// before graph provenance is attached; the structured value is not sent.
func attachReadResult(command string, result *mcp.CallToolResult) {
	if result == nil || (command != "gograph_source" && command != "gograph_context") {
		return
	}
	fields := map[string]any{
		"schema_version": "gograph.read.v1",
		"command":        strings.TrimPrefix(command, "gograph_"),
		"status":         "ok",
	}
	var texts []string
	for _, content := range result.Content {
		if text, ok := mcp.AsTextContent(content); ok {
			texts = append(texts, text.Text)
		}
	}
	text := strings.Join(texts, "\n")
	if result.IsError {
		fields["status"] = "refused"
		fields["reason"] = text
	} else if command == "gograph_source" {
		fields["source"] = text
	} else {
		var payload map[string]any
		if err := json.Unmarshal([]byte(text), &payload); err != nil {
			// Do not discard a future non-JSON compatibility answer.
			fields["content"] = text
		} else {
			for key, value := range payload {
				fields[key] = value
			}
			if reason, _ := payload["source_error"].(string); reason != "" {
				fields["status"] = "partial"
				fields["reason"] = reason
			}
		}
	}
	result.StructuredContent = fields
}
