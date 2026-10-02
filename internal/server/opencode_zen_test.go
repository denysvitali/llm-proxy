package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

// Exercise the real Zen backend through every client API, both buffered and
// streamed. This catches endpoint selection mistakes that generic wire fakes
// cannot detect, and proves the legacy provider name shares the new routing.
func TestOpenCodeZenClientAPIs(t *testing.T) {
	models := []struct {
		id, path, response, stream string
	}{
		{"gpt-6-sol", "/responses",
			`{"id":"resp_1","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}],"usage":{"input_tokens":2,"output_tokens":1}}`,
			"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n"},
		{"claude-sonnet-5", "/messages",
			`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"Hello"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`,
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"usage\":{\"input_tokens\":2}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
		{"kimi-k3", "/chat/completions",
			`{"id":"chat_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`,
			"data: {\"id\":\"chat_1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hello\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chat_1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n"},
	}
	qwen := models[1]
	qwen.id = "qwen3.8-flash"
	models = append(models, qwen)
	clients := []struct{ path, bodyField, end string }{
		{"/v1/messages", `"max_tokens":128,"messages":[{"role":"user","content":"Hello"}]`, "message_stop"},
		{"/v1/chat/completions", `"messages":[{"role":"user","content":"Hello"}]`, "[DONE]"},
		{"/v1/responses", `"input":"Hello"`, "response.completed"},
	}
	for _, provider := range []string{"opencode", "opencode-zen"} {
		for _, model := range models {
			for _, client := range clients {
				for _, streaming := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s%s/stream=%t", provider, model.id, client.path, streaming), func(t *testing.T) {
						var calls atomic.Int32
						upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							calls.Add(1)
							if r.Method != http.MethodPost || r.URL.Path != "/zen/v1"+model.path {
								t.Errorf("upstream request = %s %s", r.Method, r.URL.Path)
							}
							var sent map[string]any
							if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
								t.Error(err)
							}
							gotStream, _ := sent["stream"].(bool)
							if sent["model"] != model.id || gotStream != streaming {
								t.Errorf("upstream model/stream = %v/%v", sent["model"], sent["stream"])
							}
							field := "messages"
							if model.path == "/responses" {
								field = "input"
							}
							if sent[field] == nil || r.Header.Get("Authorization") != "Bearer zen-test-key" {
								t.Error("missing wire input or upstream key")
							}
							response, contentType := model.response, "application/json"
							if streaming {
								response, contentType = model.stream, "text/event-stream"
							}
							w.Header().Set("Content-Type", contentType)
							_, _ = io.WriteString(w, response)
						}))
						defer upstream.Close()
						bc := config.BackendConfig{Type: provider, BaseURL: upstream.URL + "/zen/v1/", APIKey: "zen-test-key"}
						b, err := backend.New(provider, backend.Options{BaseURL: bc.BaseURL, APIKey: bc.APIKey})
						if err != nil {
							t.Fatal(err)
						}
						s := newTestServer(t, []backend.Backend{b}, bc)
						body := fmt.Sprintf(`{"model":%q,"stream":%t,%s}`, provider+"/"+model.id, streaming, client.bodyField)
						rec := postOpenAI(t, s, client.path, body)
						if calls.Load() != 1 || rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Hello") {
							t.Fatalf("calls=%d status=%d body=%s", calls.Load(), rec.Code, rec.Body)
						}
						if streaming {
							if rec.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(rec.Body.String(), client.end) {
								t.Fatalf("incomplete client stream: %s", rec.Body)
							}
						} else if !json.Valid(rec.Body.Bytes()) {
							t.Fatalf("invalid client JSON: %s", rec.Body)
						}
						if client.path == "/v1"+model.path {
							want := model.response
							if streaming {
								want = model.stream
							}
							if rec.Body.String() != want {
								t.Fatal("native upstream response changed")
							}
						}
					})
				}
			}
		}
	}
}

func TestOpenCodeZenCatalogAndRoutes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"gpt-6-sol"},{"id":"gemini-3.8-flash"},{"id":"jev-1.13"}]}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/responses" {
			t.Errorf("unexpected upstream request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"resp_1","status":"completed","output":[]}`)
	}))
	defer upstream.Close()
	bc := config.BackendConfig{Type: "opencode-zen", BaseURL: upstream.URL, APIKey: "zen-test-key"}
	b, err := backend.New(bc.Type, backend.Options{BaseURL: bc.BaseURL, APIKey: bc.APIKey})
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, []backend.Backend{b}, bc)
	s.cfg.Routes = map[string]config.ModelRoute{"zen-gpt": {Backend: bc.Type, Model: "gpt-6-sol"}}
	rec, list := getModels(t, s, "/v1/models?backend=opencode-zen")
	if rec.Code != http.StatusOK || len(list.Data) != 1 || list.Data[0].ID != "opencode-zen/gpt-6-sol" || list.Data[0].OwnedBy != bc.Type {
		t.Fatalf("Zen catalog: status=%d list=%+v", rec.Code, list)
	}
	for _, model := range []string{"opencode-zen/gpt-6-sol", "gpt-6-sol", "zen-gpt"} {
		rec := postOpenAI(t, s, "/v1/responses", fmt.Sprintf(`{"model":%q,"input":"Hello"}`, model))
		if rec.Code != http.StatusOK {
			t.Fatalf("route %s: status=%d body=%s", model, rec.Code, rec.Body)
		}
	}
}
