package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

// msgFakeBackend is a configurable backend.Backend for the /v1/messages
// tests: it records every Request handed to Send and replies with a canned
// status/content-type/body. (Named distinctly from fakeBackend in
// models_test.go; both live in this package.)
type msgFakeBackend struct {
	mu        sync.Mutex
	name      string
	supported map[backend.Kind]bool
	models    []string

	status      int
	contentType string
	body        string
	sendErr     error

	lastKind      backend.Kind
	lastModel     string
	lastRaw       []byte
	lastHeader    http.Header
	lastStreaming bool
	sendCount     int
}

var _ backend.Backend = (*msgFakeBackend)(nil)

func (f *msgFakeBackend) Name() string {
	if f.name == "" {
		return "fake"
	}
	return f.name
}

func (f *msgFakeBackend) Models(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.models...), nil
}

func (f *msgFakeBackend) Supports(kind backend.Kind) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.supported[kind]
}

func (f *msgFakeBackend) Send(_ context.Context, req *backend.Request) (*backend.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendCount++
	f.lastKind = req.Kind
	f.lastModel = req.Model
	f.lastRaw = append([]byte(nil), req.RawBody...)
	if req.Header != nil {
		f.lastHeader = req.Header.Clone()
	}
	f.lastStreaming = req.Streaming
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	header := http.Header{}
	if f.contentType != "" {
		header.Set("Content-Type", f.contentType)
	}
	return &backend.Response{
		Status: f.status,
		Header: header,
		Body:   io.NopCloser(strings.NewReader(f.body)),
	}, nil
}

// captured returns a consistent snapshot of what the last Send received.
func (f *msgFakeBackend) captured() (kind backend.Kind, model string, raw []byte, streaming bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastKind, f.lastModel, f.lastRaw, f.lastStreaming
}

// msgQuietLogger swallows log output so expected warnings stay out of test logs.
func msgQuietLogger() *logrus.Logger {
	log := logrus.New()
	log.SetOutput(io.Discard)
	return log
}

// newMsgServer builds an auth-free Server around fb. Routes map m1 to the
// fake backend under upstream model "upstream-m1"; DefaultRoute points at the
// fake backend unless mutate clears it.
func newMsgServer(t *testing.T, fb *msgFakeBackend, mutate func(*config.Config)) *Server {
	t.Helper()
	cfg := &config.Config{
		Backends:     []config.BackendConfig{{Type: "fake", APIKey: "k"}},
		Routes:       map[string]config.ModelRoute{"m1": {Backend: "fake", Model: "upstream-m1"}},
		DefaultRoute: config.ModelRoute{Backend: "fake"},
	}
	if mutate != nil {
		mutate(cfg)
	}
	return New(cfg, msgQuietLogger(), nil, []backend.Backend{fb})
}

