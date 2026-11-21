package translate

import (
	"errors"
	"fmt"
	"strings"
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

// TestAppendContextLimitGap covers the compaction hint appended to item-limit
// rejections: the provider's message stays verbatim, and the appended sentence
// matches Claude Code's "prompt is too long: N tokens > M tokens" parser with
// a gap that clears the overshoot.
func TestAppendContextLimitGap(t *testing.T) {
	provider := "context_length_exceeded: [code=context_length_exceeded] Input is too long: conversation history exceeds the limit on input items (1052 > 1000 items). Compact the conversation and retry."
	enriched := AppendContextLimitGap(provider, 300000)
	if enriched == provider {
		t.Fatalf("no gap appended: %s", provider)
	}
	if !strings.HasPrefix(enriched, provider) {
		t.Fatalf("provider message altered: %s", enriched)
	}
	// Gap = est * (actual-limit) * 4/3 / actual = 300000 * 52 * 4 / (1052 * 3) = 19772.
	var actual, limit, kept, dropped int
	if _, err := fmt.Sscanf(enriched[len(provider):], " | prompt is too long: %d tokens > %d tokens", &kept, &dropped); err != nil {
		t.Fatalf("appended sentence unparsable: %s", enriched)
	}
	// Claude Code's parser: /prompt is too long[^0-9]*(\d+)\s*tokens?\s*>\s*(\d+)/i
	if actual, limit = kept, dropped; actual-limit <= 0 {
		t.Fatalf("non-positive gap: %d - %d", actual, limit)
	}
	// The drop must at least clear the item overshoot fraction (52/1052 of tokens).
	wantGap := 300000 * 52 / 1052
	if actual-limit < wantGap {
		t.Fatalf("gap %d below required fraction %d", actual-limit, wantGap)
	}
	// Below the floor (4k tokens), the gap is floored so a retry can matter.
	small := AppendContextLimitGap(provider, 300)
	if small == provider {
		t.Fatalf("floored gap not appended for tiny estimate")
	}

	// Non-matching and degenerate inputs pass through untouched.
	for _, msg := range []string{
		"",
		"your quota is exhausted",
		"exceeds the limit on input items (900 > 1000 items)",
		"exceeds the limit on input items (abc > 1000 items)",
	} {
		if got := AppendContextLimitGap(msg, 300000); got != msg {
			t.Fatalf("AppendContextLimitGap(%q) = %q, want unchanged", msg, got)
		}
	}
	if got := AppendContextLimitGap(provider, 0); got != provider {
		t.Fatalf("zero estimate must not append: %s", got)
	}
}

// Token-window rejections get the same parseable sentence: OpenAI's numbered
// phrasing keeps the provider's counts, a count-free context_length_exceeded
// gets a fixed fraction of the estimate, and Anthropic-phrased messages stay
// untouched.
func TestAppendContextLimitGapTokenWindow(t *testing.T) {
	parse := func(t *testing.T, enriched, provider string) (int, int) {
		t.Helper()
		if !strings.HasPrefix(enriched, provider) || enriched == provider {
			t.Fatalf("hint not appended verbatim: %s", enriched)
		}
		var actual, limit int
		if _, err := fmt.Sscanf(enriched[len(provider):], " | prompt is too long: %d tokens > %d tokens", &actual, &limit); err != nil {
			t.Fatalf("appended sentence unparsable: %s", enriched)
		}
		return actual, limit
	}

	openai := "context_length_exceeded: This model's maximum context length is 128000 tokens. However, your messages resulted in 130500 tokens."
	if actual, limit := parse(t, AppendContextLimitGap(openai, 999999), openai); actual != 130500 || limit != 128000 {
		t.Fatalf("OpenAI counts = %d > %d, want 130500 > 128000", actual, limit)
	}

	bare := "context_length_exceeded: [code=context_length_exceeded] Input is too long: the request exceeds the model's token context limit. Compact the conversation and retry."
	if actual, limit := parse(t, AppendContextLimitGap(bare, 800000), bare); actual != 800000 || limit != 700000 {
		t.Fatalf("bare overflow = %d > %d, want 800000 > 700000", actual, limit)
	}
	if got := AppendContextLimitGap(bare, 0); got != bare {
		t.Fatalf("zero estimate must not append: %s", got)
	}

	for _, msg := range []string{
		"prompt is too long: 210000 tokens > 200000 maximum",
		"upstream_error: [code=upstream_error] The request could not be completed.",
	} {
		if got := AppendContextLimitGap(msg, 300000); got != msg {
			t.Fatalf("AppendContextLimitGap(%q) = %q, want unchanged", msg, got)
		}
	}
}
