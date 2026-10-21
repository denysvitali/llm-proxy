package translate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	if err := validateGenerationControls(body, "openai-responses"); err != nil {
		return nil, err
	}
	controls, err := translatedOpenAIControls(body, "openai-responses", "anthropic")
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

	converted := anthropicRequest{
		Model:       model,
		MaxTokens:   req.MaxOutputTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
	}
	if converted.MaxTokens <= 0 {
		converted.MaxTokens = defaultMaxTokens
	}

	if req.Instructions != nil {
		converted.System = *req.Instructions
	}
	tools, namespaces, err := responsesTools(body)
	if err != nil {
		return nil, fmt.Errorf("decode tools: %w", err)
	}
	choice, err := responsesWireToolChoice(req.ToolChoice, namespaces)
	if err != nil {
		return nil, err
	}
	conversationStarted := false
	for i, item := range input {
		if item.Type == "message" && isPromptRole(item.Role) {
			if conversationStarted {
				msg, err := anthropicSystemMessage(item.Content, "input_text")
				if err != nil {
					return nil, fmt.Errorf("input item %d: %w", i, err)
				}
				converted.Messages = append(converted.Messages, msg)
				continue
			}
			converted.System, err = appendAnthropicInstruction(converted.System, item.Content, "input_text")
			if err != nil {
				return nil, fmt.Errorf("input item %d: %w", i, err)
			}
			continue
		}
		conversationStarted = true
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
			input, err := argumentsJSON(item.Arguments)
			if err != nil {
				return nil, err
			}
			name, err := responsesWireToolName(item.Name, item.Namespace, namespaces)
			if err != nil {
				return nil, err
			}
			converted.Messages = append(converted.Messages, AnthropicMessage{
				Role: "assistant",
				Content: mustMarshal([]anthropicBlock{{
					Type:  "tool_use",
					ID:    item.CallID,
					Name:  name,
					Input: input,
				}}),
			})
		case "function_call_output":
			content, err := openAIToolResultToAnthropic(item.Output, true)
			if err != nil {
				return nil, fmt.Errorf("input item %d: tool result: %w", i, err)
			}
			converted.Messages = append(converted.Messages, AnthropicMessage{
				Role: "user",
				Content: mustMarshal([]anthropicBlock{{
					Type:      "tool_result",
					ToolUseID: item.CallID,
					Content:   content,
				}}),
			})
		default:
			return nil, fmt.Errorf("input item %d: unsupported type %q", i, item.Type)
		}
	}
	if len(converted.Messages) == 0 {
		return nil, fmt.Errorf("request contains no translatable input")
	}

	for _, tool := range tools {
		// strict is dropped: false is the Messages wire's default, and true
		// only guarantees the backend enforces the schema — Codex sends
		// strict:false on every tool, so failing the turn over an annotation
		// no backend observes would be a gratuitous rejection.
		_ = tool.Strict
		if tool.Name == "" {
			continue
		}
		schema, err := sanitizeSchema(tool.Parameters)
		if err != nil {
			return nil, err
		}
		converted.Tools = append(converted.Tools, AnthropicTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schema,
		})
	}
	converted.ToolChoice = toolChoiceToAnthropicWithNamespaces(choice, nil)

	return marshalWithOpenAIControls(converted, controls)
}

// anthropicMessageFromItem preserves supported text/image parts in order.
func anthropicMessageFromItem(item responsesInputItem) (*AnthropicMessage, error) {
	if item.Role != "user" && item.Role != "assistant" {
		return nil, fmt.Errorf("unsupported message role %q", item.Role)
	}
	blocks, err := openAIContentToAnthropic(item.Content, true, item.Role == "user")
	if err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("empty content array cannot form a Messages turn")
	}
	return &AnthropicMessage{Role: item.Role, Content: mustMarshal(blocks)}, nil
}

// ResponsesFromAnthropic converts a non-streaming Anthropic Messages response
// into an OpenAI Responses response object.
func ResponsesFromAnthropic(body []byte, model string) ([]byte, error) {
	return responsesFromAnthropic(body, model, nil, nil)
}

