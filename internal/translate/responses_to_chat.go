package translate

import (
	"encoding/json"
	"fmt"
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
	if err := validateGenerationControls(body, "openai-responses"); err != nil {
		return nil, err
	}
	controls, err := translatedOpenAIControls(body, "openai-responses", "openai-chat")
	if err != nil {
		return nil, err
	}
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
	if err := validateResponsesHistory(input); err != nil {
		return nil, err
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

	if req.Instructions != nil {
		converted.Messages = append(converted.Messages, openAIMessage{Role: "system", Content: *req.Instructions})
	}

	tools, namespaces, err := responsesTools(body)
	if err != nil {
		return nil, fmt.Errorf("decode tools: %w", err)
	}
	choice, err := responsesWireToolChoice(req.ToolChoice, namespaces)
	if err != nil {
		return nil, err
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
			if _, err := argumentsJSON(item.Arguments); err != nil {
				return nil, err
			}
			name, err := responsesWireToolName(item.Name, item.Namespace, namespaces)
			if err != nil {
				return nil, err
			}
			call := openAIToolCall{ID: item.CallID, Type: "function"}
			call.Function.Name = name
			call.Function.Arguments = item.Arguments
			// Only adjacent call items belong to this same call group. Never
			// merge across a result, a message, or ignored history metadata.
			if i > 0 && input[i-1].Type == "function_call" {
				last := &converted.Messages[len(converted.Messages)-1]
				last.ToolCalls = append(last.ToolCalls, call)
			} else {
				converted.Messages = append(converted.Messages, openAIMessage{Role: "assistant", ToolCalls: []openAIToolCall{call}})
			}
		case "function_call_output":
			output, err := openAIToolOutput(item.Output, true)
			if err != nil {
				return nil, fmt.Errorf("input item %d: %w", i, err)
			}
			converted.Messages = append(converted.Messages, openAIMessage{
				Role:       "tool",
				Content:    output,
				ToolCallID: item.CallID,
			})
		default:
			return nil, fmt.Errorf("input item %d: unsupported type %q", i, item.Type)
		}
	}
	if len(converted.Messages) == 0 {
		return nil, fmt.Errorf("request contains no translatable input")
	}

	for _, tool := range tools {
		if tool.Name == "" {
			continue
		}
		schema, err := sanitizeSchema(tool.Parameters)
		if err != nil {
			return nil, err
		}
		converted.Tools = append(converted.Tools, openAITool{
			Type: "function",
			Function: openAIFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  schema,
				Strict:      tool.Strict,
			},
		})
	}
	converted.ToolChoice = responsesToolChoiceToChatWithNamespaces(choice, nil)
	if req.ParallelToolCalls != nil {
		converted.ParallelToolCalls = req.ParallelToolCalls
	}

	return marshalWithOpenAIControls(converted, controls)
}

// chatMessageFromItem preserves message roles, text, and multipart order.
func chatMessageFromItem(item responsesInputItem) (*openAIMessage, error) {
	role := item.Role
	switch role {
	case "system", "developer", "user", "assistant":
	default:
		return nil, fmt.Errorf("unsupported message role %q", role)
	}
	if text, ok := plainString(item.Content); ok {
		return &openAIMessage{Role: role, Content: text}, nil
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(item.Content, &parts); err != nil || len(parts) == 0 {
		return nil, fmt.Errorf("message content must be a string or a non-empty array")
	}
	chatParts := make([]map[string]any, 0, len(parts))
	for _, raw := range parts {
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			return nil, fmt.Errorf("message content part must be an object")
		}
		switch header.Type {
		case "input_text", "output_text":
			var part struct {
				Type string  `json:"type"`
				Text *string `json:"text"`
			}
			if err := decodeContentPart(raw, &part); err != nil || part.Text == nil {
				return nil, fmt.Errorf("text part requires string text and supported fields")
			}
			chatParts = append(chatParts, map[string]any{"type": "text", "text": *part.Text})
		case "input_image":
			var part struct {
				Type     string `json:"type"`
				ImageURL string `json:"image_url"`
				Detail   string `json:"detail"`
			}
			if err := decodeContentPart(raw, &part); err != nil {
				return nil, fmt.Errorf("unsupported image fields: %w", err)
			}
			if role != "user" || part.ImageURL == "" {
				return nil, fmt.Errorf("input_image requires a user message and image_url")
			}
			image := map[string]string{"url": part.ImageURL}
			if part.Detail != "" {
				image["detail"] = part.Detail
			}
			chatParts = append(chatParts, map[string]any{"type": "image_url", "image_url": image})
		default:
			return nil, fmt.Errorf("unsupported message content type %q", header.Type)
		}
	}
	// A single text part has the same meaning as the string shorthand.
	if len(chatParts) == 1 && chatParts[0]["type"] == "text" {
		return &openAIMessage{Role: role, Content: chatParts[0]["text"]}, nil
	}
	return &openAIMessage{Role: role, Content: chatParts}, nil
}

