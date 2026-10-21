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
		case "metadata", "context_management", "thinking", "output_config":
			// Claude Code sends these on every turn. metadata is telemetry and
			// context_management asks for server-side history edits; neither
			// changes what the backend should compute, so both are dropped.
			// thinking only gates whether reasoning is surfaced (WantsThinking)
			// and output_config is validated and mapped by anthropicEffort.
			// A graceful loss beats failing the turn, as on the Codex path.
			continue
		default:
			if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return fmt.Errorf("messages field %q cannot be represented on this OpenAI route", name)
			}
		}
	}
	if request.TopK != nil {
		return fmt.Errorf("messages top_k has no OpenAI mapping")
	}
	if !isJSONNull(request.Thinking) {
		var thinking struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(request.Thinking, &thinking); err != nil || thinking.Type == "" {
			return fmt.Errorf("messages thinking requires an object with a type")
		}
	}
	if _, err := anthropicEffort(request.fields["output_config"]); err != nil {
		return err
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

func isJSONNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// anthropicEffort maps Messages output_config.effort onto the OpenAI
// reasoning effort. Only effort is mappable: format (structured output)
// changes the response contract, so it still requires a native route.
// "max" has no portable OpenAI value and degrades to "high".
func anthropicEffort(raw json.RawMessage) (string, error) {
	if isJSONNull(raw) {
		return "", nil
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil || config == nil {
		return "", fmt.Errorf("messages output_config requires an object")
	}
	var effort string
	for key, value := range config {
		switch key {
		case "effort":
			if isJSONNull(value) {
				continue
			}
			if err := json.Unmarshal(value, &effort); err != nil {
				return "", fmt.Errorf("output_config.effort must be a string")
			}
		default:
			if !isJSONNull(value) {
				return "", fmt.Errorf("messages output_config.%s cannot be represented on this OpenAI route", key)
			}
		}
	}
	switch effort {
	case "", "low", "medium", "high":
		return effort, nil
	case "max":
		return "high", nil
	default:
		return "", fmt.Errorf("output_config.effort %q has no OpenAI mapping", effort)
	}
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
