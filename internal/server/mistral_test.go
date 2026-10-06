package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestMistralVibeStampsCallSourceThroughProxy(t *testing.T) {
	for _, tc := range []struct {
		provider string
		stamped  bool // whether metadata.call_source=vibe_code reaches the upstream
		vibeUA   bool // whether the Vibe User-Agent reaches the upstream
	}{
		{provider: "mistral-vibe", stamped: true, vibeUA: true},
		{provider: "mistral", stamped: false, vibeUA: false},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var sent map[string]any
				if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
					t.Error(err)
					return
				}
				metadata, _ := sent["metadata"].(map[string]any)
				callSource, _ := metadata["call_source"].(string)
				if got := callSource == "vibe_code"; got != tc.stamped {
					t.Errorf("metadata.call_source=vibe_code stamped=%t at upstream, want %t (body: %v)", got, tc.stamped, sent["metadata"])
				}
				if gotUA := strings.Contains(r.Header.Get("User-Agent"), "Mistral-Vibe"); gotUA != tc.vibeUA {
					t.Errorf("User-Agent %q contains Mistral-Vibe=%t, want %t", r.Header.Get("User-Agent"), gotUA, tc.vibeUA)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"x","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
			}))
			defer upstream.Close()
			bc := config.BackendConfig{Type: tc.provider, BaseURL: upstream.URL + "/v1", APIKey: "secret"}
			b, err := backend.New(tc.provider, backend.Options{BaseURL: bc.BaseURL, APIKey: bc.APIKey})
			if err != nil {
				t.Fatal(err)
			}
			s := newTestServer(t, []backend.Backend{b}, bc)
			rec := postOpenAI(t, s, "/v1/chat/completions", fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, tc.provider+"/mistral-large-latest"))
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
		})
	}
}

func TestMistralClientAPIs(t *testing.T) {
	clients := []struct {
		path, body, responseEnd string
	}{
		{"/v1/chat/completions", `"messages":[{"role":"developer","content":"Be concise"},{"role":"user","content":"Hello"}],"max_completion_tokens":32,"stream_options":{"include_usage":true},"parallel_tool_calls":true`, "[DONE]"},
		{"/v1/messages", `"max_tokens":32,"messages":[{"role":"user","content":"Hello"}]`, "message_stop"},
		{"/v1/responses", `"input":"Hello","max_output_tokens":32`, "response.completed"},
	}
	chatResponse := `{"id":"chatcmpl-test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"Hello from Mistral"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`
	chatStream := "data: {\"id\":\"chatcmpl-test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hello from Mistral\"}}]}\n\ndata: {\"id\":\"chatcmpl-test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	for _, provider := range []string{"mistral", "mistral-vibe"} {
		for _, client := range clients {
			for _, streaming := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%t", provider, client.path, streaming), func(t *testing.T) {
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
							t.Errorf("upstream request = %s %s; want POST /v1/chat/completions", r.Method, r.URL.Path)
						}
						if got := r.Header.Get("Authorization"); got != "Bearer mistral-test-secret" {
							t.Errorf("Authorization = %q", got)
						}
						var sent map[string]any
						if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
							t.Error(err)
							return
						}
						gotStream, _ := sent["stream"].(bool)
						if sent["model"] != "mistral-large-latest" || gotStream != streaming || sent["max_tokens"] != float64(32) {
							t.Errorf("forwarded model/stream/max_tokens = %v/%v/%v", sent["model"], sent["stream"], sent["max_tokens"])
						}
						if client.path == "/v1/chat/completions" {
							messages, _ := sent["messages"].([]any)
							if len(messages) < 2 || messages[0].(map[string]any)["role"] != "system" {
								t.Errorf("developer message was not converted to system: %#v", sent["messages"])
							}
						}
						if _, exists := sent["max_completion_tokens"]; exists {
							t.Error("max_completion_tokens was not translated to max_tokens")
						}
						if _, exists := sent["stream_options"]; exists {
							t.Error("stream_options leaked to Mistral")
						}
						if client.path == "/v1/chat/completions" && sent["parallel_tool_calls"] != true {
							t.Errorf("parallel_tool_calls = %v, want true", sent["parallel_tool_calls"])
						}
						response, contentType := chatResponse, "application/json"
						if streaming {
							response, contentType = chatStream, "text/event-stream"
						}
						w.Header().Set("Content-Type", contentType)
						_, _ = io.WriteString(w, response)
					}))
					defer upstream.Close()
					bc := config.BackendConfig{Type: provider, BaseURL: upstream.URL + "/v1", APIKey: "mistral-test-secret"}
					b, err := backend.New(provider, backend.Options{BaseURL: bc.BaseURL, APIKey: bc.APIKey})
					if err != nil {
						t.Fatal(err)
					}
					s := newTestServer(t, []backend.Backend{b}, bc)
					body := fmt.Sprintf(`{"model":%q,"stream":%t,%s}`, provider+"/mistral-large-latest", streaming, client.body)
					rec := postOpenAI(t, s, client.path, body)
					if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Hello from Mistral") {
						t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
					}
					if streaming {
						if rec.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(rec.Body.String(), client.responseEnd) {
							t.Fatalf("incomplete stream: %s", rec.Body)
						}
					} else if !json.Valid(rec.Body.Bytes()) {
						t.Fatalf("invalid JSON: %s", rec.Body)
					}
					if client.path == "/v1/chat/completions" && rec.Body.String() != responseFor(streaming, chatResponse, chatStream) {
						t.Fatal("native chat response was changed")
					}
				})
			}
		}
	}
}

