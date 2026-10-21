package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestGeneratedResponsesEnvelopeHTTP(t *testing.T) {
	for _, tc := range []struct {
		kind backend.Kind
		body string
	}{
		{backend.KindOpenAIChat, `{"id":"chat_1","created":123,"model":"provider-model","choices":[{"finish_reason":"stop","message":{"content":""}}]}`},
		{backend.KindAnthropic, `{"id":"msg_1","model":"provider-model","stop_reason":"end_turn","content":[{"type":"text","text":""}]}`},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			fb := &fakeOABackend{name: "upstream", body: tc.body, kinds: map[backend.Kind]bool{tc.kind: true}}
			s := newOATestServer(t, fb, map[string]config.ModelRoute{"alias": {Backend: "upstream", Model: "provider-model"}})
			request := `{"model":"alias","input":"hello","temperature":0,"top_p":0,"max_output_tokens":12,"parallel_tool_calls":false,"tool_choice":"none","tools":[]}`
			rec := postOpenAI(t, s, "/v1/responses", request)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{"object": `"response"`, "model": `"provider-model"`, "status": `"completed"`, "temperature": "0", "top_p": "0", "max_output_tokens": "12", "parallel_tool_calls": "false", "tool_choice": `"none"`, "tools": "[]", "error": "null", "incomplete_details": "null"} {
				if string(fields[key]) != want {
					t.Errorf("%s = %s, want %s", key, fields[key], want)
				}
			}
			var output []struct {
				ID      string `json:"id"`
				Status  string `json:"status"`
				Content []struct {
					Text        *string         `json:"text"`
					Annotations json.RawMessage `json:"annotations"`
				} `json:"content"`
			}
			if err := json.Unmarshal(fields["output"], &output); err != nil {
				t.Fatal(err)
			}
			if len(output) != 1 || output[0].ID == "" || output[0].Status != "completed" || len(output[0].Content) != 1 || output[0].Content[0].Text == nil || *output[0].Content[0].Text != "" || string(output[0].Content[0].Annotations) != "[]" {
				t.Fatalf("empty content envelope = %s", fields["output"])
			}
		})
	}
}

