package translate

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Chat tool messages are text-only, so screenshot-style tool results move
// their images into one user message after the turn's tool messages, which
// must stay contiguous for parallel calls.
func TestToOpenAIDefersToolResultImages(t *testing.T) {
	const png = "iVBORw0KGgo="
	image := `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + png + `"}}`
	body := `{"model":"m","max_tokens":10,"messages":[` +
		`{"role":"user","content":"look"},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"Read","input":{}},{"type":"tool_use","id":"b","name":"Shot","input":{}}]},` +
		`{"role":"user","content":[` +
		`{"type":"tool_result","tool_use_id":"a","content":[{"type":"text","text":"file.png"},` + image + `]},` +
		`{"type":"tool_result","tool_use_id":"b","is_error":true,"content":[` + image + `]},` +
		`{"type":"text","text":"what do you see?"}]}]}`
	request, err := ParseRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	out, err := ToOpenAI(request, "m")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	url := "data:image/png;base64," + png
	imagePart := map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}}
	text := func(s string) map[string]any { return map[string]any{"type": "text", "text": s} }
	want := []any{
		map[string]any{"role": "tool", "tool_call_id": "a", "content": []any{text("file.png"), text(chatToolImageNote(1))}},
		map[string]any{"role": "tool", "tool_call_id": "b", "content": []any{text(anthropicToolErrorPrefix), text(chatToolImageNote(1))}},
		map[string]any{"role": "user", "content": []any{
			text("Images returned by tool call a:"), imagePart,
			text("Images returned by tool call b:"), imagePart,
			text("what do you see?"),
		}},
	}
	if len(got.Messages) != 5 {
		t.Fatalf("messages = %s", out)
	}
	var tail []any
	for _, message := range got.Messages[2:] {
		tail = append(tail, normalizeJSON(t, message))
	}
	if !reflect.DeepEqual(tail, normalizeJSON(t, want)) {
		t.Fatalf("got  %s", mustJSON(t, tail))
	}
}

func normalizeJSON(t *testing.T, value any) any {
	t.Helper()
	var out any
	if err := json.Unmarshal(mustJSON(t, value), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
