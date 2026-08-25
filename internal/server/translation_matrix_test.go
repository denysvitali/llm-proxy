package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/backend/apodex"
	"github.com/denysvitali/llm-proxy/internal/config"
)

// The tests here exercise the translation matrix through the full HTTP
// surface: a client speaking one dialect reaches a backend speaking another,
// and "<backend>/<model>" qualified IDs pin their backend directly.

const cannedAnthropicText = `{"id":"msg_1","model":"u-a",` +
	`"content":[{"type":"text","text":"hi there"}],"stop_reason":"end_turn",` +
	`"usage":{"input_tokens":3,"output_tokens":4}}`

func anthropicOnlyBackend(name string) *fakeOABackend {
	return &fakeOABackend{
		name:   name,
		kinds:  map[backend.Kind]bool{backend.KindAnthropic: true},
		header: http.Header{"Content-Type": []string{"application/json"}},
		body:   cannedAnthropicText,
	}
}

func TestChatClientOnAnthropicBackend(t *testing.T) {
	fb := anthropicOnlyBackend("claude-like")
	s := newOATestServer(t, fb, map[string]config.ModelRoute{
		"gpt-a": {Backend: "claude-like", Model: "u-a"},
	})

	rec := postOpenAI(t, s, "/v1/chat/completions",
		`{"model":"gpt-a","messages":[{"role":"user","content":"hello"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req := fb.lastRequest()
	if req == nil {
		t.Fatal("no request reached the backend")
	}
	if req.Kind != backend.KindAnthropic {
		t.Errorf("upstream Kind = %q, want %q", req.Kind, backend.KindAnthropic)
	}
	var sent map[string]any
	if err := json.Unmarshal(req.RawBody, &sent); err != nil {
		t.Fatalf("decode forwarded body: %v", err)
	}
	if sent["model"] != "u-a" {
		t.Errorf("forwarded model = %v, want u-a", sent["model"])
	}
	if _, ok := sent["max_tokens"]; !ok {
		t.Error("max_tokens missing: Anthropic requires it")
	}

	var out struct {
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Object != "chat.completion" || len(out.Choices) != 1 {
		t.Fatalf("response envelope = %+v", out)
	}
	if out.Choices[0].Message.Content != "hi there" || out.Choices[0].FinishReason != "stop" {
		t.Errorf("choice = %+v", out.Choices[0])
	}
	if out.Usage.PromptTokens != 3 || out.Usage.CompletionTokens != 4 {
		t.Errorf("usage = %+v, want 3/4", out.Usage)
	}
}

func TestChatClientOnAnthropicBackendStreams(t *testing.T) {
	streamBody := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"m1","model":"u-a"}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hey"}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	fb := &fakeOABackend{
		name:   "claude-like",
		kinds:  map[backend.Kind]bool{backend.KindAnthropic: true},
		header: http.Header{"Content-Type": []string{"text/event-stream"}},
		body:   streamBody,
	}
	s := newOATestServer(t, fb, map[string]config.ModelRoute{
		"gpt-a": {Backend: "claude-like", Model: "u-a"},
	})

	rec := postOpenAI(t, s, "/v1/chat/completions",
		`{"model":"gpt-a","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{`"delta":{"role":"assistant"}`, `"content":"hey"`, `"finish_reason"`, "[DONE]"} {
		if !strings.Contains(body, want) {
			t.Errorf("streamed body missing %q:\n%s", want, body)
		}
	}
}

func TestResponsesClientOnAnthropicBackend(t *testing.T) {
	fb := anthropicOnlyBackend("claude-like")
	s := newOATestServer(t, fb, map[string]config.ModelRoute{
		"codex-m": {Backend: "claude-like", Model: "u-a"},
	})

	rec := postOpenAI(t, s, "/v1/responses", `{"model":"codex-m","input":"hello"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Status != "completed" {
		t.Errorf("status = %q, want completed", out.Status)
	}
	found := false
	for _, item := range out.Output {
		for _, c := range item.Content {
			if c.Type == "output_text" && c.Text == "hi there" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("output = %+v, want output_text %q", out.Output, "hi there")
	}
	if out.Usage.InputTokens != 3 || out.Usage.OutputTokens != 4 {
		t.Errorf("usage = %+v, want 3/4", out.Usage)
	}
}

func TestResponsesClientOnApodexUsesChatCompatibilityPath(t *testing.T) {
	var upstreamPath string
	var upstreamBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPath = r.URL.Path
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chat-apodex",
			"model":"apodex-1.1",
			"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}
		}`))
	}))
	defer upstream.Close()

	client := apodex.New(upstream.URL, "test-token")
	s := newTestServer(t, []backend.Backend{client}, config.BackendConfig{Type: "apodex"})
	rec := postOpenAI(t, s, "/v1/responses", `{
		"model":"apodex/apodex-1.1",
		"instructions":"base instructions",
		"input":[
			{"type":"message","role":"user","content":"first"},
			{"type":"reasoning","content":null,"encrypted_content":"opaque"},
			{"type":"message","role":"developer","content":"developer rules"},
			{"type":"message","role":"user","content":"latest"}
		]
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if upstreamPath != "/chat/completions" {
		t.Fatalf("upstream path = %q, want Chat compatibility endpoint", upstreamPath)
	}
	var forwarded struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(upstreamBody, &forwarded); err != nil {
		t.Fatalf("decode upstream body: %v", err)
	}
	if len(forwarded.Messages) != 3 || forwarded.Messages[0].Role != "system" ||
		forwarded.Messages[0].Content != "base instructions\n\ndeveloper rules" {
		t.Fatalf("forwarded messages = %+v, want one leading merged prompt", forwarded.Messages)
	}
	if strings.Contains(string(upstreamBody), "opaque") ||
		strings.Contains(string(upstreamBody), `"content":null`) {
		t.Fatalf("opaque Responses reasoning reached Apodex Chat: %s", upstreamBody)
	}
	if !strings.Contains(rec.Body.String(), `"type":"message"`) ||
		!strings.Contains(rec.Body.String(), `"text":"ok"`) {
		t.Fatalf("translated Responses body = %s", rec.Body.String())
	}
}

func newTwoBackendServer(t *testing.T, first, second *fakeOABackend) *Server {
	t.Helper()
	return newTestServer(t,
		[]backend.Backend{first, second},
		config.BackendConfig{Type: first.name},
		config.BackendConfig{Type: second.name},
	)
}

func TestQualifiedModelRoutesToNamedBackend(t *testing.T) {
	first := anthropicOnlyBackend("alpha")
	second := anthropicOnlyBackend("beta")
	second.body = `{"id":"m2","model":"beta-up","content":[{"type":"text","text":"from beta"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
	// Catalog of alpha also lists "shared"; without qualification the first
	// backend would win.
	first.models = []string{"shared"}

	s := newTwoBackendServer(t, first, second)

	rec := postOpenAI(t, s, "/v1/chat/completions",
		`{"model":"beta/shared","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req := second.lastRequest()
	if req == nil {
		t.Fatal("qualified ID did not reach the beta backend")
	}
	if req.Model != "shared" {
		t.Errorf("upstream model = %q, want bare remainder \"shared\"", req.Model)
	}
	if first.lastRequest() != nil {
		t.Error("alpha must not see requests addressed to beta")
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Choices) != 1 || out.Choices[0].Message.Content != "from beta" {
		t.Errorf("answer = %+v, want beta's reply", out)
	}
}

func TestQualifiedModelKeepsNestedUpstreamName(t *testing.T) {
	nous := &fakeOABackend{
		name:  "nous",
		kinds: map[backend.Kind]bool{backend.KindOpenAIChat: true},
	}
	s := newTestServer(t, []backend.Backend{nous}, config.BackendConfig{Type: "nous"})

	rec := postOpenAI(t, s, "/v1/chat/completions",
		`{"model":"nous/nousresearch/hermes-4-70b","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	req := nous.lastRequest()
	if req == nil {
		t.Fatal("no request reached the backend")
	}
	if req.Model != "nousresearch/hermes-4-70b" {
		t.Errorf("upstream model = %q, want nested remainder kept intact", req.Model)
	}
}

func TestQualifiedModelDisabledBackendNotFound(t *testing.T) {
	fb := anthropicOnlyBackend("alpha")
	enabled := false
	s := newTestServer(t, []backend.Backend{fb}, config.BackendConfig{Type: "alpha", Enabled: &enabled})

	rec := postOpenAI(t, s, "/v1/chat/completions",
		`{"model":"alpha/anything","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for disabled backend, body = %s", rec.Code, rec.Body.String())
	}
}

func TestQualifiedModelUnknownPrefixFallsThrough(t *testing.T) {
	fb := anthropicOnlyBackend("alpha")
	s := newTestServer(t, []backend.Backend{fb}, config.BackendConfig{Type: "alpha"})
	s.cfg.DefaultRoute = config.ModelRoute{Backend: "alpha"}

	// "ghost/not-a-backend" names no backend, so normal resolution applies:
	// the default route takes the full string as its model name.
	rec := postOpenAI(t, s, "/v1/chat/completions",
		`{"model":"ghost/not-a-backend","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	req := fb.lastRequest()
	if req == nil || req.Model != "ghost/not-a-backend" {
		t.Fatalf("upstream model = %v, want full string passed through", req)
	}
}
