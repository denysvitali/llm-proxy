package translate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type streamedResponseItem struct {
	index     int
	id        string
	kind      string
	callID    string
	name      string
	namespace string
	text      strings.Builder
	args      strings.Builder
	phase     streamedItemPhase
}

// streamedItemPhase is the lifecycle of one emitted Responses output item.
//
//	open  --closeResponseItem-->  closed  (emits the *.done events)
//
// Text and argument deltas are dropped once closed; closing twice is a no-op.
type streamedItemPhase int

const (
	streamedItemOpen streamedItemPhase = iota
	streamedItemClosed
)

func (p streamedItemPhase) String() string {
	switch p {
	case streamedItemOpen:
		return "open"
	case streamedItemClosed:
		return "closed"
	default:
		return fmt.Sprintf("streamedItemPhase(%d)", int(p))
	}
}

// accepts reports whether the item is present and still takes deltas.
func (item *streamedResponseItem) accepts() bool {
	return item != nil && item.phase == streamedItemOpen
}

// close applies the open -> closed transition and reports whether it happened.
func (item *streamedResponseItem) close() bool {
	if !item.accepts() {
		return false
	}
	item.phase = streamedItemClosed
	return true
}

// responsesItemStreamPhase is the lifecycle of the outbound Responses stream.
//
//	idle      --ensureCreated-->  streaming  (response.created + in_progress)
//	idle      --Finish-->         finished   (created first, then completed)
//	streaming --Finish-->         finished   (response.completed/incomplete)
//	streaming --Fail-->           finished   (response.failed)
//	idle      --Fail-->           idle       (nothing emitted)
//
// The phase is derived from the started/finished fields, which
// responses_anthropic.go reads directly; markCreated and markFinished are the
// only writers. Sequence numbers increase by one per emitted frame, in emit.
type responsesItemStreamPhase int

const (
	responsesItemStreamIdle responsesItemStreamPhase = iota
	responsesItemStreamStreaming
	responsesItemStreamFinished
)

func (p responsesItemStreamPhase) String() string {
	switch p {
	case responsesItemStreamIdle:
		return "idle"
	case responsesItemStreamStreaming:
		return "streaming"
	case responsesItemStreamFinished:
		return "finished"
	default:
		return fmt.Sprintf("responsesItemStreamPhase(%d)", int(p))
	}
}

// responsesItemStream owns stable identity, complete content lifecycles and
// terminal output. Multiple function calls may remain open while deltas interleave.
type responsesItemStream struct {
	writer  io.Writer
	flush   func()
	model   string
	id      string
	created int64
	request []byte

	started    bool
	finished   bool
	writeErr   error
	sequence   int // next sequence_number; only emit advances it
	incomplete bool
	reason     string
	usage      responsesUsage
	items      []*streamedResponseItem
	current    *streamedResponseItem
}

func (s *responsesItemStream) phase() responsesItemStreamPhase {
	switch {
	case s.finished:
		return responsesItemStreamFinished
	case s.started:
		return responsesItemStreamStreaming
	default:
		return responsesItemStreamIdle
	}
}

// markCreated applies idle -> streaming and reports whether it happened.
func (s *responsesItemStream) markCreated() bool {
	if s.phase() != responsesItemStreamIdle {
		return false
	}
	s.started = true
	return true
}

// markFinished applies streaming -> finished. Callers create the response
// first, so a finished stream has always started.
func (s *responsesItemStream) markFinished() {
	if s.phase() == responsesItemStreamStreaming {
		s.finished = true
	}
}

// accepting reports whether new items and deltas may still be emitted.
func (s *responsesItemStream) accepting() bool {
	return s.phase() != responsesItemStreamFinished && s.writeErr == nil
}

