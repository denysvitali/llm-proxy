package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// validateAnthropicTranslation checks that a Messages request fits an OpenAI
// route and returns the messages to translate, with mid-conversation tool
// changes already folded into request.Tools.
func validateAnthropicTranslation(request *Request, responses bool) ([]AnthropicMessage, error) {
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
				return nil, fmt.Errorf("messages field %q cannot be represented on this OpenAI route", name)
			}
		}
	}
	if request.TopK != nil {
		return nil, fmt.Errorf("messages top_k has no OpenAI mapping")
	}
	if !isJSONNull(request.Thinking) {
		var thinking struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(request.Thinking, &thinking); err != nil || thinking.Type == "" {
			return nil, fmt.Errorf("messages thinking requires an object with a type")
		}
	}
	if _, err := anthropicEffort(request.fields["output_config"]); err != nil {
		return nil, err
	}
	if responses && len(request.StopSequences) != 0 {
		return nil, fmt.Errorf("responses cannot represent stop_sequences")
	}
	if raw, ok := request.fields["tools"]; ok && !isJSONNull(raw) {
		var tools []json.RawMessage
		if err := json.Unmarshal(raw, &tools); err != nil {
			return nil, err
		}
		// Server tools (web_search_*, bash_*, ...) run on Anthropic's side and
		// cannot execute behind an OpenAI backend, so they are skipped, the
		// same way the Responses path skips client-executed tools.
		request.Tools = request.Tools[:0]
		for _, rawTool := range tools {
			tool, ok, err := anthropicFunctionTool(rawTool)
			if err != nil {
				return nil, err
			}
			if ok {
				request.Tools = append(request.Tools, tool)
			}
		}
	}
	messages, err := applyAnthropicToolChanges(request)
	if err != nil {
		return nil, err
	}
	for _, tool := range request.Tools {
		if tool.Name == "" {
			return nil, fmt.Errorf("translated tools require a non-empty name")
		}
	}
	return messages, nil
}

// anthropicFunctionTool decodes one Messages tool definition; ok is false for
// server tools, which have no OpenAI equivalent. Hints with no effect on what
// is computed are stripped.
func anthropicFunctionTool(raw json.RawMessage) (AnthropicTool, bool, error) {
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return AnthropicTool{}, false, fmt.Errorf("messages tool must be an object")
	}
	if header.Type != "" && header.Type != "custom" {
		return AnthropicTool{}, false, nil
	}
	raw = stripAnthropicAnnotations(raw, "type", "cache_control", "defer_loading", "eager_input_streaming", "strict")
	var tool AnthropicTool
	if err := decodeContentPart(raw, &tool); err != nil {
		return AnthropicTool{}, false, fmt.Errorf("unsupported Messages tool fields: %w", err)
	}
	return tool, true, nil
}

// applyAnthropicToolChanges folds the tool_addition and tool_removal blocks
// Claude Code sends in system messages into request.Tools. OpenAI declares
// tools once per request, so the final tool set after every change is
// declared up front. A system message carrying only tool changes is dropped;
// one with text keeps its text.
func applyAnthropicToolChanges(request *Request) ([]AnthropicMessage, error) {
	if err := applyToolChangeBlocks(request, request.System); err != nil {
		return nil, err
	}
	messages := make([]AnthropicMessage, 0, len(request.Messages))
	for _, message := range request.Messages {
		if message.Role == "system" {
			if err := applyToolChangeBlocks(request, message.Content); err != nil {
				return nil, fmt.Errorf("system message: %w", err)
			}
			if onlyToolChanges(message.Content) {
				continue
			}
		}
		messages = append(messages, message)
	}
	return messages, nil
}

type anthropicToolChange struct {
	Type string `json:"type"`
	Tool struct {
		Type       string          `json:"type"`
		Name       string          `json:"name"`
		Definition json.RawMessage `json:"definition"`
	} `json:"tool"`
}

func applyToolChangeBlocks(request *Request, raw json.RawMessage) error {
	if _, ok := plainString(raw); ok || isJSONNull(raw) {
		return nil
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil // reported by content validation
	}
	for _, part := range parts {
		var change anthropicToolChange
		if err := json.Unmarshal(part, &change); err != nil {
			continue
		}
		if change.Type != "tool_addition" && change.Type != "tool_removal" {
			continue
		}
		part = stripAnthropicAnnotations(part, "cache_control")
		if err := decodeContentPart(part, &change); err != nil {
			return fmt.Errorf("%s requires a tool and no unsupported fields%s", change.Type, unknownFieldDetail(err))
		}
		switch {
		case change.Type == "tool_removal" && change.Tool.Type == "tool_reference" && change.Tool.Name != "":
			request.Tools = withoutTool(request.Tools, change.Tool.Name)
		case change.Type == "tool_addition" && change.Tool.Type == "tool_reference" && change.Tool.Name != "":
			// References a tool already declared with defer_loading, and
			// deferred tools are declared on the OpenAI side regardless.
		case change.Type == "tool_addition" && change.Tool.Type == "tool_definition":
			tool, ok, err := anthropicFunctionTool(change.Tool.Definition)
			if err != nil {
				return err
			}
			if ok {
				request.Tools = append(withoutTool(request.Tools, tool.Name), tool)
			}
		default:
			return fmt.Errorf("%s requires a tool_reference with a name or a tool_definition", change.Type)
		}
	}
	return nil
}

func onlyToolChanges(raw json.RawMessage) bool {
	var parts []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil || len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if part.Type != "tool_addition" && part.Type != "tool_removal" {
			return false
		}
	}
	return true
}

func withoutTool(tools []AnthropicTool, name string) []AnthropicTool {
	kept := tools[:0:0]
	for _, tool := range tools {
		if tool.Name != name {
			kept = append(kept, tool)
		}
	}
	return kept
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