// ResponsesFromAnthropicForRequest restores namespace metadata for tool calls
// when the client request used Codex's grouped-tool extension.
func ResponsesFromAnthropicForRequest(body []byte, model string, requestBody []byte) ([]byte, error) {
	namespaces, err := responseNamespaceMap(requestBody)
	if err != nil {
		return nil, fmt.Errorf("decode request tools: %w", err)
	}
	return responsesFromAnthropic(body, model, namespaces, requestBody)
}

func responsesFromAnthropic(body []byte, model string, namespaces map[string]responseNamespaceTool, requestBody []byte) ([]byte, error) {
	var response anthropicResponseIn
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	if err := checkUpstreamError(response.Error); err != nil {
		return nil, err
	}
	if err := validateAnthropicOutput(body); err != nil {
		return nil, err
	}
	if err := validateAnthropicUsage(response.Usage); err != nil {
		return nil, err
	}

	if err := validateAnthropicStop(response.StopReason); err != nil {
		return nil, err
	}
	if response.Model != "" {
		model = response.Model
	}

	out, err := newResponsesResponse(response.ID, model, 0, requestBody)
	if err != nil {
		return nil, err
	}
	status := "completed"
	if response.StopReason == "max_tokens" || response.StopReason == "model_context_window_exceeded" {
		status = "incomplete"
	}
	ids := responseCallIDs{}
	for _, block := range response.Content {
		switch block.Type {
		case "thinking":
			out.Output = append(out.Output, responsesOutputItem{
				ID:      out.nextItemID(),
				Type:    "reasoning",
				Summary: []responsesOutputContent{{Type: "summary_text", Text: block.Thinking}},
			})
		case "text":
			out.Output = append(out.Output, responsesOutputItem{
				ID:      out.nextItemID(),
				Type:    "message",
				Role:    "assistant",
				Status:  status,
				Content: []responsesOutputContent{{Type: "output_text", Text: block.Text}},
			})
		case "tool_use":
			arguments, err := compactJSON(block.Input)
			if err != nil {
				return nil, err
			}
			callID, err := ids.resolve(block.ID, "")
			if err != nil {
				return nil, err
			}
			name, namespace := restoreResponsesToolName(block.Name, namespaces)
			out.Output = append(out.Output, responsesOutputItem{
				ID:        out.nextItemID(),
				Type:      "function_call",
				CallID:    callID,
				Namespace: namespace,
				Name:      name,
				Arguments: arguments,
				Status:    status,
			})
		}
	}
	setResponsesFinish(&out, anthropicStopToFinish(response.StopReason, false))
	if response.StopReason == "model_context_window_exceeded" {
		out.IncompleteDetails = json.RawMessage(`{"reason":"model_context_window_exceeded"}`)
	}
	if response.StopReason == "refusal" {
		// Messages marks refusal on the turn rather than on individual text blocks.
		out.Status = "completed"
		out.IncompleteDetails = nil
		for i := range out.Output {
			for j := range out.Output[i].Content {
				part := &out.Output[i].Content[j]
				if part.Type == "output_text" {
					part.Type, part.Refusal, part.Text = "refusal", part.Text, ""
				}
			}
		}
	}

	out.Usage = anthropicUsageToResponses(response.Usage)

	return json.Marshal(out)
}

// ---------------------------------------------------------------------------
// Stream direction: Anthropic SSE -> Responses events.

// ResponsesStreamFromAnthropic converts an upstream Anthropic SSE stream into
// OpenAI Responses API events.
type ResponsesStreamFromAnthropic struct {
	stream       responsesItemStream
	blocks       map[int]*streamedResponseItem
	state        anthropicStreamState
	namespaces   map[string]responseNamespaceTool
	failure      error
	messageUsage AnthropicUsage
}

func NewResponsesStreamFromAnthropic(writer io.Writer, flush func(), model string) *ResponsesStreamFromAnthropic {
	return newResponsesStreamFromAnthropic(writer, flush, model, nil)
}

// NewResponsesStreamFromAnthropicForRequest restores namespace metadata for
// tool calls when the client request used Codex's grouped-tool extension.
func NewResponsesStreamFromAnthropicForRequest(writer io.Writer, flush func(), model string, requestBody []byte) *ResponsesStreamFromAnthropic {
	namespaces, _ := responseNamespaceMap(requestBody)
	stream := newResponsesStreamFromAnthropic(writer, flush, model, namespaces)
	stream.stream.request = requestBody
	return stream
}