func (s *responsesItemStream) addItem(kind, callID, name, namespace string) *streamedResponseItem {
	if !s.accepting() {
		return nil
	}
	s.ensureCreated()
	item := &streamedResponseItem{index: len(s.items), id: fmt.Sprintf("%s_item_%d", s.id, len(s.items)),
		kind: kind, callID: callID, name: name, namespace: namespace}
	s.items = append(s.items, item)
	s.emit("response.output_item.added", map[string]any{"output_index": item.index, "item": s.itemPayload(item, false)})
	switch kind {
	case "message", "refusal":
		s.emit("response.content_part.added", map[string]any{"item_id": item.id, "output_index": item.index,
			"content_index": 0, "part": s.partPayload(item, "")})
	case "reasoning":
		s.emit("response.reasoning_part.added", map[string]any{"item_id": item.id, "output_index": item.index,
			"content_index": 0, "part": s.partPayload(item, "")})
	}
	return item
}

func (s *responsesItemStream) openItem(kind string) bool {
	if !s.accepting() {
		return false
	}
	if s.current.accepts() && s.current.kind == kind && kind != "function_call" {
		return true
	}
	s.closeItem()
	s.current = s.addItem(kind, "", "", "")
	return s.current != nil
}

func (s *responsesItemStream) closeItem() {
	s.closeResponseItem(s.current)
	s.current = nil
}

func (s *responsesItemStream) partPayload(item *streamedResponseItem, text string) map[string]any {
	switch item.kind {
	case "refusal":
		return map[string]any{"type": "refusal", "refusal": text}
	case "reasoning":
		return map[string]any{"type": "reasoning_text", "text": text}
	default:
		return map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
	}
}

func (s *responsesItemStream) itemPayload(item *streamedResponseItem, done bool) map[string]any {
	status := "in_progress"
	if done {
		status = "completed"
		if s.incomplete {
			status = "incomplete"
		}
	}
	switch item.kind {
	case "function_call":
		payload := map[string]any{"type": "function_call", "id": item.id, "call_id": item.callID,
			"name": item.name, "arguments": item.args.String(), "status": status}
		if item.namespace != "" {
			payload["namespace"] = item.namespace
		}
		return payload
	case "reasoning":
		payload := map[string]any{"type": "reasoning", "id": item.id, "summary": []any{}}
		if done {
			payload["content"] = []any{s.partPayload(item, item.text.String())}
		}
		return payload
	default:
		content := []any{}
		if done {
			content = append(content, s.partPayload(item, item.text.String()))
		}
		return map[string]any{"type": "message", "id": item.id, "role": "assistant", "status": status, "content": content}
	}
}

func (s *responsesItemStream) closeResponseItem(item *streamedResponseItem) {
	if !item.close() {
		return
	}
	payload := map[string]any{"item_id": item.id, "output_index": item.index}
	switch item.kind {
	case "function_call":
		payload["arguments"] = item.args.String()
		s.emit("response.function_call_arguments.done", payload)
	case "message", "refusal", "reasoning":
		payload["content_index"] = 0
		event := "response.output_text.done"
		payload["text"] = item.text.String()
		switch item.kind {
		case "refusal":
			event = "response.refusal.done"
			delete(payload, "text")
			payload["refusal"] = item.text.String()
		case "reasoning":
			event = "response.reasoning_text.done"
		}
		s.emit(event, payload)
		partEvent := "response.content_part.done"
		if item.kind == "reasoning" {
			partEvent = "response.reasoning_part.done"
		}
		s.emit(partEvent, map[string]any{"item_id": item.id, "output_index": item.index,
			"content_index": 0, "part": s.partPayload(item, item.text.String())})
	}
	s.emit("response.output_item.done", map[string]any{"output_index": item.index, "item": s.itemPayload(item, true)})
}

func (s *responsesItemStream) appendItemText(item *streamedResponseItem, delta string) {
	if !item.accepts() {
		return
	}
	item.text.WriteString(delta)
	event := "response.output_text.delta"
	switch item.kind {
	case "reasoning":
		event = "response.reasoning_text.delta"
	case "refusal":
		event = "response.refusal.delta"
	}
	s.emit(event, map[string]any{"item_id": item.id, "output_index": item.index, "content_index": 0, "delta": delta})
}

