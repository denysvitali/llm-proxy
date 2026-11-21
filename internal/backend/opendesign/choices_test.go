package opendesign

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// splitReply is Open Design's observed non-streaming answer to a turn that
// both talks and calls a tool: the text and the call arrive as two choices.
const splitReply = `{"id":"resp_1","object":"chat.completion","model":"deepseek-v4.1-flash",` +
	`"choices":[{"index":0,"message":{"role":"assistant","content":"I'll check that for you."}},` +
	`{"index":1,"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"index":0,"type":"function","id":"call_00_x","function":{"name":"get_weather","arguments":"{\"city\": \"Zurich\"}"}}]}}],` +
	`"usage":{"prompt_tokens":32,"completion_tokens":17,"total_tokens":49}}`

func send(t *testing.T, req *backend.Request, body string) []byte {
	t.Helper()
	resp := &backend.Response{Status: http.StatusOK, Header: http.Header{"Content-Length": {"1"}}, Body: io.NopCloser(strings.NewReader(body))}
	mergeSplitChoices(req, resp)
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMergeSplitChoices(t *testing.T) {
	out := send(t, &backend.Request{Kind: backend.KindOpenAIChat, RawBody: []byte(`{"model":"m"}`)}, splitReply)
	var got struct {
		ID      string `json:"id"`
		Choices []struct {
			Index        int    `json:"index"`
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage map[string]int `json:"usage"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("merged body invalid: %v\n%s", err, out)
	}
	if len(got.Choices) != 1 {
		t.Fatalf("choices = %d, want 1: %s", len(got.Choices), out)
	}
	c := got.Choices[0]
	if c.Index != 0 || c.FinishReason != "tool_calls" || c.Message.Role != "assistant" || c.Message.Content != "I'll check that for you." {
		t.Fatalf("merged choice = %+v", c)
	}
	if len(c.Message.ToolCalls) != 1 || c.Message.ToolCalls[0].ID != "call_00_x" || c.Message.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("tool calls = %+v", c.Message.ToolCalls)
	}
	if got.ID != "resp_1" || got.Usage["total_tokens"] != 49 {
		t.Fatalf("envelope fields lost: %s", out)
	}
}

func TestMergeSplitChoicesLeavesOtherReplies(t *testing.T) {
	single := `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"hi"}}]}`
	typed := `{"choices":[{"index":0,"message":{"content":[{"type":"text","text":"a"}]}},{"index":1,"message":{"content":"b"}}]}`
	for _, test := range []struct {
		name string
		req  backend.Request
		body string
	}{
		{"single choice", backend.Request{Kind: backend.KindOpenAIChat}, single},
		{"explicit n>1", backend.Request{Kind: backend.KindOpenAIChat, RawBody: []byte(`{"n":2}`)}, splitReply},
		{"streaming", backend.Request{Kind: backend.KindOpenAIChat, Streaming: true}, splitReply},
		{"responses", backend.Request{Kind: backend.KindOpenAIResponses}, splitReply},
		{"typed content", backend.Request{Kind: backend.KindOpenAIChat}, typed},
		{"not json", backend.Request{Kind: backend.KindOpenAIChat}, "oops"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if out := send(t, &test.req, test.body); string(out) != test.body {
				t.Fatalf("body changed:\n got %s\nwant %s", out, test.body)
			}
		})
	}
}
