package translate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type openAIChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int              `json:"index"`
		FinishReason string           `json:"finish_reason"`
		Delta        openAIMessageOut `json:"delta"`
	} `json:"choices"`
	Usage *openAIUsage    `json:"usage"`
	Error json.RawMessage `json:"error"`
}

type chatAnthropicTool struct {
	index int
	id    string
	name  string
}

// StreamWriter turns an OpenAI chat-completions SSE stream into the Anthropic
// Messages event stream clients expect.
type StreamWriter struct {
	writer          io.Writer
	flush           func()
	model           string
	includeThinking bool

	started    bool
	finished   bool
	writeErr   error
	failure    error
	blockOpen  bool
	blockIndex int
	textIndex  int
	tools      map[int]*chatAnthropicTool
	toolIDs    responseCallIDs
	stopReason string
	usage      AnthropicUsage
}

func NewStreamWriter(writer io.Writer, flush func(), model string, includeThinking bool) *StreamWriter {
	return &StreamWriter{writer: writer, flush: flush, model: model, includeThinking: includeThinking,
		tools: map[int]*chatAnthropicTool{}, toolIDs: responseCallIDs{}}
}

// Consume leaves failures unfinished so the exchange can retry before output,
// or call Fail exactly once after output. Write errors are never hidden.
func (s *StreamWriter) Consume(body io.Reader) error {
	terminal := false
	err := scanSSE(body, func(payload []byte) (bool, error) {
		if string(payload) == "[DONE]" {
			if !s.started {
				return false, errors.New("upstream stream ended without emitting any events")
			}
			s.Finish()
			terminal = true
			return true, s.writeErr
		}
		var chunk openAIChunk
		if err := json.Unmarshal(payload, &chunk); err != nil {
			return false, fmt.Errorf("invalid upstream Chat event: %w", err)
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			s.failure = chatChunkError(chunk.Error)
			return false, s.failure
		}
		if chunk.Usage != nil {
			if err := validateChatUsage(*chunk.Usage); err != nil {
				return false, err
			}
		}
		if len(chunk.Choices) > 1 || (len(chunk.Choices) == 1 && chunk.Choices[0].Index != 0) {
			return false, errors.New("translated Chat stream requires one choice at index zero")
		}
		for _, choice := range chunk.Choices {
			if err := validateChatFinish(choice.FinishReason); err != nil {
				return false, err
			}
			if s.includeThinking && choice.Delta.ReasoningContent != "" {
				return false, errUnsignedThinking
			}
		}
		if err := s.consumeChunk(chunk); err != nil {
			return false, err
		}
		return false, s.writeErr
	})
	if err != nil {
		return err
	}
	if terminal {
		return s.writeErr
	}
	if s.started {
		return errors.New("upstream stream ended before the completion marker")
	}
	return errors.New("upstream stream ended without emitting any events")
}

func (s *StreamWriter) consumeChunk(chunk openAIChunk) error {
	// Validate repeated tool metadata before publishing any part of this event.
	for _, choice := range chunk.Choices {
		for _, call := range choice.Delta.ToolCalls {
			if call.Index < 0 {
				return errors.New("upstream tool index must be non-negative")
			}
			if tool := s.tools[call.Index]; tool != nil {
				if (call.ID != "" && tool.id != toolUseID(call.ID)) || (call.Function.Name != "" && tool.name != call.Function.Name) {
					return errors.New("upstream changed tool identity at an existing index")
				}
			} else if call.Function.Name == "" {
				return errors.New("upstream tool arguments require a named call before deltas")
			}
		}
	}
	s.ensureStarted(chunk.ID, chunk.Model)
	if chunk.Usage != nil {
		s.usage = responsesUsageToAnthropic(chatUsageToResponses(*chunk.Usage))
	}
	if len(chunk.Choices) == 0 {
		return s.writeErr
	}
	choice := chunk.Choices[0]
	text := choice.Delta.Content
	if choice.Delta.Refusal != nil {
		text += *choice.Delta.Refusal
		s.stopReason = "refusal"
	}
	if text != "" || choice.Delta.ContentPresent || choice.Delta.Refusal != nil {
		if !s.blockOpen {
			s.textIndex = s.blockIndex
			s.blockIndex++
			s.blockOpen = true
			s.emit("content_block_start", map[string]any{"type": "content_block_start", "index": s.textIndex,
				"content_block": map[string]any{"type": "text", "text": ""}})
		}
		s.emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": s.textIndex,
			"delta": map[string]any{"type": "text_delta", "text": text}})
	}
	for _, call := range choice.Delta.ToolCalls {
		if err := s.consumeToolCall(call); err != nil {
			return err
		}
	}
	if choice.FinishReason != "" && s.stopReason != "refusal" {
		s.stopReason = stopReason(choice.FinishReason)
	}
	return s.writeErr
}

// Tool state follows the upstream index, not whichever block most recently
// emitted a delta. Repeated identity metadata is continuation, not a new call.
func (s *StreamWriter) consumeToolCall(call openAIToolCall) error {
	tool := s.tools[call.Index]
	if tool == nil {
		id, err := s.toolIDs.resolve(call.ID, "toolu_")
		if err != nil {
			return err
		}
		tool = &chatAnthropicTool{index: s.blockIndex, id: id, name: call.Function.Name}
		s.blockIndex++
		s.tools[call.Index] = tool
		s.emit("content_block_start", map[string]any{"type": "content_block_start", "index": tool.index,
			"content_block": map[string]any{"type": "tool_use", "id": tool.id, "name": tool.name, "input": map[string]any{}}})
	}
	if call.Function.Arguments != "" {
		s.emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": tool.index,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": call.Function.Arguments}})
	}
	return s.writeErr
}

func (s *StreamWriter) ensureStarted(id, model string) {
	if s.started || s.finished {
		return
	}
	s.started = true
	if model != "" {
		s.model = model
	}
	s.emit("message_start", map[string]any{"type": "message_start", "message": AnthropicMessageOut{
		ID: messageID(id), Type: "message", Role: "assistant", Model: s.model, Content: []map[string]any{},
	}})
}

func (s *StreamWriter) closeBlocks() {
	for index := 0; index < s.blockIndex; index++ {
		s.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
	}
	s.blockIndex = 0
	s.blockOpen = false
}

func (s *StreamWriter) Finish() {
	if !s.started || s.finished {
		return
	}
	s.finished = true
	s.closeBlocks()
	reason := s.stopReason
	if reason == "" {
		reason = "end_turn"
	}
	s.emit("message_delta", map[string]any{"type": "message_delta",
		"delta": map[string]any{"stop_reason": reason, "stop_sequence": nil}, "usage": s.usage})
	s.emit("message_stop", map[string]any{"type": "message_stop"})
}

func (s *StreamWriter) Fail(message string) {
	if !s.started || s.finished {
		return
	}
	s.finished = true
	s.closeBlocks()
	errType := "api_error"
	var upstream *UpstreamError
	if errors.As(s.failure, &upstream) {
		errType = upstream.Type
	}
	s.emit("error", map[string]any{"type": "error", "error": map[string]any{"type": errType, "message": message}})
}

func (s *StreamWriter) emit(event string, payload any) {
	if s.writeErr == nil {
		s.writeErr = writeStreamFrame(s.writer, s.flush, event, payload)
	}
}
