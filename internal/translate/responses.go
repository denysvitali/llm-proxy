package translate

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// This file translates between the Anthropic Messages API and the OpenAI
// Responses API, so Anthropic clients can talk to backends that only expose
// /v1/responses. Requests become Responses "input" item arrays (the system
// prompt travels as "instructions", tool results as function_call_output
// items) and Responses responses become Anthropic content blocks again.
// Streaming lives in responses_stream.go; the Chat* helpers adapt OpenAI
// chat-completions clients onto the same Responses-only backends.

type responsesRequest struct {
	Model             string                  `json:"model"`
	Instructions      string                  `json:"instructions,omitempty"`
	Input             []responsesInputItem    `json:"input"`
	Tools             []responsesFunctionTool `json:"tools,omitempty"`
	ToolChoice        any                     `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool                   `json:"parallel_tool_calls,omitempty"`
	Reasoning         *responsesReasoning     `json:"reasoning,omitempty"`
	Store             bool                    `json:"store"`
	Stream            bool                    `json:"stream,omitempty"`
	MaxOutputTokens   int                     `json:"max_output_tokens,omitempty"`
	Temperature       *float64                `json:"temperature,omitempty"`
	TopP              *float64                `json:"top_p,omitempty"`
}

type responsesReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type responsesInputItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
	Output    string          `json:"output,omitempty"`
}

type responsesInputContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type responsesFunctionTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      bool            `json:"strict,omitempty"`
}

type responsesResponse struct {
	ID                string                `json:"id"`
	Status            string                `json:"status"`
	Model             string                `json:"model"`
	Output            []responsesOutputItem `json:"output"`
	Usage             responsesUsage        `json:"usage"`
	IncompleteDetails json.RawMessage       `json:"incomplete_details,omitempty"`
	Error             *responsesErrorBody   `json:"error,omitempty"`
}

type responsesOutputItem struct {
	ID        string                   `json:"id"`
	Type      string                   `json:"type"`
	Role      string                   `json:"role,omitempty"`
	Status    string                   `json:"status,omitempty"`
	Content   []responsesOutputContent `json:"content,omitempty"`
	Summary   []responsesOutputContent `json:"summary,omitempty"`
	CallID    string                   `json:"call_id,omitempty"`
	Name      string                   `json:"name,omitempty"`
	Arguments string                   `json:"arguments,omitempty"`
}

type responsesOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type responsesUsage struct {
	InputTokens         int64                 `json:"input_tokens"`
	OutputTokens        int64                 `json:"output_tokens"`
	TotalTokens         int64                 `json:"total_tokens,omitempty"`
	InputTokensDetails  responsesTokenDetails `json:"input_tokens_details,omitempty"`
	OutputTokensDetails responsesTokenDetails `json:"output_tokens_details,omitempty"`
}

type responsesTokenDetails struct {
	CachedTokens int64 `json:"cached_tokens"`
}

type responsesErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// ToResponses renders an Anthropic Messages request as an OpenAI Responses
// API request: system prompts become "instructions", conversation history
// becomes the "input" item array, and tools are flattened to the Responses
// function-tool shape.
func ToResponses(request *Request, model string) ([]byte, error) {
	converted := responsesRequest{
		Model:           model,
		Instructions:    systemText(request.System),
		Stream:          request.Stream,
		MaxOutputTokens: request.MaxTokens,
		Temperature:     request.Temperature,
		TopP:            request.TopP,
	}

	for _, message := range request.Messages {
		items, err := responsesItems(message)
		if err != nil {
			return nil, err
		}
		converted.Input = append(converted.Input, items...)
	}
	if len(converted.Input) == 0 {
		return nil, fmt.Errorf("request contains no messages")
	}

	for _, tool := range request.Tools {
		if tool.Name == "" {
			continue
		}
		converted.Tools = append(converted.Tools, responsesFunctionTool{
			Type:        "function",
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  sanitizeSchema(tool.InputSchema),
		})
	}
	converted.ToolChoice, converted.ParallelToolCalls = responsesToolChoice(request.ToolChoice)

	if request.WantsThinking() {
		converted.Reasoning = &responsesReasoning{Effort: "high", Summary: "auto"}
	}

	return json.Marshal(converted)
}

// responsesItems converts one Anthropic message into the zero or more
// Responses input items it corresponds to. Text runs become message items;
// tool_use and tool_result blocks become function_call and
// function_call_output items, keeping their relative order intact.
func responsesItems(message AnthropicMessage) ([]responsesInputItem, error) {
	role := message.Role
	if role == "" {
		role = "user"
	}
	contentType := "input_text"
	if role == "assistant" {
		contentType = "output_text"
	}

	if text, ok := plainString(message.Content); ok {
		if strings.TrimSpace(text) == "" {
			return nil, nil
		}
		return []responsesInputItem{responsesMessageItem(role, contentType, text)}, nil
	}

	var blocks []anthropicBlock
	if err := json.Unmarshal(message.Content, &blocks); err != nil {
		return nil, fmt.Errorf("decode %s content: %w", role, err)
	}

	var items []responsesInputItem
	var parts []responsesInputContent
	flushText := func() {
		if len(parts) == 0 {
			return
		}
		items = append(items, responsesInputItem{
			Type:    "message",
			Role:    role,
			Content: encodeParts(parts),
		})
		parts = nil
	}

	for _, block := range blocks {
		switch block.Type {
		case "text":
			parts = append(parts, responsesInputContent{Type: contentType, Text: block.Text})
		case "image":
			if url := imageURL(block.Source); url != "" {
				parts = append(parts, responsesInputContent{Type: "input_image", ImageURL: url})
			}
		case "tool_use":
			flushText()
			items = append(items, responsesInputItem{
				Type:      "function_call",
				CallID:    block.ID,
				Name:      block.Name,
				Arguments: compactJSON(block.Input),
			})
		case "tool_result":
			flushText()
			items = append(items, responsesInputItem{
				Type:   "function_call_output",
				CallID: block.ToolUseID,
				Output: toolResultText(block),
			})
		case "thinking", "redacted_thinking":
			// Reasoning traces are provider-specific and are not replayed upstream.
		}
	}
	flushText()
	return items, nil
}

func responsesMessageItem(role, contentType, text string) responsesInputItem {
	return responsesInputItem{
		Type:    "message",
		Role:    role,
		Content: encodeParts([]responsesInputContent{{Type: contentType, Text: text}}),
	}
}

func encodeParts(parts []responsesInputContent) json.RawMessage {
	encoded, err := json.Marshal(parts)
	if err != nil {
		return json.RawMessage(`[]`)
	}
	return encoded
}

// responsesToolChoice maps an Anthropic tool_choice to its Responses
// equivalent, which names forced functions at the top level rather than
// under a "function" wrapper.
func responsesToolChoice(raw json.RawMessage) (any, *bool) {
	if len(raw) == 0 {
		return nil, nil
	}
	var choice struct {
		Type                   string `json:"type"`
		Name                   string `json:"name"`
		DisableParallelToolUse bool   `json:"disable_parallel_tool_use"`
	}
	if err := json.Unmarshal(raw, &choice); err != nil {
		return nil, nil
	}
	var parallel *bool
	if choice.DisableParallelToolUse {
		disabled := false
		parallel = &disabled
	}
	switch choice.Type {
	case "auto":
		return "auto", parallel
	case "any":
		return "required", parallel
	case "none":
		return "none", parallel
	case "tool":
		if choice.Name == "" {
			return "auto", parallel
		}
		return map[string]any{"type": "function", "name": choice.Name}, parallel
	}
	return nil, parallel
}

// FromResponses converts a non-streaming Responses API response into an
// Anthropic Messages response. Reasoning output is surfaced as a thinking
// block only when the client asked for extended thinking.
func FromResponses(body []byte, model string, includeThinking bool) ([]byte, error) {
	var response responsesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}

	out := AnthropicMessageOut{
		ID:      messageID(response.ID),
		Type:    "message",
		Role:    "assistant",
		Model:   model,
		Content: []map[string]any{},
		Usage: AnthropicUsage{
			InputTokens:          int(response.Usage.InputTokens),
			OutputTokens:         int(response.Usage.OutputTokens),
			CacheReadInputTokens: int(response.Usage.InputTokensDetails.CachedTokens),
		},
	}
	if response.Model != "" {
		out.Model = response.Model
	}

	stopReason := "end_turn"
	for _, item := range response.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				switch part.Type {
				case "output_text", "refusal":
					out.Content = append(out.Content, map[string]any{"type": "text", "text": part.Text})
				}
			}
		case "reasoning":
			if !includeThinking {
				continue
			}
			if text := reasoningText(item.Summary, item.Content); text != "" {
				out.Content = append(out.Content, map[string]any{
					"type":      "thinking",
					"thinking":  text,
					"signature": thinkingSignature,
				})
			}
		case "function_call":
			stopReason = "tool_use"
			input := json.RawMessage(strings.TrimSpace(item.Arguments))
			if len(input) == 0 || !json.Valid(input) {
				input = json.RawMessage(`{}`)
			}
			out.Content = append(out.Content, map[string]any{
				"type":  "tool_use",
				"id":    item.CallID,
				"name":  item.Name,
				"input": input,
			})
		}
	}
	if response.Status == "incomplete" {
		stopReason = "max_tokens"
	}
	out.StopReason = &stopReason

	if len(out.Content) == 0 {
		out.Content = append(out.Content, map[string]any{"type": "text", "text": ""})
	}
	return json.Marshal(out)
}

// reasoningText flattens the summary and raw reasoning parts of a Responses
// reasoning output item into one thinking-block payload.
func reasoningText(summary, content []responsesOutputContent) string {
	var builder strings.Builder
	appendParts := func(parts []responsesOutputContent) {
		for _, part := range parts {
			if part.Text == "" {
				continue
			}
			if builder.Len() > 0 {
				builder.WriteString("\n\n")
			}
			builder.WriteString(part.Text)
		}
	}
	appendParts(summary)
	appendParts(content)
	return builder.String()
}

// ---------------------------------------------------------------------------
// OpenAI chat-completions <-> Responses (routing chat clients onto
// responses-only backends).

type chatRequestIn struct {
	Model               string          `json:"model"`
	Messages            []chatMessageIn `json:"messages"`
	MaxTokens           int             `json:"max_tokens"`
	MaxCompletionTokens int             `json:"max_completion_tokens"`
	Temperature         *float64        `json:"temperature"`
	TopP                *float64        `json:"top_p"`
	Stream              bool            `json:"stream"`
	Tools               []openAITool    `json:"tools"`
	ToolChoice          json.RawMessage `json:"tool_choice"`
}

type chatMessageIn struct {
	Role       string           `json:"role"`
	Content    json.RawMessage  `json:"content"`
	ToolCalls  []chatToolCallIn `json:"tool_calls"`
	ToolCallID string           `json:"tool_call_id"`
}

type chatToolCallIn struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ChatToResponses renders an OpenAI chat-completions request as a Responses
// API request: system/developer messages merge into "instructions", assistant
// tool_calls become function_call items, and "tool" messages become
// function_call_output items.
func ChatToResponses(body []byte, model string) ([]byte, error) {
	var chat chatRequestIn
	if err := json.Unmarshal(body, &chat); err != nil {
		return nil, err
	}
	if len(chat.Messages) == 0 {
		return nil, fmt.Errorf("request contains no messages")
	}

	converted := responsesRequest{
		Model:           model,
		Input:           []responsesInputItem{},
		Stream:          chat.Stream,
		Temperature:     chat.Temperature,
		TopP:            chat.TopP,
		MaxOutputTokens: chat.MaxCompletionTokens,
	}
	if converted.MaxOutputTokens <= 0 {
		converted.MaxOutputTokens = chat.MaxTokens
	}

	var instructions strings.Builder
	for _, message := range chat.Messages {
		switch message.Role {
		case "system", "developer":
			if text := chatContentText(message.Content); text != "" {
				if instructions.Len() > 0 {
					instructions.WriteString("\n\n")
				}
				instructions.WriteString(text)
			}
		case "assistant":
			if text := chatContentText(message.Content); text != "" {
				converted.Input = append(converted.Input, responsesMessageItem("assistant", "output_text", text))
			}
			for _, call := range message.ToolCalls {
				converted.Input = append(converted.Input, responsesInputItem{
					Type:      "function_call",
					CallID:    call.ID,
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				})
			}
		case "tool":
			converted.Input = append(converted.Input, responsesInputItem{
				Type:   "function_call_output",
				CallID: message.ToolCallID,
				Output: chatContentText(message.Content),
			})
		default:
			converted.Input = append(converted.Input, chatUserItems(message)...)
		}
	}
	converted.Instructions = instructions.String()

	for _, tool := range chat.Tools {
		if tool.Function.Name == "" {
			continue
		}
		converted.Tools = append(converted.Tools, responsesFunctionTool{
			Type:        "function",
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  sanitizeSchema(tool.Function.Parameters),
		})
	}
	converted.ToolChoice = chatToolChoiceToResponses(chat.ToolChoice)

	return json.Marshal(converted)
}

// chatUserItems converts a user chat message (plain string or multipart
// content) into Responses message items.
func chatUserItems(message chatMessageIn) []responsesInputItem {
	var parts []responsesInputContent
	if text, ok := plainString(message.Content); ok {
		parts = append(parts, responsesInputContent{Type: "input_text", Text: text})
	} else if len(message.Content) > 0 {
		var chunks []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
		}
		if err := json.Unmarshal(message.Content, &chunks); err != nil {
			return nil
		}
		for _, chunk := range chunks {
			switch chunk.Type {
			case "text":
				parts = append(parts, responsesInputContent{Type: "input_text", Text: chunk.Text})
			case "image_url":
				if chunk.ImageURL.URL != "" {
					parts = append(parts, responsesInputContent{Type: "input_image", ImageURL: chunk.ImageURL.URL})
				}
			}
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return []responsesInputItem{{
		Type:    "message",
		Role:    "user",
		Content: encodeParts(parts),
	}}
}

// chatContentText flattens chat message content, which may be a string or a
// list of typed parts, into plain text.
func chatContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if text, ok := plainString(raw); ok {
		return text
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range parts {
		if part.Type != "text" || part.Text == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(part.Text)
	}
	return builder.String()
}

// chatToolChoiceToResponses maps an OpenAI chat tool_choice (string, or an
// object nesting the function name) to its Responses equivalent.
func chatToolChoiceToResponses(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	trimmed := strings.TrimSpace(string(raw))
	switch trimmed {
	case "", "null":
		return nil
	case `"auto"`, `"none"`, `"required"`:
		return strings.Trim(trimmed, `"`)
	}
	var choice struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &choice); err != nil {
		return nil
	}
	if choice.Type == "function" && choice.Function.Name != "" {
		return map[string]any{"type": "function", "name": choice.Function.Name}
	}
	if choice.Type != "" {
		return choice.Type
	}
	return nil
}

