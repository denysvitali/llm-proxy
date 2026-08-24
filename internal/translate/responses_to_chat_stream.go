package translate

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// This file routes OpenAI Responses API clients (e.g. Codex, which no longer
// supports wire_api="chat") onto chat-completions-only backends such as
// Venice: requests are rewritten from Responses input items to chat messages,
// non-streaming replies are converted back into a Responses response object,
// and streaming replies are re-emitted as Responses SSE events.
//
// The event sequence itself (response.created → output_item.added / deltas /
// output_item.done → response.completed) is built by responsesItemStream and
// shared with the Anthropic-upstream adapter in responses_anthropic.go.

// responsesItemStream assembles Responses API SSE events regardless of which
// upstream dialect feeds it. Adapters translate their upstream events into
// openItem/closeItem/append* calls; the stream owns sequencing, item IDs and
// the terminal response.completed payload.
type responsesItemStream struct {
	writer io.Writer
	flush  func()
	model  string
	id     string

	started    bool // response.created emitted
	finished   bool
	outputIdx  int
	itemOpen   string // type of the currently open output item ("" = none)
	itemText   strings.Builder
	args       strings.Builder
	callID     string
	callName   string
	incomplete bool
	usageIn    int64
	usageOut   int64
	doneItems  []map[string]any
}

// openItem emits response.created (once) and response.output_item.added for a
// new output item of the given type. It returns false if the stream already
// finished.
func (s *responsesItemStream) openItem(kind string) bool {
	if s.finished {
		return false
	}
	s.ensureCreated()
	if s.itemOpen == kind && kind != "function_call" {
		return true // keep appending to the open message/reasoning item
	}
	if s.itemOpen != "" {
		s.closeItem()
	}
	s.itemOpen = kind
	switch kind {
	case "message":
		s.emit("response.output_item.added", map[string]any{
			"type":         "response.output_item.added",
			"output_index": s.outputIdx,
			"item": map[string]any{
				"type": "message", "id": s.id + "_msg", "role": "assistant", "status": "in_progress",
				"content": []any{},
			},
		})
	case "reasoning":
		s.emit("response.output_item.added", map[string]any{
			"type":         "response.output_item.added",
			"output_index": s.outputIdx,
			"item": map[string]any{
				"type": "reasoning", "id": s.id + "_reasoning", "summary": []any{},
			},
		})
	case "function_call":
		args := "{}"
		if s.args.Len() > 0 {
			args = s.args.String()
		}
		s.emit("response.output_item.added", map[string]any{
			"type":         "response.output_item.added",
			"output_index": s.outputIdx,
			"item": map[string]any{
				"type": "function_call", "id": s.callID, "call_id": s.callID,
				"name": s.callName, "arguments": args, "status": "in_progress",
			},
		})
	}
	return true
}

// closeItem emits response.output_item.done for the currently open item.
func (s *responsesItemStream) closeItem() {
	if s.itemOpen == "" {
		return
	}
	var done map[string]any
	switch s.itemOpen {
	case "message":
		done = map[string]any{
			"type": "message", "id": s.id + "_msg", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": s.itemText.String()}},
		}
	case "reasoning":
		done = map[string]any{
			"type": "reasoning", "id": s.id + "_reasoning",
			"summary": []any{}, "content": []any{},
		}
	case "function_call":
		args := s.args.String()
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		done = map[string]any{
			"type": "function_call", "id": s.callID, "call_id": s.callID,
			"name": s.callName, "arguments": args, "status": "completed",
		}
	}
	s.emit("response.output_item.done", map[string]any{
		"type":         "response.output_item.done",
		"output_index": s.outputIdx,
		"item":         done,
	})
	s.doneItems = append(s.doneItems, done)
	s.itemOpen = ""
	s.outputIdx++
	s.itemText.Reset()
}

// appendText streams one text fragment into the open message item.
func (s *responsesItemStream) appendText(delta string) {
	s.itemText.WriteString(delta)
	s.emit("response.output_text.delta", map[string]any{
		"type":         "response.output_text.delta",
		"item_id":      s.id + "_msg",
		"output_index": s.outputIdx,
		"delta":        delta,
	})
}

// appendReasoning streams one reasoning fragment into the open reasoning item.
func (s *responsesItemStream) appendReasoning(delta string) {
	s.emit("response.reasoning_text.delta", map[string]any{
		"type":         "response.reasoning_text.delta",
		"item_id":      s.id + "_reasoning",
		"output_index": s.outputIdx,
		"delta":        delta,
	})
}

// appendArgs streams one function-arguments fragment into the open
// function_call item.
func (s *responsesItemStream) appendArgs(delta string) {
	s.args.WriteString(delta)
	s.emit("response.function_call_arguments.delta", map[string]any{
		"type":         "response.function_call_arguments.delta",
		"item_id":      s.callID,
		"output_index": s.outputIdx,
		"delta":        delta,
	})
}

// startToolCall closes whatever is open and prepares a fresh function_call
// item under the given call identity.
func (s *responsesItemStream) startToolCall(callID, name string) {
	s.closeItem()
	s.callID = callID
	s.callName = name
	s.args.Reset()
	s.openItem("function_call")
}

// setUsage records the final token counts reported upstream.
func (s *responsesItemStream) setUsage(in, out int64) {
	s.usageIn, s.usageOut = in, out
}

func (s *responsesItemStream) ensureCreated() {
	if s.started {
		return
	}
	s.started = true
	s.emit("response.created", map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id": s.id, "model": s.model, "status": "in_progress",
		},
	})
}

