package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func validateAnthropicTranslation(request *Request, responses bool) error {
	for name, raw := range request.fields {
		switch name {
		case "model", "max_tokens", "messages", "system", "temperature", "top_p", "stop_sequences", "stream", "tools", "tool_choice":
			continue
		default:
			if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return fmt.Errorf("messages field %q cannot be represented on this OpenAI route", name)
			}
		}
	}
	if request.TopK != nil || len(request.Thinking) != 0 && !bytes.Equal(bytes.TrimSpace(request.Thinking), []byte("null")) {
		return fmt.Errorf("messages top_k and thinking controls have no lossless OpenAI mapping")
	}
	if responses && len(request.StopSequences) != 0 {
		return fmt.Errorf("responses cannot represent stop_sequences")
	}
	if raw, ok := request.fields["tools"]; ok {
		var tools []json.RawMessage
		if err := json.Unmarshal(raw, &tools); err != nil {
			return err
		}
		for _, rawTool := range tools {
			var tool AnthropicTool
			if err := decodeContentPart(rawTool, &tool); err != nil {
				return fmt.Errorf("unsupported Messages tool fields: %w", err)
			}
		}
	}
	for _, tool := range request.Tools {
		if tool.Name == "" {
			return fmt.Errorf("translated tools require a non-empty name")
		}
	}
	return nil
}

func anthropicToolChoice(raw json.RawMessage, responses bool) (any, *bool, error) {
	if len(raw) == 0 {
		return nil, nil, nil
	}
	var choice struct {
		Type                   string `json:"type"`
		Name                   string `json:"name"`
		DisableParallelToolUse *bool  `json:"disable_parallel_tool_use"`
	}
	if err := decodeContentPart(raw, &choice); err != nil {
		return nil, nil, fmt.Errorf("unsupported Messages tool_choice: %w", err)
	}
	var parallel *bool
	if choice.DisableParallelToolUse != nil {
		value := !*choice.DisableParallelToolUse
		parallel = &value
	}
	if choice.Type != "tool" && choice.Name != "" {
		return nil, nil, fmt.Errorf("tool_choice.name requires type tool")
	}
	switch choice.Type {
	case "auto":
		return "auto", parallel, nil
	case "any":
		return "required", parallel, nil
	case "none":
		if parallel != nil {
			return nil, nil, fmt.Errorf("tool_choice none cannot include disable_parallel_tool_use")
		}
		return "none", nil, nil
	case "tool":
		if choice.Name != "" {
			if responses {
				return map[string]any{"type": "function", "name": choice.Name}, parallel, nil
			}
			return map[string]any{"type": "function", "function": map[string]string{"name": choice.Name}}, parallel, nil
		}
	}
	return nil, nil, fmt.Errorf("tool_choice requires auto, any, none, or a named tool")
}
