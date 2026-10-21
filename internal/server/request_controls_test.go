package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestTranslatedControlRejectionPreservesNativeRequests(t *testing.T) {
	for _, api := range []struct {
		path, input string
		kind        backend.Kind
	}{
		{"/v1/chat/completions", `"messages":[{"role":"user","content":"hi"}]`, backend.KindOpenAIChat},
		{"/v1/responses", `"input":"hi"`, backend.KindOpenAIResponses},
	} {
		for _, control := range []string{`"previous_response_id":"prior"`, `"store":false`, `"tool_choice":"provider_extension"`, `"provider_extension":false`} {
			for _, native := range []bool{false, true} {
				name := api.path + "/" + control + "/translated"
				kind := backend.KindAnthropic
				if native {
					name = api.path + "/" + control + "/native"
					kind = api.kind
				}
				t.Run(name, func(t *testing.T) {
					fb := &fakeOABackend{name: "upstream", body: `{"ok":true}`, kinds: map[backend.Kind]bool{kind: true}}
					s := newOATestServer(t, fb, map[string]config.ModelRoute{"alias": {Backend: "upstream", Model: "model"}})
					body := `{"model":"alias",` + api.input + `,` + control + `}`
					rec := postOpenAI(t, s, api.path, body)
					if !native {
						if rec.Code != http.StatusBadRequest || fb.lastRequest() != nil {
							t.Fatalf("unsupported control dispatched: %d %s", rec.Code, rec.Body.String())
						}
						return
					}
					if rec.Code != http.StatusOK || fb.lastRequest() == nil {
						t.Fatalf("native request rejected: %d %s", rec.Code, rec.Body.String())
					}
					if got := string(fb.lastRequest().RawBody); got != strings.Replace(body, `"alias"`, `"model"`, 1) {
						t.Fatalf("native bytes changed: %s", got)
					}
				})
			}
		}
	}
}

func TestMessagesTranslationBoundariesPreserveNative(t *testing.T) {
	for _, extra := range []string{
		`"safeguards":{"example":true}`,
		`"output_config":{"format":{"type":"json_schema","schema":{"type":"object"}}}`,
		`"system":[{"type":"text","text":"system","cache_control":{"type":"ephemeral"}}]`,
		`"tools":[{"name":"f","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}]`,
	} {
		for _, kind := range []backend.Kind{backend.KindAnthropic, backend.KindOpenAIChat, backend.KindOpenAIResponses} {
			t.Run(string(kind)+"/"+extra, func(t *testing.T) {
				fb := &fakeOABackend{name: "upstream", body: `{"ok":true}`, kinds: map[backend.Kind]bool{kind: true}}
				s := newOATestServer(t, fb, map[string]config.ModelRoute{"alias": {Backend: "upstream", Model: "model"}})
				body := `{"model":"alias","max_tokens":16,"messages":[{"role":"user","content":"hi"}],` + extra + `}`
				rec := postOpenAI(t, s, "/v1/messages", body)
				if kind != backend.KindAnthropic {
					if rec.Code != http.StatusBadRequest || fb.lastRequest() != nil {
						t.Fatalf("unsupported input dispatched: %d %s", rec.Code, rec.Body.String())
					}
					return
				}
				if rec.Code != http.StatusOK || fb.lastRequest() == nil {
					t.Fatalf("native request rejected: %d %s", rec.Code, rec.Body.String())
				}
				if got := string(fb.lastRequest().RawBody); got != strings.Replace(body, `"alias"`, `"model"`, 1) {
					t.Fatalf("native bytes changed: %s", got)
				}
			})
		}
	}
}

