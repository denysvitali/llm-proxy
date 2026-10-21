package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// anthropicInputBlocks validates content before cross-wire conversion.
// Provider-only block fields with meaning (documents, unknown block fields)
// have no OpenAI equivalent and are rejected. Annotations Claude Code sends on
// every turn degrade instead: cache_control is a caching hint and empty
// citations carry nothing, so both are stripped, and replayed thinking blocks
// are opaque provider state the OpenAI side cannot use, so they are dropped.
func anthropicInputBlocks(raw json.RawMessage) ([]anthropicBlock, error) {
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil || parts == nil {
		return nil, fmt.Errorf("messages content must be a string or a block array")
	}
	blocks := make([]anthropicBlock, 0, len(parts))
	for _, part := range parts {
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(part, &header); err != nil {
			return nil, fmt.Errorf("content block must be an object")
		}
		part = stripAnthropicAnnotations(part, "cache_control", "citations")
		var block anthropicBlock
		switch header.Type {
		case "thinking", "redacted_thinking":
			continue
		case "text":
			var value struct {
				Type string  `json:"type"`
				Text *string `json:"text"`
			}
			if err := decodeContentPart(part, &value); err != nil || value.Text == nil {
				return nil, fmt.Errorf("text block requires string text and no unsupported fields")
			}
			block = anthropicBlock{Type: "text", Text: *value.Text}
		case "image":
			var value struct {
				Type   string           `json:"type"`
				Source *anthropicSource `json:"source"`
			}
			if err := decodeContentPart(part, &value); err != nil {
				return nil, fmt.Errorf("unsupported image fields: %w", err)
			}
			if _, err := anthropicImageURL(value.Source); err != nil {
				return nil, err
			}
			block = anthropicBlock{Type: "image", Source: value.Source}
		case "tool_use":
			var value struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			}
			if err := decodeContentPart(part, &value); err != nil || value.ID == "" || value.Name == "" {
				return nil, fmt.Errorf("tool_use requires id, name and no unsupported fields")
			}
			block = anthropicBlock{Type: "tool_use", ID: value.ID, Name: value.Name, Input: value.Input}
		case "tool_result":
			var value struct {
				Type      string          `json:"type"`
				ToolUseID string          `json:"tool_use_id"`
				Content   json.RawMessage `json:"content"`
				IsError   bool            `json:"is_error"`
			}
			if err := decodeContentPart(part, &value); err != nil || value.ToolUseID == "" {
				return nil, fmt.Errorf("tool_result requires tool_use_id and no unsupported fields")
			}
			block = anthropicBlock{Type: "tool_result", ToolUseID: value.ToolUseID, Content: value.Content, IsError: value.IsError}
		default:
			return nil, fmt.Errorf("messages content type %q cannot be represented on this OpenAI route", header.Type)
		}
		blocks = append(blocks, block)
	}
	return blocks, nil
}

// stripAnthropicAnnotations removes hint fields that do not change what the
// backend computes. citations is only stripped when null or empty, since
// populated citations are content the OpenAI wire cannot carry.
func stripAnthropicAnnotations(raw json.RawMessage, keys ...string) json.RawMessage {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return raw
	}
	changed := false
	for _, key := range keys {
		value, ok := fields[key]
		if !ok {
			continue
		}
		if key == "citations" {
			trimmed := bytes.TrimSpace(value)
			if !isJSONNull(value) && !bytes.Equal(trimmed, []byte("[]")) {
				continue
			}
		}
		delete(fields, key)
		changed = true
	}
	if !changed {
		return raw
	}
	return mustMarshal(fields)
}

func anthropicImageURL(source *anthropicSource) (string, error) {
	if source != nil {
		switch source.Type {
		case "url":
			if source.URL != "" && source.MediaType == "" && source.Data == "" {
				return source.URL, nil
			}
		case "base64":
			if source.Data != "" && source.MediaType != "" && source.URL == "" {
				return "data:" + source.MediaType + ";base64," + source.Data, nil
			}
		}
	}
	return "", fmt.Errorf("image requires a URL source or base64 data with an explicit media_type")
}

func anthropicSystemContent(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if _, ok := plainString(raw); ok {
		return raw, nil
	}
	blocks, err := anthropicInputBlocks(raw)
	if err != nil {
		return nil, err
	}
	parts := make([]map[string]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "text" {
			return nil, fmt.Errorf("system content requires text blocks")
		}
		parts = append(parts, map[string]string{"type": "text", "text": block.Text})
	}
	return json.Marshal(parts)
}

const anthropicToolErrorPrefix = "[Tool execution failed]\n"

// OpenAI has no is_error flag. Keep the failure explicit as a text prefix;
// content itself stays ordered and is never converted to a success result.
func anthropicToolOutput(block anthropicBlock, responses bool) (json.RawMessage, error) {
	raw := block.Content
	if len(raw) == 0 {
		raw = json.RawMessage(`""`)
	}
	if text, ok := plainString(raw); ok {
		if block.IsError {
			text = anthropicToolErrorPrefix + text
		}
		return json.Marshal(text)
	}
	blocks, err := anthropicInputBlocks(raw)
	if err != nil {
		return nil, err
	}
	textType := "text"
	if responses {
		textType = "input_text"
	}
	parts := make([]map[string]any, 0, len(blocks))
	if block.IsError {
		parts = append(parts, map[string]any{"type": textType, "text": anthropicToolErrorPrefix})
	}
	for _, part := range blocks {
		switch part.Type {
		case "text":
			parts = append(parts, map[string]any{"type": textType, "text": part.Text})
		case "image":
			if !responses {
				return nil, fmt.Errorf("chat tool messages cannot represent image results")
			}
			url, err := anthropicImageURL(part.Source)
			if err != nil {
				return nil, err
			}
			parts = append(parts, map[string]any{"type": "input_image", "image_url": url})
		default:
			return nil, fmt.Errorf("unsupported tool result content %q", part.Type)
		}
	}
	return json.Marshal(parts)
}