func (s *responsesItemStream) appendText(delta string)      { s.appendItemText(s.current, delta) }
func (s *responsesItemStream) appendReasoning(delta string) { s.appendItemText(s.current, delta) }

func (s *responsesItemStream) appendItemArgs(item *streamedResponseItem, delta string) {
	if !item.accepts() {
		return
	}
	item.args.WriteString(delta)
	s.emit("response.function_call_arguments.delta", map[string]any{"item_id": item.id, "output_index": item.index, "delta": delta})
}

func (s *responsesItemStream) envelope(status string) map[string]any {
	response, err := newResponsesResponse(s.id, s.model, s.created, s.request)
	if err != nil {
		s.writeErr = err
		return nil
	}
	var envelope map[string]any
	if err := json.Unmarshal(mustMarshal(response), &envelope); err != nil {
		s.writeErr = err
		return nil
	}
	envelope["status"] = status
	envelope["output"] = []any{}
	if status != "in_progress" {
		output := make([]any, 0, len(s.items))
		for _, item := range s.items {
			output = append(output, s.itemPayload(item, true))
		}
		envelope["output"] = output
		envelope["usage"] = s.usage
	}
	if s.incomplete {
		reason := s.reason
		if reason == "" {
			reason = "max_output_tokens"
		}
		envelope["incomplete_details"] = map[string]any{"reason": reason}
	}
	return envelope
}

func (s *responsesItemStream) ensureCreated() {
	if s.phase() != responsesItemStreamIdle {
		return
	}
	s.id = responseID(s.id, "resp_")
	if s.created == 0 {
		s.created = nowUnix()
	}
	s.markCreated()
	s.emit("response.created", map[string]any{"response": s.envelope("in_progress")})
	s.emit("response.in_progress", map[string]any{"response": s.envelope("in_progress")})
}

func (s *responsesItemStream) Finish() {
	if s.phase() == responsesItemStreamFinished {
		return
	}
	s.ensureCreated()
	for _, item := range s.items {
		s.closeResponseItem(item)
	}
	s.markFinished()
	status, event := "completed", "response.completed"
	if s.incomplete {
		status, event = "incomplete", "response.incomplete"
	}
	s.emit(event, map[string]any{"response": s.envelope(status)})
}

func (s *responsesItemStream) Fail(message string) {
	if s.phase() != responsesItemStreamStreaming {
		return
	}
	s.incomplete = true
	for _, item := range s.items {
		s.closeResponseItem(item)
	}
	s.markFinished()
	response := s.envelope("failed")
	response["incomplete_details"] = nil
	response["error"] = map[string]any{"code": "api_error", "message": message}
	s.emit("response.failed", map[string]any{"response": response})
}

func (s *responsesItemStream) emit(event string, payload map[string]any) {
	if s.writeErr != nil {
		return
	}
	payload["type"] = event
	payload["sequence_number"] = s.sequence
	s.sequence++
	s.writeErr = writeStreamFrame(s.writer, s.flush, event, payload)
}

// ResponsesStreamFromChatWriter maps upstream call indices to independent items.
type ResponsesStreamFromChatWriter struct {
	stream     responsesItemStream
	namespaces map[string]responseNamespaceTool
	tools      map[int][]*streamedResponseItem
	callIDs    responseCallIDs
	content    responsesFromChatContent
}

// responsesFromChatContent records what assistant content the upstream sent.
//
//	none       --empty content field-->      emptyOnly
//	none       --text content opened-->      text
//	emptyOnly  --text content opened-->      text
//	text       --any content-->              text
//
// Finish emits an empty message only in emptyOnly, matching the buffered path.
type responsesFromChatContent int

const (
	responsesFromChatContentNone responsesFromChatContent = iota
	responsesFromChatContentEmptyOnly
	responsesFromChatContentText
)

