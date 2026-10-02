package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	_ "github.com/denysvitali/llm-proxy/internal/backend/all"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestAnyRouterCatalogAndAlias(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing catalog/inference authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"stealth/fledge-alpha"}]}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/chat/completions" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		if got["model"] != "stealth/fledge-alpha" {
			t.Errorf("upstream model = %v", got["model"])
		}
		_, _ = io.WriteString(w, `{"id":"chat-test","choices":[{"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	bc := config.BackendConfig{Type: "anyrouter", BaseURL: upstream.URL + "/api/v1", APIKey: "test-token"}
	c, err := backend.New(bc.Type, backend.Options{BaseURL: bc.BaseURL, APIKey: bc.APIKey})
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, []backend.Backend{c}, bc)
	s.cfg.Routes = map[string]config.ModelRoute{"fledge-alpha": {Backend: "anyrouter", Model: "stealth/fledge-alpha"}}
	rec, list := getModels(t, s, "/v1/models")
	if rec.Code != http.StatusOK || len(list.Data) != 1 || list.Data[0].ID != "anyrouter/stealth/fledge-alpha" {
		t.Fatalf("model discovery: status %d, list %+v", rec.Code, list)
	}
	for _, model := range []string{"anyrouter/stealth/fledge-alpha", "stealth/fledge-alpha", "fledge-alpha"} {
		rec := postOpenAI(t, s, "/v1/chat/completions", fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Hello"}]}`, model))
		if rec.Code != http.StatusOK {
			t.Fatalf("route %s: status %d, body %s", model, rec.Code, rec.Body)
		}
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "anyrouter/stealth/fledge-alpha") || strings.Contains(rec.Body.String(), "test-token") {
		t.Fatalf("dashboard catalog: status %d, body %s", rec.Code, rec.Body)
	}
}

func TestAnyRouterClientAPIs(t *testing.T) {
	for _, tc := range []struct {
		path     string
		body     string
		response string
		stream   string
	}{
		{"/v1/messages", `{"model":"anyrouter/stealth/fledge-alpha","max_tokens":128,"messages":[{"role":"user","content":"Hello"}],"thinking":{"type":"enabled","budget_tokens":64}}`, `{"id":"msg-test","type":"message","role":"assistant","content":[{"type":"text","text":"Hello"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1},"anyrouter_metadata":{"provider":"opencode-zen"}}`, "event: message_stop\ndata: {\"type\":\"message_stop\",\"anyrouter_metadata\":{\"provider\":\"opencode-zen\"}}\n\n"},
		{"/v1/chat/completions", `{"model":"anyrouter/stealth/fledge-alpha","messages":[{"role":"user","content":"Hello"}],"reasoning_effort":"high","provider":{"order":["opencode-zen"]},"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object"}}}]}`, `{"id":"chat-test","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],"anyrouter_metadata":{"provider":"opencode-zen"}}`, "data: {\"id\":\"chat-test\",\"choices\":[{\"delta\":{\"content\":\"Hello\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
		{"/v1/responses", `{"model":"anyrouter/stealth/fledge-alpha","input":"Hello","reasoning":{"effort":"high"},"store":false,"tools":[{"type":"function","name":"weather","parameters":{"type":"object"}}]}`, `{"id":"resp-test","object":"response","status":"completed","output":[],"anyrouter_metadata":{"provider":"opencode-zen"}}`, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-test\",\"status\":\"completed\",\"output\":[]}}\n\nevent: response.anyrouter.metadata\ndata: {\"type\":\"response.anyrouter.metadata\",\"anyrouter_metadata\":{\"provider\":\"opencode-zen\"}}\n\n"},
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
				input["model"] = "stealth/fledge-alpha"
				response, contentType := tc.response, "application/json"
				if streaming {
					response, contentType = tc.stream, "text/event-stream"
				}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost || r.URL.Path != "/api"+tc.path {
						t.Errorf("upstream request = %s %s", r.Method, r.URL.Path)
					}
					var got map[string]any
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Error(err)
					}
					if !reflect.DeepEqual(got, input) {
						t.Errorf("forwarded body = %#v, want %#v", got, input)
					}
					if r.Header.Get("Authorization") != "Bearer test-token" {
						t.Error("missing upstream key")
					}
					w.Header().Set("Content-Type", contentType)
					_, _ = io.WriteString(w, response)
				}))
				defer upstream.Close()
				bc := config.BackendConfig{Type: "anyrouter", BaseURL: upstream.URL + "/api/v1/", APIKey: "test-token"}
				cfg := config.Config{Backends: []config.BackendConfig{bc}}
				cfg.Defaults()
				if err := cfg.Validate(); err != nil {
					t.Fatal(err)
				}
				c, err := backend.New(bc.Type, backend.Options{BaseURL: bc.BaseURL, APIKey: bc.APIKey})
				if err != nil {
					t.Fatal(err)
				}
				s := newTestServer(t, []backend.Backend{c}, bc)
				rec := postOpenAI(t, s, tc.path, string(request))
				if rec.Code != http.StatusOK || rec.Body.String() != response {
					t.Fatalf("status = %d, body = %s, want %s", rec.Code, rec.Body.String(), response)
				}
			})
		}
	}
}
