package translate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestAnthropicWriterPhaseStrings(t *testing.T) {
	for _, tc := range []struct {
		got, want string
	}{
		{chatToAnthropicPending.String(), "pending"},
		{chatToAnthropicOpen.String(), "open"},
		{chatToAnthropicFinished.String(), "finished"},
		{chatToAnthropicFailed.String(), "failed"},
		{chatToAnthropicPhase(9).String(), "chatToAnthropicPhase(9)"},
		{chatToAnthropicTextNone.String(), "none"},
		{chatToAnthropicTextOpen.String(), "open"},
		{anthropicToChatPending.String(), "pending"},
		{anthropicToChatStreaming.String(), "streaming"},
		{anthropicToChatFinished.String(), "finished"},
		{anthropicToChatFailed.String(), "failed"},
		{anthropicStreamAwaitingStart.String(), "awaiting_message_start"},
		{anthropicStreamStreaming.String(), "streaming"},
		{anthropicStreamStopped.String(), "stopped"},
		{anthropicStreamBlockOpen.String(), "open"},
		{anthropicStreamBlockOpenArgs.String(), "open_args"},
		{anthropicStreamBlockClosed.String(), "closed"},
		{toolHistoryUndeclared.String(), "undeclared"},
		{toolHistoryDeclared.String(), "declared"},
		{toolHistoryResolved.String(), "resolved"},
	} {
		if tc.got != tc.want {
			t.Errorf("String() = %q, want %q", tc.got, tc.want)
		}
	}
}

func TestToolHistoryCallTransitions(t *testing.T) {
	var h toolHistory
	if got := h.state("a"); got != toolHistoryUndeclared {
		t.Fatalf("initial state = %v", got)
	}
	if err := h.result("a", "function"); err == nil {
		t.Fatal("accepted a result for an undeclared call")
	}
	if err := h.call("a", "function"); err != nil || h.state("a") != toolHistoryDeclared {
		t.Fatalf("call: err=%v state=%v", err, h.state("a"))
	}
	if err := h.result("a", "custom_tool_call"); err == nil || h.state("a") != toolHistoryDeclared {
		t.Fatalf("mismatched kind: err=%v state=%v", err, h.state("a"))
	}
	if err := h.result("a", "function"); err != nil || h.state("a") != toolHistoryResolved {
		t.Fatalf("result: err=%v state=%v", err, h.state("a"))
	}
	if err := h.call("a", "function"); err == nil || !strings.Contains(err.Error(), "duplicate tool call") {
		t.Fatalf("redeclare resolved call: %v", err)
	}
	if err := h.result("a", "function"); err == nil || !strings.Contains(err.Error(), "duplicate tool result") {
		t.Fatalf("second result: %v", err)
	}
}

