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
	"github.com/denysvitali/llm-proxy/internal/backend/cloudflare"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestCloudflareMessagesUsesChatWire(t *testing.T) {
	c, err := cloudflare.New("http://localhost/ai/v1", "test-token")
	if err != nil {
		t.Fatal(err)
	}
	wire, ok := resolveWire(backend.KindAnthropic, c, "stealth/union-alpha")
	if !ok || wire.native || wire.path == nil || wire.path.kind != backend.KindOpenAIChat {
		t.Fatalf("Messages wire = %+v, available = %t; want translated Chat wire", wire, ok)
	}
}

func TestCloudflareMessagesToolTranslation(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", streaming), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/ai/v1/chat/completions" {
					t.Errorf("upstream request = %s %s; want Chat Completions", r.Method, r.URL.Path)
				}
				var got map[string]any
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
					return
				}
				var want map[string]any
				if err := json.Unmarshal([]byte(`{
					"tools":[{"type":"function","function":{"name":"get_weather","description":"Get weather.","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}],
					"tool_choice":{"type":"function","function":{"name":"get_weather"}},
					"messages":[{"role":"user","content":"Get weather in Rome."},{"role":"assistant","tool_calls":[{"index":0,"id":"toolu_previous","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Rome\"}"}}]},{"role":"tool","tool_call_id":"toolu_previous","content":"Sunny"},{"role":"user","content":"Now get weather in Paris."}]
				}`), &want); err != nil {
					t.Error(err)
					return
				}
				for key, value := range want {
					if !reflect.DeepEqual(got[key], value) {
						t.Errorf("forwarded %s = %#v, want %#v", key, got[key], value)
					}
				}
				gotStreaming, _ := got["stream"].(bool)
				if got["model"] != "stealth/union-alpha" || gotStreaming != streaming || got["max_tokens"] != float64(128) {
					t.Errorf("forwarded request = %#v", got)
				}
				if r.Header.Get("Anthropic-Version") != "" {
					t.Error("Anthropic header leaked onto Chat wire")
				}
				if streaming {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, strings.Join([]string{
						`data: {"id":"chat-test","object":"chat.completion.chunk","choices":[{"delta":{"role":"assistant"}}]}`,
						`data: {"id":"chat-test","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
						`data: {"id":"chat-test","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}`,
						`data: {"id":"chat-test","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]}}]}`,
						`data: {"id":"chat-test","choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
						`data: {"id":"chat-test","choices":[],"usage":{"prompt_tokens":20,"completion_tokens":8}}`,
						`data: [DONE]`, "",
					}, "\n\n"))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"chat-test","object":"chat.completion","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":8}}`)
			}))
			defer upstream.Close()
			c, err := cloudflare.New(upstream.URL+"/ai/v1", "test-token")
			if err != nil {
				t.Fatal(err)
			}
			defer c.HTTP.CloseIdleConnections()
			s := newTestServer(t, []backend.Backend{c}, config.BackendConfig{Type: "cloudflare"})
			request := fmt.Sprintf(`{"model":"cloudflare/stealth/union-alpha","max_tokens":128,"stream":%t,"messages":[{"role":"user","content":"Get weather in Rome."},{"role":"assistant","content":[{"type":"tool_use","id":"toolu_previous","name":"get_weather","input":{"city":"Rome"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_previous","content":"Sunny"},{"type":"text","text":"Now get weather in Paris."}]}],"tools":[{"name":"get_weather","description":"Get weather.","input_schema":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}],"tool_choice":{"type":"tool","name":"get_weather"}}`, streaming)
			rec := postOpenAI(t, s, "/v1/messages", request)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if streaming {
				body := rec.Body.String()
				for _, want := range []string{"event: message_start", "event: content_block_start", `"type":"tool_use"`, `"name":"get_weather"`, `"partial_json":"{\"city\":"`, `"partial_json":"\"Paris\"}"`, "event: content_block_stop", `"stop_reason":"tool_use"`, `"output_tokens":8`, "event: message_stop"} {
					if !strings.Contains(body, want) {
						t.Errorf("stream missing %q: %s", want, body)
					}
				}
				for _, forbidden := range []string{"chat.completion.chunk", "[DONE]", "event: error", "interrupted"} {
					if strings.Contains(body, forbidden) {
						t.Errorf("stream contains %q: %s", forbidden, body)
					}
				}
				if strings.Count(body, "event: message_stop\n") != 1 {
					t.Errorf("expected exactly one message_stop: %s", body)
				}
				return
			}
			var got map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			if err := json.Unmarshal([]byte(`{"type":"message","role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_call_1","name":"get_weather","input":{"city":"Paris"}}],"usage":{"input_tokens":20,"output_tokens":8}}`), &want); err != nil {
				t.Fatal(err)
			}
			for key, value := range want {
				if !reflect.DeepEqual(got[key], value) {
					t.Errorf("response %s = %#v, want %#v", key, got[key], value)
				}
			}
			if _, ok := got["choices"]; ok {
				t.Error("Chat choices leaked into Messages response")
			}
		})
	}
}

func TestCloudflareClientAPIs(t *testing.T) {
	for _, tc := range []struct {
		path     string
		body     string
		response string
		stream   string
	}{
		{"/v1/chat/completions", `{"model":"cloudflare/stealth/union-alpha","messages":[{"role":"user","content":"Capital of France?"}],"max_tokens":16,"provider_options":{"preserve":true}}`, `{"id":"chat-test","object":"chat.completion","choices":[{"message":{"content":"Paris"}}],"provider_field":"keep"}`, "data: {\"choices\":[{\"delta\":{\"content\":\"Paris\"}}],\"provider_field\":\"keep\"}\n\ndata: [DONE]\n\n"},
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