type chatResponseOut struct {
	ID      string          `json:"id"`
	Object  string          `json:"object"`
	Created int64           `json:"created"`
	Model   string          `json:"model"`
	Choices []chatChoiceOut `json:"choices"`
	Usage   chatUsageOut    `json:"usage"`
}

type chatChoiceOut struct {
	Index        int            `json:"index"`
	Message      chatMessageOut `json:"message"`
	FinishReason string         `json:"finish_reason"`
}

type chatMessageOut struct {
	Role      string            `json:"role"`
	Content   string            `json:"content"`
	ToolCalls []chatToolCallOut `json:"tool_calls,omitempty"`
}

type chatToolCallOut struct {
	ID       string      `json:"id"`
	Type     string      `json:"type"`
	Function chatFuncOut `json:"function"`
}

type chatFuncOut struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatUsageOut struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

// ChatFromResponses converts a non-streaming Responses API response into an
// OpenAI chat-completions response.
func ChatFromResponses(body []byte, model string) ([]byte, error) {
	var response responsesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}

	var content strings.Builder
	var toolCalls []chatToolCallOut
	for _, item := range response.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" || part.Type == "refusal" {
					content.WriteString(part.Text)
				}
			}
		case "function_call":
			toolCalls = append(toolCalls, chatToolCallOut{
				ID:       item.CallID,
				Type:     "function",
				Function: chatFuncOut{Name: item.Name, Arguments: item.Arguments},
			})
		}
	}

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}
	if response.Status == "incomplete" {
		finishReason = "length"
	}

	id := response.ID
	if id == "" {
		id = "chatcmpl-llm-proxy"
	}
	if response.Model != "" {
		model = response.Model
	}
	total := response.Usage.TotalTokens
	if total == 0 {
		total = response.Usage.InputTokens + response.Usage.OutputTokens
	}

	return json.Marshal(chatResponseOut{
		ID:      id,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []chatChoiceOut{{
			Index:        0,
			Message:      chatMessageOut{Role: "assistant", Content: content.String(), ToolCalls: toolCalls},
			FinishReason: finishReason,
		}},
		Usage: chatUsageOut{
			PromptTokens:     response.Usage.InputTokens,
			CompletionTokens: response.Usage.OutputTokens,
			TotalTokens:      total,
		},
	})
}
