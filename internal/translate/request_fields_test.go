package translate

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestChatRequestFieldBoundaries(t *testing.T) {
	for _, route := range []struct {
		name    string
		convert func([]byte, string) ([]byte, error)
	}{{"responses", ChatToResponses}, {"messages", ChatToAnthropic}} {
		for _, tc := range []struct{ name, fields, want string }{
			{"message name", `"messages":[{"role":"user","content":"hi","name":"speaker"}]`, "name"},
			{"legacy call", `"messages":[{"role":"assistant","function_call":{"name":"f","arguments":"{}"}}]`, "function_call"},
			{"refusal field", `"messages":[{"role":"assistant","content":"hi","refusal":"no"}]`, "refusal"},
			{"reasoning history", `"messages":[{"role":"assistant","content":"hi","reasoning_content":"opaque"}]`, "reasoning_content"},
			{"null message", `"messages":[null]`, "object"},
			{"empty assistant", `"messages":[{"role":"assistant","content":null}]`, "requires content"},
			{"misplaced empty calls", `"messages":[{"role":"user","content":"hi","tool_calls":[]}]`, "assistant"},
			{"misplaced empty call id", `"messages":[{"role":"user","content":"hi","tool_call_id":""}]`, "tool message"},
			{"unknown call field", `"messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}","extra":true}}]}]`, "extra"},
			{"nonfunction call", `"messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"custom","function":{"name":"f","arguments":"{}"}}]}]`, "type function"},
			{"unnamed call", `"messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"arguments":"{}"}}]}]`, "non-empty name"},
			{"null tool", `"messages":[{"role":"user","content":"hi"}],"tools":[null]`, "object"},
			{"tool extension", `"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","cache_control":{}}}]`, "cache_control"},
			{"custom tool", `"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"custom","function":{"name":"f"}}]`, "type function"},
			{"duplicate tools", `"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f"}},{"type":"function","function":{"name":"f"}}]`, "duplicate tool"},
		} {
			t.Run(route.name+"/"+tc.name, func(t *testing.T) {
				out, err := route.convert([]byte(`{"model":"source",`+tc.fields+`}`), "target")
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("out=%s err=%v, want %q", out, err, tc.want)
				}
			})
		}
		t.Run(route.name+"/supported nested fields", func(t *testing.T) {
			body := `{"messages":[{"role":"user","content":""},{"role":"assistant","content":null,"tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{\"n\":9007199254740993}"}}]},{"role":"tool","tool_call_id":"c","content":""}],"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object","properties":{"n":{"const":9007199254740993}}}}}]}`
			out, err := route.convert([]byte(body), "target")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(out), "9007199254740993") {
				t.Fatalf("numeric lexeme lost: %s", out)
			}
		})
	}
}

func TestResponsesRequestFieldBoundaries(t *testing.T) {
	for _, route := range []struct {
		name    string
		convert func([]byte, string) ([]byte, error)
	}{{"chat", ResponsesToChat}, {"messages", ResponsesToAnthropic}} {
		for _, tc := range []struct{ name, item, want string }{
			{"null item", `null`, "object"},
			{"unknown field", `{"role":"user","content":"hi","extra":false}`, "extra"},
			// Item ids are deliberately representable-with-loss (Codex replays
			// them); status only functions as an annotation on function items.
			{"replay status", `{"type":"message","role":"assistant","content":"hi","status":"completed"}`, "status"},
			{"message call field", `{"role":"user","content":"hi","call_id":null}`, "call_id"},
			{"call content field", `{"type":"function_call","call_id":"c","name":"f","arguments":"{}","content":"hi"}`, "content"},
			{"call missing name", `{"type":"function_call","call_id":"c","arguments":"{}"}`, "name"},
			{"output name field", `{"type":"function_call_output","call_id":"c","name":"f","output":"hi"}`, "name"},
			// Replayed reasoning is opaque provider state and is dropped.
			{"only reasoning", `{"type":"reasoning","summary":[],"encrypted_content":"opaque"}`, "no input items"},
		} {
			t.Run(route.name+"/"+tc.name, func(t *testing.T) {
				out, err := route.convert([]byte(`{"input":[`+tc.item+`]}`), "target")
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("out=%s err=%v, want %q", out, err, tc.want)
				}
			})
		}
		t.Run(route.name+"/supported variants", func(t *testing.T) {
			body := `{"input":[{"role":"user","content":""},{"type":"function_call","call_id":"c","name":"f","arguments":"{\"n\":9007199254740993}"},{"type":"function_call_output","call_id":"c","output":""}]}`
			out, err := route.convert([]byte(body), "target")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(out), "9007199254740993") {
				t.Fatalf("numeric lexeme lost: %s", out)
			}
		})
	}
}