// postMsg serves method POST path with body through the full handler stack.
func postMsg(t *testing.T, s *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// decodeAnthropicError parses an Anthropic-shaped error response body.
func decodeAnthropicError(t *testing.T, rec *httptest.ResponseRecorder) anthropicErrorBody {
	t.Helper()
	var parsed anthropicErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return parsed
}

func TestMessagesMissingModel(t *testing.T) {
	fb := &msgFakeBackend{
		supported: map[backend.Kind]bool{backend.KindAnthropic: true},
		status:    http.StatusOK,
	}
	s := newMsgServer(t, fb, nil)

	rec := postMsg(t, s, "/v1/messages", `{"max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	parsed := decodeAnthropicError(t, rec)
	if parsed.Type != "error" || parsed.Error.Type != "invalid_request_error" || parsed.Error.Message == "" {
		t.Errorf("error shape = %+v, want type=error / invalid_request_error with message", parsed)
	}
}

func TestMessagesInvalidJSON(t *testing.T) {
	fb := &msgFakeBackend{supported: map[backend.Kind]bool{backend.KindAnthropic: true}}
	s := newMsgServer(t, fb, nil)

	rec := postMsg(t, s, "/v1/messages", `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := decodeAnthropicError(t, rec).Error.Type; got != "invalid_request_error" {
		t.Errorf("error type = %q, want invalid_request_error", got)
	}
}

func TestMessagesUnknownModelNoDefault(t *testing.T) {
	fb := &msgFakeBackend{
		supported: map[backend.Kind]bool{backend.KindAnthropic: true},
		models:    []string{"catalog-only-model"},
		status:    http.StatusOK,
	}
	s := newMsgServer(t, fb, func(c *config.Config) {
		c.DefaultRoute = config.ModelRoute{} // no fallback: unknown models must 404
	})

	rec := postMsg(t, s, "/v1/messages", `{"model":"nope-model","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}
	parsed := decodeAnthropicError(t, rec)
	if parsed.Type != "error" || parsed.Error.Type != "not_found_error" {
		t.Errorf("error shape = %+v, want not_found_error", parsed)
	}
	if !strings.Contains(parsed.Error.Message, "nope-model") {
		t.Errorf("message = %q, want it to name the model", parsed.Error.Message)
	}
}

func TestMessagesPassthroughNative(t *testing.T) {
	// A complete stream: passthrough only relays verbatim once the upstream
	// stream terminates properly; an unterminated one is a retry case.
	const upstreamSSE = "event: ping\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"
	fb := &msgFakeBackend{
		supported:   map[backend.Kind]bool{backend.KindAnthropic: true},
		status:      http.StatusOK,
		contentType: "text/event-stream",
		body:        upstreamSSE,
	}
	s := newMsgServer(t, fb, nil)

	body := `{"model":"m1","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`
	rec := postMsg(t, s, "/v1/messages", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("content-type = %q, want text/event-stream relayed from upstream", got)
	}
	if rec.Body.String() != upstreamSSE {
		t.Errorf("body = %q, want %q", rec.Body.String(), upstreamSSE)
	}

	kind, model, raw, streaming := fb.captured()
	if kind != backend.KindAnthropic {
		t.Errorf("upstream kind = %q, want anthropic", kind)
	}
	if model != "upstream-m1" {
		t.Errorf("upstream request model = %q, want upstream-m1", model)
	}
	if !streaming {
		t.Error("upstream request Streaming = false, want true")
	}
	var forwarded struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
	}
	if err := json.Unmarshal(raw, &forwarded); err != nil {
		t.Fatalf("decode forwarded body %q: %v", raw, err)
	}
	if forwarded.Model != "upstream-m1" {
		t.Errorf("forwarded body model = %q, want upstream-m1", forwarded.Model)
	}
	if forwarded.MaxTokens != 64 {
		t.Errorf("forwarded body max_tokens = %d, want 64 (other fields must be preserved)", forwarded.MaxTokens)
	}
}

func TestMessagesViaOpenAIChatTranslation(t *testing.T) {
	const chatJSON = `{"id":"chatcmpl-42","model":"venice-upstream","choices":[{"index":0,"message":{"role":"assistant","content":"hello world"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":2}}`
	fb := &msgFakeBackend{
		supported:   map[backend.Kind]bool{backend.KindOpenAIChat: true},
		status:      http.StatusOK,
		contentType: "application/json",
		body:        chatJSON,
	}
	s := newMsgServer(t, fb, nil)

	body := `{"model":"m1","max_tokens":128,"system":"be nice","messages":[{"role":"user","content":"say hi"}]}`
	rec := postMsg(t, s, "/v1/messages", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var out struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Role       string `json:"role"`
		Model      string `json:"model"`
		Content    []map[string]any
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode Anthropic response %q: %v", rec.Body.String(), err)
	}
	if out.Type != "message" || out.Role != "assistant" {
		t.Errorf("type/role = %q/%q, want message/assistant", out.Type, out.Role)
	}
	if len(out.Content) != 1 || out.Content[0]["type"] != "text" || out.Content[0]["text"] != "hello world" {
		t.Errorf("content = %v, want one text block 'hello world'", out.Content)
	}
	if out.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q, want end_turn", out.StopReason)
	}
	if out.Usage.InputTokens != 11 || out.Usage.OutputTokens != 2 {
		t.Errorf("usage = %+v, want input 11 output 2", out.Usage)
	}

	kind, _, raw, streaming := fb.captured()
	if kind != backend.KindOpenAIChat {
		t.Errorf("upstream kind = %q, want openai-chat", kind)
	}
	if streaming {
		t.Error("upstream request Streaming = true, want false")
	}
	var forwarded struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &forwarded); err != nil {
		t.Fatalf("decode forwarded chat body %q: %v", raw, err)
	}
	if forwarded.Model != "upstream-m1" {
		t.Errorf("forwarded chat model = %q, want upstream-m1", forwarded.Model)
	}
	if len(forwarded.Messages) == 0 || forwarded.Messages[0].Role != "system" {
		t.Errorf("forwarded messages = %+v, want system prompt translated first", forwarded.Messages)
	}
}

func TestMessagesViaOpenAIResponsesTranslation(t *testing.T) {
	const responsesJSON = `{"id":"resp_77","status":"completed","model":"grok-upstream","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"responses hi"}]}],"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}}`
	fb := &msgFakeBackend{
		supported:   map[backend.Kind]bool{backend.KindOpenAIResponses: true},
		status:      http.StatusOK,
		contentType: "application/json",
		body:        responsesJSON,
	}
	s := newMsgServer(t, fb, nil)

	body := `{"model":"m1","max_tokens":99,"messages":[{"role":"user","content":[{"type":"text","text":"say hi"}]}]}`
	rec := postMsg(t, s, "/v1/messages", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var out struct {
		Type       string `json:"type"`
		Content    []map[string]any
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode Anthropic response %q: %v", rec.Body.String(), err)
	}
	if out.Type != "message" {
		t.Errorf("type = %q, want message", out.Type)
	}
	if len(out.Content) != 1 || out.Content[0]["text"] != "responses hi" {
		t.Errorf("content = %v, want one text block 'responses hi'", out.Content)
	}
	if out.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q, want end_turn", out.StopReason)
	}
	if out.Usage.InputTokens != 7 || out.Usage.OutputTokens != 3 {
		t.Errorf("usage = %+v, want input 7 output 3", out.Usage)
	}

	kind, _, raw, _ := fb.captured()
	if kind != backend.KindOpenAIResponses {
		t.Errorf("upstream kind = %q, want openai-responses", kind)
	}
	var forwarded struct {
		Model string `json:"model"`
		Input []struct {
			Role string `json:"role"`
			Type string `json:"type"`
		} `json:"input"`
	}
	if err := json.Unmarshal(raw, &forwarded); err != nil {
		t.Fatalf("decode forwarded responses body %q: %v", raw, err)
	}
	if forwarded.Model != "upstream-m1" {
		t.Errorf("forwarded responses model = %q, want upstream-m1", forwarded.Model)
	}
	if len(forwarded.Input) == 0 || forwarded.Input[0].Role != "user" {
		t.Errorf("forwarded input items = %+v, want user message item first", forwarded.Input)
	}
}

func TestCountTokens(t *testing.T) {
	fb := &msgFakeBackend{supported: map[backend.Kind]bool{backend.KindAnthropic: true}}
	s := newMsgServer(t, fb, nil)

	body := `{"model":"m1","messages":[{"role":"user","content":"count me"}]}`
	rec := postMsg(t, s, "/v1/messages/count_tokens", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	want := (len(body) + 2) / 3
	var out struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode count response %q: %v", rec.Body.String(), err)
	}
	if out.InputTokens != want {
		t.Errorf("input_tokens = %d, want %d", out.InputTokens, want)
	}
	warnings := rec.Header().Values("Warning")
	if len(warnings) == 0 {
		t.Fatal("missing Warning header")
	}
	const wantWarning = `299 llm-proxy "token count is a conservative estimate"`
	found := false
	for _, w := range warnings {
		if w == wantWarning {
			found = true
		}
	}
	if !found {
		t.Errorf("Warning headers = %v, want %q", warnings, wantWarning)
	}
	if fb.sendCount != 0 {
		t.Errorf("count_tokens hit upstream %d times, want 0", fb.sendCount)
	}
}

func TestMessagesUpstreamErrorRelay(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		upstream    string
		wantStatus  int
		wantErrType string
	}{
		{name: "mapped empty body 500", status: 500, wantStatus: 500, wantErrType: "api_error"},
		{name: "mapped empty body 429", status: 429, wantStatus: 429, wantErrType: "rate_limit_error"},
		{name: "relayed non-empty body", status: 502, contentType: "text/plain", upstream: "upstream exploded",
			wantStatus: 502},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fb := &msgFakeBackend{
				supported:   map[backend.Kind]bool{backend.KindAnthropic: true},
				status:      tc.status,
				contentType: tc.contentType,
				body:        tc.upstream,
			}
			s := newMsgServer(t, fb, nil)

			rec := postMsg(t, s, "/v1/messages", `{"model":"m1","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.upstream != "" {
				if rec.Body.String() != tc.upstream {
					t.Errorf("body = %q, want verbatim %q", rec.Body.String(), tc.upstream)
				}
				if ct := rec.Header().Get("Content-Type"); ct != tc.contentType {
					t.Errorf("content-type = %q, want %q", ct, tc.contentType)
				}
				return
			}
			parsed := decodeAnthropicError(t, rec)
			if parsed.Type != "error" || parsed.Error.Type != tc.wantErrType {
				t.Errorf("error shape = %+v, want type=error / %s", parsed, tc.wantErrType)
			}
		})
	}
}

func TestMessagesBodyTooLarge(t *testing.T) {
	fb := &msgFakeBackend{
		supported: map[backend.Kind]bool{backend.KindAnthropic: true},
		status:    http.StatusOK,
	}
	s := newMsgServer(t, fb, func(c *config.Config) {
		c.Server.MaxBodyBytes = 16 // tiny limit so the test body overflows it
	})

	big := `{"model":"m1","max_tokens":8,"messages":[{"role":"user","content":"` + strings.Repeat("x", 100) + `"}]}`
	rec := postMsg(t, s, "/v1/messages", big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413, body = %s", rec.Code, rec.Body.String())
	}
	parsed := decodeAnthropicError(t, rec)
	if parsed.Error.Type != "invalid_request_error" {
		t.Errorf("error type = %q, want invalid_request_error", parsed.Error.Type)
	}
	if _, _, _, streaming := fb.captured(); fb.sendCount != 0 {
		t.Errorf("oversized body reached upstream (%d sends, streaming=%v)", fb.sendCount, streaming)
	}
}
