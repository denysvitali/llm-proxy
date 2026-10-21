package translate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"
)

// Streaming half of the Responses translation: ResponsesStreamWriter turns an
// upstream Responses SSE stream into the Anthropic Messages event sequence,
// and ChatResponsesStreamWriter turns it into OpenAI chat-completions chunks.

// responsesEvent is one decoded Responses SSE payload.
type responsesEvent struct {
	Type         string               `json:"type"`
	OutputIndex  int                  `json:"output_index"`
	ContentIndex int                  `json:"content_index"`
	ItemID       string               `json:"item_id"`
	Arguments    string               `json:"arguments"`
	Text         string               `json:"text"`
	Refusal      string               `json:"refusal"`
	Delta        string               `json:"delta,omitempty"`
	Item         *responsesOutputItem `json:"item,omitempty"`
	Response     *responsesResponse   `json:"response,omitempty"`
	Error        *responsesErrorBody  `json:"error,omitempty"`
}

// responsesOutBlock tracks one open Anthropic content block and which
// upstream output index it came from.
type responsesOutBlock struct {
	index int
	kind  string // "text", "tool_use", or "ignored"
	open  bool
	// sentArgDelta records that input_json bytes were already forwarded for
	// this tool_use block (via function_call_arguments.delta or the item
	// fallback). Partial JSON fragments concatenate client-side, so the
	// complete arguments must never be re-emitted after any delta went out.
	sentArgDelta bool
}

// ResponsesStreamWriter converts an OpenAI Responses SSE stream into the
// Anthropic Messages event stream clients expect, mirroring StreamWriter.
type ResponsesStreamWriter struct {
	writer          io.Writer
	flush           func()
	model           string
	includeThinking bool

	started  bool
	finished bool
	failure  error
	writeErr error
	state    responsesStreamState

	blocks         map[int]*responsesOutBlock
	nextBlockIndex int
	stopReason     string
	usage          AnthropicUsage
}

func NewResponsesStreamWriter(writer io.Writer, flush func(), model string, includeThinking bool) *ResponsesStreamWriter {
	return &ResponsesStreamWriter{
		writer:          writer,
		flush:           flush,
		model:           model,
		includeThinking: includeThinking,
		blocks:          map[int]*responsesOutBlock{},
	}
}

// Consume reads the upstream SSE stream to completion, emitting Anthropic
// events as it goes.
func (s *ResponsesStreamWriter) Consume(body io.Reader) error {
	err := scanSSE(body, func(payload []byte) (bool, error) {
		if string(payload) == "[DONE]" {
			return false, errors.New("responses stream requires a terminal response event")
		}
		var event responsesEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return false, fmt.Errorf("invalid upstream Responses event: %w", err)
		}
		if event.Type == "response.failed" || event.Type == "error" {
			s.failure = ResponsesFailure(payload)
			return true, nil
		}
		if event.Response != nil {
			if err := validateResponsesUsage(event.Response.Usage); err != nil {
				return false, err
			}
		}
		if s.includeThinking {
			if (event.Type == "response.reasoning_summary_text.delta" || event.Type == "response.reasoning_text.delta") && event.Delta != "" {
				return false, errUnsignedThinking
			}
			if event.Item != nil && event.Item.Type == "reasoning" && reasoningText(event.Item.Summary, event.Item.Content) != "" {
				return false, errUnsignedThinking
			}
			if event.Response != nil {
				for _, item := range event.Response.Output {
					if item.Type == "reasoning" && reasoningText(item.Summary, item.Content) != "" {
						return false, errUnsignedThinking
					}
				}
			}
		}
		frames, err := s.state.consume(event)
		if err != nil {
			return false, err
		}
		terminal := false
		for _, frame := range frames {
			terminal = s.consumeEvent(frame)
			if s.writeErr != nil {
				return false, s.writeErr
			}
		}
		return terminal, nil
	})
	if err != nil {
		// The upstream broke mid-stream; leave the response unfinished so the
		// caller can end it with an explicit failure instead of a
		// "completed" envelope that hides the truncation.
		return err
	}
	if s.failure != nil {
		return s.failure
	}
	if s.finished {
		return s.writeErr
	}
	if s.started {
		return errors.New("upstream stream ended without completing the response")
	}
	return errors.New("upstream ended the stream without emitting any events")
}

