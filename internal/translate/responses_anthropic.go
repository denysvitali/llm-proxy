package translate

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// This file routes OpenAI Responses API clients onto backends that only
// expose the Anthropic Messages API: requests become Messages requests,
// non-streaming replies become Responses response objects, and streaming
// replies are re-emitted as Responses SSE events on top of the shared
// responsesItemStream builder from responses_to_chat_stream.go.

// ResponsesToAnthropic renders an OpenAI Responses request as an Anthropic
// Messages request: "instructions" becomes the system prompt, message items
// become user/assistant messages, function_call / function_call_output items
// become tool_use / tool_result blocks.
func ResponsesToAnthropic(body []byte, model string) ([]byte, error) {
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

	converted := anthropicRequest{
		Model:       model,
		MaxTokens:   req.MaxOutputTokens,
		System:      req.Instructions,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
	}
	if converted.MaxTokens <= 0 {
		converted.MaxTokens = defaultMaxTokens
	}

	for i, item := range input {
		switch item.Type {
		case "message":
			msg, err := anthropicMessageFromItem(item)
			if err != nil {
				return nil, fmt.Errorf("input item %d: %w", i, err)
			}
			if msg != nil {
				converted.Messages = append(converted.Messages, *msg)
			}
		case "function_call":
			// Assistant-initiated tool call in the conversation history.
			converted.Messages = append(converted.Messages, AnthropicMessage{
				Role: "assistant",
				Content: mustMarshal([]anthropicBlock{{
					Type:  "tool_use",
					ID:    item.CallID,
					Name:  item.Name,
					Input: argumentsJSON(item.Arguments),
				}}),
			})
		case "function_call_output":
			converted.Messages = append(converted.Messages, AnthropicMessage{
				Role: "user",
				Content: mustMarshal([]anthropicBlock{{
					Type:      "tool_result",
					ToolUseID: item.CallID,
					Content:   mustMarshal(item.Output),
				}}),
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
		converted.Tools = append(converted.Tools, AnthropicTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: sanitizeSchema(tool.Parameters),
		})
	}
	converted.ToolChoice = toolChoiceToAnthropic(req.ToolChoice)

	return json.Marshal(converted)
}

// anthropicMessageFromItem converts one Responses message item into an
// Anthropic message; it returns nil for items that carry no content.
func anthropicMessageFromItem(item responsesInputItem) (*AnthropicMessage, error) {
	role := item.Role
	if role == "" {
		role = "user"
	}
	if text, ok := plainString(item.Content); ok {
		if strings.TrimSpace(text) == "" {
			return nil, nil
		}
		return &AnthropicMessage{Role: role, Content: mustMarshal([]anthropicBlock{{Type: "text", Text: text}})}, nil
	}
	var parts []responsesInputContent
	if len(item.Content) > 0 {
		if err := json.Unmarshal(item.Content, &parts); err != nil {
			return nil, fmt.Errorf("decode content: %w", err)
		}
	}

	var blocks []anthropicBlock
	for _, part := range parts {
		switch part.Type {
		case "input_text", "output_text":
			if part.Text != "" {
				blocks = append(blocks, anthropicBlock{Type: "text", Text: part.Text})
			}
		case "input_image":
			if block := imageBlock(part.ImageURL); block != nil {
				blocks = append(blocks, *block)
			}
		}
	}
	if len(blocks) == 0 {
		return nil, nil
	}
	return &AnthropicMessage{Role: role, Content: mustMarshal(blocks)}, nil
}

// ResponsesFromAnthropic converts a non-streaming Anthropic Messages response
// into an OpenAI Responses response object.
func ResponsesFromAnthropic(body []byte, model string) ([]byte, error) {
	var response anthropicResponseIn
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}

	id := response.ID
	if id == "" {
		id = "resp_llm-proxy"
	}
	if response.Model != "" {
		model = response.Model
	}

	out := responsesResponse{
		ID:     id,
		Status: "completed",
		Model:  model,
		Output: []responsesOutputItem{},
	}
	itemID := func() string {
		return fmt.Sprintf("%s_item_%d", id, len(out.Output))
	}
	for _, block := range response.Content {
		switch block.Type {
		case "thinking":
			out.Output = append(out.Output, responsesOutputItem{
				ID:      itemID(),
				Type:    "reasoning",
				Summary: []responsesOutputContent{{Type: "summary_text", Text: block.Thinking}},
			})
		case "text":
			out.Output = append(out.Output, responsesOutputItem{
				ID:      itemID(),
				Type:    "message",
				Role:    "assistant",
				Status:  "completed",
				Content: []responsesOutputContent{{Type: "output_text", Text: block.Text}},
			})
		case "tool_use":
			out.Output = append(out.Output, responsesOutputItem{
				ID:        block.ID,
				Type:      "function_call",
				CallID:    block.ID,
				Name:      block.Name,
				Arguments: compactJSON(block.Input),
				Status:    "completed",
			})
		}
	}
	if response.StopReason == "max_tokens" {
		out.Status = "incomplete"
		out.IncompleteDetails = json.RawMessage(`{"reason":"max_output_tokens"}`)
	}

	out.Usage = responsesUsage{
		InputTokens:  int64(response.Usage.InputTokens),
		OutputTokens: int64(response.Usage.OutputTokens),
		TotalTokens:  int64(response.Usage.InputTokens + response.Usage.OutputTokens),
	}
	if response.Usage.CacheReadInputTokens > 0 {
		out.Usage.InputTokensDetails = responsesTokenDetails{CachedTokens: int64(response.Usage.CacheReadInputTokens)}
	}

	return json.Marshal(out)
}

// ---------------------------------------------------------------------------
// Stream direction: Anthropic SSE -> Responses events.

// ResponsesStreamFromAnthropic converts an upstream Anthropic SSE stream into
// OpenAI Responses API events.
type ResponsesStreamFromAnthropic struct {
	stream responsesItemStream
	blocks map[int]string // anthropic block index -> open item kind
}

func NewResponsesStreamFromAnthropic(writer io.Writer, flush func(), model string) *ResponsesStreamFromAnthropic {
	return &ResponsesStreamFromAnthropic{
		stream: responsesItemStream{
			writer: writer,
			flush:  flush,
			model:  model,
			id:     "resp_llm-proxy",
		},
		blocks: map[int]string{},
	}
}

// Consume reads the upstream SSE stream to completion.
func (s *ResponsesStreamFromAnthropic) Consume(body io.Reader) error {
	err := scanSSE(body, func(payload []byte) (bool, error) {
		var event anthropicEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return false, nil
		}
		return s.consumeEvent(event), nil
	})
	s.Finish()
	return err
}

