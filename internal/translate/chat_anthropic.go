package translate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// This file routes OpenAI chat-completions clients onto backends that only
// expose the Anthropic Messages API: requests become Messages requests,
// non-streaming replies become chat-completions responses, and streaming
// replies become chat-completion chunks.

// defaultMaxTokens stands in when a client sends neither max_tokens nor
// max_completion_tokens; the Anthropic API requires the field.
const defaultMaxTokens = 4096

// anthropicRequest is the outbound Messages request shape built by
// ChatToAnthropic and ResponsesToAnthropic.
type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	Messages    []AnthropicMessage `json:"messages"`
	System      any                `json:"system,omitempty"`
	Temperature *float64           `json:"temperature,omitempty"`
	TopP        *float64           `json:"top_p,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
	Tools       []AnthropicTool    `json:"tools,omitempty"`
	ToolChoice  any                `json:"tool_choice,omitempty"`
}

// ChatToAnthropic renders an OpenAI chat-completions request as an Anthropic
// Messages request: leading system messages become the system prompt,
// assistant tool_calls become tool_use blocks, and "tool" messages become
// tool_result blocks grouped into one user turn.
func ChatToAnthropic(body []byte, model string) ([]byte, error) {
	if err := validateGenerationControls(body, "openai-chat"); err != nil {
		return nil, err
	}
	controls, err := translatedOpenAIControls(body, "openai-chat", "anthropic")
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

	converted := anthropicRequest{
		Model:       model,
		MaxTokens:   chat.MaxCompletionTokens,
		Temperature: chat.Temperature,
		TopP:        chat.TopP,
		Stream:      chat.Stream,
	}
	if converted.MaxTokens <= 0 {
		converted.MaxTokens = chat.MaxTokens
	}
	if converted.MaxTokens <= 0 {
		converted.MaxTokens = defaultMaxTokens
	}

	// Tool results queue here so consecutive "tool" messages share one user
	// turn, mirroring how Anthropic carries them inside a single message.
	var toolResults []anthropicBlock
	flushToolResults := func() {
		if len(toolResults) == 0 {
			return
		}
		converted.Messages = append(converted.Messages, AnthropicMessage{
			Role:    "user",
			Content: mustMarshal(toolResults),
		})
		toolResults = nil
	}

	conversationStarted := false
	for i, message := range chat.Messages {
		if isPromptRole(message.Role) {
			var err error
			converted.System, err = appendAnthropicInstruction(converted.System, message.Role, message.Content, "text", conversationStarted)
			if err != nil {
				return nil, fmt.Errorf("message %d: %w", i, err)
			}
			continue
		}
		conversationStarted = true
		switch message.Role {
		case "assistant":
			flushToolResults()
			var blocks []anthropicBlock
			if len(message.Content) != 0 && strings.TrimSpace(string(message.Content)) != "null" {
				var err error
				blocks, err = openAIContentToAnthropic(message.Content, false, false)
				if err != nil {
					return nil, fmt.Errorf("message %d: %w", i, err)
				}
			} else if len(message.ToolCalls) == 0 {
				return nil, fmt.Errorf("message %d: assistant requires content or tool calls", i)
			}
			for _, call := range message.ToolCalls {
				input, err := argumentsJSON(call.Function.Arguments)
				if err != nil {
					return nil, err
				}
				blocks = append(blocks, anthropicBlock{
					Type:  "tool_use",
					ID:    call.ID,
					Name:  call.Function.Name,
					Input: input,
				})
			}
			if len(blocks) == 0 {
				return nil, fmt.Errorf("message %d: empty content array cannot form a Messages turn", i)
			}
			converted.Messages = append(converted.Messages, AnthropicMessage{
				Role:    "assistant",
				Content: mustMarshal(blocks),
			})
		case "tool":
			content, err := openAIToolResultToAnthropic(message.Content, false)
			if err != nil {
				return nil, fmt.Errorf("message %d: tool result: %w", i, err)
			}
			toolResults = append(toolResults, anthropicBlock{
				Type:      "tool_result",
				ToolUseID: message.ToolCallID,
				Content:   content,
			})
		case "user":
			flushToolResults()
			blocks, err := openAIContentToAnthropic(message.Content, false, true)
			if err != nil {
				return nil, fmt.Errorf("message %d: %w", i, err)
			}
			if len(blocks) == 0 {
				return nil, fmt.Errorf("message %d: empty content array cannot form a Messages turn", i)
			}
			converted.Messages = append(converted.Messages, AnthropicMessage{
				Role:    "user",
				Content: mustMarshal(blocks),
			})
		default:
			return nil, fmt.Errorf("message %d: unsupported role %q", i, message.Role)
		}
	}
	flushToolResults()

	for _, tool := range chat.Tools {
		// strict is dropped: see the note in responses_anthropic.go — the
		// annotation has no Messages counterpart and false is already default.
		_ = tool.Function.Strict
		if tool.Function.Name == "" {
			continue
		}
		schema, err := sanitizeSchema(tool.Function.Parameters)
		if err != nil {
			return nil, err
		}
		converted.Tools = append(converted.Tools, AnthropicTool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: schema,
		})
	}
	converted.ToolChoice = chatToolChoiceToAnthropic(chat.ToolChoice)

	return marshalWithOpenAIControls(converted, controls)
}

// imageBlock converts an image reference into an Anthropic image block:
// data URLs split into base64 sources, remote URLs pass through.
func imageBlock(url string) *anthropicBlock {
	const prefix = "data:"
	if !strings.HasPrefix(url, prefix) {
		if url == "" {
			return nil
		}
		return &anthropicBlock{Type: "image", Source: &anthropicSource{Type: "url", URL: url}}
	}
	meta, data, ok := strings.Cut(strings.TrimPrefix(url, prefix), ";base64,")
	if !ok || data == "" {
		return nil
	}
	mediaType := meta
	if mediaType == "" {
		mediaType = "image/png"
	}
	return &anthropicBlock{Type: "image", Source: &anthropicSource{Type: "base64", MediaType: mediaType, Data: data}}
}

// chatToolChoiceToAnthropic maps an OpenAI tool_choice (raw JSON) to its
// Anthropic equivalent.
func chatToolChoiceToAnthropic(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var choice any
	if err := json.Unmarshal(raw, &choice); err != nil {
		return nil
	}
	return toolChoiceToAnthropic(choice)
}

// toolChoiceToAnthropic maps an already-decoded OpenAI/Responses tool_choice
// value to its Anthropic equivalent.
func toolChoiceToAnthropic(choice any) any {
	return toolChoiceToAnthropicWithNamespaces(choice, nil)
}

func toolChoiceToAnthropicWithNamespaces(choice any, namespaces map[string]responseNamespaceTool) any {
	switch choice := choice.(type) {
	case string:
		switch choice {
		case "auto":
			return map[string]any{"type": "auto"}
		case "none":
			return map[string]any{"type": "none"}
		case "required", "any":
			return map[string]any{"type": "any"}
		}
		return nil
	case map[string]any:
		if choice["type"] == "function" {
			// Chat-completions nests the function under "function";
			// Responses names it at the top level.
			name, _ := choice["name"].(string)
			if name == "" {
				if fn, ok := choice["function"].(map[string]any); ok {
					name, _ = fn["name"].(string)
				}
			}
			if namespace, _ := choice["namespace"].(string); namespace != "" {
				name = qualifyResponsesToolName(namespace, name)
			} else if tool, ok := namespaces[name]; ok {
				name = tool.Qualified
			}
			if name != "" {
				return map[string]any{"type": "tool", "name": name}
			}
		}
		return nil
	default:
		return nil
	}
}

// argumentsJSON validates without decoding numbers or inventing missing input.
func argumentsJSON(args string) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" || !json.Valid([]byte(trimmed)) || trimmed[0] != '{' {
		return nil, fmt.Errorf("tool arguments must be a complete JSON object")
	}
	return json.RawMessage(trimmed), nil
}

func mustMarshal(v any) json.RawMessage {
	encoded, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`[]`)
	}
	return encoded
}

// ---------------------------------------------------------------------------
// Response direction: Anthropic -> chat completions.

type anthropicResponseIn struct {
	ID         string            `json:"id"`
	Model      string            `json:"model"`
	Content    []anthropicBlock  `json:"content"`
	StopReason string            `json:"stop_reason"`
	Usage      AnthropicUsage    `json:"usage"`
	Error      *upstreamErrorObj `json:"error"`
}

// ChatFromAnthropic converts a non-streaming Anthropic Messages response into
// an OpenAI chat-completions response.
func ChatFromAnthropic(body []byte, model string) ([]byte, error) {
	var response anthropicResponseIn
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	if err := validateAnthropicUsage(response.Usage); err != nil {
		return nil, err
	}
	if err := checkUpstreamError(response.Error); err != nil {
		return nil, err
	}
	if err := validateAnthropicOutput(body); err != nil {
		return nil, err
	}

	if err := validateAnthropicStop(response.StopReason); err != nil {
		return nil, err
	}
	var content strings.Builder
	var reasoning strings.Builder
	var toolCalls []chatToolCallOut
	ids := responseCallIDs{}
	for _, block := range response.Content {
		switch block.Type {
		case "text":
			content.WriteString(block.Text)
		case "thinking":
			reasoning.WriteString(block.Thinking)
		case "tool_use":
			arguments, err := compactJSON(block.Input)
			if err != nil {
				return nil, err
			}
			id, err := ids.resolve(block.ID, "")
			if err != nil {
				return nil, err
			}
			toolCalls = append(toolCalls, chatToolCallOut{
				ID:       id,
				Type:     "function",
				Function: chatFuncOut{Name: block.Name, Arguments: arguments},
			})
		}
	}

	id := responseID(response.ID, "chatcmpl-")
	if response.Model != "" {
		model = response.Model
	}
	usage := responsesUsageToChat(anthropicUsageToResponses(response.Usage))

	message := chatMessageOut{Role: "assistant", Content: content.String(), ReasoningContent: reasoning.String(), ToolCalls: toolCalls}
	if response.StopReason == "refusal" {
		text := message.Content
		message.Refusal = &text
		message.Content = ""
	}
	return json.Marshal(chatResponseOut{
		ID:      id,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []chatChoiceOut{{
			Index:        0,
			Message:      message,
			FinishReason: anthropicStopToFinish(response.StopReason, len(toolCalls) > 0),
		}},
		Usage: usage,
	})
}

// anthropicStopToFinish maps an Anthropic stop_reason to an OpenAI
// finish_reason; tool presence covers upstreams that omit the reason.
func anthropicStopToFinish(stop string, hasTools bool) string {
	switch stop {
	case "max_tokens", "model_context_window_exceeded":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "refusal":
		return "content_filter"
	case "":
		if hasTools {
			return "tool_calls"
		}
		return "stop"
	default: // end_turn, stop_sequence, pause_turn, ...
		return "stop"
	}
}

// ---------------------------------------------------------------------------
// Stream direction: Anthropic SSE -> chat-completion chunks.

// anthropicEvent is one decoded Anthropic SSE payload.
type anthropicEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		ID    string         `json:"id"`
		Model string         `json:"model"`
		Usage AnthropicUsage `json:"usage"`
	} `json:"message"`
	ContentBlock *anthropicStreamContent `json:"content_block"`
	Delta        *anthropicStreamDelta   `json:"delta"`
	Usage        *anthropicUsageDelta    `json:"usage"`
	Error        *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type anthropicStreamContent struct {
	Type     string          `json:"type"`
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	Input    json.RawMessage `json:"input"`
}

type anthropicStreamDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Thinking    string `json:"thinking"`
	PartialJSON string `json:"partial_json"`
	StopReason  string `json:"stop_reason"`
}

// ChatStreamFromAnthropic converts an upstream Anthropic SSE stream into
// OpenAI chat-completions chunks terminated by [DONE].
type ChatStreamFromAnthropic struct {
	writer  io.Writer
	flush   func()
	model   string
	id      string
	created int64

	started      bool
	finished     bool
	blockKinds   map[int]string // anthropic block index -> content kind
	toolSlots    map[int]int    // anthropic block index -> client tool_calls index
	nextTool     int
	sawTool      bool
	stopReason   string
	usage        chatUsageOut
	messageUsage AnthropicUsage
	state        anthropicStreamState
	writeErr     error
	failure      error
}

// NewChatStreamFromAnthropic returns a translator writing chat chunks to
// writer, flushed after every chunk via flush.
func NewChatStreamFromAnthropic(writer io.Writer, flush func(), model string) *ChatStreamFromAnthropic {
	return &ChatStreamFromAnthropic{
		writer:     writer,
		flush:      flush,
		model:      model,
		id:         responseID("", "chatcmpl_"),
		created:    time.Now().Unix(),
		blockKinds: map[int]string{},
		toolSlots:  map[int]int{},
	}
}

// Consume reads the upstream SSE stream to completion. It returns an error
// when the upstream breaks mid-stream or ends without its message_stop, so
// the caller can end the stream with an explicit failure instead of one that
// hides the truncation.
func (c *ChatStreamFromAnthropic) Consume(body io.Reader) error {
	err := scanSSE(body, func(payload []byte) (bool, error) {
		var event anthropicEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return false, fmt.Errorf("invalid upstream Messages event: %w", err)
		}
		if err := event.validateUsage(c.messageUsage); err != nil {
			return false, err
		}
		frames, err := c.state.consume(event)
		if err != nil {
			c.failure = err
			return false, err
		}
		terminal := false
		for _, frame := range frames {
			terminal = c.consumeEvent(frame)
			if c.writeErr != nil {
				return false, c.writeErr
			}
		}
		return terminal, nil
	})
	if err != nil {
		return err
	}
	if c.finished {
		return c.writeErr
	}
	if c.started {
		return errors.New("upstream stream ended before the completion marker")
	}
	return errors.New("upstream stream ended without emitting any events")
}

// consumeEvent handles one upstream event and reports whether the stream is
// terminal.
func (c *ChatStreamFromAnthropic) consumeEvent(event anthropicEvent) bool {
	switch event.Type {
	case "message_start":
		if event.Message != nil {
			if event.Message.ID != "" {
				c.id = event.Message.ID
			}
			if event.Message.Model != "" {
				c.model = event.Message.Model
			}
			c.messageUsage = event.Message.Usage
			c.usage = responsesUsageToChat(anthropicUsageToResponses(c.messageUsage))
		}
		c.ensureStarted()

	case "content_block_start":
		if event.ContentBlock == nil {
			return false
		}
		c.blockKinds[event.Index] = event.ContentBlock.Type
		if event.ContentBlock.Type == "text" {
			text := event.ContentBlock.Text
			c.emitChunk(chatDeltaOut{Content: &text}, nil)
		} else if event.ContentBlock.Type == "thinking" && event.ContentBlock.Thinking != "" {
			c.emitChunk(chatDeltaOut{ReasoningContent: event.ContentBlock.Thinking}, nil)
		}
		if event.ContentBlock.Type == "tool_use" {
			slot := c.nextTool
			c.nextTool++
			c.toolSlots[event.Index] = slot
			c.sawTool = true
			c.emitChunk(chatDeltaOut{ToolCalls: []chatChunkToolCall{{
				Index:    slot,
				ID:       event.ContentBlock.ID,
				Type:     "function",
				Function: chatFuncOut{Name: event.ContentBlock.Name},
			}}}, nil)
		}

	case "content_block_delta":
		if event.Delta == nil {
			return false
		}
		switch event.Delta.Type {
		case "text_delta":
			text := event.Delta.Text
			c.emitChunk(chatDeltaOut{Content: &text}, nil)
		case "thinking_delta":
			c.emitChunk(chatDeltaOut{ReasoningContent: event.Delta.Thinking}, nil)
		case "input_json_delta":
			slot, ok := c.toolSlots[event.Index]
			if !ok {
				return false
			}
			c.emitChunk(chatDeltaOut{ToolCalls: []chatChunkToolCall{{
				Index:    slot,
				Function: chatFuncOut{Arguments: event.Delta.PartialJSON},
			}}}, nil)
		}

	case "content_block_stop":
		delete(c.blockKinds, event.Index)

	case "message_delta":
		if event.Delta != nil && event.Delta.StopReason != "" {
			c.stopReason = anthropicStopToFinish(event.Delta.StopReason, false)
		}
		if event.Usage != nil {
			event.Usage.apply(&c.messageUsage)
			c.usage = responsesUsageToChat(anthropicUsageToResponses(c.messageUsage))
		}

	case "message_stop":
		c.Finish()
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
		c.writeData(map[string]any{"error": map[string]any{
			"message": message,
			"type":    errType,
			"code":    nil,
		}})
		c.writeString("data: [DONE]\n\n")
		c.finished = true
		return true
	}
	return false
}

func (c *ChatStreamFromAnthropic) ensureStarted() {
	if c.started {
		return
	}
	c.started = true
	c.emitChunk(chatDeltaOut{Role: "assistant"}, nil)
}

func (c *ChatStreamFromAnthropic) emitChunk(delta chatDeltaOut, finishReason *string) {
	if !c.started {
		c.ensureStarted()
	}
	c.writeData(chatChunk{
		ID:      c.id,
		Object:  "chat.completion.chunk",
		Created: c.created,
		Model:   c.model,
		Choices: []chatChunkChoice{{Index: 0, Delta: delta, FinishReason: finishReason}},
	})
}

func (c *ChatStreamFromAnthropic) writeData(payload any) {
	if c.writeErr == nil {
		c.writeErr = writeStreamFrame(c.writer, c.flush, "", payload)
	}
}

func (c *ChatStreamFromAnthropic) writeString(text string) {
	if c.writeErr == nil {
		c.writeErr = writeStreamBytes(c.writer, c.flush, []byte(text))
	}
}

// Finish emits the terminating finish_reason chunk and [DONE]. It is safe to
// call more than once, and emits nothing when no content was streamed.
func (c *ChatStreamFromAnthropic) Finish() {
	if c.finished || !c.started {
		return
	}
	c.finished = true
	reason := c.stopReason
	if reason == "" {
		reason = anthropicStopToFinish("", c.sawTool)
	}
	finishReason := reason
	c.writeData(chatChunk{
		ID:      c.id,
		Object:  "chat.completion.chunk",
		Created: c.created,
		Model:   c.model,
		Choices: []chatChunkChoice{{Index: 0, FinishReason: &finishReason}},
		Usage:   &c.usage,
	})
	c.writeString("data: [DONE]\n\n")
}

// Fail aborts the stream with an error chunk instead of the terminating
// finish_reason chunk and [DONE], so a mid-stream break reaches the client
// as a real failure rather than a completed turn.
func (c *ChatStreamFromAnthropic) Fail(message string) {
	if c.finished {
		return
	}
	c.finished = true
	if !c.started {
		return
	}
	c.writeData(map[string]any{"error": map[string]any{
		"message": message,
		"type":    "api_error",
		"code":    nil,
	}})
}