// consumeEvent handles one upstream event and reports whether the stream is
// terminal (nothing further should be consumed).
func (s *ResponsesStreamWriter) consumeEvent(event responsesEvent) bool {
	switch event.Type {
	case "response.created", "response.in_progress":
		if event.Response != nil {
			s.ensureStarted(event.Response.ID, event.Response.Model)
		}

	case "response.output_item.added":
		if event.Item == nil {
			return false
		}
		switch event.Item.Type {
		case "message":
			s.openUpstreamBlock(event.OutputIndex, "text", nil)
		case "function_call":
			s.openUpstreamBlock(event.OutputIndex, "tool_use", map[string]any{
				"type":  "tool_use",
				"id":    event.Item.CallID,
				"name":  event.Item.Name,
				"input": map[string]any{},
			})
			s.stopReason = "tool_use"
		case "reasoning":
			s.blocks[event.OutputIndex] = &responsesOutBlock{kind: "ignored"}
		}

	case "response.output_text.delta", "response.refusal.delta":
		block := s.openUpstreamBlock(event.OutputIndex, "text", nil)
		if event.Type == "response.refusal.delta" {
			s.stopReason = "refusal"
		}
		s.emitDelta(block.index, map[string]any{"type": "text_delta", "text": event.Delta})

	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		return false

	case "response.function_call_arguments.delta":
		block := s.blocks[event.OutputIndex]
		if block == nil || block.kind != "tool_use" || !block.open {
			return false
		}
		s.emitDelta(block.index, map[string]any{"type": "input_json_delta", "partial_json": event.Delta})
		block.sentArgDelta = true

	case "response.output_item.done":
		s.closeUpstreamBlock(event.OutputIndex)

	case "response.completed", "response.incomplete":
		if event.Response != nil {
			s.captureUsage(event.Response.Usage)
			finish, _ := responsesFinish(*event.Response, s.stopReason == "tool_use")
			switch finish {
			case "length":
				s.stopReason = "max_tokens"
			case "content_filter":
				s.stopReason = "refusal"
			}
			s.ensureStarted(event.Response.ID, event.Response.Model)
		}
		s.Finish()
		return true

	}
	return false
}

func (s *ResponsesStreamWriter) captureUsage(usage responsesUsage) {
	s.usage = responsesUsageToAnthropic(usage)
}

// openUpstreamBlock returns the Anthropic block bound to an upstream output
// index, opening a new content block of the given kind on first sight. The
// lazy path covers upstreams that send deltas without output_item.added;
// start overrides the content_block_start payload when non-nil (used for
// tool_use blocks carrying their id and name).
func (s *ResponsesStreamWriter) openUpstreamBlock(outputIndex int, kind string, start map[string]any) *responsesOutBlock {
	if block, ok := s.blocks[outputIndex]; ok && block.kind != "ignored" {
		return block
	}
	block := &responsesOutBlock{index: s.nextBlockIndex, kind: kind, open: true}
	s.nextBlockIndex++
	s.blocks[outputIndex] = block

	if start == nil {
		switch kind {
		case "text":
			start = map[string]any{"type": "text", "text": ""}
		default:
			start = map[string]any{"type": kind}
		}
	}
	s.ensureStarted("", "")
	s.emit("content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         block.index,
		"content_block": start,
	})
	return block
}

func (s *ResponsesStreamWriter) closeUpstreamBlock(outputIndex int) {
	if block, ok := s.blocks[outputIndex]; ok {
		s.closeBlock(block)
	}
}

func (s *ResponsesStreamWriter) closeBlock(block *responsesOutBlock) {
	if !block.open {
		return
	}
	block.open = false
	s.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": block.index})
}

func (s *ResponsesStreamWriter) emitDelta(index int, delta map[string]any) {
	s.emit("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": index,
		"delta": delta,
	})
}

func (s *ResponsesStreamWriter) emitError(errType, message string) {
	s.emit("error", map[string]any{
		"type":  "error",
		"error": map[string]any{"type": errType, "message": message},
	})
}

func (s *ResponsesStreamWriter) ensureStarted(id, model string) {
	if s.started {
		return
	}
	s.started = true
	if model != "" {
		s.model = model
	}
	s.emit("message_start", map[string]any{
		"type": "message_start",
		"message": AnthropicMessageOut{
			ID:      messageID(id),
			Type:    "message",
			Role:    "assistant",
			Model:   s.model,
			Content: []map[string]any{},
		},
	})
}

// closeOpenBlocks closes every still-open content block in emission order.
func (s *ResponsesStreamWriter) closeOpenBlocks() {
	open := make([]*responsesOutBlock, 0, len(s.blocks))
	for _, block := range s.blocks {
		if block.open {
			open = append(open, block)
		}
	}
	sort.Slice(open, func(i, j int) bool { return open[i].index < open[j].index })
	for _, block := range open {
		s.closeBlock(block)
	}
}

