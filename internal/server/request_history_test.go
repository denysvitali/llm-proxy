package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestResponsesOpaqueHistoryAndToolsRequireNative(t *testing.T) {
	for _, fields := range []string{
		`"input":[{"role":"user","content":"hi"},{"type":"reasoning","encrypted_content":"opaque"}]`,
		`"input":[{"role":"user","content":"hi"},{"type":"additional_tools","tools":[{"type":"web_search"}]}]`,
		`"input":[{"type":"custom_tool_call","call_id":"c1","name":"shell","input":"ls"},{"type":"custom_tool_call_output","call_id":"c1","output":"ok"}]`,
	} {
		for _, wire := range []backend.Kind{backend.KindAnthropic, backend.KindOpenAIChat, backend.KindOpenAIResponses} {
			t.Run(string(wire)+"/"+fields, func(t *testing.T) {
				fb := &fakeOABackend{name: "upstream", body: `{"status":"completed","output":[]}`, kinds: map[backend.Kind]bool{wire: true}}
				s := newOATestServer(t, fb, map[string]config.ModelRoute{"alias": {Backend: "upstream", Model: "model"}})
				body := `{"model":"alias",` + fields + `}`
				rec := postOpenAI(t, s, "/v1/responses", body)
				if wire != backend.KindOpenAIResponses {
					if rec.Code != http.StatusBadRequest || fb.lastRequest() != nil {
						t.Fatalf("unsupported translation dispatched: %d %s", rec.Code, rec.Body.String())
					}
					return
				}
				if rec.Code != http.StatusOK || fb.lastRequest() == nil {
					t.Fatalf("native request rejected: %d %s", rec.Code, rec.Body.String())
				}
				want := strings.Replace(body, `"model":"alias"`, `"model":"model"`, 1)
				if string(fb.lastRequest().RawBody) != want {
					t.Fatalf("native request changed: %s", fb.lastRequest().RawBody)
				}
			})
		}
	}
}

func TestTranslatedHistoryIdentityBoundary(t *testing.T) {
	for _, input := range []struct {
		path           string
		kind           backend.Kind
		valid, invalid string
	}{
		{"/v1/messages", backend.KindAnthropic,
			`"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"test","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"ok"}]}]`,
			`"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"missing","content":"ok"}]}]`},
		{"/v1/chat/completions", backend.KindOpenAIChat,
			`"messages":[{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"test","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"ok"}]`,
			`"messages":[{"role":"tool","tool_call_id":"missing","content":"ok"}]`},
		{"/v1/responses", backend.KindOpenAIResponses,
			`"input":[{"type":"function_call","call_id":"call_1","name":"test","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]`,
			`"input":[{"type":"function_call_output","call_id":"missing","output":"ok"}]`},
	} {
		for _, wire := range []backend.Kind{backend.KindAnthropic, backend.KindOpenAIChat, backend.KindOpenAIResponses} {
			if input.kind == wire {
				continue
			}
			for _, valid := range []bool{false, true} {
				label := "orphan"
				fields := input.invalid
				if valid {
					label, fields = "matched", input.valid
				}
				t.Run(string(input.kind)+"/"+string(wire)+"/"+label, func(t *testing.T) {
					response := `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`
					if wire == backend.KindOpenAIChat {
						response = `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`
					}
					if wire == backend.KindOpenAIResponses {
						response = `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`
					}
					fb := &fakeOABackend{name: "upstream", body: response, kinds: map[backend.Kind]bool{wire: true}}
					s := newOATestServer(t, fb, map[string]config.ModelRoute{"alias": {Backend: "upstream", Model: "model"}})
					rec := postOpenAI(t, s, input.path, `{"model":"alias",`+fields+`}`)
					if valid {
						if rec.Code != http.StatusOK || fb.lastRequest() == nil {
							t.Fatalf("matched history rejected: %d %s", rec.Code, rec.Body.String())
						}
					} else if rec.Code != http.StatusBadRequest || fb.lastRequest() != nil || !strings.Contains(rec.Body.String(), "missing") {
						t.Fatalf("orphan history dispatched: %d %s", rec.Code, rec.Body.String())
					}
				})
			}
		}
	}
}

func TestClientExecutedToolsDegradeToFunction(t *testing.T) {
	for _, tools := range []string{
		// Codex sends non-function tools and function tools with client-side
		// extras on every turn; a chat or Messages backend is expected to serve
		// the remaining function surface instead of failing the exchange.
		`[{"type":"web_search"},{"type":"custom","name":"shell"}]`,
		`[{"type":"function","name":"multi_agent_v1","tools":[{"name":"sub"}],"parameters":{"type":"object"},"strict":false}]`,
	} {
		for _, wire := range []backend.Kind{backend.KindAnthropic, backend.KindOpenAIChat} {
			t.Run(string(wire)+"/"+tools, func(t *testing.T) {
				body := `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
				if wire == backend.KindAnthropic {
					body = `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`
				}
				fb := &fakeOABackend{name: "upstream", body: body, kinds: map[backend.Kind]bool{wire: true}}
				s := newOATestServer(t, fb, map[string]config.ModelRoute{"alias": {Backend: "upstream", Model: "model"}})
				rec := postOpenAI(t, s, "/v1/responses", `{"model":"alias","input":"hi","tools":`+tools+`}`)
				if rec.Code != http.StatusOK || fb.lastRequest() == nil {
					t.Fatalf("tool degration failed: %d %s", rec.Code, rec.Body.String())
				}
				var sent struct {
					Tools []json.RawMessage `json:"tools"`
				}
				if err := json.Unmarshal(fb.lastRequest().RawBody, &sent); err != nil {
					t.Fatal(err)
				}
				for _, raw := range sent.Tools {
					var tool map[string]any
					if err := json.Unmarshal(raw, &tool); err != nil {
						t.Fatal(err)
					}
					// Either a chat function tool or a Messages tool entry; only
					// the function surface must survive, so no client-side extras
					// (multi_agent's nested tool list, custom/web markers) may.
					if wire == backend.KindAnthropic && tool["name"] == nil || tool["tools"] != nil || tool["type"] != nil && tool["type"] != "function" {
						t.Fatalf("non-function tool forwarded: %s", fb.lastRequest().RawBody)
					}
				}
			})
		}
	}
}