func TestMistralTypedContentAcrossClientAPIs(t *testing.T) {
	clients := []struct {
		path, body, terminal, bufferedTerminal string
	}{
		{"/v1/chat/completions", `"messages":[{"role":"user","content":"Hello"}]`, "[DONE]", ""},
		{"/v1/messages", `"max_tokens":128,"thinking":{"type":"enabled","budget_tokens":64},"messages":[{"role":"user","content":"Hello"}]`, "message_stop", "end_turn"},
		{"/v1/responses", `"input":"Hello"`, "response.completed", "completed"},
	}
	buffered := `{"id":"chat-typed","object":"chat.completion","model":"mistral-large-latest","choices":[{"index":0,"message":{"role":"assistant","content":[{"type":"thinking","thinking":[{"type":"text","text":"Let me reason"}]},{"type":"text","text":"Hello from Mistral"}]},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":4}}`
	streamed := "data: {\"id\":\"chat-typed\",\"choices\":[{\"index\":0,\"delta\":{\"content\":[{\"type\":\"thinking\",\"thinking\":[{\"type\":\"text\",\"text\":\"Let me reason\"}]}]}}]}\n\n" +
		"data: {\"id\":\"chat-typed\",\"choices\":[{\"index\":0,\"delta\":{\"content\":[{\"type\":\"text\",\"text\":\"Hello from Mistral\"}]}}]}\n\n" +
		"data: {\"id\":\"chat-typed\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" + "data: [DONE]\n\n"

	for _, client := range clients {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", client.path, streaming), func(t *testing.T) {
				response, contentType := buffered, "application/json"
				if streaming {
					response, contentType = streamed, "text/event-stream"
				}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
						t.Errorf("upstream request = %s %s", r.Method, r.URL.Path)
					}
					w.Header().Set("Content-Type", contentType)
					_, _ = io.WriteString(w, response)
				}))
				defer upstream.Close()
				bc := config.BackendConfig{Type: "mistral", BaseURL: upstream.URL + "/v1", APIKey: "mistral-test-secret"}
				b, err := backend.New(bc.Type, backend.Options{BaseURL: bc.BaseURL, APIKey: bc.APIKey})
				if err != nil {
					t.Fatal(err)
				}
				s := newTestServer(t, []backend.Backend{b}, bc)
				body := fmt.Sprintf(`{"model":"mistral/mistral-large-latest","stream":%t,%s}`, streaming, client.body)
				rec := postOpenAI(t, s, client.path, body)
				if rec.Code != http.StatusOK {
					t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
				}
				if client.path == "/v1/chat/completions" {
					if rec.Body.String() != response {
						t.Fatalf("native response changed:\n got %s\nwant %s", rec.Body, response)
					}
					return
				}
				terminal := client.bufferedTerminal
				if streaming {
					terminal = client.terminal
				}
				if !strings.Contains(rec.Body.String(), "Hello from Mistral") || !strings.Contains(rec.Body.String(), terminal) {
					t.Fatalf("translated response missing answer or terminal %q: %s", terminal, rec.Body)
				}
			})
		}
	}
}

func responseFor(streaming bool, buffered, stream string) string {
	if streaming {
		return stream
	}
	return buffered
}