// Finish closes any dangling content block and ends the message. It is safe
// to call more than once, and is a no-op when nothing was ever emitted.
func (s *ResponsesStreamWriter) Finish() {
	if s.finished {
		return
	}
	s.finished = true
	if !s.started {
		return
	}

	s.closeOpenBlocks()

	reason := s.stopReason
	if reason == "" {
		reason = "end_turn"
	}
	s.emit("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": reason, "stop_sequence": nil},
		"usage": s.usage,
	})
	s.emit("message_stop", map[string]any{"type": "message_stop"})
}

// Fail aborts the stream with an explicit Anthropic error event instead of a
// normal completion, so a mid-stream break reaches the client as a real
// failure it can retry rather than a truncated answer that looks finished.
func (s *ResponsesStreamWriter) Fail(message string) {
	if s.finished {
		return
	}
	s.finished = true
	if !s.started {
		return
	}
	s.closeOpenBlocks()
	errType := "api_error"
	var upstream *UpstreamError
	if errors.As(s.failure, &upstream) {
		errType = upstream.Type
	}
	s.emitError(errType, message)
}

func (s *ResponsesStreamWriter) emit(event string, payload any) {
	if s.writeErr == nil {
		s.writeErr = writeStreamFrame(s.writer, s.flush, event, payload)
	}
}

// ---------------------------------------------------------------------------
// Chat-completions chunk stream.

type chatChunk struct {
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Created int64             `json:"created"`
	Model   string            `json:"model"`
	Choices []chatChunkChoice `json:"choices"`
	Usage   *chatUsageOut     `json:"usage,omitempty"`
	Error   json.RawMessage   `json:"error,omitempty"`
}

type chatChunkChoice struct {
	Index        int          `json:"index"`
	Delta        chatDeltaOut `json:"delta"`
	FinishReason *string      `json:"finish_reason"`
}

type chatDeltaOut struct {
	Role             string              `json:"role,omitempty"`
	Content          *string             `json:"content,omitempty"`
	Refusal          *string             `json:"refusal,omitempty"`
	ReasoningContent string              `json:"reasoning_content,omitempty"`
	ToolCalls        []chatChunkToolCall `json:"tool_calls,omitempty"`
}

type chatChunkToolCall struct {
	Index    int         `json:"index"`
	ID       string      `json:"id,omitempty"`
	Type     string      `json:"type,omitempty"`
	Function chatFuncOut `json:"function"`
}

// ChatResponsesStreamWriter converts an OpenAI Responses SSE stream into
// OpenAI chat-completions chunks terminated by [DONE].
type ChatResponsesStreamWriter struct {
	writer  io.Writer
	flush   func()
	model   string
	id      string
	created int64

	started    bool
	finished   bool
	failure    error
	writeErr   error
	state      responsesStreamState
	sawTool    bool
	stopReason string
	usage      *chatUsageOut
	toolIndex  map[int]int
	nextTool   int
}

func ChatStreamFromResponses(writer io.Writer, flush func(), model string) *ChatResponsesStreamWriter {
	return &ChatResponsesStreamWriter{
		writer:    writer,
		flush:     flush,
		model:     model,
		id:        responseID("", "chatcmpl_"),
		created:   time.Now().Unix(),
		toolIndex: map[int]int{},
	}
}