func TestAnthropicStreamStatePhases(t *testing.T) {
	var s anthropicStreamState
	if _, err := s.consume(anthropicEvent{Type: "message_stop"}); err == nil || err.Error() != "upstream event requires message_start" {
		t.Fatalf("bare message_stop error = %v", err)
	}
	if _, err := s.consume(anthropicEvent{Type: "ping"}); err != nil || s.phase != anthropicStreamAwaitingStart {
		t.Fatalf("ping: err=%v phase=%v", err, s.phase)
	}
	if _, err := s.consume(phaseTestEvent(t, `{"type":"message_start","message":{}}`)); err != nil || s.phase != anthropicStreamStreaming {
		t.Fatalf("message_start: err=%v phase=%v", err, s.phase)
	}
	if _, err := s.consume(phaseTestEvent(t, `{"type":"message_start","message":{}}`)); err == nil || err.Error() != "upstream requires one message_start" {
		t.Fatalf("second message_start: %v", err)
	}
	start := anthropicEvent{Type: "content_block_start", ContentBlock: &anthropicStreamContent{Type: "tool_use", ID: "call_1", Name: "f", Input: []byte(`{"a":1}`)}}
	if _, err := s.consume(start); err != nil || s.blocks[0].phase != anthropicStreamBlockOpen {
		t.Fatalf("tool start: err=%v", err)
	}
	frames, err := s.consume(anthropicEvent{Type: "content_block_stop"})
	if err != nil || len(frames) != 2 || frames[0].Delta == nil || frames[0].Delta.PartialJSON != `{"a":1}` || s.blocks[0].phase != anthropicStreamBlockClosed {
		t.Fatalf("argless close: frames=%+v err=%v", frames, err)
	}
	if frames, err := s.consume(anthropicEvent{Type: "content_block_stop"}); err != nil || frames != nil {
		t.Fatalf("repeated stop: frames=%+v err=%v", frames, err)
	}
	start.Index = 1
	if _, err := s.consume(start); err == nil {
		t.Fatal("accepted a reused tool id")
	}
	start.ContentBlock.ID = "call_2"
	if _, err := s.consume(start); err != nil {
		t.Fatal(err)
	}
	if _, err := s.consume(anthropicEvent{Type: "content_block_delta", Index: 1, Delta: &anthropicStreamDelta{Type: "input_json_delta", PartialJSON: "{}"}}); err != nil || s.blocks[1].phase != anthropicStreamBlockOpenArgs {
		t.Fatalf("args delta: err=%v phase=%v", err, s.blocks[1].phase)
	}
	if frames, err := s.consume(anthropicEvent{Type: "content_block_stop", Index: 1}); err != nil || len(frames) != 1 {
		t.Fatalf("close with args: frames=%+v err=%v", frames, err)
	}
	if _, err := s.consume(anthropicEvent{Type: "message_delta", Delta: &anthropicStreamDelta{StopReason: "end_turn"}}); err != nil || s.phase != anthropicStreamStopped {
		t.Fatalf("stop_reason: err=%v phase=%v", err, s.phase)
	}
	if _, err := s.consume(anthropicEvent{Type: "content_block_start", Index: 2, ContentBlock: &anthropicStreamContent{Type: "text"}}); err == nil || err.Error() != "upstream content arrived after stop_reason" {
		t.Fatalf("content after stop: %v", err)
	}
}

func TestChatToAnthropicTerminalOnlyFromOpen(t *testing.T) {
	var out bytes.Buffer
	s := NewStreamWriter(&out, nil, "m", false)
	s.Finish()
	s.Fail("boom")
	if out.Len() != 0 || s.phase != chatToAnthropicPending {
		t.Fatalf("pending writer emitted %q phase=%v", out.String(), s.phase)
	}
	s.ensureStarted("id", "")
	s.Fail("boom")
	if s.phase != chatToAnthropicFailed {
		t.Fatalf("phase after Fail = %v", s.phase)
	}
	n := out.Len()
	s.Finish()
	s.ensureStarted("id", "")
	if out.Len() != n || s.phase != chatToAnthropicFailed {
		t.Fatalf("terminal writer emitted %q", out.String()[n:])
	}
}

func TestAnthropicToChatFailFromPendingIsSilent(t *testing.T) {
	var out bytes.Buffer
	c := NewChatStreamFromAnthropic(&out, nil, "m")
	c.Fail("boom")
	c.Finish()
	if out.Len() != 0 || c.phase != anthropicToChatFailed {
		t.Fatalf("pending stream emitted %q phase=%v", out.String(), c.phase)
	}
	c = NewChatStreamFromAnthropic(&out, nil, "m")
	c.ensureStarted()
	c.Finish()
	n := out.Len()
	c.Fail("boom")
	if c.phase != anthropicToChatFinished || out.Len() != n || !strings.HasSuffix(out.String(), "data: [DONE]\n\n") {
		t.Fatalf("finished stream: phase=%v out=%q", c.phase, out.String())
	}
}

func phaseTestEvent(t *testing.T, raw string) anthropicEvent {
	t.Helper()
	var event anthropicEvent
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatal(err)
	}
	return event
}
