package translate

import (
	"encoding/json"
	"fmt"
	"strings"
)

// toolHistory tracks identities across a translated request. Native requests
// retain provider-owned history semantics and do not use this validator.
type toolHistory struct {
	calls   map[string]string
	results map[string]bool
}

func (h *toolHistory) call(id, kind string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("tool call must have a non-empty id")
	}
	if h.calls == nil {
		h.calls = make(map[string]string)
		h.results = make(map[string]bool)
	}
	if _, exists := h.calls[id]; exists {
		return fmt.Errorf("duplicate tool call id %q", id)
	}
	h.calls[id] = kind
	return nil
}

func (h *toolHistory) result(id, kind string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("tool result must have a non-empty call id")
	}
	if h.calls[id] != kind {
		return fmt.Errorf("tool result %q has no preceding matching call", id)
	}
	if h.results[id] {
		return fmt.Errorf("duplicate tool result for call %q", id)
	}
	h.results[id] = true
	return nil
}

func validateAnthropicHistory(messages []AnthropicMessage) error {
	if len(messages) == 0 {
		return fmt.Errorf("request contains no messages")
	}
	var history toolHistory
	for _, message := range messages {
		if _, ok := plainString(message.Content); ok {
			continue
		}
		var blocks []anthropicBlock
		if err := json.Unmarshal(message.Content, &blocks); err != nil {
			return fmt.Errorf("message content must be a string or block array")
		}
		for _, block := range blocks {
			switch block.Type {
			case "tool_use":
				if message.Role != "assistant" {
					return fmt.Errorf("tool_use must belong to an assistant message")
				}
				if err := history.call(block.ID, "function"); err != nil {
					return err
				}
			case "tool_result":
				if message.Role != "user" {
					return fmt.Errorf("tool_result must belong to a user message")
				}
				if err := history.result(block.ToolUseID, "function"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateChatHistory(messages []chatMessageIn) error {
	var history toolHistory
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if message.Role != "assistant" {
				return fmt.Errorf("tool_calls must belong to an assistant message")
			}
			if err := history.call(call.ID, "function"); err != nil {
				return err
			}
		}
		if message.Role == "tool" {
			if err := history.result(message.ToolCallID, "function"); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateResponsesHistory(items []responsesInputItem) error {
	var history toolHistory
	for _, item := range items {
		switch item.Type {
		case "function_call", "custom_tool_call":
			if err := history.call(item.CallID, item.Type); err != nil {
				return err
			}
		case "function_call_output", "custom_tool_call_output":
			if err := history.result(item.CallID, strings.TrimSuffix(item.Type, "_output")); err != nil {
				return err
			}
		}
	}
	return nil
}
