package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// scriptedBackend plays a queue of Send outcomes; the last step repeats once
// the queue is empty, so exhausted-retry tests need no explicit loop.
type scriptedBackend struct {
	mu    sync.Mutex
	steps []step
	calls int
	kinds map[backend.Kind]bool
}

type step struct {
	resp *backend.Response
	err  error
}

var _ backend.Backend = (*scriptedBackend)(nil)

// newScripted builds a backend exposing exactly one wire-format kind so
// dialect selection resolves the way msgFakeBackend's supported map does.
func newScripted(kind backend.Kind, steps ...step) *scriptedBackend {
	return &scriptedBackend{
		steps: steps,
		kinds: map[backend.Kind]bool{kind: true},
	}
}

// persistentRetrySteps queues maxAlwaysRetries failures followed by a final
// successful attempt.
func persistentRetrySteps(status int, body string, final step) []step {
	steps := make([]step, 0, maxAlwaysRetries+1)
	for range maxAlwaysRetries {
		steps = append(steps, step{resp: unavailableResponse(status, body)})
	}
	return append(steps, final)
}

func (b *scriptedBackend) Name() string { return "fake" }

func (b *scriptedBackend) Models(context.Context) ([]string, error) {
	return []string{"m1"}, nil
}

func (b *scriptedBackend) Supports(k backend.Kind) bool { return b.kinds[k] }

func (b *scriptedBackend) Send(_ context.Context, _ *backend.Request) (*backend.Response, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	if len(b.steps) == 0 {
		return nil, errors.New("scriptedBackend: no steps left")
	}
	next := b.steps[0]
	if len(b.steps) > 1 {
		b.steps = b.steps[1:]
	}
	return next.resp, next.err
}

func (b *scriptedBackend) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

// sseResponse builds a 200 SSE upstream response carrying the given raw body.
func sseResponse(contentType, body string) *backend.Response {
	header := http.Header{}
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &backend.Response{
		Status: http.StatusOK,
		Header: header,
		Body:   io.NopCloser(strings.NewReader(body)),
	}
}

// failingBody yields prefix then fails, simulating an upstream that breaks
// mid-transfer.
type failingBody struct {
	prefix string
	used   bool
}

func (b *failingBody) Read(p []byte) (int, error) {
	if !b.used {
		b.used = true
		return copy(p, b.prefix), nil
	}
	return 0, errors.New("connection reset mid-body")
}

func (b *failingBody) Close() error { return nil }

// outcomeDelta captures a retry-outcome counter so a test can assert its own
// increment regardless of what earlier tests already recorded.
func outcomeDelta(s *Server, phase, outcome string) func() float64 {
	before := testutil.ToFloat64(s.metrics.retryOutcomes.WithLabelValues(phase, outcome))
	return func() float64 {
		return testutil.ToFloat64(s.metrics.retryOutcomes.WithLabelValues(phase, outcome)) - before
	}
}

// zeroRetryBackoff disables exponential sleeps so persistent-retry coverage
// can exercise the full attempt budget without slowing the suite.
func zeroRetryBackoff(t *testing.T) {
	t.Helper()
	original := testRetryDelayScale
	testRetryDelayScale = 0
	t.Cleanup(func() { testRetryDelayScale = original })
}

const anthropicStreamRequest = `{"model":"m1","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}`

const fullAnthropicSSE = "event: message_start\n" +
	`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[]}}` + "\n\n" +
	"event: content_block_delta\n" +
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}` + "\n\n" +
	"event: message_delta\n" +
	`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}` + "\n\n" +
	"event: message_stop\n" +
	`data: {"type":"message_stop"}` + "\n\n"

const brokenAnthropicSSE = "event: message_start\n" +
	`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[]}}` + "\n\n" +
	"event: content_block_delta\n" +
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}` + "\n\n"

const fullChatSSE = `data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n" +
	`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"hi"}}]}` + "\n\n" +
	`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
	"data: [DONE]\n\n"

const brokenChatSSE = `data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n" +
	`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n"

const networkErrorChatSSE = `data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":"network_error"}]}` + "\n\n" +
	"data: [DONE]\n\n"