func TestUnknownOpenAIRequestControls(t *testing.T) {
	for _, route := range []struct {
		name, input string
		convert     func([]byte, string) ([]byte, error)
	}{{"chat-responses", `"messages":[{"role":"user","content":"hi"}]`, ChatToResponses}, {"chat-messages", `"messages":[{"role":"user","content":"hi"}]`, ChatToAnthropic}, {"responses-chat", `"input":"hi"`, ResponsesToChat}, {"responses-messages", `"input":"hi"`, ResponsesToAnthropic}} {
		for _, field := range []string{`"provider_extension":false`, `"provider_extension":0`, `"provider_extension":{}`, `"stream_options":{"include_usage":true}`} {
			t.Run(route.name+"/"+field, func(t *testing.T) {
				if out, err := route.convert([]byte(`{`+route.input+`,`+field+`}`), "target"); err == nil {
					t.Fatalf("unknown control dropped: %s", out)
				}
			})
		}
		t.Run(route.name+"/null is absent", func(t *testing.T) {
			if _, err := route.convert([]byte(`{`+route.input+`,"provider_extension":null}`), "target"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResponsesInstructionsPresence(t *testing.T) {
	for _, route := range []struct {
		name    string
		convert func([]byte, string) ([]byte, error)
	}{{"chat", ResponsesToChat}, {"messages", ResponsesToAnthropic}} {
		for _, value := range []string{"", "null", `""`, `" rules "`} {
			t.Run(route.name+"/"+value, func(t *testing.T) {
				body := `{"input":"hi"`
				if value != "" {
					body += `,"instructions":` + value
				}
				out, err := route.convert([]byte(body+`}`), "target")
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(out, &fields); err != nil {
					t.Fatal(err)
				}
				var instruction json.RawMessage
				if route.name == "chat" {
					var messages []struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					}
					if err := json.Unmarshal(fields["messages"], &messages); err != nil {
						t.Fatal(err)
					}
					if messages[0].Role == "system" {
						instruction = messages[0].Content
					}
				} else {
					instruction = fields["system"]
				}
				if value == "" || value == "null" {
					if len(instruction) != 0 {
						t.Fatalf("absent instruction invented: %s", out)
					}
				} else if string(instruction) != value {
					t.Fatalf("instruction changed: %s", out)
				}
			})
		}
	}
}

func TestResponsesInstructionsLeadingSystemOrder(t *testing.T) {
	body := []byte(`{"instructions":"","input":[{"role":"system","content":[{"type":"input_text","text":" first "},{"type":"input_text","text":""}]},{"role":"system","content":" second "},{"role":"user","content":"hi"}]}`)
	for _, route := range []struct {
		name, field, want string
		convert           func([]byte, string) ([]byte, error)
	}{
		{"chat", "messages", `[{"role":"system","content":""},{"role":"system","content":[{"type":"text","text":" first "},{"type":"text","text":""}]},{"role":"system","content":" second "},{"role":"user","content":"hi"}]`, ResponsesToChat},
		{"messages", "system", `[{"type":"text","text":""},{"type":"text","text":" first "},{"type":"text","text":""},{"type":"text","text":" second "}]`, ResponsesToAnthropic},
	} {
		t.Run(route.name, func(t *testing.T) {
			out, err := route.convert(body, "target")
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(out, &fields); err != nil {
				t.Fatal(err)
			}
			var got, want any
			if err := json.Unmarshal(fields[route.field], &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(route.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("instruction order changed: %s, want %s", fields[route.field], route.want)
			}
		})
	}
}
