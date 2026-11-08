package translate

import (
	"bytes"
	"strings"
	"testing"
)

// signatureDeltaStream is the upstream shape MiniMax Code's Messages route
// sends: every thinking block closes with an opaque signature_delta before the
// answer text starts. The signature is provider state that no translated wire
// can carry, so it must be dropped instead of failing the whole stream.
const signatureDeltaStream = `event: message_start
data: {"type":"message_start","message":{"id":"msg_sig","model":"u","usage":{"input_tokens":209}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"let me "}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"653c81eda48a309b"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hello!"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":13}}

event: message_stop
data: {"type":"message_stop"}

`

func TestSignatureDeltaDoesNotBreakChatStream(t *testing.T) {
	var out bytes.Buffer
	if err := NewChatStreamFromAnthropic(&out, nil, "client").Consume(strings.NewReader(signatureDeltaStream)); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	var text strings.Builder
	for _, event := range chatChunkEvents(t, out.String()) {
		choices, _ := event.Data["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		if content, ok := delta["content"].(string); ok {
			text.WriteString(content)
		}
	}
	if text.String() != "Hello!" {
		t.Fatalf("answer text = %q, want %q; stream: %s", text.String(), "Hello!", out.String())
	}
	if strings.Contains(out.String(), "653c81eda48a309b") {
		t.Fatalf("provider signature leaked to the client: %s", out.String())
	}
	if !strings.Contains(out.String(), "data: [DONE]") {
		t.Fatalf("stream did not finish: %s", out.String())
	}
}

func TestSignatureDeltaDoesNotBreakResponsesStream(t *testing.T) {
	var out bytes.Buffer
	if err := NewResponsesStreamFromAnthropic(&out, nil, "client").Consume(strings.NewReader(signatureDeltaStream)); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if !strings.Contains(out.String(), "response.completed") {
		t.Fatalf("stream did not complete: %s", out.String())
	}
	if strings.Contains(out.String(), "653c81eda48a309b") {
		t.Fatalf("provider signature leaked to the client: %s", out.String())
	}
}

// A signature_delta on a block that never opened as thinking is still
// contradictory upstream state, not provider signing state to drop.
func TestSignatureDeltaOnNonThinkingBlockStillFails(t *testing.T) {
	var s anthropicStreamState
	if _, err := s.consume(phaseTestEvent(t, `{"type":"message_start","message":{}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.consume(anthropicEvent{Type: "content_block_start", ContentBlock: &anthropicStreamContent{Type: "text"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.consume(anthropicEvent{Type: "content_block_delta", Index: 0, Delta: &anthropicStreamDelta{Type: "text_delta", Text: "hi"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.consume(anthropicEvent{Type: "content_block_delta", Index: 0, Delta: &anthropicStreamDelta{Type: "signature_delta"}}); err == nil {
		t.Fatal("accepted a signature_delta on a text block")
	}
}
