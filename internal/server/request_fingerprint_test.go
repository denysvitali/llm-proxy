package server

import (
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// The fingerprint must describe the conversation's structure while leaking
// none of its text, tool arguments, or schemas.
func TestRequestFingerprint(t *testing.T) {
	const secret = "TOP-SECRET-PROMPT"
	chat := `{"model":"m","max_tokens":32000,"stream":true,"reasoning_effort":"high",` +
		`"response_format":{"type":"json_schema","json_schema":{"name":"` + secret + `"}},` +
		`"tools":[{"type":"function","function":{"name":"` + secret + `","parameters":{}}}],"messages":[` +
		`{"role":"system","content":"` + secret + `"},` +
		`{"role":"user","content":[{"type":"text","text":"` + secret + `"},{"type":"image_url","image_url":{"url":"x"}},{"type":"image_url","image_url":{"url":"y"}}]},` +
		`{"role":"assistant","content":null,"tool_calls":[{"id":"a"},{"id":"b"}]},` +
		`{"role":"tool","tool_call_id":"a","content":"` + secret + `"},` +
		`{"role":"assistant","content":""}]}`
	got := requestFingerprint(backend.KindOpenAIChat, []byte(chat))
	if strings.Contains(got, secret) {
		t.Fatalf("fingerprint leaks content: %s", got)
	}
	for _, want := range []string{"messages=5", "tools=1", "max_tokens=32000", "reasoning_effort=high", "response_format={json_schema}",
		"tail=s[17] u[text:17,image_url*2] a[call*2] t[17] a[0]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("fingerprint %q lacks %q", got, want)
		}
	}

	responses := `{"input":[{"type":"function_call","arguments":"{\"q\":1}"},{"type":"function_call_output","output":"` + secret + `"}]}`
	if got := requestFingerprint(backend.KindOpenAIResponses, []byte(responses)); !strings.Contains(got, "input=2") ||
		!strings.Contains(got, "function_call[args:7] function_call_output[19]") || strings.Contains(got, secret) {
		t.Fatalf("responses fingerprint = %s", got)
	}

	var long strings.Builder
	long.WriteString(`{"messages":[`)
	for i := range 20 {
		if i > 0 {
			long.WriteString(",")
		}
		long.WriteString(`{"role":"user","content":"hi"}`)
	}
	long.WriteString(`]}`)
	if got := requestFingerprint(backend.KindOpenAIChat, []byte(long.String())); !strings.Contains(got, "tail=… u[2]") || strings.Count(got, "u[2]") != fingerprintTail {
		t.Fatalf("tail not bounded: %s", got)
	}
	if got := requestFingerprint(backend.KindOpenAIChat, []byte("nope")); got != "bytes=4 unparsable" {
		t.Fatalf("unparsable = %s", got)
	}
}
