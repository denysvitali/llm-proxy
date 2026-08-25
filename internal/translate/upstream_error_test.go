package translate

import (
	"errors"
	"testing"
)

// TestFromOpenAIRejectsErrorBody covers gateways that answer HTTP 200 with an
// OpenAI error object; the decoder must surface it, not emit an empty Message.
func TestFromOpenAIRejectsErrorBody(t *testing.T) {
	body := []byte(`{"error":{"message":"usage limit exceeded","type":"insufficient_quota","code":2002}}`)
	_, err := FromOpenAI(body, "m1", false)
	var upstream *UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("FromOpenAI error body: err = %v, want UpstreamError", err)
	}
	if upstream.Message != "usage limit exceeded" || upstream.Type != "insufficient_quota" {
		t.Fatalf("unexpected error fields: %+v", upstream)
	}
}

// TestFromResponsesRejectsErrorBody checks the Responses->Anthropic decoder.
func TestFromResponsesRejectsErrorBody(t *testing.T) {
	body := []byte(`{"id":"resp_1","status":"failed","error":{"code":"server_error","message":"overloaded"}}`)
	_, err := FromResponses(body, "m1", false)
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.Message != "overloaded" {
		t.Fatalf("FromResponses error body: err = %v, want UpstreamError(overloaded)", err)
	}
}

// TestChatFromResponsesRejectsErrorBody checks the Responses->chat decoder.
func TestChatFromResponsesRejectsErrorBody(t *testing.T) {
	body := []byte(`{"error":{"message":"quota exhausted","type":"insufficient_quota"}}`)
	_, err := ChatFromResponses(body, "m1")
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.Message != "quota exhausted" {
		t.Fatalf("ChatFromResponses error body: err = %v, want UpstreamError", err)
	}
}

// TestChatFromAnthropicRejectsErrorBody checks the Anthropic->chat decoder.
func TestChatFromAnthropicRejectsErrorBody(t *testing.T) {
	body := []byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
	_, err := ChatFromAnthropic(body, "m1")
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.Message != "Overloaded" || upstream.Type != "overloaded_error" {
		t.Fatalf("ChatFromAnthropic error body: err = %v, want UpstreamError", err)
	}
}

// TestDecodersAcceptSuccessBodiesWithoutError guards against the check
// misfiring on legitimate success payloads.
func TestDecodersAcceptSuccessBodiesWithoutError(t *testing.T) {
	if _, err := FromOpenAI([]byte(`{"id":"c1","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`), "m1", false); err != nil {
		t.Fatalf("FromOpenAI success body rejected: %v", err)
	}
	if _, err := FromResponses([]byte(`{"id":"resp_1","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hi"}]}]}`), "m1", false); err != nil {
		t.Fatalf("FromResponses success body rejected: %v", err)
	}
	if _, err := ChatFromAnthropic([]byte(`{"id":"msg_1","model":"m","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`), "m1"); err != nil {
		t.Fatalf("ChatFromAnthropic success body rejected: %v", err)
	}
}