func TestResponsesToolIdentityHTTP(t *testing.T) {
	tools := `[{"type":"function","name":"space__run"},{"type":"namespace","name":"space","tools":[{"type":"function","name":"run"}]},{"type":"namespace","name":"space__","tools":[{"type":"function","name":"run"}]}]`
	for _, kind := range []backend.Kind{backend.KindOpenAIChat, backend.KindAnthropic} {
		for index, namespace := range []string{"", "space", "space__"} {
			t.Run(string(kind)+"/"+namespace, func(t *testing.T) {
				name := "run"
				if namespace == "" {
					name = "space__run"
				}
				choice := fmt.Sprintf(`{"type":"function","namespace":%q,"name":%q}`, namespace, name)
				request := fmt.Sprintf(`{"model":"alias","input":[{"role":"user","content":"run"},{"type":"function_call","call_id":"previous","namespace":%q,"name":%q,"arguments":"{}"},{"type":"function_call_output","call_id":"previous","output":"ok"}],"tools":%s,"tool_choice":%s}`, namespace, name, tools, choice)
				body := `{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`
				if kind == backend.KindAnthropic {
					body = `{"stop_reason":"end_turn","content":[{"type":"text","text":"ok"}]}`
				}
				fb := &fakeOABackend{name: "upstream", body: body, kinds: map[backend.Kind]bool{kind: true}}
				s := newOATestServer(t, fb, map[string]config.ModelRoute{"alias": {Backend: "upstream", Model: "provider-model"}})
				rec := postOpenAI(t, s, "/v1/responses", request)
				if rec.Code != http.StatusOK {
					t.Fatalf("probe status = %d: %s", rec.Code, rec.Body.String())
				}
				var upstream struct {
					Tools []struct {
						Name     string
						Function struct{ Name string }
					}
					ToolChoice struct {
						Name     string
						Function struct{ Name string }
					} `json:"tool_choice"`
					Messages []struct {
						Role      string
						Content   json.RawMessage
						ToolCalls []struct {
							Function struct{ Name string }
						} `json:"tool_calls"`
					}
				}
				if err := json.Unmarshal(fb.lastRequest().RawBody, &upstream); err != nil {
					t.Fatal(err)
				}
				if len(upstream.Tools) != 3 || len(upstream.Messages) != 3 {
					t.Fatalf("declarations/history changed: %s", fb.lastRequest().RawBody)
				}
				seen := map[string]bool{}
				for _, tool := range upstream.Tools {
					wire := tool.Name
					if kind == backend.KindOpenAIChat {
						wire = tool.Function.Name
					}
					if wire == "" || seen[wire] {
						t.Fatalf("colliding wire declarations: %s", fb.lastRequest().RawBody)
					}
					seen[wire] = true
				}
				wire := upstream.Tools[index].Name
				history := ""
				forced := upstream.ToolChoice.Name
				if kind == backend.KindOpenAIChat {
					wire = upstream.Tools[index].Function.Name
					forced = upstream.ToolChoice.Function.Name
					if len(upstream.Messages[1].ToolCalls) != 1 {
						t.Fatalf("missing historical call: %s", fb.lastRequest().RawBody)
					}
					history = upstream.Messages[1].ToolCalls[0].Function.Name
					fb.body = fmt.Sprintf(`{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"id":"next","type":"function","function":{"name":%q,"arguments":"{}"}}]}}]}`, wire)
				} else {
					var blocks []struct{ Name string }
					if err := json.Unmarshal(upstream.Messages[1].Content, &blocks); err != nil || len(blocks) != 1 {
						t.Fatalf("missing historical call: %s", fb.lastRequest().RawBody)
					}
					history = blocks[0].Name
					fb.body = fmt.Sprintf(`{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"next","name":%q,"input":{}}]}`, wire)
				}
				if history != wire || forced != wire {
					t.Fatalf("declaration %q, history %q, choice %q", wire, history, forced)
				}
				rec = postOpenAI(t, s, "/v1/responses", request)
				if rec.Code != http.StatusOK {
					t.Fatalf("reply status = %d: %s", rec.Code, rec.Body.String())
				}
				var response struct {
					Tools      json.RawMessage
					ToolChoice json.RawMessage `json:"tool_choice"`
					Output     []struct {
						Name, Namespace string
						CallID          string `json:"call_id"`
					}
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if len(response.Output) != 1 || response.Output[0].Name != name || response.Output[0].Namespace != namespace || response.Output[0].CallID != "next" {
					t.Fatalf("reply identity changed: %s", rec.Body.String())
				}
				for key, pair := range map[string][2]string{"tools": {tools, string(response.Tools)}, "tool_choice": {choice, string(response.ToolChoice)}} {
					var want, got any
					if err := json.Unmarshal([]byte(pair[0]), &want); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal([]byte(pair[1]), &got); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(want, got) {
						t.Errorf("%s leaked wire aliases: %s", key, pair[1])
					}
				}
			})
		}
	}
}

func TestUnsupportedBufferedOutputHTTPBoundary(t *testing.T) {
	for _, tc := range []struct {
		kind                backend.Kind
		path, request, body string
	}{
		{backend.KindOpenAIChat, "/v1/chat/completions", `{"model":"alias","messages":[{"role":"user","content":"hello"}]}`, ` {"id":"chat_1","choices":[{"finish_reason":"stop","message":{"audio":{"id":"audio"}}}]} `},
		{backend.KindOpenAIResponses, "/v1/responses", `{"model":"alias","input":"hello"}`, ` {"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"x","annotations":[{"type":"url_citation"}]}]}]} `},
		{backend.KindAnthropic, "/v1/messages", `{"model":"alias","max_tokens":12,"messages":[{"role":"user","content":"hello"}]}`, ` {"id":"msg_1","stop_reason":"end_turn","content":[{"type":"redacted_thinking","data":"opaque"}]} `},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			for _, native := range []bool{false, true} {
				fb := &fakeOABackend{name: "upstream", body: tc.body, kinds: map[backend.Kind]bool{tc.kind: true}}
				s := newOATestServer(t, fb, map[string]config.ModelRoute{"alias": {Backend: "upstream", Model: "provider-model"}})
				path, request := tc.path, tc.request
				if !native {
					path, request = "/v1/responses", `{"model":"alias","input":"hello"}`
					if tc.kind == backend.KindOpenAIResponses {
						path, request = "/v1/chat/completions", `{"model":"alias","messages":[{"role":"user","content":"hello"}]}`
					}
				}
				rec := postOpenAI(t, s, path, request)
				if native {
					if rec.Code != http.StatusOK || rec.Body.String() != tc.body {
						t.Fatalf("native output changed: %d %s", rec.Code, rec.Body.String())
					}
				} else if rec.Code != http.StatusBadGateway {
					t.Fatalf("unsupported output converted: %d %s", rec.Code, rec.Body.String())
				}
			}
		})
	}
}
