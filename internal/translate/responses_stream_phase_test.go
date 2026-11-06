package translate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestResponsesWriterPhaseTransitions(t *testing.T) {
	cases := []struct {
		from          responsesWriterPhase
		start         bool
		afterStart    responsesWriterPhase
		finish        bool
		afterFinish   responsesWriterPhase
		started, done bool
	}{
		{responsesWriterIdle, true, responsesWriterStreaming, false, responsesWriterClosedEmpty, false, false},
		{responsesWriterStreaming, false, responsesWriterStreaming, true, responsesWriterClosed, true, false},
		{responsesWriterClosedEmpty, true, responsesWriterClosed, false, responsesWriterClosedEmpty, false, true},
		{responsesWriterClosed, false, responsesWriterClosed, false, responsesWriterClosed, true, true},
	}
	for _, tc := range cases {
		if tc.from.started() != tc.started || tc.from.finished() != tc.done {
			t.Errorf("%v: started=%v finished=%v", tc.from, tc.from.started(), tc.from.finished())
		}
		p := tc.from
		if got := p.start(); got != tc.start || p != tc.afterStart {
			t.Errorf("%v.start() = %v -> %v, want %v -> %v", tc.from, got, p, tc.start, tc.afterStart)
		}
		p = tc.from
		if got := p.finish(); got != tc.finish || p != tc.afterFinish {
			t.Errorf("%v.finish() = %v -> %v, want %v -> %v", tc.from, got, p, tc.finish, tc.afterFinish)
		}
	}
}

func TestResponsesStreamItemAndPartPhases(t *testing.T) {
	var s responsesStreamState
	mustConsume := func(event responsesEvent) []responsesEvent {
		t.Helper()
		frames, err := s.consume(event)
		if err != nil {
			t.Fatalf("%s: %v", event.Type, err)
		}
		return frames
	}
	mustConsume(responsesEvent{Type: "response.output_item.added", Item: &responsesOutputItem{Type: "function_call", Name: "f", CallID: "c"}})
	if got := s.items[0].phase; got != responsesItemAnnounced {
		t.Fatalf("after added: %v", got)
	}
	mustConsume(responsesEvent{Type: "response.function_call_arguments.delta", Delta: `{"a"`})
	if got := s.items[0].phase; got != responsesItemArgsStreaming {
		t.Fatalf("after delta: %v", got)
	}
	if frames := mustConsume(responsesEvent{Type: "response.function_call_arguments.done", Arguments: `{"a"`}); len(frames) != 0 {
		t.Fatalf("done after deltas replayed %v", frames)
	}
	if got := s.items[0].phase; got != responsesItemArgsDone {
		t.Fatalf("after done: %v", got)
	}
	if _, err := s.consume(responsesEvent{Type: "response.function_call_arguments.delta", Delta: "x"}); err == nil {
		t.Fatal("delta after arguments done was accepted")
	}
	mustConsume(responsesEvent{Type: "response.output_item.done", Item: &responsesOutputItem{Type: "function_call", Name: "f", CallID: "c"}})
	if got := s.items[0].phase; got != responsesItemClosed {
		t.Fatalf("after item done: %v", got)
	}

	// A text part completed by snapshot replays one delta and is then done.
	frames := mustConsume(responsesEvent{Type: "response.output_text.done", OutputIndex: 1, Text: "hi"})
	if len(frames) != 1 || frames[0].Type != "response.output_text.delta" || frames[0].Delta != "hi" {
		t.Fatalf("snapshot replay = %+v", frames)
	}
	if got := s.items[1].parts[0].phase; got != responsesPartDone {
		t.Fatalf("part after done: %v", got)
	}
	if _, err := s.consume(responsesEvent{Type: "response.output_text.delta", OutputIndex: 1, Delta: "x"}); err == nil {
		t.Fatal("text after part completion was accepted")
	}

	// The terminal event closes every unclosed item, replaying its done event.
	frames = mustConsume(responsesEvent{Type: "response.completed", Response: &responsesResponse{}})
	if len(frames) != 2 || frames[0].Type != "response.output_item.done" || frames[0].OutputIndex != 1 || frames[1].Type != "response.completed" {
		t.Fatalf("terminal frames = %+v", frames)
	}
	if got := s.items[1].phase; got != responsesItemClosed {
		t.Fatalf("after terminal: %v", got)
	}
}

func TestResponsesTerminalKinds(t *testing.T) {
	cases := []struct {
		status, details string
		want            responsesTerminalKind
		chat, chatTools string
	}{
		{"", "", responsesTerminalCompleted, "stop", "tool_calls"},
		{"completed", "", responsesTerminalCompleted, "stop", "tool_calls"},
		{"incomplete", `{"reason":"max_output_tokens"}`, responsesTerminalLength, "length", "length"},
		{"incomplete", `{"reason":"model_context_window_exceeded"}`, responsesTerminalLength, "length", "length"},
		{"incomplete", `{"reason":"content_filter"}`, responsesTerminalContentFilter, "content_filter", "content_filter"},
	}
	for _, tc := range cases {
		response := responsesResponse{Status: tc.status, IncompleteDetails: json.RawMessage(tc.details)}
		kind, err := responsesTerminal(response)
		if err != nil || kind != tc.want || kind.chatFinish(false) != tc.chat || kind.chatFinish(true) != tc.chatTools {
			t.Errorf("%s %s: kind=%v err=%v", tc.status, tc.details, kind, err)
		}
	}
	for _, response := range []responsesResponse{{Status: "in_progress"}, {Status: "incomplete", IncompleteDetails: json.RawMessage(`{"reason":"other"}`)}} {
		if _, err := responsesTerminal(response); err == nil {
			t.Errorf("%+v classified as terminal", response)
		}
	}
	if responsesTerminalLength.String() != "length" || responsesTerminalKind(7).String() != "responsesTerminalKind(7)" {
		t.Error("terminal kind strings")
	}
}

func TestResponsesFromChatContentAndItemPhases(t *testing.T) {
	var c responsesFromChatContent
	c.sawEmpty()
	if c != responsesFromChatContentEmptyOnly {
		t.Fatalf("none --empty--> %v", c)
	}
	c.sawText()
	c.sawEmpty()
	if c != responsesFromChatContentText {
		t.Fatalf("text --empty--> %v", c)
	}

	var out bytes.Buffer
	w := NewResponsesStreamFromChat(&out, nil, "m")
	if w.stream.phase() != responsesItemStreamIdle {
		t.Fatalf("initial stream phase %v", w.stream.phase())
	}
	w.Fail("ignored")
	if w.stream.phase() != responsesItemStreamIdle || out.Len() != 0 {
		t.Fatalf("Fail before creation: phase=%v output=%q", w.stream.phase(), out.String())
	}
	if !w.stream.openItem("message") || w.stream.phase() != responsesItemStreamStreaming {
		t.Fatalf("openItem phase %v", w.stream.phase())
	}
	item := w.stream.current
	w.Finish()
	if w.stream.phase() != responsesItemStreamFinished || item.phase != streamedItemClosed || item.accepts() {
		t.Fatalf("after Finish: stream=%v item=%v", w.stream.phase(), item.phase)
	}
	if w.stream.openItem("message") {
		t.Fatal("finished stream opened an item")
	}
	if !strings.Contains(out.String(), "response.completed") {
		t.Fatalf("missing completion: %s", out.String())
	}
	var nilItem *streamedResponseItem
	if nilItem.accepts() || nilItem.close() {
		t.Fatal("nil item accepted a transition")
	}
}