func responsesToolChoiceToChatWithNamespaces(raw any, namespaces map[string]responseNamespaceTool) any {
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
			if namespace, _ := choice["namespace"].(string); namespace != "" {
				name = qualifyResponsesToolName(namespace, name)
			} else if tool, ok := namespaces[name]; ok {
				name = tool.Qualified
			}
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
	return responsesFromChat(body, model, nil, nil)
}

// ResponsesFromChatForRequest is the request-aware form of ResponsesFromChat.
// It restores namespace metadata for function calls from a flat upstream
// response when the request used Codex's namespace tool extension.
func ResponsesFromChatForRequest(body []byte, model string, requestBody []byte) ([]byte, error) {
	namespaces, err := responseNamespaceMap(requestBody)
	if err != nil {
		return nil, fmt.Errorf("decode request tools: %w", err)
	}
	return responsesFromChat(body, model, namespaces, requestBody)
}

func responsesFromChat(body []byte, model string, namespaces map[string]responseNamespaceTool, requestBody []byte) ([]byte, error) {
	var chat chatResponseOut
	if err := json.Unmarshal(body, &chat); err != nil {
		return nil, err
	}
	if err := validateChatUsage(chat.Usage); err != nil {
		return nil, err
	}
	if err := checkUpstreamError(chat.Error); err != nil {
		return nil, err
	}
	if err := validateChatOutput(body); err != nil {
		return nil, err
	}
	if err := validateChatChoiceCount(len(chat.Choices)); err != nil {
		return nil, err
	}

	out, err := newResponsesResponse(chat.ID, model, chat.Created, requestBody)
	if err != nil {
		return nil, err
	}
	if chat.Model != "" {
		out.Model = chat.Model
	}

	ids := responseCallIDs{}
	for _, choice := range chat.Choices {
		if err := validateChatFinish(choice.FinishReason); err != nil {
			return nil, err
		}
		status := "completed"
		if choice.FinishReason == "length" || choice.FinishReason == "content_filter" {
			status = "incomplete"
		}
		if choice.Message.ReasoningContent != "" {
			out.Output = append(out.Output, responsesOutputItem{ID: out.nextItemID(), Type: "reasoning", Summary: []responsesOutputContent{{Type: "summary_text", Text: choice.Message.ReasoningContent}}})
		}
		if choice.Message.ContentPresent {
			out.Output = append(out.Output, responsesOutputItem{
				ID:     out.nextItemID(),
				Type:   "message",
				Role:   "assistant",
				Status: status,
				Content: []responsesOutputContent{{
					Type: "output_text",
					Text: choice.Message.Content,
				}},
			})
		}
		if choice.Message.Refusal != nil {
			out.Output = append(out.Output, responsesOutputItem{ID: out.nextItemID(), Type: "message", Role: "assistant", Status: status, Content: []responsesOutputContent{{Type: "refusal", Refusal: *choice.Message.Refusal}}})
		}
		for _, call := range choice.Message.ToolCalls {
			args := call.Function.Arguments
			if choice.FinishReason != "length" {
				if _, err := argumentsJSON(args); err != nil {
					return nil, err
				}
			}
			id, err := ids.resolve(call.ID, "")
			if err != nil {
				return nil, err
			}
			name, namespace := restoreResponsesToolName(call.Function.Name, namespaces)
			out.Output = append(out.Output, responsesOutputItem{
				ID:        out.nextItemID(),
				Type:      "function_call",
				CallID:    id,
				Namespace: namespace,
				Name:      name,
				Arguments: args,
				Status:    status,
			})
		}
		setResponsesFinish(&out, choice.FinishReason)
	}

	out.Usage = chatUsageToResponses(chat.Usage)

	return json.Marshal(out)
}

// nowUnix stamps synthesized stream payloads; a variable for tests.
var nowUnix = time.Now().Unix