const partialNetworkErrorChatSSE = `data: {"id":"c1","choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n" +
	`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"network_error"}]}` + "\n\n" +
	"data: [DONE]\n\n"

const brokenResponsesSSE = "event: response.created\n" +
	`data: {"type":"response.created","response":{"id":"resp_1","model":"m"}}` + "\n\n" +
	"event: response.output_text.delta\n" +
	`data: {"type":"response.output_text.delta","output_index":0,"delta":"partial"}` + "\n\n"

const fullResponsesSSE = "event: response.created\n" +
	`data: {"type":"response.created","response":{"id":"resp_1","model":"m"}}` + "\n\n" +
	"event: response.output_text.delta\n" +
	`data: {"type":"response.output_text.delta","output_index":0,"delta":"hi"}` + "\n\n" +
	"event: response.completed\n" +
	`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed"}}` + "\n\n"

// TestMessagesRetriesCleanEmptyStream covers the failure opencode shipped for
// a while: an upstream 200 whose body ends before any event. The proxy
// retries while nothing has reached the client, so the turn just looks
// slower.
func TestMessagesRetriesCleanEmptyStream(t *testing.T) {
	upstream := newScripted(backend.KindOpenAIChat,
		step{resp: sseResponse("text/event-stream", "")},
		step{resp: sseResponse("text/event-stream", fullChatSSE)},
	)
	s := newMsgServerWith(t, upstream)

	recovered := outcomeDelta(s, retryPhaseBody, retryRecovered)

	rec := postMsg(t, s, "/v1/messages", anthropicStreamRequest)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if upstream.callCount() != 2 {
		t.Fatalf("upstream attempts = %d, want 2", upstream.callCount())
	}
	body := rec.Body.String()
	for _, want := range []string{"event: message_start", "event: content_block_delta", "event: message_stop"} {
		if !strings.Contains(body, want) {
			t.Fatalf("retried stream missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "event: error") {
		t.Fatalf("a retried break must stay invisible to the client:\n%s", body)
	}
	if recovered() < 1 {
		t.Fatalf("expected a %q/%q metric increment", retryPhaseBody, retryRecovered)
	}
}

// TestMessagesRetriesBrokenNativeStreamBeforeCommit covers an upstream that
// answers 200 and drops the connection before any event of a native
// Anthropic stream. Nothing has been forwarded, so the proxy retries
// transparently instead of letting the turn die.
func TestMessagesRetriesBrokenNativeStreamBeforeCommit(t *testing.T) {
	upstream := newScripted(backend.KindAnthropic,
		step{resp: sseResponse("text/event-stream", "")},
		step{resp: sseResponse("text/event-stream", fullAnthropicSSE)},
	)
	s := newMsgServerWith(t, upstream)

	recovered := outcomeDelta(s, retryPhaseBody, retryRecovered)

	rec := postMsg(t, s, "/v1/messages", anthropicStreamRequest)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if upstream.callCount() != 2 {
		t.Fatalf("upstream attempts = %d, want 2", upstream.callCount())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"type":"message_stop"`) {
		t.Fatalf("retried native stream missing message_stop:\n%s", body)
	}
	if strings.Contains(body, "event: error") {
		t.Fatalf("a retried break must stay invisible to the client:\n%s", body)
	}
	if recovered() < 1 {
		t.Fatalf("expected a %q/%q metric increment", retryPhaseBody, retryRecovered)
	}
}

// TestMessagesSurfacesNativeBreakAfterContent feeds a native Anthropic
// stream that dies after content was already forwarded. Replaying would
// duplicate it, so the break becomes the protocol's in-band error event.
func TestMessagesSurfacesNativeBreakAfterContent(t *testing.T) {
	upstream := newScripted(backend.KindAnthropic,
		step{resp: sseResponse("text/event-stream", brokenAnthropicSSE)},
	)
	s := newMsgServerWith(t, upstream)

	surfaced := outcomeDelta(s, retryPhaseBody, retrySurfaced)

	rec := postMsg(t, s, "/v1/messages", anthropicStreamRequest)
	body := rec.Body.String()
	if !strings.Contains(body, "event: content_block_delta") {
		t.Fatalf("forwarded content missing:\n%s", body)
	}
	if !strings.Contains(body, "event: error") || !strings.Contains(body, `"type":"api_error"`) {
		t.Fatalf("in-band error event missing:\n%s", body)
	}
	if strings.Contains(body, `"type":"message_stop"`) {
		t.Fatalf("broken stream must not be closed with message_stop:\n%s", body)
	}
	if surfaced() < 1 {
		t.Fatalf("expected a %q/%q metric increment", retryPhaseBody, retrySurfaced)
	}
}

// TestMessagesSurfacesTranslatedBreakAfterContent feeds a chat-completions
// upstream that dies after content flowed through translation; the Anthropic
// client must see an explicit error event, not a fake end_turn.
func TestMessagesSurfacesTranslatedBreakAfterContent(t *testing.T) {
	upstream := newScripted(backend.KindOpenAIChat,
		step{resp: sseResponse("text/event-stream", brokenChatSSE)},
	)
	s := newMsgServerWith(t, upstream)

	surfaced := outcomeDelta(s, retryPhaseBody, retrySurfaced)

	rec := postMsg(t, s, "/v1/messages", anthropicStreamRequest)
	body := rec.Body.String()
	if !strings.Contains(body, `"text":"partial"`) {
		t.Fatalf("forwarded content missing:\n%s", body)
	}
	if !strings.Contains(body, "event: error") {
		t.Fatalf("in-band error event missing:\n%s", body)
	}
	if strings.Contains(body, `"stop_reason":"end_turn"`) || strings.Contains(body, `"type":"message_stop"`) {
		t.Fatalf("broken stream must not be dressed up as completed:\n%s", body)
	}
	if surfaced() < 1 {
		t.Fatalf("expected a %q/%q metric increment", retryPhaseBody, retrySurfaced)
	}
}

// TestMessagesRetriesBufferedBodyBreak covers the non-streaming translated
// path: an upstream body that breaks mid-read is re-fetched while nothing
// has reached the client.
func TestMessagesRetriesBufferedBodyBreak(t *testing.T) {
	upstream := newScripted(backend.KindOpenAIChat,
		step{resp: &backend.Response{
			Status: http.StatusOK,
			Header: http.Header{},
			Body:   io.NopCloser(&failingBody{prefix: `{"choices":[{"message":{"role":"assistant","content":"hi"`}),
		}},
		step{resp: &backend.Response{
			Status: http.StatusOK,
			Header: http.Header{},
			Body: io.NopCloser(strings.NewReader(
				`{"id":"c1","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)),
		}},
	)
	s := newMsgServerWith(t, upstream)

	recovered := outcomeDelta(s, retryPhaseBody, retryRecovered)

	rec := postMsg(t, s, "/v1/messages", `{"model":"m1","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if upstream.callCount() != 2 {
		t.Fatalf("upstream attempts = %d, want 2", upstream.callCount())
	}
	if !strings.Contains(rec.Body.String(), `"text":"hi"`) {
		t.Fatalf("converted body missing content: %s", rec.Body.String())
	}
	if recovered() < 1 {
		t.Fatalf("expected a %q/%q metric increment", retryPhaseBody, retryRecovered)
	}
}

// TestMessagesRetriesConnectPhase covers a transient 503 before any response
// exists: one extra Send, then success.
func TestMessagesRetriesConnectPhase(t *testing.T) {
	upstream := newScripted(backend.KindOpenAIChat,
		step{resp: &backend.Response{Status: http.StatusServiceUnavailable, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("down"))}},
		step{resp: sseResponse("text/event-stream", fullChatSSE)},
	)
	s := newMsgServerWith(t, upstream)

	recovered := outcomeDelta(s, retryPhaseConnect, retryRecovered)

	rec := postMsg(t, s, "/v1/messages", anthropicStreamRequest)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if upstream.callCount() != 2 {
		t.Fatalf("upstream attempts = %d, want 2", upstream.callCount())
	}
	if !strings.Contains(rec.Body.String(), "event: message_stop") {
		t.Fatalf("retried stream incomplete:\n%s", rec.Body.String())
	}
	if recovered() < 1 {
		t.Fatalf("expected a %q/%q metric increment", retryPhaseConnect, retryRecovered)
	}
}

// TestResponsesAlwaysRetriesUnprocessableEntity covers providers that use 422
// as a persistent overload signal. Codex otherwise treats that status as fatal
// even though no response bytes have been forwarded yet.
func TestResponsesAlwaysRetriesUnprocessableEntity(t *testing.T) {
	zeroRetryBackoff(t)
	upstream := newScripted(backend.KindOpenAIResponses,
		persistentRetrySteps(
			http.StatusUnprocessableEntity,
			`{"error":{"message":"temporarily unavailable"}}`,
			step{resp: sseResponse("text/event-stream", fullResponsesSSE)},
		)...)
	s := newMsgServerWith(t, upstream)

	recovered := outcomeDelta(s, retryPhaseConnect, retryRecovered)

	rec := postMsg(t, s, "/v1/responses", `{"model":"m1","stream":true,"input":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if upstream.callCount() != maxAlwaysRetries+1 {
		t.Fatalf("upstream attempts = %d, want %d", upstream.callCount(), maxAlwaysRetries+1)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "response.completed") {
		t.Fatalf("retried Responses stream incomplete:\n%s", body)
	}
	if strings.Contains(body, "422") || strings.Contains(body, "temporarily unavailable") {
		t.Fatalf("pre-output 422 should remain invisible to the client:\n%s", body)
	}
	if recovered() < 1 {
		t.Fatalf("expected a %q/%q metric increment", retryPhaseConnect, retryRecovered)
	}
}

// unavailableResponse builds a JSON error response for a retryable status.
func unavailableResponse(status int, body string) *backend.Response {
	return &backend.Response{
		Status: status,
		Header: http.Header{},
		Body:   io.NopCloser(strings.NewReader(body)),
	}
}

// TestResponsesAlwaysRetriesTooManyRequests ensures rate-limit failures are
// hidden from clients across every provider-supplied attempt.
func TestResponsesAlwaysRetriesTooManyRequests(t *testing.T) {
	zeroRetryBackoff(t)
	upstream := newScripted(backend.KindOpenAIResponses, persistentRetrySteps(
		http.StatusTooManyRequests,
		`{"error":{"message":"rate limited"}}`,
		step{resp: sseResponse("text/event-stream", fullResponsesSSE)},
	)...)
	s := newMsgServerWith(t, upstream)

	recovered := outcomeDelta(s, retryPhaseConnect, retryRecovered)

	rec := postMsg(t, s, "/v1/responses", `{"model":"m1","stream":true,"input":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if upstream.callCount() != maxAlwaysRetries+1 {
		t.Fatalf("upstream attempts = %d, want %d", upstream.callCount(), maxAlwaysRetries+1)
	}
	if !strings.Contains(rec.Body.String(), "response.completed") {
		t.Fatalf("retried Responses stream incomplete:\n%s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "rate limited") {
		t.Fatalf("pre-output rate-limit failure should remain invisible:\n%s", rec.Body.String())
	}
	if recovered() < 1 {
		t.Fatalf("expected a %q/%q metric increment", retryPhaseConnect, retryRecovered)
	}
}

// TestResponsesRetriesTooManyRequestsHonorsRetryAfter ensures provider rate-
// limit guidance is respected without allowing an unbounded client wait.
func TestResponsesRetriesTooManyRequestsHonorsRetryAfter(t *testing.T) {
	before := time.Now()
	zeroRetryBackoff(t)
	upstream := newScripted(backend.KindOpenAIResponses,
		step{resp: &backend.Response{
			Status: http.StatusTooManyRequests,
			Header: http.Header{"Retry-After": []string{"1"}},
			Body:   io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
		}},
		step{resp: sseResponse("text/event-stream", fullResponsesSSE)},
	)
	s := newMsgServerWith(t, upstream)

	rec := postMsg(t, s, "/v1/responses", `{"model":"m1","stream":true,"input":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if elapsed := time.Since(before); elapsed < time.Second {
		t.Fatalf("retry after = %s, want at least the requested second", elapsed)
	}
	if upstream.callCount() != 2 {
		t.Fatalf("upstream attempts = %d, want 2", upstream.callCount())
	}
}

// TestMessagesConnectPhaseExhausted answers 503 forever; after the retry
// budget the client sees the upstream status relayed in its own dialect.
func TestMessagesConnectPhaseExhausted(t *testing.T) {
	zeroRetryBackoff(t)
	upstream := newScripted(backend.KindOpenAIChat,
		step{resp: &backend.Response{Status: http.StatusServiceUnavailable, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"down"}}`))}},
	)
	s := newMsgServerWith(t, upstream)

	exhausted := outcomeDelta(s, retryPhaseConnect, retryExhausted)

	rec := postMsg(t, s, "/v1/messages", anthropicStreamRequest)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if parsed := decodeAnthropicError(t, rec); parsed.Error.Type != "api_error" {
		t.Fatalf("expected Anthropic-shaped error, got %s", rec.Body.String())
	}
	if upstream.callCount() != maxAlwaysRetries+1 {
		t.Fatalf("upstream attempts = %d, want %d (1 + maxAlwaysRetries)", upstream.callCount(), maxAlwaysRetries+1)
	}
	if exhausted() < 1 {
		t.Fatalf("expected a %q/%q metric increment", retryPhaseConnect, retryExhausted)
	}
}

// TestResponsesEndpointSurfacesBreakAfterContent checks the Responses
// client dialect: a native Responses stream that dies mid-answer is closed
// with a response.failed event.
func TestResponsesEndpointSurfacesBreakAfterContent(t *testing.T) {
	upstream := newScripted(backend.KindOpenAIResponses,
		step{resp: sseResponse("text/event-stream", brokenResponsesSSE)},
	)
	s := newMsgServerWith(t, upstream)

	surfaced := outcomeDelta(s, retryPhaseBody, retrySurfaced)

	rec := postMsg(t, s, "/v1/responses", `{"model":"m1","stream":true,"input":"hi"}`)
	body := rec.Body.String()
	if !strings.Contains(body, `"delta":"partial"`) {
		t.Fatalf("forwarded content missing:\n%s", body)
	}
	if !strings.Contains(body, "event: response.failed") {
		t.Fatalf("response.failed event missing:\n%s", body)
	}
	if strings.Contains(body, "response.completed") {
		t.Fatalf("broken stream must not be completed:\n%s", body)
	}
	if surfaced() < 1 {
		t.Fatalf("expected a %q/%q metric increment", retryPhaseBody, retrySurfaced)
	}
}

// TestResponsesAcceptsLargeTerminalEvent covers Grok's native Responses
// stream, whose response.completed data includes the full response and may be
// far larger than the rolling completion window. Grok closes after that event
// without a separate [DONE] sentinel.
func TestResponsesAcceptsLargeTerminalEvent(t *testing.T) {
	terminal := "event: response.completed\n" +
		`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"` +
		strings.Repeat("x", retryCompletionWindow*2) +
		`"}]}]}}` + "\n\n"
	upstream := newScripted(backend.KindOpenAIResponses,
		step{resp: sseResponse("text/event-stream", terminal)},
	)
	s := newMsgServerWith(t, upstream)

	rec := postMsg(t, s, "/v1/responses", `{"model":"m1","stream":true,"input":"hi"}`)
	body := rec.Body.String()
	if !strings.Contains(body, "response.completed") {
		t.Fatalf("terminal event missing:\n%s", body)
	}
	if strings.Contains(body, "response.failed") {
		t.Fatalf("successful large terminal event was marked failed:\n%s", body)
	}
}

// TestResponsesRetriesTranslatedNetworkError covers providers such as
// OpenCode Zen's x-preview-f-free, which encode an upstream failure as a 200
// chat stream with finish_reason=network_error. That must be retried while no
// Responses bytes have reached the client, rather than becoming an empty
// response.completed event.
func TestResponsesRetriesTranslatedNetworkError(t *testing.T) {
	upstream := newScripted(backend.KindOpenAIChat,
		step{resp: sseResponse("text/event-stream", networkErrorChatSSE)},
		step{resp: sseResponse("text/event-stream", fullChatSSE)},
	)
	s := newMsgServerWith(t, upstream)

	recovered := outcomeDelta(s, retryPhaseBody, retryRecovered)

	rec := postMsg(t, s, "/v1/responses", `{"model":"m1","stream":true,"input":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if upstream.callCount() != 2 {
		t.Fatalf("upstream attempts = %d, want 2", upstream.callCount())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "response.completed") || !strings.Contains(body, `"delta":"hi"`) {
		t.Fatalf("retried Responses stream incomplete: %s", body)
	}
	if strings.Contains(body, "response.failed") {
		t.Fatalf("pre-output upstream failure should remain invisible: %s", body)
	}
	if recovered() < 1 {
		t.Fatalf("expected a %q/%q metric increment", retryPhaseBody, retryRecovered)
	}
}

// TestResponsesSurfacesTranslatedNetworkErrorAfterContent ensures a provider
// failure after output has started is surfaced as response.failed, without a
// fake completion or a replay that would duplicate the partial output.
func TestResponsesSurfacesTranslatedNetworkErrorAfterContent(t *testing.T) {
	upstream := newScripted(backend.KindOpenAIChat,
		step{resp: sseResponse("text/event-stream", partialNetworkErrorChatSSE)},
	)
	s := newMsgServerWith(t, upstream)

	surfaced := outcomeDelta(s, retryPhaseBody, retrySurfaced)

	rec := postMsg(t, s, "/v1/responses", `{"model":"m1","stream":true,"input":"hi"}`)
	body := rec.Body.String()
	if !strings.Contains(body, `"delta":"partial"`) {
		t.Fatalf("forwarded content missing: %s", body)
	}
	if !strings.Contains(body, "response.failed") || strings.Contains(body, "response.completed") {
		t.Fatalf("translated failure was not surfaced correctly: %s", body)
	}
	if surfaced() < 1 {
		t.Fatalf("expected a %q/%q metric increment", retryPhaseBody, retrySurfaced)
	}
}

// TestChatCompletionsSurfacesBreakAfterContent checks the chat-client
// dialect: a native chat stream that dies mid-answer is closed with an
// error chunk instead of [DONE].
func TestChatCompletionsSurfacesBreakAfterContent(t *testing.T) {
	upstream := newScripted(backend.KindOpenAIChat,
		step{resp: sseResponse("text/event-stream", brokenChatSSE)},
	)
	s := newMsgServerWith(t, upstream)

	surfaced := outcomeDelta(s, retryPhaseBody, retrySurfaced)

	rec := postMsg(t, s, "/v1/chat/completions", `{"model":"m1","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	body := rec.Body.String()
	if !strings.Contains(body, `"content":"partial"`) {
		t.Fatalf("forwarded content missing:\n%s", body)
	}
	if !strings.Contains(body, `"error"`) {
		t.Fatalf("error chunk missing:\n%s", body)
	}
	if strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("broken stream must not end with [DONE]:\n%s", body)
	}
	if surfaced() < 1 {
		t.Fatalf("expected a %q/%q metric increment", retryPhaseBody, retrySurfaced)
	}
}

// newMsgServerWith routes m1 at the given backend, mirroring newMsgServer's
// config without needing a throwaway fake.
func newMsgServerWith(t *testing.T, fb backend.Backend) *Server {
	t.Helper()
	cfg := &config.Config{
		Backends:     []config.BackendConfig{{Type: "fake", APIKey: "k"}},
		Routes:       map[string]config.ModelRoute{"m1": {Backend: "fake", Model: "upstream-m1"}},
		DefaultRoute: config.ModelRoute{Backend: "fake"},
	}
	return New(cfg, msgQuietLogger(), nil, []backend.Backend{fb})
}