func (c responsesFromChatContent) String() string {
	switch c {
	case responsesFromChatContentNone:
		return "none"
	case responsesFromChatContentEmptyOnly:
		return "empty_only"
	case responsesFromChatContentText:
		return "text"
	default:
		return fmt.Sprintf("responsesFromChatContent(%d)", int(c))
	}
}

func (c *responsesFromChatContent) sawEmpty() {
	if *c == responsesFromChatContentNone {
		*c = responsesFromChatContentEmptyOnly
	}
}

func (c *responsesFromChatContent) sawText() { *c = responsesFromChatContentText }

func NewResponsesStreamFromChat(writer io.Writer, flush func(), model string) *ResponsesStreamFromChatWriter {
	return newResponsesStreamFromChat(writer, flush, model, nil)
}

func NewResponsesStreamFromChatForRequest(writer io.Writer, flush func(), model string, requestBody []byte) *ResponsesStreamFromChatWriter {
	namespaces, _ := responseNamespaceMap(requestBody)
	stream := newResponsesStreamFromChat(writer, flush, model, namespaces)
	stream.stream.request = requestBody
	return stream
}

func newResponsesStreamFromChat(writer io.Writer, flush func(), model string, namespaces map[string]responseNamespaceTool) *ResponsesStreamFromChatWriter {
	return &ResponsesStreamFromChatWriter{stream: responsesItemStream{writer: writer, flush: flush, model: model},
		namespaces: namespaces, tools: map[int][]*streamedResponseItem{}, callIDs: responseCallIDs{}}
}

func (s *ResponsesStreamFromChatWriter) Consume(body io.Reader) error {
	err := scanSSE(body, func(payload []byte) (bool, error) {
		if string(payload) == "[DONE]" {
			if s.stream.phase() == responsesItemStreamIdle {
				return false, errors.New("upstream stream ended without emitting any events")
			}
			s.Finish()
			return true, s.stream.writeErr
		}
		var chunk chatChunk
		if err := json.Unmarshal(payload, &chunk); err != nil {
			return false, fmt.Errorf("invalid upstream Chat event: %w", err)
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return false, chatChunkError(chunk.Error)
		}
		if s.stream.phase() == responsesItemStreamIdle {
			if chunk.ID != "" {
				s.stream.id = chunk.ID
			}
			if chunk.Model != "" {
				s.stream.model = chunk.Model
			}
			if chunk.Created != 0 {
				s.stream.created = chunk.Created
			}
		}
		if err := s.consumeChunk(chunk); err != nil {
			return false, err
		}
		return false, s.stream.writeErr
	})
	if err != nil {
		return err
	}
	switch s.stream.phase() {
	case responsesItemStreamFinished:
		return s.stream.writeErr
	case responsesItemStreamStreaming:
		return errors.New("upstream stream ended before the completion marker")
	}
	return errors.New("upstream stream ended without emitting any events")
}

// openTool returns the function_call item currently open for an upstream call
// index, or nil when none has started.
func (s *ResponsesStreamFromChatWriter) openTool(upstreamIndex int) *streamedResponseItem {
	items := s.tools[upstreamIndex]
	if len(items) == 0 {
		return nil
	}
	return items[len(items)-1]
}

// toolRestarts reports whether a delta at an existing index announces a
// different call instead of continuing it. Some upstreams (Open Design's AMR
// gateway among them) stream sequential calls on one reused index; that is a
// new call, not corruption. A name is required: a bare id change is a
// continuation, since the item must stay bound to the call it announced.
func toolRestarts(existingID, existingName, existingNamespace, callID, callName, callNamespace string) bool {
	if callName == "" {
		return false
	}
	if callID != "" && callID != existingID {
		return true
	}
	return callName != existingName || callNamespace != existingNamespace
}

