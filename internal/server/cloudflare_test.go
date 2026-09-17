package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/backend/cloudflare"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestCloudflareClientAPIs(t *testing.T) {
	for _, tc := range []struct {
		path     string
		body     string
		response string
		stream   string
	}{
		{"/v1/chat/completions", `{"model":"cloudflare/stealth/union-alpha","messages":[{"role":"user","content":"Capital of France?"}],"max_tokens":16,"provider_options":{"preserve":true}}`, `{"id":"chat-test","object":"chat.completion","choices":[{"message":{"content":"Paris"}}],"provider_field":"keep"}`, "data: {\"choices\":[{\"delta\":{\"content\":\"Paris\"}}],\"provider_field\":\"keep\"}\n\ndata: [DONE]\n\n"},
		{"/v1/messages", `{"model":"cloudflare/stealth/union-alpha","messages":[{"role":"user","content":"Capital of France?"}],"max_tokens":16,"thinking":{"type":"adaptive"},"provider_options":{"preserve":true}}`, `{"id":"msg-test","type":"message","role":"assistant","content":[{"type":"text","text":"Paris"}],"stop_reason":"end_turn","provider_field":"keep"}`, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-test\"},\"provider_field\":\"keep\"}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
		{"/v1/responses", `{"model":"cloudflare/stealth/union-alpha","input":"Capital of France?","max_output_tokens":16,"reasoning":{"effort":"high"},"provider_options":{"preserve":true}}`, `{"id":"resp-test","object":"response","status":"completed","output":[],"provider_field":"keep"}`, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-test\",\"status\":\"completed\",\"output\":[]},\"provider_field\":\"keep\"}\n\n"},
	} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.path, streaming), func(t *testing.T) {
				var input map[string]any
				if err := json.Unmarshal([]byte(tc.body), &input); err != nil {
					t.Fatal(err)
				}
				input["stream"] = streaming
				request, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				input["model"] = "stealth/union-alpha"
				response, contentType := tc.response, "application/json"
				if streaming {
					response, contentType = tc.stream, "text/event-stream"
				}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost || r.URL.Path != "/ai"+tc.path {
						t.Errorf("upstream request = %s %s", r.Method, r.URL.Path)
					}
					var got map[string]any
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Error(err)
					}
					if !reflect.DeepEqual(got, input) {
						t.Errorf("forwarded body = %#v, want %#v", got, input)
					}
					w.Header().Set("Content-Type", contentType)
					_, _ = io.WriteString(w, response)
				}))
				defer upstream.Close()
				c, err := cloudflare.New(upstream.URL+"/ai/v1", "test-token")
				if err != nil {
					t.Fatal(err)
				}
				defer c.HTTP.CloseIdleConnections()
				s := newTestServer(t, []backend.Backend{c}, config.BackendConfig{Type: "cloudflare"})
				rec := postOpenAI(t, s, tc.path, string(request))
				if rec.Code != http.StatusOK || rec.Body.String() != response {
					t.Fatalf("status = %d, body = %s, want %s", rec.Code, rec.Body.String(), response)
				}
			})
		}
	}
}