// Consume reads the upstream Responses SSE stream to completion, emitting
// chat chunks as it goes.
func (c *ChatResponsesStreamWriter) Consume(body io.Reader) error {
	err := scanSSE(body, func(payload []byte) (bool, error) {
		if string(payload) == "[DONE]" {
			return false, errors.New("responses stream requires a terminal response event")
		}
		var event responsesEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return false, fmt.Errorf("invalid upstream Responses event: %w", err)
		}
		if event.Type == "response.failed" || event.Type == "error" {
			c.failure = ResponsesFailure(payload)
			return true, nil
		}
		if event.Response != nil {
			if err := validateResponsesUsage(event.Response.Usage); err != nil {
				return false, err
			}
		}
		frames, err := c.state.consume(event)
		if err != nil {
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
		// The upstream broke mid-stream; leave the stream unfinished so the
		// caller can end it with an explicit failure instead of a finished
		// turn that hides the truncation.
		return err
	}
	if c.failure != nil {
		return c.failure
	}
	if c.finished {
		return c.writeErr
	}
	if c.started {
		return errors.New("upstream stream ended before the completion marker")
	}
	return errors.New("upstream ended the stream without emitting any events")
}

func (c *ChatResponsesStreamWriter) consumeEvent(event responsesEvent) bool {
	switch event.Type {
	case "response.created", "response.in_progress":
		if event.Response != nil {
			c.ensureStarted(event.Response.ID, event.Response.Model)
		}

	case "response.output_item.added":
		if event.Item == nil || event.Item.Type != "function_call" {
			return false
		}
		c.ensureStarted("", "")
		index := c.toolFor(event.OutputIndex)
		c.sawTool = true
		c.emitChunk(chatDeltaOut{ToolCalls: []chatChunkToolCall{{
			Index:    index,
			ID:       event.Item.CallID,
			Type:     "function",
			Function: chatFuncOut{Name: event.Item.Name},
		}}}, nil)

	case "response.output_text.delta":
		text := event.Delta
		c.emitChunk(chatDeltaOut{Content: &text}, nil)

	case "response.refusal.delta":
		text := event.Delta
		c.emitChunk(chatDeltaOut{Refusal: &text}, nil)

	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		c.emitChunk(chatDeltaOut{ReasoningContent: event.Delta}, nil)

	case "response.function_call_arguments.delta":
		index := c.toolFor(event.OutputIndex)
		c.sawTool = true
		c.emitChunk(chatDeltaOut{ToolCalls: []chatChunkToolCall{{
			Index:    index,
			Function: chatFuncOut{Arguments: event.Delta},
		}}}, nil)

	case "response.completed", "response.incomplete":
		if event.Response != nil {
			usage := responsesUsageToChat(event.Response.Usage)
			c.usage = &usage
			c.stopReason, _ = responsesFinish(*event.Response, c.sawTool)
			c.ensureStarted(event.Response.ID, event.Response.Model)
		}
		c.Finish()
		return true

	}
	return false
}

// toolFor maps an upstream output index to a monotonically increasing
// tool_calls array index for the client.
func (c *ChatResponsesStreamWriter) toolFor(outputIndex int) int {
	if index, ok := c.toolIndex[outputIndex]; ok {
		return index
	}
	index := c.nextTool
	c.nextTool++
	c.toolIndex[outputIndex] = index
	return index
}

func (c *ChatResponsesStreamWriter) ensureStarted(id, model string) {
	if c.started {
		return
	}
	c.started = true
	if id != "" {
		c.id = id
	}
	if model != "" {
		c.model = model
	}
	c.emitChunk(chatDeltaOut{Role: "assistant"}, nil)
}

func (c *ChatResponsesStreamWriter) emitChunk(delta chatDeltaOut, finishReason *string) {
	if !c.started {
		c.ensureStarted("", "")
	}
	c.writeData(chatChunk{
		ID:      c.id,
		Object:  "chat.completion.chunk",
		Created: c.created,
		Model:   c.model,
		Choices: []chatChunkChoice{{Index: 0, Delta: delta, FinishReason: finishReason}},
	})
}

func (c *ChatResponsesStreamWriter) writeData(payload any) {
	if c.writeErr == nil {
		c.writeErr = writeStreamFrame(c.writer, c.flush, "", payload)
	}
}

// Finish emits the terminating finish_reason chunk and [DONE]. It is safe to
// call more than once, and emits nothing when no content was streamed.
func (c *ChatResponsesStreamWriter) Finish() {
	if c.finished {
		return
	}
	c.finished = true
	if !c.started {
		return
	}

	reason := c.stopReason
	if reason == "" {
		if c.sawTool {
			reason = "tool_calls"
		} else {
			reason = "stop"
		}
	}
	finishReason := reason
	chunk := chatChunk{
		ID:      c.id,
		Object:  "chat.completion.chunk",
		Created: c.created,
		Model:   c.model,
		Choices: []chatChunkChoice{{Index: 0, FinishReason: &finishReason}},
		Usage:   c.usage,
	}
	c.writeData(chunk)
	c.writeString("data: [DONE]\n\n")
}

// Fail aborts the stream with an error chunk instead of the terminating
// finish_reason chunk and [DONE], so a mid-stream break reaches the client
// as a real failure rather than a completed turn.
func (c *ChatResponsesStreamWriter) Fail(message string) {
	if c.finished {
		return
	}
	c.finished = true
	if !c.started {
		return
	}
	errType := "api_error"
	var code any
	var upstream *UpstreamError
	if errors.As(c.failure, &upstream) {
		errType = upstream.Type
		code = upstream.Code
	}
	c.writeData(map[string]any{"error": map[string]any{
		"message": message,
		"type":    errType,
		"code":    code,
	}})
}

func (c *ChatResponsesStreamWriter) writeString(text string) {
	if c.writeErr == nil {
		c.writeErr = writeStreamBytes(c.writer, c.flush, []byte(text))
	}
}
