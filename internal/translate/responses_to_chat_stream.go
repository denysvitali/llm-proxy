package translate

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// ResponsesStreamFromChat converts an upstream OpenAI chat-completions SSE
// stream into OpenAI Responses API events, so Responses clients (Codex) can
// consume chat-only backends. It mirrors the event subset the real API
// emits for plain text and function calls: response.created,
// response.output_item.added, response.output_text.delta,
// response.function_call_arguments.delta, response.output_item.done and
// response.completed.
type ResponsesStreamFromChatWriter struct {
	writer io.Writer
	flush  func()
	model  string
	id     string

	finished   bool
	createdID  bool
	outputIdx  int
	itemOpen   string // type of the currently open output item ("" = none)
	itemText   strings.Builder
	args       strings.Builder
	callID     string
	callName   string
	finishSeen string
	usage      *chatUsageOut
	doneItems  []map[string]any
}

func NewResponsesStreamFromChat(writer io.Writer, flush func(), model string) *ResponsesStreamFromChatWriter {
	return &ResponsesStreamFromChatWriter{
		writer: writer,
		flush:  flush,
		model:  model,
		id:     "resp_llm-proxy",
	}
}

// Consume reads the upstream chat-completions SSE stream to completion.
func (s *ResponsesStreamFromChatWriter) Consume(body io.Reader) error {
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
		var chunk chatChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if chunk.ID != "" {
			s.id = chunk.ID
		}
		if chunk.Model != "" {
			s.model = chunk.Model
		}
		s.consumeChunk(chunk)
	}
	s.Finish()
	return scanner.Err()
}

func (s *ResponsesStreamFromChatWriter) consumeChunk(chunk chatChunk) {
	for _, choice := range chunk.Choices {
		delta := choice.Delta
		if delta.ReasoningContent != "" && !s.openItem("reasoning") {
			return
		}
		if delta.Content != nil && *delta.Content != "" {
			if !s.openItem("message") {
				return
			}
			s.itemText.WriteString(*delta.Content)
			s.emit("response.output_text.delta", map[string]any{
				"type":         "response.output_text.delta",
				"item_id":      s.id + "_msg",
				"output_index": s.outputIdx,
				"delta":        *delta.Content,
			})
		}
		if delta.ReasoningContent != "" {
			s.emit("response.reasoning_text.delta", map[string]any{
				"type":         "response.reasoning_text.delta",
				"item_id":      s.id + "_reasoning",
				"output_index": s.outputIdx,
				"delta":        delta.ReasoningContent,
			})
		}
		for _, call := range delta.ToolCalls {
			if call.ID != "" || call.Function.Name != "" {
				// First fragment of a new tool call: open a function_call item.
				s.closeItem()
				s.callID = call.ID
				s.callName = call.Function.Name
				s.args.Reset()
				if !s.openItem("function_call") {
					return
				}
			}
			if call.Function.Arguments != "" {
				if s.itemOpen != "function_call" {
					continue
				}
				s.args.WriteString(call.Function.Arguments)
				s.emit("response.function_call_arguments.delta", map[string]any{
					"type":         "response.function_call_arguments.delta",
					"item_id":      s.callID,
					"output_index": s.outputIdx,
					"delta":        call.Function.Arguments,
				})
			}
		}
		if choice.FinishReason != nil {
			s.finishSeen = *choice.FinishReason
		}
	}
	if chunk.Usage != nil {
		s.usage = chunk.Usage
	}
}

// openItem emits response.created (once) and response.output_item.added for a
// new output item of the given type. It returns false if the stream already
// finished.
func (s *ResponsesStreamFromChatWriter) openItem(kind string) bool {
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
func (s *ResponsesStreamFromChatWriter) closeItem() {
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

func (s *ResponsesStreamFromChatWriter) ensureCreated() {
	if s.createdID {
		return
	}
	s.createdID = true
	s.emit("response.created", map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id": s.id, "model": s.model, "status": "in_progress",
		},
	})
}

// Finish closes any open item and emits response.completed.
func (s *ResponsesStreamFromChatWriter) Finish() {
	if s.finished {
		return
	}
	s.finished = true
	if !s.createdID {
		// Upstream produced nothing; still answer with a valid empty response.
		s.ensureCreated()
	}
	s.closeItem()

	status := "completed"
	incomplete := map[string]any(nil)
	if s.finishSeen == "length" {
		status = "incomplete"
		incomplete = map[string]any{"reason": "max_output_tokens"}
	}

	response := map[string]any{
		"id": s.id, "model": s.model, "status": status,
		"output": s.collectedOutput(), "usage": s.usagePayload(),
		"object": "response", "created_at": nowUnix(),
	}
	if incomplete != nil {
		response["incomplete_details"] = incomplete
	}
	s.emit("response.completed", map[string]any{
		"type": "response.completed", "response": response,
	})
}

// collectedOutput returns the finished items for the terminal
// response.completed payload.
func (s *ResponsesStreamFromChatWriter) collectedOutput() []map[string]any {
	if s.doneItems == nil {
		return []map[string]any{}
	}
	return s.doneItems
}

func (s *ResponsesStreamFromChatWriter) usagePayload() map[string]any {
	input, output := int64(0), int64(0)
	if s.usage != nil {
		input = s.usage.PromptTokens
		output = s.usage.CompletionTokens
	}
	total := input + output
	return map[string]any{
		"input_tokens": input, "output_tokens": output, "total_tokens": total,
	}
}

func (s *ResponsesStreamFromChatWriter) emit(event string, payload any) {
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
