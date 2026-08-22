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

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

// fakeOABackend is an OpenAI-endpoints test double. Named to avoid colliding
// with fakes in other test files in this package. Supports is driven by the
// kinds map (nil means every kind); Send replays status/header/body and
// records every request it receives.
type fakeOABackend struct {
	name    string
	models  []string
	kinds   map[backend.Kind]bool
	status  int
	header  http.Header
	body    string
	sendErr error

	mu      sync.Mutex
	gotReqs []*backend.Request
}

var _ backend.Backend = (*fakeOABackend)(nil)

func (f *fakeOABackend) Name() string { return f.name }

func (f *fakeOABackend) Models(context.Context) ([]string, error) {
	return append([]string(nil), f.models...), nil
}

func (f *fakeOABackend) Supports(kind backend.Kind) bool {
	if f.kinds == nil {
		return true
	}
	return f.kinds[kind]
}

func (f *fakeOABackend) Send(_ context.Context, req *backend.Request) (*backend.Response, error) {
	f.mu.Lock()
	f.gotReqs = append(f.gotReqs, req)
	f.mu.Unlock()
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	status := f.status
	if status == 0 {
		status = http.StatusOK
	}
	header := f.header.Clone()
	if header == nil {
		header = http.Header{}
	}
	return &backend.Response{
		Status: status,
		Header: header,
		Body:   io.NopCloser(strings.NewReader(f.body)),
	}, nil
}

func (f *fakeOABackend) lastRequest() *backend.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.gotReqs) == 0 {
		return nil
	}
	return f.gotReqs[len(f.gotReqs)-1]
}

// newOATestServer builds a Server routing through one fake backend. The
// backend participates in routing only if its config entry exists, so Type
// mirrors the fake's name.
func newOATestServer(t *testing.T, fb *fakeOABackend, routes map[string]config.ModelRoute) *Server {
	t.Helper()
	isolatePrometheus(t)
	return New(&config.Config{
		Backends: []config.BackendConfig{{Type: fb.name}},
		Routes:   routes,
	}, quietLogger(), nil, []backend.Backend{fb})
}