func (s *ResponsesStreamFromChatWriter) consumeChunk(chunk chatChunk) error {
	if chunk.Usage != nil {
		if err := validateChatUsage(*chunk.Usage); err != nil {
			return err
		}
	}
	if len(chunk.Choices) > 1 || (len(chunk.Choices) == 1 && chunk.Choices[0].Index != 0) {
		return errors.New("translated Chat stream requires one choice at index zero")
	}
	for _, choice := range chunk.Choices {
		if choice.FinishReason != nil {
			if err := validateChatFinish(*choice.FinishReason); err != nil {
				return err
			}
		}
		for _, call := range choice.Delta.ToolCalls {
			if call.Index < 0 {
				return errors.New("upstream tool index must be non-negative")
			}
			name, namespace := restoreResponsesToolName(call.Function.Name, s.namespaces)
			tool := s.openTool(call.Index)
			if tool == nil {
				if call.Function.Name == "" {
					return errors.New("upstream tool arguments require a named call before deltas")
				}
				continue
			}
			if toolRestarts(tool.callID, tool.name, tool.namespace, call.ID, name, namespace) {
				continue
			}
			if call.ID != "" && tool.callID != call.ID {
				return errors.New("upstream changed tool identity at an existing index")
			}
			if call.Function.Name != "" && (tool.name != name || tool.namespace != namespace) {
				return errors.New("upstream changed tool identity at an existing index")
			}
		}
	}
	for _, choice := range chunk.Choices {
		delta := choice.Delta
		if delta.ReasoningContent != "" && s.stream.openItem("reasoning") {
			s.stream.appendReasoning(delta.ReasoningContent)
		}
		// Some upstreams (WorkBuddy) attach empty content/refusal strings to
		// every reasoning delta; opening items for them would interleave empty
		// messages between reasoning fragments.
		if delta.Content != nil && *delta.Content == "" {
			s.content.sawEmpty()
			s.stream.ensureCreated()
		} else if delta.Content != nil && s.stream.openItem("message") {
			s.content.sawText()
			s.stream.appendText(*delta.Content)
		}
		if delta.Refusal != nil && *delta.Refusal != "" && s.stream.openItem("refusal") {
			s.stream.appendText(*delta.Refusal)
		}
		for _, call := range delta.ToolCalls {
			tool := s.openTool(call.Index)
			if tool != nil {
				name, namespace := restoreResponsesToolName(call.Function.Name, s.namespaces)
				if toolRestarts(tool.callID, tool.name, tool.namespace, call.ID, name, namespace) {
					s.stream.closeResponseItem(tool)
					tool = nil
				}
			}
			if tool == nil {
				id, err := s.callIDs.resolve(call.ID, "")
				if err != nil {
					return err
				}
				name, namespace := restoreResponsesToolName(call.Function.Name, s.namespaces)
				s.stream.closeItem()
				tool = s.stream.addItem("function_call", id, name, namespace)
				s.tools[call.Index] = append(s.tools[call.Index], tool)
			}
			if call.Function.Arguments != "" {
				s.stream.appendItemArgs(tool, call.Function.Arguments)
			}
		}
		if choice.FinishReason != nil {
			switch *choice.FinishReason {
			case "length":
				s.stream.incomplete, s.stream.reason = true, "max_output_tokens"
			case "content_filter":
				s.stream.incomplete, s.stream.reason = true, "content_filter"
			}
		}
	}
	if chunk.Usage != nil {
		s.stream.usage = chatUsageToResponses(*chunk.Usage)
	}
	return s.stream.writeErr
}

func chatChunkError(raw json.RawMessage) error {
	var upstream UpstreamError
	if err := json.Unmarshal(raw, &upstream); err != nil {
		return &UpstreamError{Type: "api_error", Message: "upstream stream failed"}
	}
	if upstream.Type == "" {
		upstream.Type = "api_error"
	}
	if upstream.Message == "" {
		upstream.Message = "upstream stream failed"
	}
	return &upstream
}

func (s *ResponsesStreamFromChatWriter) Finish() {
	if s.content == responsesFromChatContentEmptyOnly && s.stream.openItem("message") {
		s.stream.appendText("")
	}
	s.stream.Finish()
}
func (s *ResponsesStreamFromChatWriter) Fail(message string) { s.stream.Fail(message) }
