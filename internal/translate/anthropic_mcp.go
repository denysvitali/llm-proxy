package translate

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Anthropic's MCP connector runs tools server-side and records each call in
// the assistant turn as an mcp_tool_use block followed by its mcp_tool_result.
// Clients replay them in history, but no backend behind this proxy runs the
// connector — Anthropic-compatible providers reject the types outright
// (MiniMax: "unsupported content type 'mcp_tool_use'"). The call already
// happened, so it is rendered as text: the model keeps what was asked and
// answered without anything left to execute.

func isMCPBlock(blockType string) bool {
	return blockType == "mcp_tool_use" || blockType == "mcp_tool_result"
}

// mcpBlockText renders an mcp_tool_use or mcp_tool_result block as text.
func mcpBlockText(part json.RawMessage) string {
	var value struct {
		Type       string          `json:"type"`
		ID         string          `json:"id"`
		Name       string          `json:"name"`
		ServerName string          `json:"server_name"`
		Input      json.RawMessage `json:"input"`
		ToolUseID  string          `json:"tool_use_id"`
		IsError    bool            `json:"is_error"`
		Content    json.RawMessage `json:"content"`
	}
	_ = json.Unmarshal(part, &value)
	if value.Type == "mcp_tool_use" {
		name := value.Name
		if value.ServerName != "" {
			name = value.ServerName + "/" + name
		}
		text := "[MCP tool call " + name + " id=" + value.ID + "]"
		if input := bytes.TrimSpace(value.Input); len(input) > 0 && !isJSONNull(input) {
			text += "\n" + string(input)
		}
		return text
	}
	header := "[MCP tool result id=" + value.ToolUseID + "]"
	if value.IsError {
		header = "[MCP tool error id=" + value.ToolUseID + "]"
	}
	if body := mcpResultText(value.Content); body != "" {
		return header + "\n" + body
	}
	return header
}

// mcpResultText flattens result content: a string, or blocks whose text parts
// are joined and whose other parts are kept as JSON.
func mcpResultText(raw json.RawMessage) string {
	if text, ok := plainString(raw); ok {
		return text
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		if isJSONNull(raw) {
			return ""
		}
		return string(bytes.TrimSpace(raw))
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		var block struct {
			Type string  `json:"type"`
			Text *string `json:"text"`
		}
		if json.Unmarshal(part, &block) == nil && block.Type == "text" && block.Text != nil {
			out = append(out, *block.Text)
		} else {
			out = append(out, string(bytes.TrimSpace(part)))
		}
	}
	return strings.Join(out, "\n")
}

// FlattenMCPBlocks rewrites mcp_tool_use and mcp_tool_result blocks in a
// native Messages request body into text blocks. Bodies without them are
// returned untouched (same bytes); otherwise top-level fields keep their
// values but not their byte layout.
func FlattenMCPBlocks(body []byte) ([]byte, bool) {
	if !bytes.Contains(body, []byte(`"mcp_tool_use"`)) && !bytes.Contains(body, []byte(`"mcp_tool_result"`)) {
		return body, false
	}
	var request map[string]json.RawMessage
	if json.Unmarshal(body, &request) != nil {
		return body, false
	}
	var messages []map[string]json.RawMessage
	if json.Unmarshal(request["messages"], &messages) != nil {
		return body, false
	}
	changed := false
	for _, message := range messages {
		var parts []json.RawMessage
		if json.Unmarshal(message["content"], &parts) != nil {
			continue // string content or malformed: nothing to flatten
		}
		touched := false
		for i, part := range parts {
			var header struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(part, &header) != nil || !isMCPBlock(header.Type) {
				continue
			}
			parts[i] = mustMarshal(map[string]string{"type": "text", "text": mcpBlockText(part)})
			touched = true
		}
		if touched {
			message["content"] = mustMarshal(parts)
			changed = true
		}
	}
	if !changed {
		return body, false
	}
	request["messages"] = mustMarshal(messages)
	encoded, err := json.Marshal(request)
	if err != nil {
		return body, false
	}
	return encoded, true
}
