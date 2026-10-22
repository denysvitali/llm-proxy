package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// This file translates between the Anthropic Messages API and the OpenAI
// Responses API, so Anthropic clients can talk to backends that only expose
// /v1/responses. Requests become Responses "input" item arrays (the system
// prompt travels as a leading message, tool results as function_call_output
// items) and Responses responses become Anthropic content blocks again.
// Streaming lives in responses_stream.go; the Chat* helpers adapt OpenAI
// chat-completions clients onto the same Responses-only backends.

type responsesRequest struct {
	Model             string                  `json:"model"`
	Instructions      *string                 `json:"instructions,omitempty"`
	Input             json.RawMessage         `json:"input,omitempty"`
	Tools             []responsesFunctionTool `json:"tools,omitempty"`
	ToolChoice        any                     `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool                   `json:"parallel_tool_calls,omitempty"`
	Reasoning         *responsesReasoning     `json:"reasoning,omitempty"`
	Store             *bool                   `json:"store,omitempty"`
	Stream            bool                    `json:"stream,omitempty"`
	MaxOutputTokens   int                     `json:"max_output_tokens,omitempty"`
	Temperature       *float64                `json:"temperature,omitempty"`
	TopP              *float64                `json:"top_p,omitempty"`
}

// inputItems decodes the request's input, which the Responses API allows as
// either an array of typed items or the plain-string shorthand.
func (r *responsesRequest) inputItems() ([]responsesInputItem, error) {
	if len(r.Input) == 0 {
		return nil, nil
	}
	if r.Input[0] == '"' {
		var text string
		if err := json.Unmarshal(r.Input, &text); err != nil {
			return nil, err
		}
		return []responsesInputItem{{
			Type:    "message",
			Role:    "user",
			Content: mustMarshal([]responsesInputContent{{Type: "input_text", Text: text}}),
		}}, nil
	}
	var rawItems []json.RawMessage
	if err := json.Unmarshal(r.Input, &rawItems); err != nil {
		return nil, err
	}
	items := make([]responsesInputItem, 0, len(rawItems))
	for i, raw := range rawItems {
		// Codex replays its earlier reasoning items (summary plus opaque
		// encrypted_content) in history. Neither chat nor Messages can carry
		// that provider state back, so the items are dropped, as the
		// encrypted content would be meaningless to another backend.
		var header struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &header) == nil && header.Type == "reasoning" {
			continue
		}
		item, err := decodeResponsesInputItem(raw)
		if err != nil {
			return nil, fmt.Errorf("input item %d: %w", i, err)
		}
		items = append(items, item)
	}
	return items, nil
}

// isPromptRole reports whether a message carries system or developer instructions.
func isPromptRole(role string) bool {
	return role == "system" || role == "developer"
}

type responsesReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type responsesInputItem struct {
	Type string `json:"type"`
	// ID and Status are accepted (Codex echoes them in replayed history) but
	// deliberately not forwarded: item identity and completion annotations
	// have no representation on the chat or Messages wires.
	ID        string          `json:"id,omitempty"`
	Status    string          `json:"status,omitempty"`
	Role      string          `json:"role,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Namespace string          `json:"namespace,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
	Input     string          `json:"input,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
}

type responsesInputContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

func (p responsesInputContent) MarshalJSON() ([]byte, error) {
	if p.Type == "input_text" || p.Type == "output_text" {
		return json.Marshal(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{p.Type, p.Text})
	}
	type content responsesInputContent
	return json.Marshal(content(p))
}

type responsesFunctionTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

type responsesResponse struct {
	responseEnvelope
	ID                string                `json:"id"`
	Status            string                `json:"status"`
	Model             string                `json:"model"`
	Output            []responsesOutputItem `json:"output"`
	Usage             responsesUsage        `json:"usage"`
	IncompleteDetails json.RawMessage       `json:"incomplete_details"`
	Error             *responsesErrorBody   `json:"error"`
}

type responsesOutputItem struct {
	ID        string                   `json:"id"`
	Type      string                   `json:"type"`
	Role      string                   `json:"role,omitempty"`
	Status    string                   `json:"status,omitempty"`
	Content   []responsesOutputContent `json:"content,omitempty"`
	Summary   []responsesOutputContent `json:"summary,omitempty"`
	CallID    string                   `json:"call_id,omitempty"`
	Namespace string                   `json:"namespace,omitempty"`
	Name      string                   `json:"name,omitempty"`
	Arguments string                   `json:"arguments,omitempty"`
}

type responsesOutputContent struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	Refusal string `json:"refusal,omitempty"`
}

type responsesUsage struct {
	InputTokens         int64                 `json:"input_tokens"`
	OutputTokens        int64                 `json:"output_tokens"`
	TotalTokens         int64                 `json:"total_tokens"`
	InputTokensDetails  cachedTokensDetail    `json:"input_tokens_details"`
	OutputTokensDetails reasoningTokensDetail `json:"output_tokens_details"`
}

type responsesErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// asUpstreamError views a Responses error body through the shared upstream
// error type; the Responses API puts the error at the top level.
func (e *responsesErrorBody) asUpstreamError() error {
	return checkUpstreamError(&upstreamErrorObj{Message: e.Message, Type: e.Type})
}

// ToResponses renders an Anthropic Messages request as an OpenAI Responses
// API request: system prompts become leading system messages, conversation
// history becomes the input item array, and tools use the function-tool shape.
func ToResponses(request *Request, model string) ([]byte, error) {
	if err := validateAnthropicTranslation(request, true); err != nil {
		return nil, err
	}
	if err := validateAnthropicHistory(request.Messages); err != nil {
		return nil, err
	}
	converted := responsesRequest{
		Model:           model,
		Stream:          request.Stream,
		MaxOutputTokens: request.MaxTokens,
		Temperature:     request.Temperature,
		TopP:            request.TopP,
	}
	if effort, _ := anthropicEffort(request.fields["output_config"]); effort != "" {
		converted.Reasoning = &responsesReasoning{Effort: effort}
	}

	var inputItems []responsesInputItem
	system, err := responsesSystemItem(request.System)
	if err != nil {
		return nil, err
	}
	if system != nil {
		inputItems = append(inputItems, *system)
	}
	for _, message := range request.Messages {
		items, err := responsesItems(message)
		if err != nil {
			return nil, err
		}
		inputItems = append(inputItems, items...)
	}
	if len(inputItems) == 0 {
		return nil, fmt.Errorf("request contains no messages")
	}
	converted.Input = mustMarshal(inputItems)

	for _, tool := range request.Tools {
		if tool.Name == "" {
			continue
		}
		schema, err := sanitizeSchema(tool.InputSchema)
		if err != nil {
			return nil, err
		}
		converted.Tools = append(converted.Tools, responsesFunctionTool{
			Type:        "function",
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  schema,
		})
	}
	converted.ToolChoice, converted.ParallelToolCalls, err = anthropicToolChoice(request.ToolChoice, true)
	if err != nil {
		return nil, err
	}

	return json.Marshal(converted)
}

// responsesItems converts one Anthropic message into the zero or more
// Responses input items it corresponds to. Text runs become message items;
// tool_use and tool_result blocks become function_call and
// function_call_output items, keeping their relative order intact.
func responsesItems(message AnthropicMessage) ([]responsesInputItem, error) {
	role := message.Role
	if role == "system" {
		item, err := responsesSystemItem(message.Content)
		if err != nil {
			return nil, fmt.Errorf("system message: %w", err)
		}
		if item == nil {
			return nil, fmt.Errorf("system message requires text content")
		}
		return []responsesInputItem{*item}, nil
	}
	if role != "user" && role != "assistant" {
		return nil, fmt.Errorf("unsupported Messages role %q", role)
	}
	contentType := "input_text"
	if role == "assistant" {
		contentType = "output_text"
	}

	if text, ok := plainString(message.Content); ok {
		return []responsesInputItem{responsesMessageItem(role, contentType, text)}, nil
	}

	blocks, err := anthropicInputBlocks(message.Content)
	if err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		if bytes.Equal(bytes.TrimSpace(message.Content), []byte("[]")) {
			return nil, fmt.Errorf("message content must not be an empty array")
		}
		// Only replayed thinking, which was dropped; the turn adds nothing.
		return nil, nil
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
			if role != "user" {
				return nil, fmt.Errorf("responses cannot represent assistant images")
			}
			url, err := anthropicImageURL(block.Source)
			if err != nil {
				return nil, err
			}
			parts = append(parts, responsesInputContent{Type: "input_image", ImageURL: url})
		case "tool_use":
			arguments, err := compactJSON(block.Input)
			if err != nil {
				return nil, err
			}
			flushText()
			items = append(items, responsesInputItem{
				Type:      "function_call",
				CallID:    block.ID,
				Name:      block.Name,
				Arguments: arguments,
			})
		case "tool_result":
			output, err := anthropicToolOutput(block, true)
			if err != nil {
				return nil, err
			}
			flushText()
			items = append(items, responsesInputItem{
				Type:   "function_call_output",
				CallID: block.ToolUseID,
				Output: output,
			})
		}
	}
	flushText()
	return items, nil
}

// responsesSystemItem renders Messages system content (top-level or a
// mid-conversation system message) as a system input item; nil when absent.
func responsesSystemItem(raw json.RawMessage) (*responsesInputItem, error) {
	system, err := anthropicSystemContent(raw)
	if err != nil || system == nil {
		return nil, err
	}
	if _, ok := plainString(system); !ok {
		system, err = openAIToolOutput(system, false)
		if err != nil {
			return nil, err
		}
	}
	return &responsesInputItem{Type: "message", Role: "system", Content: system}, nil
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

// FromResponses converts a non-streaming Responses API response into an
// Anthropic Messages response. Unsigned reasoning is suppressed unless requested,
// in which case conversion fails rather than fabricating signed thinking state.
func FromResponses(body []byte, model string, includeThinking bool) ([]byte, error) {
	var response responsesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	if err := validateResponsesUsage(response.Usage); err != nil {
		return nil, err
	}
	if response.Error != nil {
		return nil, response.Error.asUpstreamError()
	}
	if err := validateResponsesOutput(body, response.Status); err != nil {
		return nil, err
	}

	out := AnthropicMessageOut{
		ID:      messageID(response.ID),
		Type:    "message",
		Role:    "assistant",
		Model:   model,
		Content: []map[string]any{},
		Usage:   responsesUsageToAnthropic(response.Usage),
	}
	if response.Model != "" {
		out.Model = response.Model
	}

	stopReason := "end_turn"
	hasTools, hasRefusal := false, false
	ids := responseCallIDs{}
	for _, item := range response.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				switch part.Type {
				case "output_text":
					out.Content = append(out.Content, map[string]any{"type": "text", "text": part.Text})
				case "refusal":
					out.Content = append(out.Content, map[string]any{"type": "text", "text": part.Refusal})
					hasRefusal = true
				}
			}
		case "reasoning":
			if !includeThinking {
				continue
			}
			if text := reasoningText(item.Summary, item.Content); text != "" {
				return nil, errUnsignedThinking
			}
		case "function_call":
			hasTools = true
			input, err := argumentsJSON(item.Arguments)
			if err != nil {
				return nil, err
			}
			id, err := ids.resolve(item.CallID, "")
			if err != nil {
				return nil, err
			}
			out.Content = append(out.Content, map[string]any{
				"type":  "tool_use",
				"id":    id,
				"name":  item.Name,
				"input": input,
			})
		}
	}
	finish, err := responsesFinish(response, hasTools)
	if err != nil {
		return nil, err
	}
	if finish != "stop" {
		stopReason = map[string]string{"tool_calls": "tool_use", "length": "max_tokens", "content_filter": "refusal"}[finish]
	}
	if hasRefusal && (response.Status == "" || response.Status == "completed") {
		stopReason = "refusal"
	}
	if response.Status == "incomplete" {
		var details struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(response.IncompleteDetails, &details)
		if details.Reason == "model_context_window_exceeded" {
			stopReason = details.Reason
		}
	}
	out.StopReason = &stopReason

	if len(out.Content) == 0 {
		out.Content = append(out.Content, map[string]any{"type": "text", "text": ""})
	}
	return json.Marshal(out)
}

// reasoningText concatenates the summary and raw display-reasoning parts of a
// Responses reasoning output item without adding separators.
func reasoningText(summary, content []responsesOutputContent) string {
	var builder strings.Builder
	appendParts := func(parts []responsesOutputContent) {
		for _, part := range parts {
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
// API request: message roles and positions are preserved, assistant
// tool_calls become function_call items, and "tool" messages become
// function_call_output items.
func ChatToResponses(body []byte, model string) ([]byte, error) {
	if err := validateGenerationControls(body, "openai-chat"); err != nil {
		return nil, err
	}
	controls, err := translatedOpenAIControls(body, "openai-chat", "openai-responses")
	if err != nil {
		return nil, err
	}
	var chat chatRequestIn
	if err := json.Unmarshal(body, &chat); err != nil {
		return nil, err
	}
	if len(chat.Messages) == 0 {
		return nil, fmt.Errorf("request contains no messages")
	}
	if err := validateChatHistory(chat.Messages); err != nil {
		return nil, err
	}

	converted := responsesRequest{
		Model:           model,
		Stream:          chat.Stream,
		Temperature:     chat.Temperature,
		TopP:            chat.TopP,
		MaxOutputTokens: chat.MaxCompletionTokens,
	}

	var inputItems []responsesInputItem
	if converted.MaxOutputTokens <= 0 {
		converted.MaxOutputTokens = chat.MaxTokens
	}

	for _, message := range chat.Messages {
		switch message.Role {
		case "assistant":
			if len(message.Content) > 0 && string(message.Content) != "null" {
				items, err := chatMessageItems(message)
				if err != nil {
					return nil, err
				}
				inputItems = append(inputItems, items...)
			}
			for _, call := range message.ToolCalls {
				if _, err := argumentsJSON(call.Function.Arguments); err != nil {
					return nil, err
				}
				inputItems = append(inputItems, responsesInputItem{
					Type:      "function_call",
					CallID:    call.ID,
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				})
			}
		case "tool":
			output, err := openAIToolOutput(message.Content, false)
			if err != nil {
				return nil, err
			}
			inputItems = append(inputItems, responsesInputItem{
				Type:   "function_call_output",
				CallID: message.ToolCallID,
				Output: output,
			})
		default:
			items, err := chatMessageItems(message)
			if err != nil {
				return nil, err
			}
			inputItems = append(inputItems, items...)
		}
	}
	if len(inputItems) == 0 {
		return nil, fmt.Errorf("request contains no translatable messages")
	}
	converted.Input = mustMarshal(inputItems)

	for _, tool := range chat.Tools {
		if tool.Function.Name == "" {
			continue
		}
		schema, err := sanitizeSchema(tool.Function.Parameters)
		if err != nil {
			return nil, err
		}
		converted.Tools = append(converted.Tools, responsesFunctionTool{
			Type:        "function",
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  schema,
			Strict:      tool.Function.Strict,
		})
	}
	converted.ToolChoice = chatToolChoiceToResponses(chat.ToolChoice)

	return marshalWithOpenAIControls(converted, controls)
}

// chatMessageItems preserves message roles and ordered text/image content.
func chatMessageItems(message chatMessageIn) ([]responsesInputItem, error) {
	switch message.Role {
	case "system", "developer", "user", "assistant":
	default:
		return nil, fmt.Errorf("unsupported message role %q", message.Role)
	}
	textType := "input_text"
	if message.Role == "assistant" {
		textType = "output_text"
	}
	parts := make([]responsesInputContent, 0)
	if text, ok := plainString(message.Content); ok {
		parts = append(parts, responsesInputContent{Type: textType, Text: text})
	} else {
		var chunks []json.RawMessage
		if err := json.Unmarshal(message.Content, &chunks); err != nil || len(chunks) == 0 {
			return nil, fmt.Errorf("message content must be a string or a non-empty array")
		}
		for _, raw := range chunks {
			var header struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(raw, &header); err != nil {
				return nil, fmt.Errorf("message content part must be an object")
			}
			switch header.Type {
			case "text":
				var chunk struct {
					Type string  `json:"type"`
					Text *string `json:"text"`
				}
				if err := decodeContentPart(raw, &chunk); err != nil || chunk.Text == nil {
					return nil, fmt.Errorf("text part requires string text and supported fields")
				}
				parts = append(parts, responsesInputContent{Type: textType, Text: *chunk.Text})
			case "image_url":
				var chunk struct {
					Type     string `json:"type"`
					ImageURL struct {
						URL    string `json:"url"`
						Detail string `json:"detail"`
					} `json:"image_url"`
				}
				if err := decodeContentPart(raw, &chunk); err != nil {
					return nil, fmt.Errorf("unsupported image fields: %w", err)
				}
				if message.Role != "user" || chunk.ImageURL.URL == "" {
					return nil, fmt.Errorf("image_url requires a user message and URL")
				}
				parts = append(parts, responsesInputContent{Type: "input_image", ImageURL: chunk.ImageURL.URL, Detail: chunk.ImageURL.Detail})
			default:
				return nil, fmt.Errorf("unsupported message content type %q", header.Type)
			}
		}
	}
	return []responsesInputItem{{
		Type:    "message",
		Role:    message.Role,
		Content: encodeParts(parts),
	}}, nil
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
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Created int64             `json:"created"`
	Model   string            `json:"model"`
	Error   *upstreamErrorObj `json:"error,omitempty"`
	Choices []chatChoiceOut   `json:"choices"`
	Usage   chatUsageOut      `json:"usage"`
}

type chatChoiceOut struct {
	Index        int            `json:"index"`
	Message      chatMessageOut `json:"message"`
	FinishReason string         `json:"finish_reason"`
}

type chatMessageOut struct {
	Role             string            `json:"role"`
	Content          string            `json:"content"`
	ContentPresent   bool              `json:"-"`
	Refusal          *string           `json:"refusal,omitempty"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
	ToolCalls        []chatToolCallOut `json:"tool_calls,omitempty"`
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
	PromptTokens            int64                  `json:"prompt_tokens"`
	CompletionTokens        int64                  `json:"completion_tokens"`
	TotalTokens             int64                  `json:"total_tokens"`
	PromptTokensDetails     *cachedTokensDetail    `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *reasoningTokensDetail `json:"completion_tokens_details,omitempty"`
}

// CacheCreationTokens is a proxy extension: OpenAI has no standard cache-write count.
type cachedTokensDetail struct {
	CachedTokens        int64 `json:"cached_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens,omitempty"`
}

type reasoningTokensDetail struct {
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

// ChatFromResponses converts a non-streaming Responses API response into an
// OpenAI chat-completions response.
func ChatFromResponses(body []byte, model string) ([]byte, error) {
	var response responsesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	if err := validateResponsesUsage(response.Usage); err != nil {
		return nil, err
	}
	if response.Error != nil {
		return nil, response.Error.asUpstreamError()
	}
	if err := validateResponsesOutput(body, response.Status); err != nil {
		return nil, err
	}

	var content strings.Builder
	var reasoning strings.Builder
	var refusal *string
	var toolCalls []chatToolCallOut
	ids := responseCallIDs{}
	for _, item := range response.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				switch part.Type {
				case "output_text":
					content.WriteString(part.Text)
				case "refusal":
					if refusal == nil {
						value := ""
						refusal = &value
					}
					*refusal += part.Refusal
				}
			}
		case "reasoning":
			reasoning.WriteString(reasoningText(item.Summary, item.Content))
		case "function_call":
			if response.Status != "incomplete" {
				if _, err := argumentsJSON(item.Arguments); err != nil {
					return nil, err
				}
			}
			id, err := ids.resolve(item.CallID, "")
			if err != nil {
				return nil, err
			}
			toolCalls = append(toolCalls, chatToolCallOut{
				ID:       id,
				Type:     "function",
				Function: chatFuncOut{Name: item.Name, Arguments: item.Arguments},
			})
		}
	}

	finishReason, err := responsesFinish(response, len(toolCalls) > 0)
	if err != nil {
		return nil, err
	}

	id := responseID(response.ID, "chatcmpl-")
	if response.Model != "" {
		model = response.Model
	}

	created := response.CreatedAt
	if created == 0 {
		created = nowUnix()
	}
	return json.Marshal(chatResponseOut{
		ID:      id,
		Object:  "chat.completion",
		Created: created,
		Model:   model,
		Choices: []chatChoiceOut{{
			Index:        0,
			Message:      chatMessageOut{Role: "assistant", Content: content.String(), ReasoningContent: reasoning.String(), Refusal: refusal, ToolCalls: toolCalls},
			FinishReason: finishReason,
		}},
		Usage: responsesUsageToChat(response.Usage),
	})
}
