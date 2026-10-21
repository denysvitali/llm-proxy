package translate

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAnthropicOpenAIContentFidelity(t *testing.T) {
	for _, responses := range []bool{false, true} {
		convert := ToOpenAI
		key, textType := "messages", "text"
		if responses {
			convert, key, textType = ToResponses, "input", "input_text"
		}
		t.Run(key, func(t *testing.T) {
			req, err := ParseRequest([]byte(`{"max_tokens":16,"system":[{"type":"text","text":" "},{"type":"text","text":""}],"messages":[{"role":"user","content":" \n\t"},{"role":"user","content":[{"type":"text","text":""},{"type":"image","source":{"type":"url","url":"https://example.invalid/image.png"}},{"type":"text","text":" after "}]}]}`))
			if err != nil {
				t.Fatal(err)
			}
			body, err := convert(req, "model")
			if err != nil {
				t.Fatal(err)
			}
			var out map[string]any
			if err := json.Unmarshal(body, &out); err != nil {
				t.Fatal(err)
			}
			items := jarray(t, out[key], key)
			if len(items) != 3 {
				t.Fatalf("items = %s", body)
			}
			system := jmap(t, items[0], "system")
			want := []any{map[string]any{"type": textType, "text": " "}, map[string]any{"type": textType, "text": ""}}
			if system["role"] != "system" || !reflect.DeepEqual(system["content"], want) {
				t.Fatalf("system = %#v", system)
			}
			content := jmap(t, items[1], "message")["content"]
			if responses {
				content = jmap(t, jarray(t, content, "content")[0], "text")["text"]
			}
			if content != " \n\t" {
				t.Fatalf("whitespace = %#v", content)
			}
			parts := jarray(t, jmap(t, items[2], "message")["content"], "content")
			if len(parts) != 3 || jmap(t, parts[0], "text")["text"] != "" || jmap(t, parts[2], "text")["text"] != " after " {
				t.Fatalf("ordered parts = %#v", parts)
			}
		})
	}
}

func TestAnthropicToolOutputFidelity(t *testing.T) {
	for _, responses := range []bool{false, true} {
		textType := "text"
		if responses {
			textType = "input_text"
		}
		for _, tc := range []struct {
			name, input, want string
			failed            bool
		}{
			{"missing", "", `""`, false},
			{"empty array", `[]`, `[]`, false},
			{"string", `" \n"`, `" \n"`, false},
			{"error string", `"bad"`, `"[Tool execution failed]\nbad"`, true},
			{"ordered text", `[{"type":"text","text":""},{"type":"text","text":" a "}]`, `[{"type":"` + textType + `","text":""},{"type":"` + textType + `","text":" a "}]`, false},
			{"error array", `[]`, `[{"type":"` + textType + `","text":"[Tool execution failed]\n"}]`, true},
		} {
			t.Run(textType+"/"+tc.name, func(t *testing.T) {
				got, err := anthropicToolOutput(anthropicBlock{Content: json.RawMessage(tc.input), IsError: tc.failed}, responses)
				if err != nil {
					t.Fatal(err)
				}
				var actual, want any
				if err := json.Unmarshal(got, &actual); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(actual, want) {
					t.Fatalf("got %s; want %s", got, tc.want)
				}
			})
		}
		image := json.RawMessage(`[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"YWJj"}}]`)
		got, err := anthropicToolOutput(anthropicBlock{Content: image}, responses)
		if !responses && err == nil {
			t.Fatal("Chat accepted image tool output")
		}
		if responses && (err != nil || string(got) != `[{"image_url":"data:image/png;base64,YWJj","type":"input_image"}]`) {
			t.Fatalf("image output = %s, %v", got, err)
		}
		for _, raw := range []string{`null`, `{}`, `[{"type":"text","text":null}]`} {
			if _, err := anthropicToolOutput(anthropicBlock{Content: json.RawMessage(raw)}, responses); err == nil {
				t.Fatalf("accepted %s", raw)
			}
		}
	}
}

func TestAnthropicOpenAIRejectsUnsupportedContent(t *testing.T) {
	for _, content := range []string{
		`[]`, `null`,
		`[{"type":"text","text":"hi","citations":[{"type":"char_location","cited_text":"x"}]}]`,
		`[{"type":"text","text":"hi","extra":true}]`,
		`[{"type":"text","text":null}]`,
		`[{"type":"document","source":{"type":"text","data":"hello"}}]`,
		`[{"type":"image","source":{"type":"base64","data":"YWJj"}}]`,
		`[{"type":"image","source":{"type":"url","url":"https://example.invalid/a","extra":true}}]`,
	} {
		for _, convert := range []func(*Request, string) ([]byte, error){ToOpenAI, ToResponses} {
			req, err := ParseRequest([]byte(`{"max_tokens":16,"messages":[{"role":"user","content":` + content + `}]}`))
			if err != nil {
				t.Fatal(err)
			}
			if got, err := convert(req, "model"); err == nil {
				t.Fatalf("unsupported %s converted to %s", content, got)
			}
		}
	}
}

func TestAnthropicOpenAIControlBoundaries(t *testing.T) {
	for _, responses := range []bool{false, true} {
		convert := ToOpenAI
		if responses {
			convert = ToResponses
		}
		for _, field := range []string{`"top_k":1`, `"thinking":{"budget_tokens":8}`, `"output_config":{"format":{"type":"json_schema"}}`, `"output_config":{"effort":"extreme"}`, `"safeguards":{"example":true}`, `"tools":[{"name":"f","input_examples":[{}]}]`, `"tool_choice":{"type":"banana"}`, `"tool_choice":{"type":"tool"}`} {
			req, err := ParseRequest([]byte(`{"max_tokens":16,"messages":[{"role":"user","content":"hi"}],` + field + `}`))
			if err != nil {
				t.Fatal(err)
			}
			if got, err := convert(req, "model"); err == nil {
				t.Fatalf("unsupported %s converted to %s", field, got)
			}
		}
		for _, disable := range []string{"true", "false"} {
			req, err := ParseRequest([]byte(`{"max_tokens":16,"messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"auto","disable_parallel_tool_use":` + disable + `}}`))
			if err != nil {
				t.Fatal(err)
			}
			got, err := convert(req, "model")
			if err != nil {
				t.Fatal(err)
			}
			var out map[string]any
			if err := json.Unmarshal(got, &out); err != nil {
				t.Fatal(err)
			}
			if out["tool_choice"] != "auto" || out["parallel_tool_calls"] != (disable == "false") {
				t.Fatalf("choice = %s", got)
			}
		}
	}
}
