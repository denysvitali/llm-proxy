package translate

import (
	"encoding/json"
	"fmt"
	"strings"
)

// toolHistoryCallState is the per-id lifecycle of one tool call in a
// translated request history.
//
//	undeclared --call--> declared --result--> resolved
//
// A second call for an id that is not undeclared, a result for an undeclared
// id (or one of another kind), and a result for a resolved id are rejected.
type toolHistoryCallState uint8

const (
	toolHistoryUndeclared toolHistoryCallState = iota
	toolHistoryDeclared
	toolHistoryResolved
)

func (s toolHistoryCallState) String() string {
	switch s {
	case toolHistoryUndeclared:
		return "undeclared"
	case toolHistoryDeclared:
		return "declared"
	case toolHistoryResolved:
		return "resolved"
	}
	return fmt.Sprintf("toolHistoryCallState(%d)", uint8(s))
}

// toolHistoryCall records the kind a call was declared with and where it is
// in its lifecycle.
type toolHistoryCall struct {
	kind  string
	state toolHistoryCallState
}

// toolHistory tracks identities across a translated request. Native requests
// retain provider-owned history semantics and do not use this validator.
type toolHistory struct {
	calls map[string]*toolHistoryCall
}

// state reports where id is in its lifecycle; unknown ids are undeclared.
func (h *toolHistory) state(id string) toolHistoryCallState {
	if call := h.calls[id]; call != nil {
		return call.state
	}
	return toolHistoryUndeclared
}

func (h *toolHistory) call(id, kind string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("tool call must have a non-empty id")
	}
	if h.state(id) != toolHistoryUndeclared {
		return fmt.Errorf("duplicate tool call id %q", id)
	}
	if h.calls == nil {
		h.calls = make(map[string]*toolHistoryCall)
	}
	h.calls[id] = &toolHistoryCall{kind: kind, state: toolHistoryDeclared}
	return nil
}

func (h *toolHistory) result(id, kind string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("tool result must have a non-empty call id")
	}
	call := h.calls[id]
	if call == nil || call.kind != kind {
		return fmt.Errorf("tool result %q has no preceding matching call", id)
	}
	if call.state == toolHistoryResolved {
		return fmt.Errorf("duplicate tool result for call %q", id)
	}
	call.state = toolHistoryResolved
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
