package translate

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// This file routes OpenAI Responses API clients (e.g. Codex, which no longer
// supports wire_api="chat") onto chat-completions-only backends such as
// Venice: requests are rewritten from Responses input items to chat messages,
// non-streaming replies are converted back into a Responses response object,
// and streaming replies are re-emitted as Responses SSE events
// (responses_to_chat_stream.go).

// ResponsesToChat renders an OpenAI Responses request as an OpenAI
// chat-completions request: "instructions" become the system message,
// message items become user/assistant messages, function_call /
// function_call_output items become assistant tool_calls / tool results.
func ResponsesToChat(body []byte, model string) ([]byte, error) {
	var req responsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	input, err := req.inputItems()
	if err != nil {
		return nil, fmt.Errorf("decode input: %w", err)
	}
	if len(input) == 0 {
		return nil, fmt.Errorf("request contains no input items")
	}

	converted := openAIRequest{
		Model:       model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
	}
	if req.MaxOutputTokens > 0 {
		converted.MaxTokens = req.MaxOutputTokens
	}
	if req.Stream {
		converted.Stream = true
		converted.StreamOptionsUsage = &streamOptions{IncludeUsage: true}
	}

	if req.Instructions != "" {
		converted.Messages = append(converted.Messages, openAIMessage{
			Role:    "system",
			Content: req.Instructions,
		})
	}

	for i, item := range input {
		switch item.Type {
		case "message":
			msg, err := chatMessageFromItem(item)
			if err != nil {
				return nil, fmt.Errorf("input item %d: %w", i, err)
			}
			if msg != nil {
				converted.Messages = append(converted.Messages, *msg)
			}
		case "function_call":
			// Assistant-initiated tool call in the conversation history.
			converted.Messages = append(converted.Messages, openAIMessage{
				Role: "assistant",
				ToolCalls: []openAIToolCall{{
					ID:   item.CallID,
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: item.Name, Arguments: item.Arguments},
				}},
			})
		case "function_call_output":
			converted.Messages = append(converted.Messages, openAIMessage{
				Role:       "tool",
				Content:    item.Output,
				ToolCallID: item.CallID,
			})
		case "reasoning":
			// Reasoning traces are provider-specific and are not replayed.
		default:
			return nil, fmt.Errorf("input item %d: unsupported type %q", i, item.Type)
		}
	}
	if len(converted.Messages) == 0 {
		return nil, fmt.Errorf("request contains no translatable input")
	}

	for _, tool := range req.Tools {
		if tool.Name == "" {
			continue
		}
		converted.Tools = append(converted.Tools, openAITool{
			Type: "function",
			Function: openAIFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}
	converted.ToolChoice = responsesToolChoiceToChat(req.ToolChoice)
	if req.ParallelToolCalls != nil {
		converted.ParallelToolCalls = req.ParallelToolCalls
	}

	return json.Marshal(converted)
}

// chatMessageFromItem converts one Responses message item into a chat
// message; it returns nil for items that carry no content.
func chatMessageFromItem(item responsesInputItem) (*openAIMessage, error) {
	role := item.Role
	if role == "" {
		role = "user"
	}
	if text, ok := plainString(item.Content); ok {
		if strings.TrimSpace(text) == "" {
			return nil, nil
		}
		return &openAIMessage{Role: role, Content: text}, nil
	}
	var parts []responsesInputContent
	if len(item.Content) > 0 {
		if err := json.Unmarshal(item.Content, &parts); err != nil {
			return nil, fmt.Errorf("decode content: %w", err)
		}
	}
	if len(parts) == 0 {
		return nil, nil
	}
	if role == "assistant" || len(parts) == 1 && parts[0].Type == "input_text" {
		// Single-text and assistant items travel as plain strings.
		var builder strings.Builder
		for _, part := range parts {
			builder.WriteString(part.Text)
		}
		return &openAIMessage{Role: role, Content: builder.String()}, nil
	}
	// Multipart user content (text + images) keeps the typed-part shape.
	chatParts := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "input_text", "output_text":
			chatParts = append(chatParts, map[string]any{"type": "text", "text": part.Text})
		case "input_image":
			chatParts = append(chatParts, map[string]any{
				"type":      "image_url",
				"image_url": map[string]string{"url": part.ImageURL},
			})
		}
	}
	return &openAIMessage{Role: role, Content: chatParts}, nil
}

// responsesToolChoiceToResponses maps a Responses tool_choice back to its
// chat-completions equivalent, which nests forced functions one level deeper.
func responsesToolChoiceToChat(raw any) any {
	switch choice := raw.(type) {
	case string:
		switch choice {
		case "auto", "none", "required":
			return choice
		}
		return nil
	case map[string]any:
		if choice["type"] == "function" {
			name, _ := choice["name"].(string)
			if name != "" {
				return map[string]any{
					"type":     "function",
					"function": map[string]any{"name": name},
				}
			}
		}
		return nil
	default:
		return nil
	}
}

// ResponsesFromChat converts a non-streaming chat-completions response into
// an OpenAI Responses response object.
func ResponsesFromChat(body []byte, model string) ([]byte, error) {
	var chat chatResponseOut
	if err := json.Unmarshal(body, &chat); err != nil {
		return nil, err
	}

	out := responsesResponse{
		ID:     chat.ID,
		Status: "completed",
		Model:  model,
		Output: []responsesOutputItem{},
	}
	if out.ID == "" {
		out.ID = "resp_llm-proxy"
	}
	if chat.Model != "" {
		out.Model = chat.Model
	}

	for _, choice := range chat.Choices {
		if choice.Message.Content != "" {
			out.Output = append(out.Output, responsesOutputItem{
				ID:     out.ID + "_msg",
				Type:   "message",
				Role:   "assistant",
				Status: "completed",
				Content: []responsesOutputContent{{
					Type: "output_text",
					Text: choice.Message.Content,
				}},
			})
		}
		for _, call := range choice.Message.ToolCalls {
			args := strings.TrimSpace(call.Function.Arguments)
			if args == "" {
				args = "{}"
			}
			out.Output = append(out.Output, responsesOutputItem{
				ID:        call.ID,
				Type:      "function_call",
				CallID:    call.ID,
				Name:      call.Function.Name,
				Arguments: args,
				Status:    "completed",
			})
		}
		if choice.FinishReason == "length" {
			out.Status = "incomplete"
			out.IncompleteDetails = json.RawMessage(`{"reason":"max_output_tokens"}`)
		}
	}

	total := chat.Usage.TotalTokens
	if total == 0 {
		total = chat.Usage.PromptTokens + chat.Usage.CompletionTokens
	}
	out.Usage = responsesUsage{
		InputTokens:  chat.Usage.PromptTokens,
		OutputTokens: chat.Usage.CompletionTokens,
		TotalTokens:  total,
	}

	return json.Marshal(out)
}

// nowUnix stamps synthesized stream payloads; a variable for tests.
var nowUnix = time.Now().Unix