func TestTranslatedRequestFieldsPreserveNative(t *testing.T) {
	for _, api := range []struct {
		path, input string
		kind        backend.Kind
	}{
		{"/v1/chat/completions", `"messages":[{"role":"user","content":"hi","name":"speaker"}]`, backend.KindOpenAIChat},
		{"/v1/chat/completions", `"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","provider_extension":false}}]`, backend.KindOpenAIChat},
		{"/v1/responses", `"input":[{"type":"message","id":"msg_prior","role":"user","content":[{"type":"input_text","text":"hi","provider_ext":1}]}]`, backend.KindOpenAIResponses},
		{"/v1/responses", `"input":[{"type":"function_call","id":"fc_1","call_id":"call","name":"f","arguments":"{}","status":"completed","seed":1}]`, backend.KindOpenAIResponses},
	} {
		for _, kind := range []backend.Kind{backend.KindAnthropic, backend.KindOpenAIChat, backend.KindOpenAIResponses} {
			t.Run(api.path+"/"+api.input+"/"+string(kind), func(t *testing.T) {
				fb := &fakeOABackend{name: "upstream", body: `{"ok":true}`, kinds: map[backend.Kind]bool{kind: true}}
				s := newOATestServer(t, fb, map[string]config.ModelRoute{"alias": {Backend: "upstream", Model: "model"}})
				body := `{"model":"alias",` + api.input + `}`
				rec := postOpenAI(t, s, api.path, body)
				if kind != api.kind {
					if rec.Code != http.StatusBadRequest || fb.lastRequest() != nil {
						t.Fatalf("unsupported fields dispatched: %d %s", rec.Code, rec.Body.String())
					}
					return
				}
				if rec.Code != http.StatusOK || fb.lastRequest() == nil {
					t.Fatalf("native request rejected: %d %s", rec.Code, rec.Body.String())
				}
				if got := string(fb.lastRequest().RawBody); got != strings.Replace(body, `"alias"`, `"model"`, 1) {
					t.Fatalf("native bytes changed: %s", got)
				}
			})
		}
	}
}

func TestRequestPipelineGenerationControls(t *testing.T) {
	for _, api := range []struct {
		path, input, limit string
		kind               backend.Kind
	}{
		{"/v1/messages", `"messages":[{"role":"user","content":"hello"}]`, "max_tokens", backend.KindAnthropic},
		{"/v1/chat/completions", `"messages":[{"role":"user","content":"hello"}]`, "max_completion_tokens", backend.KindOpenAIChat},
		{"/v1/responses", `"input":"hello"`, "max_output_tokens", backend.KindOpenAIResponses},
	} {
		for _, controls := range []struct {
			name, fields string
			valid        bool
		}{
			{"zero sampling", `"temperature":0,"top_p":0`, true},
			{"negative temperature", `"temperature":-1`, false},
			{"excess top_p", `"top_p":2`, false},
			{"zero token limit", `"` + api.limit + `":0`, false},
			{"fractional token limit", `"` + api.limit + `":1.5`, false},
			{"one token limit", `"` + api.limit + `":1`, true},
		} {
			t.Run(string(api.kind)+"/"+controls.name, func(t *testing.T) {
				fb := &fakeOABackend{name: "native", body: `{"ok":true}`, kinds: map[backend.Kind]bool{api.kind: true}}
				s := newOATestServer(t, fb, map[string]config.ModelRoute{"alias": {Backend: "native", Model: "upstream"}})
				body := `{"model":"alias","stream":false,` + api.input + `,` + controls.fields + `}`
				rec := postOpenAI(t, s, api.path, body)
				if controls.valid {
					if rec.Code != http.StatusOK {
						t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
					}
					request := fb.lastRequest()
					if request == nil || string(request.RawBody) != strings.Replace(body, `"alias"`, `"upstream"`, 1) {
						t.Fatalf("valid native bytes changed: %+v", request)
					}
					return
				}
				if rec.Code != http.StatusBadRequest || fb.lastRequest() != nil {
					t.Fatalf("invalid controls dispatched or accepted: status = %d, body = %s", rec.Code, rec.Body.String())
				}
				var response struct {
					Type  string `json:"type"`
					Error struct {
						Type string `json:"type"`
					} `json:"error"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if response.Error.Type != "invalid_request_error" || api.kind == backend.KindAnthropic && response.Type != "error" {
					t.Fatalf("incorrect client error envelope: %s", rec.Body.String())
				}
			})
		}
	}
}
