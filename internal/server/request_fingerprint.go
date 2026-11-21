package server

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// fingerprintTail is how many trailing conversation entries a fingerprint
// spells out; rejections caused by history shape are almost always visible
// at the end of the conversation.
const fingerprintTail = 12

// requestFingerprint summarizes the structure of an outbound payload for the
// log line of an upstream rejection: sizes, counts, the generation controls,
// and the role/part sequence of the conversation tail. It never includes
// prompt text, tool arguments or schemas, so it is safe to log while still
// telling apart rejections caused by request shape — an opaque provider 400
// such as Open Design's "[code=upstream_error] The request could not be
// completed." otherwise leaves nothing to debug from.
//
// Entries are rendered as role[parts], e.g. "a[text:120,call*2]" for an
// assistant message with 120 bytes of text and two tool calls, "t[18]" for a
// Chat tool message, or "a[]" for an assistant message without content.
func requestFingerprint(kind backend.Kind, payload []byte) string {
	var body map[string]json.RawMessage
	if json.Unmarshal(payload, &body) != nil {
		return fmt.Sprintf("bytes=%d unparsable", len(payload))
	}
	parts := []string{fmt.Sprintf("bytes=%d", len(payload))}
	var entries []json.RawMessage
	switch kind {
	case backend.KindOpenAIResponses:
		_ = json.Unmarshal(body["input"], &entries)
		parts = append(parts, fmt.Sprintf("input=%d", len(entries)))
	default:
		_ = json.Unmarshal(body["messages"], &entries)
		parts = append(parts, fmt.Sprintf("messages=%d", len(entries)))
	}
	var tools []json.RawMessage
	_ = json.Unmarshal(body["tools"], &tools)
	parts = append(parts, fmt.Sprintf("tools=%d", len(tools)))
	for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens", "reasoning_effort", "stream", "tool_choice", "response_format", "thinking"} {
		if raw, ok := body[key]; ok {
			parts = append(parts, key+"="+compactScalar(raw))
		}
	}
	largest := 0
	for _, entry := range entries {
		largest = max(largest, len(entry))
	}
	parts = append(parts, fmt.Sprintf("largest_entry=%d", largest))
	start := max(len(entries)-fingerprintTail, 0)
	tail := make([]string, 0, len(entries)-start)
	for _, entry := range entries[start:] {
		tail = append(tail, entryFingerprint(entry))
	}
	if start > 0 {
		tail = append([]string{"…"}, tail...)
	}
	parts = append(parts, "tail="+strings.Join(tail, " "))
	return strings.Join(parts, " ")
}

// compactScalar renders a control value; objects and arrays only by their
// kind, since response_format and tool_choice can embed schemas or names.
func compactScalar(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(trimmed, "{"):
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.Type != "" {
			return "{" + probe.Type + "}"
		}
		return "{…}"
	case strings.HasPrefix(trimmed, "["):
		return "[…]"
	case strings.HasPrefix(trimmed, `"`):
		var s string
		_ = json.Unmarshal(raw, &s)
		if len(s) > 24 {
			s = s[:24] + "…"
		}
		return s
	default:
		return trimmed
	}
}

// entryFingerprint renders one message or input item as role[parts].
func entryFingerprint(raw json.RawMessage) string {
	var entry struct {
		Role       string          `json:"role"`
		Type       string          `json:"type"`
		Content    json.RawMessage `json:"content"`
		ToolCalls  []any           `json:"tool_calls"`
		Output     json.RawMessage `json:"output"`
		Arguments  string          `json:"arguments"`
		Reasoning  json.RawMessage `json:"reasoning_content"`
		ToolCallID string          `json:"tool_call_id"`
	}
	if json.Unmarshal(raw, &entry) != nil {
		return "?"
	}
	label := entry.Role
	if label == "" {
		label = entry.Type
	}
	switch label {
	case "user", "assistant", "system", "developer", "tool":
		label = label[:1]
	}
	var parts []string
	switch entry.Type {
	case "function_call_output":
		parts = append(parts, fmt.Sprint(len(entry.Output)))
	case "function_call":
		parts = append(parts, fmt.Sprintf("args:%d", len(entry.Arguments)))
	default:
		parts = append(parts, contentFingerprint(entry.Content)...)
	}
	if n := len(entry.ToolCalls); n > 0 {
		parts = append(parts, fmt.Sprintf("call*%d", n))
	}
	if len(entry.Reasoning) > 0 && string(entry.Reasoning) != "null" {
		parts = append(parts, "reasoning")
	}
	return label + "[" + strings.Join(parts, ",") + "]"
}

// contentFingerprint lists content parts as type:bytes (strings as their
// length), collapsing runs of the same part type.
func contentFingerprint(raw json.RawMessage) []string {
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case trimmed == "" || trimmed == "null":
		return nil
	case strings.HasPrefix(trimmed, `"`):
		var s string
		_ = json.Unmarshal(raw, &s)
		return []string{fmt.Sprint(len(s))}
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return []string{"?"}
	}
	var out []string
	last, run := "", 0
	flush := func() {
		if run > 1 {
			out[len(out)-1] += fmt.Sprintf("*%d", run)
		}
	}
	for _, item := range items {
		var part struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(item, &part)
		if part.Type == last && part.Type != "text" {
			run++
			continue
		}
		flush()
		last, run = part.Type, 1
		if part.Type == "text" || part.Type == "input_text" || part.Type == "output_text" {
			out = append(out, fmt.Sprintf("%s:%d", part.Type, len(part.Text)))
		} else {
			out = append(out, part.Type)
		}
	}
	flush()
	return out
}