func TestMistralTranslatedToolResultsNormalizeIDs(t *testing.T) {
	for _, tc := range []struct {
		name, path, request string
	}{
		{"anthropic", "/v1/messages", `{"model":"mistral/mistral-large-latest","max_tokens":32,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01JLONGANTHROPICTOOLID","name":"lookup","input":{"q":"one"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01JLONGANTHROPICTOOLID","content":"found"}]}]}`},
		{"responses", "/v1/responses", `{"model":"mistral/mistral-large-latest","input":[{"type":"function_call","call_id":"call_01JLONGRESPONSESTOOLID","name":"lookup","arguments":"{\"q\":\"one\"}"},{"type":"function_call_output","call_id":"call_01JLONGRESPONSESTOOLID","output":"found"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" {
					t.Errorf("upstream path = %s", r.URL.Path)
				}
				var sent struct {
					Messages []struct {
						Role       string `json:"role"`
						ToolCallID string `json:"tool_call_id"`
						ToolCalls  []struct {
							ID string `json:"id"`
						} `json:"tool_calls"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
					t.Error(err)
					return
				}
				var callID, resultID string
				for _, message := range sent.Messages {
					if len(message.ToolCalls) != 0 {
						callID = message.ToolCalls[0].ID
					}
					if message.Role == "tool" {
						resultID = message.ToolCallID
					}
				}
				if !regexp.MustCompile(`^[A-Za-z0-9]{9}$`).MatchString(callID) || resultID != callID {
					t.Errorf("translated call/result IDs = %q/%q; want same 9-character alphanumeric ID", callID, resultID)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"chat-test","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
			}))
			defer upstream.Close()
			bc := config.BackendConfig{Type: "mistral", BaseURL: upstream.URL + "/v1", APIKey: "secret"}
			b, err := backend.New(bc.Type, backend.Options{BaseURL: bc.BaseURL, APIKey: bc.APIKey})
			if err != nil {
				t.Fatal(err)
			}
			s := newTestServer(t, []backend.Backend{b}, bc)
			rec := postOpenAI(t, s, tc.path, tc.request)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
		})
	}
}

func TestMistralCatalogRoutingAndOverview(t *testing.T) {
	const secret = "mistral-api-key-must-not-leak"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"mistral-large-latest","aliases":["mistral-large"],"capabilities":{"completion_chat":true}},{"id":"mistral-embed","capabilities":{"completion_chat":false}},{"id":"mistral-archived","capabilities":{"completion_chat":true},"archived":true}]}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("upstream request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"chat-test","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	bc := config.BackendConfig{Type: "mistral", BaseURL: upstream.URL + "/v1", APIKey: secret}
	b, err := backend.New(bc.Type, backend.Options{BaseURL: bc.BaseURL, APIKey: bc.APIKey})
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, []backend.Backend{b}, bc)
	s.cfg.Routes = map[string]config.ModelRoute{"mistral-short": {Backend: "mistral", Model: "mistral-large-latest"}}
	rec, catalog := getModels(t, s, "/v1/models?backend=mistral")
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", rec.Code, rec.Body)
	}
	ids := map[string]bool{}
	for _, model := range catalog.Data {
		ids[model.ID] = true
	}
	for _, want := range []string{"mistral/mistral-large-latest", "mistral/mistral-large"} {
		if !ids[want] {
			t.Errorf("catalog missing %q: %+v", want, catalog.Data)
		}
	}
	if ids["mistral/mistral-embed"] || ids["mistral/mistral-archived"] {
		t.Errorf("catalog included non-chat or archived model: %+v", catalog.Data)
	}
	for _, model := range []string{"mistral/mistral-large-latest", "mistral-large-latest", "mistral-short"} {
		rec := postOpenAI(t, s, "/v1/chat/completions", fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, model))
		if rec.Code != http.StatusOK {
			t.Errorf("route %q status=%d body=%s", model, rec.Code, rec.Body)
		}
	}
	info, page := renderOverview(t, s, nil)
	if info.Code != http.StatusOK || !strings.Contains(info.Body.String(), `"hasKey":true`) || strings.Contains(info.Body.String(), secret) {
		t.Fatalf("overview key metadata/status invalid: status=%d body=%s", info.Code, info.Body)
	}
	backends, ok := page["backends"].([]any)
	if !ok || len(backends) != 1 || backends[0].(map[string]any)["name"] != "mistral" {
		t.Errorf("overview backend metadata = %#v", page["backends"])
	}
}