// postOpenAI issues an authenticated-free POST against s.Handler().
func postOpenAI(t *testing.T, s *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestChatCompletionsPassthroughStreamsAndRewritesModel(t *testing.T) {
	fb := &fakeOABackend{
		name:   "fakeoa",
		header: http.Header{"Content-Type": []string{"text/event-stream"}},
		body: "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
			"data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hi\"},\"finish_reason\":null}]}\n\n" +
			"data: [DONE]\n\n",
	}
	s := newOATestServer(t, fb, map[string]config.ModelRoute{
		"gpt-x": {Backend: "fakeoa", Model: "upstream-x"},
	})

	rec := postOpenAI(t, s, "/v1/chat/completions",
		`{"model":"gpt-x","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if !rec.Flushed {
		t.Error("SSE response was never flushed")
	}
	streamed := rec.Body.String()
	if !strings.Contains(streamed, "chat.completion.chunk") || !strings.Contains(streamed, "[DONE]") {
		t.Errorf("streamed body missing SSE payload: %q", streamed)
	}

	req := fb.lastRequest()
	if req == nil {
		t.Fatal("no request reached the backend")
	}
	if req.Kind != backend.KindOpenAIChat {
		t.Errorf("Kind = %q, want %q", req.Kind, backend.KindOpenAIChat)
	}
	if req.Model != "upstream-x" {
		t.Errorf("Model = %q, want upstream-x", req.Model)
	}
	if !req.Streaming {
		t.Error("Streaming flag lost")
	}
	var forwarded map[string]any
	if err := json.Unmarshal(req.RawBody, &forwarded); err != nil {
		t.Fatalf("decode forwarded body: %v", err)
	}
	if forwarded["model"] != "upstream-x" {
		t.Errorf("forwarded model = %v, want upstream-x", forwarded["model"])
	}
	messages, ok := forwarded["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("forwarded messages = %v, want one preserved entry", forwarded["messages"])
	}
}

func TestChatCompletionsTranslatedOntoResponsesBackend(t *testing.T) {
	fb := &fakeOABackend{
		name:   "fakeoa",
		kinds:  map[backend.Kind]bool{backend.KindOpenAIResponses: true},
		header: http.Header{"Content-Type": []string{"application/json"}},
		body: `{"id":"resp_1","status":"completed","model":"upstream-y",` +
			`"output":[{"type":"message","content":[{"type":"output_text","text":"hello world"}]}],` +
			`"usage":{"input_tokens":7,"output_tokens":9,"total_tokens":16}}`,
	}
	s := newOATestServer(t, fb, map[string]config.ModelRoute{
		"gpt-y": {Backend: "fakeoa", Model: "upstream-y"},
	})

	rec := postOpenAI(t, s, "/v1/chat/completions",
		`{"model":"gpt-y","messages":[{"role":"system","content":"be nice"},{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var out struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			TotalTokens      int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode chat response: %v", err)
	}
	if out.Object != "chat.completion" {
		t.Errorf("object = %q, want chat.completion", out.Object)
	}
	if len(out.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(out.Choices))
	}
	choice := out.Choices[0]
	if choice.Message.Content != "hello world" {
		t.Errorf("content = %q, want %q", choice.Message.Content, "hello world")
	}
	if choice.FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", choice.FinishReason)
	}
	if out.Usage.PromptTokens != 7 || out.Usage.CompletionTokens != 9 || out.Usage.TotalTokens != 16 {
		t.Errorf("usage = %+v, want 7/9/16", out.Usage)
	}

	req := fb.lastRequest()
	if req == nil {
		t.Fatal("no request reached the backend")
	}
	if req.Kind != backend.KindOpenAIResponses {
		t.Errorf("Kind = %q, want %q", req.Kind, backend.KindOpenAIResponses)
	}
	if req.Model != "upstream-y" {
		t.Errorf("Model = %q, want upstream-y", req.Model)
	}
	var translated struct {
		Model        string `json:"model"`
		Instructions string `json:"instructions"`
		Input        []struct {
			Type string `json:"type"`
			Role string `json:"role"`
		} `json:"input"`
	}
	if err := json.Unmarshal(req.RawBody, &translated); err != nil {
		t.Fatalf("decode translated body: %v", err)
	}
	if translated.Model != "upstream-y" {
		t.Errorf("translated model = %q, want upstream-y", translated.Model)
	}
	if translated.Instructions != "be nice" {
		t.Errorf("instructions = %q, want %q", translated.Instructions, "be nice")
	}
	if len(translated.Input) != 1 || translated.Input[0].Role != "user" {
		t.Errorf("input items = %+v, want one user item", translated.Input)
	}
}

func TestChatCompletionsTranslatedStream(t *testing.T) {
	fb := &fakeOABackend{
		name:   "fakeoa",
		kinds:  map[backend.Kind]bool{backend.KindOpenAIResponses: true},
		header: http.Header{"Content-Type": []string{"text/event-stream"}},
		body: "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_s\",\"status\":\"in_progress\",\"model\":\"upstream-z\"}}\n\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hel\"}\n\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"lo\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_s\",\"status\":\"completed\",\"model\":\"upstream-z\",\"usage\":{\"input_tokens\":2,\"output_tokens\":2,\"total_tokens\":4}}}\n\n" +
			"data: [DONE]\n\n",
	}
	s := newOATestServer(t, fb, map[string]config.ModelRoute{
		"gpt-z": {Backend: "fakeoa", Model: "upstream-z"},
	})

	rec := postOpenAI(t, s, "/v1/chat/completions",
		`{"model":"gpt-z","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if !rec.Flushed {
		t.Error("translated stream was never flushed")
	}
	streamed := rec.Body.String()
	for _, want := range []string{
		`"role":"assistant"`,
		`"content":"Hel"`,
		`"content":"lo"`,
		`"finish_reason":"stop"`,
		`"total_tokens":4`,
		"[DONE]",
	} {
		if !strings.Contains(streamed, want) {
			t.Errorf("stream missing %s in:\n%s", want, streamed)
		}
	}
}

func TestChatCompletionsUnsupportedBackend(t *testing.T) {
	fb := &fakeOABackend{
		name:  "fakeoa",
		kinds: map[backend.Kind]bool{},
	}
	s := newOATestServer(t, fb, map[string]config.ModelRoute{
		"gpt-n": {Backend: "fakeoa"},
	})

	rec := postOpenAI(t, s, "/v1/chat/completions",
		`{"model":"gpt-n","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	var body openAIErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Type != "invalid_request_error" {
		t.Errorf("type = %q, want invalid_request_error", body.Error.Type)
	}
	if !strings.Contains(body.Error.Message, "does not support the Chat Completions API") {
		t.Errorf("message = %q, want mention of unsupported Chat Completions", body.Error.Message)
	}
	if fb.lastRequest() != nil {
		t.Error("no request should have reached the backend")
	}
}

func TestResponsesPassthroughRewritesModel(t *testing.T) {
	fb := &fakeOABackend{
		name:   "fakeoa",
		kinds:  map[backend.Kind]bool{backend.KindOpenAIResponses: true},
		header: http.Header{"Content-Type": []string{"application/json"}},
		body:   `{"id":"resp_p","status":"completed","model":"upstream-p","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
	}
	s := newOATestServer(t, fb, map[string]config.ModelRoute{
		"gpt-p": {Backend: "fakeoa", Model: "upstream-p"},
	})

	rec := postOpenAI(t, s, "/v1/responses", `{"model":"gpt-p","input":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if got := rec.Body.String(); got != fb.body {
		t.Errorf("relayed body = %q, want verbatim upstream body", got)
	}

	req := fb.lastRequest()
	if req == nil {
		t.Fatal("no request reached the backend")
	}
	if req.Kind != backend.KindOpenAIResponses {
		t.Errorf("Kind = %q, want %q", req.Kind, backend.KindOpenAIResponses)
	}
	var forwarded map[string]any
	if err := json.Unmarshal(req.RawBody, &forwarded); err != nil {
		t.Fatalf("decode forwarded body: %v", err)
	}
	if forwarded["model"] != "upstream-p" {
		t.Errorf("forwarded model = %v, want upstream-p", forwarded["model"])
	}
	if forwarded["input"] != "hi" {
		t.Errorf("forwarded input = %v, want hi", forwarded["input"])
	}
}

func TestResponsesRejectedOnChatOnlyBackend(t *testing.T) {
	fb := &fakeOABackend{
		name:  "fakeoa",
		kinds: map[backend.Kind]bool{backend.KindOpenAIChat: true},
	}
	s := newOATestServer(t, fb, map[string]config.ModelRoute{
		"gpt-c": {Backend: "fakeoa"},
	})

	rec := postOpenAI(t, s, "/v1/responses", `{"model":"gpt-c","input":"hi"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	var body openAIErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Type != "invalid_request_error" {
		t.Errorf("type = %q, want invalid_request_error", body.Error.Type)
	}
	for _, want := range []string{"does not accept the Responses API", "; use /v1/chat/completions"} {
		if !strings.Contains(body.Error.Message, want) {
			t.Errorf("message = %q, want substring %q", body.Error.Message, want)
		}
	}
	if fb.lastRequest() != nil {
		t.Error("no request should have reached the backend")
	}
}

func TestChatCompletionsUnknownModel404(t *testing.T) {
	fb := &fakeOABackend{name: "fakeoa", models: []string{"known-model"}}
	s := newOATestServer(t, fb, nil)

	rec := postOpenAI(t, s, "/v1/chat/completions",
		`{"model":"ghost","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}
	var body openAIErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Type != "invalid_request_error" {
		t.Errorf("type = %q, want invalid_request_error", body.Error.Type)
	}
	if body.Error.Code != "model_not_found" {
		t.Errorf("code = %q, want model_not_found", body.Error.Code)
	}
	if !strings.Contains(body.Error.Message, `"ghost"`) ||
		!strings.Contains(body.Error.Message, "has no available backend") {
		t.Errorf("message = %q, want model name and availability hint", body.Error.Message)
	}
}

func TestResponsesUnknownModel404(t *testing.T) {
	fb := &fakeOABackend{name: "fakeoa", models: []string{"known-model"}}
	s := newOATestServer(t, fb, nil)

	rec := postOpenAI(t, s, "/v1/responses", `{"model":"ghost","input":"hi"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}
	var body openAIErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Code != "model_not_found" {
		t.Errorf("code = %q, want model_not_found", body.Error.Code)
	}
	if body.Error.Type != "invalid_request_error" {
		t.Errorf("type = %q, want invalid_request_error", body.Error.Type)
	}
}

func TestChatCompletionsUpstreamErrorRelayed(t *testing.T) {
	upstreamErr := `{"error":{"message":"overloaded upstream","type":"server_error","code":null}}`
	fb := &fakeOABackend{
		name:   "fakeoa",
		status: http.StatusServiceUnavailable,
		header: http.Header{
			"Content-Type": []string{"application/json"},
			"X-Request-Id": []string{"req-up-42"},
		},
		body: upstreamErr,
	}
	s := newOATestServer(t, fb, map[string]config.ModelRoute{
		"gpt-e": {Backend: "fakeoa"},
	})

	rec := postOpenAI(t, s, "/v1/chat/completions",
		`{"model":"gpt-e","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSuffix(rec.Body.String(), "\n"); got != upstreamErr {
		t.Errorf("relayed body = %q, want upstream body %q", got, upstreamErr)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if id := rec.Header().Get("X-Request-Id"); id != "req-up-42" {
		t.Errorf("X-Request-Id = %q, want req-up-42", id)
	}
}

func TestChatCompletionsUpstreamEmptyErrorSynthesized(t *testing.T) {
	fb := &fakeOABackend{
		name:   "fakeoa",
		status: http.StatusServiceUnavailable,
		body:   "",
	}
	s := newOATestServer(t, fb, map[string]config.ModelRoute{
		"gpt-e": {Backend: "fakeoa"},
	})

	rec := postOpenAI(t, s, "/v1/responses", `{"model":"gpt-e","input":"hi"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", rec.Code, rec.Body.String())
	}
	var body openAIErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Type != "api_error" {
		t.Errorf("type = %q, want api_error", body.Error.Type)
	}
}

func TestChatCompletionsGarbageEnvelope(t *testing.T) {
	fb := &fakeOABackend{name: "fakeoa"}
	s := newOATestServer(t, fb, nil)

	rec := postOpenAI(t, s, "/v1/chat/completions", `{not-json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	var body openAIErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Type != "invalid_request_error" {
		t.Errorf("type = %q, want invalid_request_error", body.Error.Type)
	}
}

func TestResponsesGarbageEnvelope(t *testing.T) {
	fb := &fakeOABackend{name: "fakeoa"}
	s := newOATestServer(t, fb, nil)

	rec := postOpenAI(t, s, "/v1/responses", `@@@`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	var body openAIErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Type != "invalid_request_error" {
		t.Errorf("type = %q, want invalid_request_error", body.Error.Type)
	}
}
