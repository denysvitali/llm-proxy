package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
)

// Cross-wire history accepts only fields with a destination representation.
// Native requests never pass through these checks.
func validateChatRequestFields(fields map[string]json.RawMessage) error {
	var messages []json.RawMessage
	if err := json.Unmarshal(fields["messages"], &messages); err != nil || len(messages) == 0 {
		return fmt.Errorf("messages must be a non-empty array")
	}
	for i, raw := range messages {
		var message chatMessageIn
		if err := decodeRequestObject(raw, &message); err != nil {
			return fmt.Errorf("unsupported message %d fields: %w", i, err)
		}
		var messageFields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &messageFields); err != nil {
			return err
		}
		if message.Role != "tool" && nonNullJSON(messageFields["tool_call_id"]) {
			return fmt.Errorf("tool_call_id requires a tool message")
		}
		if message.Role != "assistant" && nonNullJSON(messageFields["tool_calls"]) {
			return fmt.Errorf("tool_calls requires an assistant message")
		}
		if message.Role == "assistant" && !nonNullJSON(message.Content) && len(message.ToolCalls) == 0 {
			return fmt.Errorf("message %d: assistant requires content or tool calls", i)
		}
		for _, call := range message.ToolCalls {
			if call.Type != "function" || call.Function.Name == "" {
				return fmt.Errorf("translated tool calls require type function and a non-empty name")
			}
		}
	}
	var tools []json.RawMessage
	if raw := fields["tools"]; nonNullJSON(raw) {
		if err := json.Unmarshal(raw, &tools); err != nil {
			return fmt.Errorf("tools must be an array")
		}
	}
	seen := make(map[string]bool, len(tools))
	for _, raw := range tools {
		var tool openAITool
		if err := decodeRequestObject(raw, &tool); err != nil {
			return fmt.Errorf("unsupported Chat tool fields: %w", err)
		}
		if tool.Type != "function" || tool.Function.Name == "" {
			return fmt.Errorf("translated tools require type function and a non-empty name")
		}
		if seen[tool.Function.Name] {
			return fmt.Errorf("duplicate tool identity %q", tool.Function.Name)
		}
		seen[tool.Function.Name] = true
	}
	return nil
}

func decodeRequestObject(raw json.RawMessage, target any) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return fmt.Errorf("expected an object")
	}
	return decodeContentPart(raw, target)
}

func decodeResponsesInputItem(raw json.RawMessage) (responsesInputItem, error) {
	var item responsesInputItem
	if err := decodeRequestObject(raw, &item); err != nil {
		return item, err
	}
	if item.Type == "" && item.Role != "" {
		item.Type = "message"
	}
	allowed := []string{"type"}
	switch item.Type {
	case "message":
		allowed = append(allowed, "role", "content")
	case "function_call":
		allowed = append(allowed, "call_id", "name", "namespace", "arguments")
		if item.Name == "" {
			return item, fmt.Errorf("function_call requires a non-empty name")
		}
	case "function_call_output":
		allowed = append(allowed, "call_id", "output")
	default:
		return item, fmt.Errorf("input type %q requires a native route", item.Type)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return item, err
	}
	for field := range fields {
		if !slices.Contains(allowed, field) {
			return item, fmt.Errorf("input %s cannot represent field %q", item.Type, field)
		}
	}
	return item, nil
}
