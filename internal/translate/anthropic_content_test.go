package translate

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestOpenAIToAnthropicContentFidelity(t *testing.T) {
	for _, responses := range []bool{false, true} {
		name, field, textType := "chat", "messages", "text"
		convert := ChatToAnthropic
		image := `{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}`
		if responses {
			name, field, textType = "responses", "input", "input_text"
			convert = ResponsesToAnthropic
			image = `{"type":"input_image","image_url":"https://example.com/image.png"}`
		}
		for _, role := range []string{"user", "assistant"} {
			for _, content := range []string{`""`, `" \n\t "`, `[{"type":"` + textType + `","text":""},{"type":"` + textType + `","text":" \n "}]`} {
				t.Run(name+"/"+role+"/"+content, func(t *testing.T) {
					out, err := convert([]byte(`{"model":"m","`+field+`":[{"role":"`+role+`","content":`+content+`}]}`), "upstream")
					if err != nil {
						t.Fatal(err)
					}
					messages := jarray(t, decodeJSON(t, out)["messages"], "messages")
					if len(messages) != 1 {
						t.Fatalf("lost message: %s", out)
					}
					message := jmap(t, messages[0], "message")
					if message["role"] != role {
						t.Fatalf("role changed: %v", message)
					}
					blocks := jarray(t, message["content"], "content")
					var text string
					var want []map[string]any
					if json.Unmarshal([]byte(content), &text) == nil {
						want = []map[string]any{{"type": "text", "text": text}}
					} else {
						want = []map[string]any{{"type": "text", "text": ""}, {"type": "text", "text": " \n "}}
					}
					assertContentBlocks(t, blocks, want)
				})
			}
		}
		t.Run(name+"/ordered image", func(t *testing.T) {
			parts := `[{"type":"` + textType + `","text":""},` + image + `,{"type":"` + textType + `","text":" after "}]`
			out, err := convert([]byte(`{"model":"m","`+field+`":[{"role":"user","content":`+parts+`}]}`), "upstream")
			if err != nil {
				t.Fatal(err)
			}
			message := jmap(t, jarray(t, decodeJSON(t, out)["messages"], "messages")[0], "message")
			assertContentBlocks(t, jarray(t, message["content"], "content"), []map[string]any{
				{"type": "text", "text": ""},
				{"type": "image", "source": map[string]any{"type": "url", "url": "https://example.com/image.png"}},
				{"type": "text", "text": " after "},
			})
		})
		t.Run(name+"/multimodal tool result", func(t *testing.T) {
			parts := `[{"type":"` + textType + `","text":""},` + image + `,{"type":"` + textType + `","text":" after "}]`
			history := `{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":` + parts + `}`
			if responses {
				history = `{"type":"function_call","call_id":"c1","name":"f","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":` + parts + `}`
			}
			out, err := convert([]byte(`{"model":"m","`+field+`":[`+history+`]}`), "upstream")
			if err != nil {
				t.Fatal(err)
			}
			messages := jarray(t, decodeJSON(t, out)["messages"], "messages")
			result := jmap(t, jarray(t, jmap(t, messages[1], "message")["content"], "content")[0], "result")
			if len(result) != 3 || result["type"] != "tool_result" || result["tool_use_id"] != "c1" {
				t.Fatalf("tool result wire shape = %v", result)
			}
			assertContentBlocks(t, jarray(t, result["content"], "result content"), []map[string]any{
				{"type": "text", "text": ""},
				{"type": "image", "source": map[string]any{"type": "url", "url": "https://example.com/image.png"}},
				{"type": "text", "text": " after "},
			})
		})
	}
}

func assertContentBlocks(t *testing.T, got []any, want []map[string]any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("blocks = %v, want %v", got, want)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("block %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestOpenAIToAnthropicRejectsUnsupportedContent(t *testing.T) {
	for _, responses := range []bool{false, true} {
		name, field, textType := "chat", "messages", "text"
		convert := ChatToAnthropic
		image := `{"type":"image_url","image_url":{"url":"https://example.com/image.png","detail":"high"}}`
		if responses {
			name, field, textType = "responses", "input", "input_text"
			convert = ResponsesToAnthropic
			image = `{"type":"input_image","image_url":"https://example.com/image.png","detail":"high"}`
		}
		for _, content := range []string{
			`null`, `[]`, `{}`, `[{"type":"input_audio"}]`, `[{"type":"input_file","file_id":"f1"}]`,
			`[{"type":"` + textType + `"}]`, `[{"type":"` + textType + `","text":null}]`,
			`[{"type":"` + textType + `","text":"ok","extension":true}]`, `[` + image + `]`,
		} {
			t.Run(name+"/"+content, func(t *testing.T) {
				out, err := convert([]byte(`{"model":"m","`+field+`":[{"role":"user","content":`+content+`}]}`), "upstream")
				if err == nil {
					t.Fatalf("unsupported content silently changed: %s", out)
				}
			})
		}
		t.Run(name+"/unknown role", func(t *testing.T) {
			if out, err := convert([]byte(`{"model":"m","`+field+`":[{"role":"unknown","content":"hi"}]}`), "upstream"); err == nil {
				t.Fatalf("unknown role silently changed: %s", out)
			}
		})
	}
}
