package opencode

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

func TestPrepareFreeTierBodyForcesStreamAndInjectsQuartet(t *testing.T) {
	for _, tt := range []struct {
		name string
		kind backend.Kind
		in   string
	}{
		{"openai no tools", backend.KindOpenAIChat, `{"model":"m","stream":false,"messages":[]}`},
		{"anthropic no tools", backend.KindAnthropic, `{"model":"claude-x","stream":false,"messages":[]}`},
		{"openai no stream key", backend.KindOpenAIChat, `{"model":"m","messages":[]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, err := prepareFreeTierBody(tt.kind, []byte(tt.in))
			if err != nil {
				t.Fatalf("prepareFreeTierBody: %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal(out, &body); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if stream, _ := body["stream"].(bool); stream != true {
				t.Errorf("stream = %v, want true", body["stream"])
			}
			tools, _ := body["tools"].([]any)
			if len(tools) != 4 {
				t.Fatalf("tools = %d, want 4 (%v)", len(tools), tools)
			}
			names := map[string]bool{}
			for _, raw := range tools {
				tool, _ := raw.(map[string]any)
				name := toolNameFromAny(tt.kind, tool)
				if name == "" {
					t.Fatalf("missing name in tool %v", tool)
				}
				names[name] = true
			}
			for _, want := range freeTierQuartet {
				if !names[want] {
					t.Errorf("missing tool %q", want)
				}
			}
			// Caller had no tools: tool_choice must keep injections inert.
			choice := body["tool_choice"]
			if tt.kind == backend.KindAnthropic {
				m, _ := choice.(map[string]any)
				if m == nil || m["type"] != "none" {
					t.Errorf("tool_choice = %v, want {type:none}", choice)
				}
			} else if choice != "none" {
				t.Errorf("tool_choice = %v, want none", choice)
			}
		})
	}
}

func TestPrepareFreeTierBodyPreservesExistingTools(t *testing.T) {
	in := []byte(`{"model":"m","tools":[{"type":"function","function":{"name":"Bash","description":"x","parameters":{"type":"object"}}}]}`)
	out, err := prepareFreeTierBody(backend.KindOpenAIChat, in)
	if err != nil {
		t.Fatalf("prepareFreeTierBody: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tools, _ := body["tools"].([]any)
	if len(tools) != 5 {
		t.Fatalf("tools = %d, want 5 (client Bash + quartet)", len(tools))
	}
	// Client already had tools: tool_choice must be left alone (absent here).
	if _, ok := body["tool_choice"]; ok {
		t.Errorf("tool_choice set to %v, want absent when client had tools", body["tool_choice"])
	}
}

func TestPrepareFreeTierBodyLeavesNonJSONAlone(t *testing.T) {
	raw := []byte("not-json\n")
	out, err := prepareFreeTierBody(backend.KindOpenAIChat, raw)
	if err != nil {
		t.Fatalf("prepareFreeTierBody: %v", err)
	}
	if string(out) != string(raw) {
		t.Errorf("out = %q, want unchanged", out)
	}
}

func toolNameFromAny(kind backend.Kind, tool map[string]any) string {
	if kind == backend.KindAnthropic {
		name, _ := tool["name"].(string)
		return name
	}
	if fn, ok := tool["function"].(map[string]any); ok {
		name, _ := fn["name"].(string)
		return name
	}
	name, _ := tool["name"].(string)
	return name
}

func TestSendFreeTierRewritesBodyAndUsesPublicBearer(t *testing.T) {
	c, rec := newRecordingClient(t, http.StatusOK, "application/json", `{}`)
	c.Key = ""
	resp, err := c.Send(t.Context(), &backend.Request{
		Kind:    backend.KindOpenAIChat,
		RawBody: []byte(`{"model":"mimo","stream":false,"messages":[]}`),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	_ = resp.Body.Close()

	if got := rec.Header.Get("Authorization"); got != "Bearer public" {
		t.Errorf("Authorization = %q, want Bearer public", got)
	}
	if got := rec.Header.Get("Accept"); got != "text/event-stream" {
		t.Errorf("Accept = %q, want text/event-stream (free tier always forces stream)", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body, &body); err != nil {
		t.Fatalf("forwarded body: %v", err)
	}
	if stream, _ := body["stream"].(bool); stream != true {
		t.Errorf("forwarded stream = %v, want true", body["stream"])
	}
	tools, _ := body["tools"].([]any)
	if len(tools) != 4 {
		t.Errorf("forwarded tools = %d, want 4", len(tools))
	}
}

func TestSendFreeTierDoesNotRewriteWhenKeyPresent(t *testing.T) {
	c, rec := newRecordingClient(t, http.StatusOK, "application/json", `{}`)
	raw := []byte(`{"model":"m","stream":false,"messages":[]}`)
	resp, err := c.Send(t.Context(), &backend.Request{Kind: backend.KindOpenAIChat, RawBody: raw})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	_ = resp.Body.Close()
	if string(rec.Body) != string(raw) {
		t.Errorf("paid-key body rewritten: %s", rec.Body)
	}
	if got := rec.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q, want application/json", got)
	}
}

func TestSendFreeTierAggregatesChatSSEForNonStreaming(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"mimo","created":1,"choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":""}]}`,
		``,
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"mimo","created":1,"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	var gotBody string
	var gotCT string
	srv := freeTierSSEServer(t, "text/event-stream", sse, &gotBody, &gotCT)
	c := New(srv.URL, "")
	c.HTTP = srv.Client()

	resp, err := c.Send(t.Context(), &backend.Request{
		Kind:      backend.KindOpenAIChat,
		RawBody:   []byte(`{"model":"m","stream":false,"messages":[]}`),
		Streaming: false,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	raw, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("response Content-Type = %q, want application/json", ct)
	}
	var completion struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
				Role    string `json:"role"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage map[string]any `json:"usage"`
	}
	if err := json.Unmarshal(raw, &completion); err != nil {
		t.Fatalf("aggregated JSON: %v\n%s", err, raw)
	}
	if completion.Object != "chat.completion" {
		t.Errorf("object = %q", completion.Object)
	}
	if len(completion.Choices) != 1 || completion.Choices[0].Message.Content != "Hello" {
		t.Errorf("choices = %+v, want Hello", completion.Choices)
	}
	if completion.Usage["total_tokens"] != float64(5) {
		t.Errorf("usage total = %v, want 5", completion.Usage)
	}
	if !strings.Contains(gotCT, "text/event-stream") {
		t.Errorf("upstream Accept/CT = %q", gotCT)
	}
}

func TestSendFreeTierAggregatesAnthropicSSE(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-x","usage":{"input_tokens":10,"output_tokens":1}}}`,
		``,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi there"}}`,
		``,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`,
		``,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	var gotBody string
	var gotCT string
	srv := freeTierSSEServer(t, "text/event-stream", sse, &gotBody, &gotCT)
	c := New(srv.URL, "")
	c.HTTP = srv.Client()

	resp, err := c.Send(t.Context(), &backend.Request{
		Kind:      backend.KindAnthropic,
		RawBody:   []byte(`{"model":"claude-x","stream":false,"messages":[]}`),
		Streaming: false,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	raw, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var msg struct {
		Type    string           `json:"type"`
		Role    string           `json:"role"`
		Content []map[string]any `json:"content"`
		Stop    string           `json:"stop_reason"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	if msg.Type != "message" || msg.Role != "assistant" || msg.Stop != "end_turn" {
		t.Errorf("msg = %+v", msg)
	}
	if len(msg.Content) != 1 || msg.Content[0]["text"] != "Hi there" {
		t.Errorf("content = %+v", msg.Content)
	}
}

func TestSendFreeTierRelaysNonSSEErrorVerbatim(t *testing.T) {
	const body = `{"type":"error","error":{"type":"FreeTierError","message":"nope"}}`
	c, _ := newRecordingClient(t, http.StatusForbidden, "application/json", body)
	c.Key = ""
	resp, err := c.Send(t.Context(), &backend.Request{
		Kind:    backend.KindOpenAIChat,
		RawBody: []byte(`{"model":"m"}`),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.Status != http.StatusForbidden || string(got) != body {
		t.Errorf("status=%d body=%s", resp.Status, got)
	}
	if resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("CT = %q", resp.Header.Get("Content-Type"))
	}
}

// freeTierSSEServer returns a server that records the request body and
// response Content-Type and replies with the given SSE content-type/body.
func freeTierSSEServer(t *testing.T, contentType, reply string, gotBody, gotCT *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*gotBody = string(b)
		*gotCT = r.Header.Get("Accept")
		if *gotCT == "" {
			*gotCT = contentType
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv
}