func (s *ResponsesStreamFromAnthropic) consumeEvent(event anthropicEvent) bool {
	switch event.Type {
	case "message_start":
		if event.Message != nil {
			if event.Message.ID != "" {
				s.stream.id = event.Message.ID
			}
			if event.Message.Model != "" {
				s.stream.model = event.Message.Model
			}
			s.stream.setUsage(int64(event.Message.Usage.InputTokens), 0)
		}
		s.stream.ensureCreated()

	case "content_block_start":
		if event.ContentBlock == nil {
			return false
		}
		switch event.ContentBlock.Type {
		case "text":
			s.blocks[event.Index] = "text"
			s.stream.openItem("message")
		case "thinking":
			s.blocks[event.Index] = "thinking"
			s.stream.openItem("reasoning")
		case "tool_use":
			s.blocks[event.Index] = "tool_use"
			s.stream.startToolCall(event.ContentBlock.ID, event.ContentBlock.Name)
		}

	case "content_block_delta":
		if event.Delta == nil {
			return false
		}
		switch s.blocks[event.Index] {
		case "text":
			if event.Delta.Text != "" {
				s.stream.appendText(event.Delta.Text)
			}
		case "thinking":
			if event.Delta.Thinking != "" {
				s.stream.appendReasoning(event.Delta.Thinking)
			}
		case "tool_use":
			if event.Delta.PartialJSON != "" {
				s.stream.appendArgs(event.Delta.PartialJSON)
			}
		}

	case "content_block_stop":
		delete(s.blocks, event.Index)
		s.stream.closeItem()

	case "message_delta":
		if event.Delta != nil && event.Delta.StopReason == "max_tokens" {
			s.stream.incomplete = true
		}
		if event.Usage != nil {
			s.stream.setUsage(s.usageInput(event), int64(event.Usage.OutputTokens))
		}

	case "message_stop":
		s.Finish()
		return true

	case "error":
		errType, message := "api_error", "upstream stream failed"
		if event.Error != nil {
			if event.Error.Type != "" {
				errType = event.Error.Type
			}
			if event.Error.Message != "" {
				message = event.Error.Message
			}
		}
		s.emitFailed(errType, message)
		s.stream.finished = true
		return true
	}
	return false
}

// usageInput returns the input-token count from a message_delta usage frame;
// these frames repeat the message totals, so fall back to what we already hold.
func (s *ResponsesStreamFromAnthropic) usageInput(event anthropicEvent) int64 {
	if event.Usage != nil && event.Usage.InputTokens > 0 {
		return int64(event.Usage.InputTokens)
	}
	return s.stream.usageIn
}

// emitFailed terminates the stream with a response.failed event.
func (s *ResponsesStreamFromAnthropic) emitFailed(errType, message string) {
	s.stream.ensureCreated()
	s.stream.emit("response.failed", map[string]any{
		"type": "response.failed",
		"response": map[string]any{
			"id": s.stream.id, "model": s.stream.model, "status": "failed",
			"error": map[string]any{"type": errType, "code": nil, "message": message},
		},
	})
}

// Finish closes any open item and emits response.completed.
func (s *ResponsesStreamFromAnthropic) Finish() {
	s.stream.Finish()
}
