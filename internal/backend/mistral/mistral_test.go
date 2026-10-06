package mistral

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

func TestRegistrationAndDefaults(t *testing.T) {
	for _, name := range []string{"mistral", "mistral-vibe"} {
		b, err := backend.New(name, backend.Options{APIKey: "secret"})
		if err != nil {
			t.Fatal(err)
		}
		client := b.(*Client)
		if client.Name() != name || client.BaseURL != defaultBaseURL {
			t.Fatalf("%s: name=%s base=%s", name, client.Name(), client.BaseURL)
		}
		for _, kind := range []backend.Kind{backend.KindAnthropic, backend.KindOpenAIChat, backend.KindOpenAIResponses, "unknown"} {
			if client.Supports(kind) != (kind == backend.KindOpenAIChat) {
				t.Errorf("Supports(%s)", kind)
			}
		}
	}
	if got := New("https://example.test/v1///", "key").BaseURL; got != "https://example.test/v1" {
		t.Fatal(got)
	}
}

func TestNormalizeRequest(t *testing.T) {
	const body = `{"model":"mistral-medium-3-5","messages":[{"role":"developer","content":"prompt"},{"role":"assistant","tool_calls":[{"id":"call_long_first","type":"function","function":{"name":"read","arguments":"{}"}},{"id":"call_long_second","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_long_second","content":"result"},{"role":"tool","tool_call_id":"call_long_first","content":"result"}],"max_completion_tokens":42,"seed":9007199254740993,"metadata":{"large":9007199254740993},"stream_options":{"include_usage":true},"parallel_tool_calls":false,"reasoning_effort":"high","safe_prompt":true}`
	result, err := normalizeRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		MaxTokens  int             `json:"max_tokens"`
		RandomSeed json.Number     `json:"random_seed"`
		Metadata   json.RawMessage `json:"metadata"`
		Messages   []struct {
			Role       string `json:"role"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MaxTokens != 42 || payload.RandomSeed.String() != "9007199254740993" || string(payload.Metadata) != `{"large":9007199254740993}` || payload.Messages[0].Role != "system" {
		t.Fatalf("lost request fields or integer precision: %s", result)
	}
	calls := payload.Messages[1].ToolCalls
	if calls[0].ID == calls[1].ID || payload.Messages[2].ToolCallID != calls[1].ID || payload.Messages[3].ToolCallID != calls[0].ID {
		t.Fatalf("call/result correlation lost: %s", result)
	}
	for _, call := range calls {
		if !regexp.MustCompile(`^[a-zA-Z0-9]{9}$`).MatchString(call.ID) {
			t.Fatal(call.ID)
		}
	}
	for _, field := range []string{`"parallel_tool_calls":false`, `"safe_prompt":true`, `"reasoning_effort":"high"`} {
		if !strings.Contains(string(result), field) {
			t.Fatalf("lost %s: %s", field, result)
		}
	}
	again, err := normalizeRequest([]byte(body))
	if err != nil || string(again) != string(result) {
		t.Fatalf("normalization is not deterministic: %s %v", again, err)
	}
	const native = ` { "messages": [{"role":"assistant","content":[{"type":"thinking","thinking":[{"type":"text","text":"reason"}]},{"type":"text","text":"answer"}],"tool_calls":[{"id":"Abc123XYZ"}]}], "max_tokens":17, "random_seed":3 } `
	unchanged, err := normalizeRequest([]byte(native))
	if err != nil || string(unchanged) != native {
		t.Fatalf("valid native history changed: %s %v", unchanged, err)
	}
	preferred, err := normalizeRequest([]byte(`{"max_tokens":7,"max_completion_tokens":8,"seed":9,"random_seed":10}`))
	if err != nil || string(preferred) != `{"max_tokens":7,"random_seed":10}` {
		t.Fatalf("native fields must take precedence: %s %v", preferred, err)
	}
}

func TestNormalizeRequestErrors(t *testing.T) {
	for _, body := range []string{`{`, `null`, `[]`, `{"messages":null}`, `{"messages":[null]}`, `{"messages":[{"tool_calls":[null]}]}`} {
		if _, err := normalizeRequest([]byte(body)); err == nil {
			t.Errorf("accepted malformed request %s", body)
		}
	}
	// An existing valid ID can collide with the deterministic ID of a foreign
	// call. Refuse that request rather than correlate a result to the wrong tool.
	body := fmt.Sprintf(`{"messages":[{"tool_calls":[{"id":"call_long_first"},{"id":%q}]}]}`, toolID("call_long_first"))
	if _, err := normalizeRequest([]byte(body)); err == nil || !strings.Contains(err.Error(), "collide") {
		t.Fatalf("collision error=%v", err)
	}
}

func TestSendHeadersAndUpstreamErrors(t *testing.T) {
	const body = `{"model":"mistral-medium-3-5","messages":[]}`
	const failure = `{"message":"quota exceeded"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("incorrect upstream request %s %s %v", r.Method, r.URL.Path, r.Header)
		}
		for _, header := range []string{"X-Api-Key", "Cookie", "Anthropic-Version"} {
			if r.Header.Get(header) != "" {
				t.Errorf("client header %s forwarded", header)
			}
		}
		received, _ := io.ReadAll(r.Body)
		if string(received) != body {
			t.Errorf("body=%s", received)
		}
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, failure)
	}))
	defer server.Close()
	response, err := New(server.URL+"/v1/", "secret").Send(t.Context(), &backend.Request{
		Kind: backend.KindOpenAIChat, RawBody: []byte(body), Streaming: true,
		Header: http.Header{"Authorization": {"Bearer client-key"}, "X-Api-Key": {"client-key"}, "Cookie": {"private"}, "Anthropic-Version": {"version"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	received, _ := io.ReadAll(response.Body)
	if response.Status != 429 || response.Header.Get("Retry-After") != "60" || string(received) != failure {
		t.Fatalf("upstream error lost: %+v %s", response, received)
	}
}

func TestMissingKeyAndInvalidRequestsAreTerminal(t *testing.T) {
	client := New("http://invalid.test/v1", "")
	if _, err := client.Models(t.Context()); !backend.IsTerminal(err) {
		t.Fatalf("Models missing key=%v", err)
	}
	for _, request := range []*backend.Request{{Kind: backend.KindOpenAIChat}, {Kind: backend.KindAnthropic}} {
		if _, err := client.Send(t.Context(), request); !backend.IsTerminal(err) {
			t.Fatalf("Send missing key=%v", err)
		}
	}
	client.Key = "secret"
	for _, request := range []*backend.Request{{Kind: backend.KindAnthropic}, {Kind: backend.KindOpenAIChat, RawBody: []byte(`{`)}} {
		if _, err := client.Send(t.Context(), request); !backend.IsTerminal(err) {
			t.Fatalf("Send invalid request=%v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.Send(ctx, &backend.Request{Kind: backend.KindOpenAIChat, RawBody: []byte(`{}`)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled send=%v", err)
	}
}

func TestModelCatalog(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       []string
	}{
		{"chat", `{"data":[{"id":"model-1","aliases":["latest","model-1",""],"capabilities":{"completion_chat":true}},{"id":"latest"},{"id":"embed","capabilities":{"completion_chat":false}},{"id":"old","archived":true},{"id":"","aliases":["orphan"]},{"id":"compatible"}]}`, 200, []string{"model-1", "latest", "compatible"}},
		{"empty", `{"data":null}`, 200, []string{}},
		{"unauthorized", `{"message":"invalid key"}`, 401, nil},
		{"malformed", `{`, 200, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer secret" {
					t.Errorf("catalog request %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			models, err := New(server.URL+"/v1", "secret").Models(t.Context())
			if tc.want == nil {
				if err == nil {
					t.Fatal("expected catalog error")
				}
				if tc.status != 200 {
					var httpErr *HTTPError
					if !errors.As(err, &httpErr) || httpErr.Status != tc.status || string(httpErr.Body) != tc.body {
						t.Fatalf("catalog HTTP error=%v", err)
					}
				}
			} else if err != nil || !reflect.DeepEqual(models, tc.want) {
				t.Fatalf("models=%v err=%v want=%v", models, err, tc.want)
			}
		})
	}
}