// Finish closes any open item and emits response.completed. It is safe to
// call more than once.
func (s *responsesItemStream) Finish() {
	if s.finished {
		return
	}
	s.finished = true
	if !s.started {
		// Upstream produced nothing; still answer with a valid empty response.
		s.ensureCreated()
	}
	s.closeItem()

	status := "completed"
	if s.incomplete {
		status = "incomplete"
	}
	response := map[string]any{
		"id": s.id, "model": s.model, "status": status,
		"output": s.collectedOutput(), "usage": s.usagePayload(),
		"object": "response", "created_at": nowUnix(),
	}
	if s.incomplete {
		response["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	}
	s.emit("response.completed", map[string]any{
		"type": "response.completed", "response": response,
	})
}

// Fail closes any open item and emits response.failed instead of
// response.completed, so a mid-stream break reaches the client as a real
// failure it can retry rather than a truncated response that looks done.
func (s *responsesItemStream) Fail(message string) {
	if s.finished {
		return
	}
	s.finished = true
	s.ensureCreated()
	s.closeItem()
	s.emit("response.failed", map[string]any{
		"type": "response.failed",
		"response": map[string]any{
			"id": s.id, "model": s.model, "status": "failed",
			"output": s.collectedOutput(), "usage": s.usagePayload(),
			"object": "response", "created_at": nowUnix(),
			"error": map[string]any{"code": "api_error", "message": message},
		},
	})
}

// collectedOutput returns the finished items for the terminal
// response.completed payload.
func (s *responsesItemStream) collectedOutput() []map[string]any {
	if s.doneItems == nil {
		return []map[string]any{}
	}
	return s.doneItems
}

func (s *responsesItemStream) usagePayload() map[string]any {
	total := s.usageIn + s.usageOut
	return map[string]any{
		"input_tokens": s.usageIn, "output_tokens": s.usageOut, "total_tokens": total,
	}
}

func (s *responsesItemStream) emit(event string, payload any) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = s.writer.Write([]byte("event: " + event + "\ndata: "))
	_, _ = s.writer.Write(encoded)
	_, _ = s.writer.Write([]byte("\n\n"))
	if s.flush != nil {
		s.flush()
	}
}

// ---------------------------------------------------------------------------
// Chat-completions upstream adapter.

// ResponsesStreamFromChat converts an upstream OpenAI chat-completions SSE
// stream into OpenAI Responses API events, so Responses clients (Codex) can
// consume chat-only backends. It mirrors the event subset the real API emits
// for plain text and function calls.
type ResponsesStreamFromChatWriter struct {
	stream responsesItemStream
}

func NewResponsesStreamFromChat(writer io.Writer, flush func(), model string) *ResponsesStreamFromChatWriter {
	return &ResponsesStreamFromChatWriter{stream: responsesItemStream{
		writer: writer,
		flush:  flush,
		model:  model,
		id:     "resp_llm-proxy",
	}}
}

// Consume reads the upstream chat-completions SSE stream to completion. It
// returns an error when the upstream breaks mid-stream or ends before its
// completion marker, leaving the response unfinished so the caller can end
// it with an explicit failure instead of one that hides the truncation.
func (s *ResponsesStreamFromChatWriter) Consume(body io.Reader) error {
	err := scanSSE(body, func(payload []byte) (bool, error) {
		if string(payload) == "[DONE]" {
			s.Finish()
			return true, nil
		}
		var chunk chatChunk
		if err := json.Unmarshal(payload, &chunk); err != nil {
			return false, nil
		}
		if chunk.ID != "" {
			s.stream.id = chunk.ID
		}
		if chunk.Model != "" {
			s.stream.model = chunk.Model
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			message := "upstream stream failed"
			var decoded struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(chunk.Error, &decoded) == nil && decoded.Message != "" {
				message = decoded.Message
			}
			s.stream.Fail(message)
			return true, nil
		}
		s.consumeChunk(chunk)
		return s.stream.finished, nil
	})
	if err != nil {
		return err
	}
	if s.stream.finished {
		return nil
	}
	if s.stream.started {
		return errors.New("upstream stream ended before the completion marker")
	}
	return errors.New("upstream stream ended without emitting any events")
}

func (s *ResponsesStreamFromChatWriter) consumeChunk(chunk chatChunk) {
	for _, choice := range chunk.Choices {
		delta := choice.Delta
		if delta.Content != nil && *delta.Content != "" {
			if !s.stream.openItem("message") {
				return
			}
			s.stream.appendText(*delta.Content)
		}
		if delta.ReasoningContent != "" {
			if !s.stream.openItem("reasoning") {
				return
			}
			s.stream.appendReasoning(delta.ReasoningContent)
		}
		for _, call := range delta.ToolCalls {
			if call.ID != "" || call.Function.Name != "" {
				// First fragment of a new tool call: open a function_call item.
				s.stream.startToolCall(call.ID, call.Function.Name)
			}
			if call.Function.Arguments != "" && s.stream.itemOpen == "function_call" {
				s.stream.appendArgs(call.Function.Arguments)
			}
		}
		if choice.FinishReason != nil && *choice.FinishReason == "length" {
			s.stream.incomplete = true
		}
	}
	if chunk.Usage != nil {
		s.stream.setUsage(chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens)
	}
}

// Finish closes any open item and emits response.completed.
func (s *ResponsesStreamFromChatWriter) Finish() {
	s.stream.Finish()
}

// Fail closes any open item and emits response.failed.
func (s *ResponsesStreamFromChatWriter) Fail(message string) {
	s.stream.Fail(message)
}
