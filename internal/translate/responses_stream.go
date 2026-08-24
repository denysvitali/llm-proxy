package translate

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
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
	Delta        string               `json:"delta,omitempty"`
	Item         *responsesOutputItem `json:"item,omitempty"`
	Response     *responsesResponse   `json:"response,omitempty"`
	Error        *responsesErrorBody  `json:"error,omitempty"`
}

// responsesOutBlock tracks one open Anthropic content block and which
// upstream output index it came from.
type responsesOutBlock struct {
	index int
	kind  string // "text", "thinking", "tool_use", or "ignored"
	open  bool
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
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxStreamLine)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			s.Finish()
			return nil
		}
		var event responsesEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		if s.consumeEvent(event) {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		// The upstream broke mid-stream; leave the response unfinished so the
		// caller can end it with an explicit failure instead of a
		// "completed" envelope that hides the truncation.
		return err
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
			if !s.includeThinking {
				s.blocks[event.OutputIndex] = &responsesOutBlock{kind: "ignored"}
				return false
			}
			s.openUpstreamBlock(event.OutputIndex, "thinking", nil)
		}

	case "response.output_text.delta":
		block := s.openUpstreamBlock(event.OutputIndex, "text", nil)
		s.emitDelta(block.index, map[string]any{"type": "text_delta", "text": event.Delta})

	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		if !s.includeThinking {
			return false
		}
		block := s.openUpstreamBlock(event.OutputIndex, "thinking", nil)
		s.emitDelta(block.index, map[string]any{"type": "thinking_delta", "thinking": event.Delta})

	case "response.function_call_arguments.delta":
		block := s.blocks[event.OutputIndex]
		if block == nil || block.kind != "tool_use" || !block.open {
			return false
		}
		s.emitDelta(block.index, map[string]any{"type": "input_json_delta", "partial_json": event.Delta})

	case "response.output_item.done":
		s.closeUpstreamBlock(event.OutputIndex)

	case "response.completed", "response.incomplete":
		if event.Response != nil {
			s.captureUsage(event.Response.Usage)
			if event.Response.Status == "incomplete" {
				s.stopReason = "max_tokens"
			}
			s.ensureStarted(event.Response.ID, event.Response.Model)
		}
		s.Finish()
		return true

	case "response.failed":
		s.emitError("api_error", "upstream stream failed")
		s.finished = true
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
		s.emitError(errType, message)
		s.finished = true
		return true
	}
	return false
}

func (s *ResponsesStreamWriter) captureUsage(usage responsesUsage) {
	s.usage = AnthropicUsage{
		InputTokens:          int(usage.InputTokens),
		OutputTokens:         int(usage.OutputTokens),
		CacheReadInputTokens: int(usage.InputTokensDetails.CachedTokens),
	}
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
		case "thinking":
			start = map[string]any{"type": "thinking", "thinking": ""}
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
	if block.kind == "thinking" {
		s.emitDelta(block.index, map[string]any{"type": "signature_delta", "signature": thinkingSignature})
	}
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
	s.emitError("api_error", message)
}

func (s *ResponsesStreamWriter) emit(event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if _, err := fmt.Fprintf(s.writer, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return
	}
	if s.flush != nil {
		s.flush()
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
		id:        "chatcmpl-llm-proxy",
		created:   time.Now().Unix(),
		toolIndex: map[int]int{},
	}
}

// Consume reads the upstream Responses SSE stream to completion, emitting
// chat chunks as it goes.
func (c *ChatResponsesStreamWriter) Consume(body io.Reader) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxStreamLine)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			c.Finish()
			return nil
		}
		var event responsesEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		if c.consumeEvent(event) {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		// The upstream broke mid-stream; leave the stream unfinished so the
		// caller can end it with an explicit failure instead of a finished
		// turn that hides the truncation.
		return err
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
			usage := chatUsageOut{
				PromptTokens:     event.Response.Usage.InputTokens,
				CompletionTokens: event.Response.Usage.OutputTokens,
				TotalTokens:      event.Response.Usage.TotalTokens,
			}
			if usage.TotalTokens == 0 {
				usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			}
			c.usage = &usage
			if event.Response.Status == "incomplete" {
				c.stopReason = "length"
			}
			c.ensureStarted(event.Response.ID, event.Response.Model)
		}
		c.Finish()
		return true

	case "response.failed", "error":
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
		c.finished = true
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
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if _, err := fmt.Fprintf(c.writer, "data: %s\n\n", data); err != nil {
		return
	}
	if c.flush != nil {
		c.flush()
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
	c.writeData(map[string]any{"error": map[string]any{
		"message": message,
		"type":    "api_error",
		"code":    nil,
	}})
}

func (c *ChatResponsesStreamWriter) writeString(text string) {
	if _, err := io.WriteString(c.writer, text); err != nil {
		return
	}
	if c.flush != nil {
		c.flush()
	}
}
