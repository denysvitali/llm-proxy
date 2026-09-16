package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/backend/cloudflare"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestCloudflareClientAPIs(t *testing.T) {
	for _, tc := range []struct {
		path string
		body string
	}{
		{"/v1/chat/completions", `{"model":"cloudflare/stealth/union-alpha","messages":[{"role":"user","content":"Capital of France?"}],"max_tokens":16}`},
		{"/v1/messages", `{"model":"cloudflare/stealth/union-alpha","messages":[{"role":"user","content":"Capital of France?"}],"max_tokens":16}`},
		{"/v1/responses", `{"model":"cloudflare/stealth/union-alpha","input":"Capital of France?","max_output_tokens":16}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/ai/v1/chat/completions" {
					t.Errorf("upstream request = %s %s", r.Method, r.URL.Path)
				}
				var body struct {
					Model    string            `json:"model"`
					Messages []json.RawMessage `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Model != "stealth/union-alpha" || len(body.Messages) == 0 {
					t.Errorf("forwarded body = %+v", body)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"chat-test","object":"chat.completion","model":"stealth/union-alpha","choices":[{"index":0,"message":{"role":"assistant","content":"Paris"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":1,"total_tokens":13}}`)
			}))
			defer upstream.Close()
			c, err := cloudflare.New(upstream.URL+"/ai/v1", "test-token")
			if err != nil {
				t.Fatal(err)
			}
			defer c.HTTP.CloseIdleConnections()
			s := newTestServer(t, []backend.Backend{c}, config.BackendConfig{Type: "cloudflare"})
			rec := postOpenAI(t, s, tc.path, tc.body)
			if rec.Code != http.StatusOK || !json.Valid(rec.Body.Bytes()) || !strings.Contains(rec.Body.String(), "Paris") {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}
