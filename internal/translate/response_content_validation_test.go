package translate

import (
	"encoding/json"
	"testing"
)

func TestBufferedUnsupportedOutputBoundaries(t *testing.T) {
	for _, output := range []string{
		`[{"type":"web_search_call","status":"completed"}]`,
		`[{"type":"custom_tool_call","name":"shell","input":"pwd"}]`,
		`[{"type":"reasoning","summary":[{"type":"other","text":"lost"}]}]`,
		`[{"type":"reasoning","content":[{"type":"reasoning_text","text":null}]}]`,
		`[{"type":"message","content":[{"type":"output_audio","audio":"data"}]}]`,
		`[{"type":"message","content":[{"type":"output_text"}]}]`,
		`[{"type":"message","content":[{"type":"output_text","text":null}]}]`,
		`[{"type":"message","content":[{"type":"refusal"}]}]`,
		`[{"type":"message","content":null}]`,
		`[{"type":"message","role":"user","content":[]}]`,
		`[{"type":"message","status":"in_progress","content":[]}]`,
		`[{"type":"message","status":"incomplete","content":[]}]`,
		`[{"type":"message","content":[{"type":"output_text","text":"answer","annotations":[{"type":"url_citation","url":"https://example.com"}]}]}]`,
		`[{"type":"message","content":[{"type":"output_text","text":"answer","logprobs":[{"token":"a"}]}]}]`,
		`[{"type":"function_call","name":"lookup"}]`,
		`[{"type":"function_call","name":"lookup","arguments":null}]`,
		`[{"type":"function_call","name":"lookup","namespace":"tools","arguments":"{}"}]`,
		`[{"type":"message","content":[],"arguments":"{}"}]`,
		`[{"type":"message","content":[],"summary":[]}]`,
		`[{"type":"message","content":[],"name":""}]`,
		`[{"type":"reasoning","summary":[],"call_id":"ignored"}]`,
		`[{"type":"reasoning","summary":[],"role":"assistant"}]`,
		`[{"type":"function_call","name":"lookup","arguments":"{}","content":[]}]`,
		`[{"type":"function_call","name":"lookup","arguments":"{}","summary":null}]`,
	} {
		body := []byte(`{"status":"completed","output":` + output + `}`)
		for name, convert := range map[string]func([]byte, string) ([]byte, error){
			"chat":     ChatFromResponses,
			"messages": func(b []byte, m string) ([]byte, error) { return FromResponses(b, m, false) },
		} {
			t.Run(name+"/"+output, func(t *testing.T) {
				if got, err := convert(body, "m"); err == nil {
					t.Fatalf("unsupported output silently converted: %s", got)
				}
			})
		}
	}

	for _, block := range []string{
		`{"type":"redacted_thinking","data":"opaque"}`,
		`{"type":"server_tool_use","id":"srv","name":"web_search","input":{}}`,
		`{"type":"image","source":{"type":"url","url":"https://example.com/i"}}`,
		`{"type":"text"}`,
		`{"type":"text","text":null}`,
		`{"type":"thinking","thinking":null}`,
		`{"type":"text","text":"answer","citations":[{"type":"char_location","start_char_index":0}]}`,
		`{"type":"tool_use","name":"lookup","input":null}`,
		`{"type":"tool_use","input":{}}`,
	} {
		body := []byte(`{"stop_reason":"end_turn","content":[` + block + `]}`)
		for name, convert := range map[string]func([]byte, string) ([]byte, error){
			"chat": ChatFromAnthropic, "responses": ResponsesFromAnthropic,
		} {
			t.Run(name+"/"+block, func(t *testing.T) {
				if got, err := convert(body, "m"); err == nil {
					t.Fatalf("unsupported output silently converted: %s", got)
				}
			})
		}
	}

	for _, fields := range []string{
		`"audio":{"id":"audio"}`, `"function_call":{"name":"lookup","arguments":"{}"}`,
		`"reasoning_details":[{"type":"reasoning.encrypted","data":"opaque"}]`,
		`"annotations":[{"type":"url_citation","url":"https://example.com"}]`,
		`"content":[{"type":"image_url","image_url":{"url":"https://example.com/i"}}]`,
		`"content":[{"type":"text","text":null}]`,
		`"content":[{"type":"text"}]`,
		`"content":[{"type":"thinking","thinking":[{"type":"other","text":"lost"}]}]`,
	} {
		body := []byte(`{"choices":[{"finish_reason":"stop","message":{` + fields + `}}]}`)
		for name, convert := range map[string]func([]byte, string) ([]byte, error){
			"responses": ResponsesFromChat,
			"messages":  func(b []byte, m string) ([]byte, error) { return FromOpenAI(b, m, false) },
		} {
			t.Run(name+"/"+fields, func(t *testing.T) {
				if got, err := convert(body, "m"); err == nil {
					t.Fatalf("unsupported output silently converted: %s", got)
				}
			})
		}
	}
}

func TestBufferedEmptyAndPlainReasoningOutput(t *testing.T) {
	messages := []byte(`{"stop_reason":"end_turn","content":[{"type":"thinking","thinking":" a ","signature":"opaque-provider-signature"},{"type":"text","text":"","citations":[]}]}`)
	out, err := ResponsesFromAnthropic(messages, "m")
	if err != nil {
		t.Fatal(err)
	}
	var response responsesResponse
	if err := json.Unmarshal(out, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 2 || response.Output[0].Summary[0].Text != " a " || len(response.Output[1].Content) != 1 || response.Output[1].Content[0].Text != "" {
		t.Fatalf("empty text or display reasoning lost: %s", out)
	}
	if _, err := ChatFromResponses(out, "m"); err != nil {
		t.Fatal(err)
	}
}