func newResponsesStreamFromAnthropic(writer io.Writer, flush func(), model string, namespaces map[string]responseNamespaceTool) *ResponsesStreamFromAnthropic {
	return &ResponsesStreamFromAnthropic{
		stream: responsesItemStream{
			writer: writer,
			flush:  flush,
			model:  model,
		},
		blocks:     map[int]*streamedResponseItem{},
		namespaces: namespaces,
	}
}

// Consume reads the upstream SSE stream to completion. It returns an error
// when the upstream breaks mid-stream or ends without its message_stop, so
// the caller can end the stream with an explicit failure instead of one that
// hides the truncation.
func (s *ResponsesStreamFromAnthropic) Consume(body io.Reader) error {
	err := scanSSE(body, func(payload []byte) (bool, error) {
		var event anthropicEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return false, fmt.Errorf("invalid upstream Messages event: %w", err)
		}
		if err := event.validateUsage(s.messageUsage); err != nil {
			return false, err
		}
		frames, err := s.state.consume(event)
		if err != nil {
			s.failure = err
			return false, err
		}
		terminal := false
		for _, frame := range frames {
			terminal = s.consumeEvent(frame)
			if s.stream.writeErr != nil {
				return false, s.stream.writeErr
			}
		}
		return terminal, nil
	})
	if err != nil {
		return err
	}
	if s.failure != nil {
		return s.failure
	}
	if s.stream.finished {
		return s.stream.writeErr
	}
	if s.stream.started {
		return errors.New("upstream stream ended before the completion marker")
	}
	return errors.New("upstream stream ended without emitting any events")
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
			s.messageUsage = event.Message.Usage
			s.stream.usage = anthropicUsageToResponses(s.messageUsage)
		}
		s.stream.ensureCreated()

	case "content_block_start":
		block := event.ContentBlock
		var item *streamedResponseItem
		switch block.Type {
		case "text":
			item = s.stream.addItem("message", "", "", "")
			s.stream.appendItemText(item, block.Text)
		case "thinking":
			item = s.stream.addItem("reasoning", "", "", "")
			s.stream.appendItemText(item, block.Thinking)
		case "tool_use":
			name, namespace := restoreResponsesToolName(block.Name, s.namespaces)
			item = s.stream.addItem("function_call", block.ID, name, namespace)
		}
		s.blocks[event.Index] = item

	case "content_block_delta":
		item := s.blocks[event.Index]
		switch event.Delta.Type {
		case "text_delta":
			s.stream.appendItemText(item, event.Delta.Text)
		case "thinking_delta":
			s.stream.appendItemText(item, event.Delta.Thinking)
		case "input_json_delta":
			s.stream.appendItemArgs(item, event.Delta.PartialJSON)
		}

	case "content_block_stop":
		s.stream.closeResponseItem(s.blocks[event.Index])

	case "message_delta":
		if event.Delta != nil {
			switch event.Delta.StopReason {
			case "max_tokens":
				s.stream.incomplete, s.stream.reason = true, "max_output_tokens"
			case "model_context_window_exceeded":
				s.stream.incomplete, s.stream.reason = true, "model_context_window_exceeded"
			case "refusal":
				// Messages supplies refusal only after text has already streamed.
				s.stream.incomplete, s.stream.reason = true, "content_filter"
			}
		}
		if event.Usage != nil {
			event.Usage.apply(&s.messageUsage)
			s.stream.usage = anthropicUsageToResponses(s.messageUsage)
		}

	case "message_stop":
		s.Finish()
		return true
	}
	return false
}

// Finish closes any open item and emits response.completed.
func (s *ResponsesStreamFromAnthropic) Finish() {
	s.stream.Finish()
}

// Fail closes any open item and emits response.failed instead of
// response.completed, so a mid-stream break reaches the client as a real
// failure it can retry rather than a truncated response that looks done.
func (s *ResponsesStreamFromAnthropic) Fail(message string) {
	s.stream.Fail(message)
}
