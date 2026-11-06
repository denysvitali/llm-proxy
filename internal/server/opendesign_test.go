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

func TestOpenDesignCatalogAndAlias(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing catalog/inference authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"deepseek-v4.1-flash"}]}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		if got["model"] != "deepseek-v4.1-flash" {
			t.Errorf("upstream model = %v", got["model"])
		}
		_, _ = io.WriteString(w, `{"id":"chat-test","choices":[{"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	bc := config.BackendConfig{Type: "opendesign", BaseURL: upstream.URL + "/v1", APIKey: "test-token"}
	c, err := backend.New(bc.Type, backend.Options{BaseURL: bc.BaseURL, APIKey: bc.APIKey})
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, []backend.Backend{c}, bc)
	s.cfg.Routes = map[string]config.ModelRoute{"sonnet": {Backend: "opendesign", Model: "deepseek-v4.1-flash"}}
	rec, list := getModels(t, s, "/v1/models")
	if rec.Code != http.StatusOK || len(list.Data) != 1 || list.Data[0].ID != "opendesign/deepseek-v4.1-flash" {
		t.Fatalf("model discovery: status %d, list %+v", rec.Code, list)
	}
	for _, model := range []string{"opendesign/deepseek-v4.1-flash", "deepseek-v4.1-flash", "sonnet"} {
		rec := postOpenAI(t, s, "/v1/chat/completions", fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Hello"}]}`, model))
		if rec.Code != http.StatusOK {
			t.Fatalf("route %s: status %d, body %s", model, rec.Code, rec.Body)
		}
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "opendesign/deepseek-v4.1-flash") || strings.Contains(rec.Body.String(), "test-token") {
		t.Fatalf("dashboard catalog: status %d, body %s", rec.Code, rec.Body)
	}
}

// TestOpenDesignClientAPIs checks the OpenAI endpoints, which pass through
// natively. Messages clients are translated; see TestOpenDesignMessagesTranslated.
func TestOpenDesignClientAPIs(t *testing.T) {
	for _, tc := range []struct {
		path     string
		body     string
		response string
		stream   string
	}{
		{"/v1/chat/completions", `{"model":"opendesign/deepseek-v4.1-flash","messages":[{"role":"user","content":"Hello"}],"reasoning_effort":"high","provider":{"order":["opencode-zen"]},"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object"}}}]}`, `{"id":"chat-test","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],"amr_metadata":{"provider":"opencode-zen"}}`, "data: {\"id\":\"chat-test\",\"choices\":[{\"delta\":{\"content\":\"Hello\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
		{"/v1/responses", `{"model":"opendesign/deepseek-v4.1-flash","input":"Hello","reasoning":{"effort":"high"},"store":false,"tools":[{"type":"function","name":"weather","parameters":{"type":"object"}}]}`, `{"id":"resp-test","object":"response","status":"completed","output":[],"amr_metadata":{"provider":"opencode-zen"}}`, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-test\",\"status\":\"completed\",\"output\":[]}}\n\nevent: response.amr.metadata\ndata: {\"type\":\"response.amr.metadata\",\"amr_metadata\":{\"provider\":\"opencode-zen\"}}\n\n"},
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
				input["model"] = "deepseek-v4.1-flash"
				response, contentType := tc.response, "application/json"
				if streaming {
					response, contentType = tc.stream, "text/event-stream"
				}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost || r.URL.Path != tc.path {
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
				bc := config.BackendConfig{Type: "opendesign", BaseURL: upstream.URL + "/v1/", APIKey: "test-token"}
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

// Open Design's own Messages endpoint fails tool_reference blocks with an
// HTML 502 and drops message_stop at max_tokens, so Messages clients go
// through Chat Completions: references become text and a length stop
// becomes a clean max_tokens ending.
func TestOpenDesignMessagesTranslated(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("upstream request = %s %s, want POST /v1/chat/completions", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "tool_reference") || !strings.Contains(string(body), "Tool Weather is now available.") {
			t.Errorf("tool_reference not inlined: %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"reasoning_content\":\"hm\"}}]}\n\n"+
			"data: {\"id\":\"c\",\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":16}}\n\n"+
			"data: [DONE]\n\n")
	}))
	defer upstream.Close()
	bc := config.BackendConfig{Type: "opendesign", BaseURL: upstream.URL + "/v1", APIKey: "test-token"}
	c, err := backend.New(bc.Type, backend.Options{BaseURL: bc.BaseURL, APIKey: bc.APIKey})
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, []backend.Backend{c}, bc)
	rec := postOpenAI(t, s, "/v1/messages", `{"model":"opendesign/deepseek-v4.1-flash","max_tokens":16,"stream":true,`+
		`"thinking":{"type":"enabled","budget_tokens":8},`+
		`"tools":[{"name":"ToolSearch","input_schema":{"type":"object"}},{"name":"Weather","defer_loading":true,"input_schema":{"type":"object"}}],`+
		`"messages":[{"role":"user","content":"load weather"},`+
		`{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"ToolSearch","input":{}}]},`+
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"tool_reference","tool_name":"Weather"}]}]}]}`)
	out := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(out, `"stop_reason":"max_tokens"`) ||
		!strings.Contains(out, "event: message_stop") || strings.Contains(out, "interrupted") {
		t.Fatalf("status = %d, body = %s", rec.Code, out)
	}
}
