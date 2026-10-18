package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestTranslatedRequestFieldBoundariesPreserveNative(t *testing.T) {
	for _, api := range []struct {
		path   string
		kind   backend.Kind
		fields []string
	}{
		{"/v1/chat/completions", backend.KindOpenAIChat, []string{
			`"messages":[{"role":"user","content":"hi","name":"speaker"}]`,
			`"messages":[{"role":"assistant","content":"hi","refusal":"no"}]`,
			`"messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}","extra":true}}]}]`,
			`"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","cache_control":{}}}]`,
			`"messages":[{"role":"user","content":"hi"}],"provider_extension":false`,
			`"messages":[{"role":"user","content":"hi"}],"stream_options":{"include_usage":true}`,
		}},
		{"/v1/responses", backend.KindOpenAIResponses, []string{
			`"input":[{"role":"user","content":"hi","extra":false}]`,
			`"input":[{"type":"message","role":"assistant","content":"hi","id":"item","status":"completed"}]`,
			`"input":[{"type":"function_call","call_id":"c","name":"f","arguments":"{}","content":"hi"}]`,
			`"input":[{"type":"function_call_output","call_id":"c","name":"f","output":"hi"}]`,
			`"input":"hi","provider_extension":0`,
		}},
	} {
		for _, fields := range api.fields {
			for _, wire := range []backend.Kind{backend.KindOpenAIChat, backend.KindOpenAIResponses, backend.KindAnthropic} {
				t.Run(api.path+"/"+string(wire)+"/"+fields, func(t *testing.T) {
					fb := &fakeOABackend{name: "upstream", body: `{"ok":true}`, kinds: map[backend.Kind]bool{wire: true}}
					s := newOATestServer(t, fb, map[string]config.ModelRoute{"alias": {Backend: "upstream", Model: "model"}})
					body := `{"model":"alias",` + fields + `}`
					rec := postOpenAI(t, s, api.path, body)
					if wire == api.kind {
						if rec.Code != http.StatusOK || fb.lastRequest() == nil {
							t.Fatalf("native rejected: %d %s", rec.Code, rec.Body.String())
						}
						if got := string(fb.lastRequest().RawBody); got != strings.Replace(body, `"alias"`, `"model"`, 1) {
							t.Fatalf("native bytes changed: %s", got)
						}
						return
					}
					if rec.Code != http.StatusBadRequest || fb.lastRequest() != nil {
						t.Fatalf("unsupported fields dispatched: %d %s", rec.Code, rec.Body.String())
					}
					var response struct {
						Error struct {
							Type string `json:"type"`
						} `json:"error"`
					}
					if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
						t.Fatal(err)
					}
					if response.Error.Type != "invalid_request_error" {
						t.Fatalf("wrong error dialect: %s", rec.Body.String())
					}
				})
			}
		}
	}
}

func TestResponsesInstructionsPresenceThroughHTTP(t *testing.T) {
	for _, wire := range []backend.Kind{backend.KindOpenAIChat, backend.KindOpenAIResponses, backend.KindAnthropic} {
		for _, value := range []string{"", "null", `""`, `" rules "`} {
			t.Run(string(wire)+"/"+value, func(t *testing.T) {
				response := `{"id":"r","object":"response","status":"completed","output":[]}`
				switch wire {
				case backend.KindOpenAIChat:
					response = `{"id":"c","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
				case backend.KindAnthropic:
					response = `{"id":"m","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`
				}
				fb := &fakeOABackend{name: "upstream", body: response, kinds: map[backend.Kind]bool{wire: true}}
				s := newOATestServer(t, fb, map[string]config.ModelRoute{"alias": {Backend: "upstream", Model: "model"}})
				body := `{"model":"alias","input":"hi"`
				if value != "" {
					body += `,"instructions":` + value
				}
				body += `}`
				rec := postOpenAI(t, s, "/v1/responses", body)
				request := fb.lastRequest()
				if rec.Code != http.StatusOK || request == nil {
					t.Fatalf("instruction request failed: %d %s", rec.Code, rec.Body.String())
				}
				if wire == backend.KindOpenAIResponses {
					if string(request.RawBody) != strings.Replace(body, `"alias"`, `"model"`, 1) {
						t.Fatalf("native bytes changed: %s", request.RawBody)
					}
					return
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(request.RawBody, &fields); err != nil {
					t.Fatal(err)
				}
				instruction := fields["system"]
				if wire == backend.KindOpenAIChat {
					var messages []struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					}
					if err := json.Unmarshal(fields["messages"], &messages); err != nil {
						t.Fatal(err)
					}
					if len(messages) > 0 && messages[0].Role == "system" {
						instruction = messages[0].Content
					}
				}
				if value == "" || value == "null" {
					if len(instruction) != 0 {
						t.Fatalf("absent instructions invented: %s", request.RawBody)
					}
				} else if string(instruction) != value {
					t.Fatalf("instructions changed: %s", request.RawBody)
				}
			})
		}
	}
}
