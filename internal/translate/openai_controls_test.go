package translate

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestTranslatedOpenAIControls(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		convert    func([]byte, string) ([]byte, error)
		want       map[string]json.RawMessage
	}{
		{"chat to responses", `{"messages":[{"role":"user","content":"hi"}],"temperature":0,"top_p":0,"parallel_tool_calls":false,"store":false,"metadata":{"tag":"x"},"reasoning_effort":"low"}`, ChatToResponses, map[string]json.RawMessage{
			"temperature": []byte(`0`), "top_p": []byte(`0`), "parallel_tool_calls": []byte(`false`), "store": []byte(`false`), "metadata": []byte(`{"tag":"x"}`), "reasoning": []byte(`{"effort":"low"}`),
		}},
		{"responses to chat", `{"input":"hi","temperature":0,"top_p":0,"parallel_tool_calls":false,"store":true,"reasoning":{"effort":"minimal"}}`, ResponsesToChat, map[string]json.RawMessage{
			"temperature": []byte(`0`), "top_p": []byte(`0`), "parallel_tool_calls": []byte(`false`), "store": []byte(`true`), "reasoning_effort": []byte(`"minimal"`),
		}},
		{"chat to messages", `{"messages":[{"role":"user","content":"hi"}],"stop":[""," END "],"temperature":0,"parallel_tool_calls":false}`, ChatToAnthropic, map[string]json.RawMessage{
			"stop_sequences": []byte(`[""," END "]`), "temperature": []byte(`0`), "tool_choice": []byte(`{"type":"auto","disable_parallel_tool_use":true}`),
		}},
		{"responses to messages", `{"input":"hi","parallel_tool_calls":true}`, ResponsesToAnthropic, map[string]json.RawMessage{
			"tool_choice": []byte(`{"type":"auto","disable_parallel_tool_use":false}`),
		}},
		{"none disables tools", `{"input":"hi","tool_choice":"none","parallel_tool_calls":false}`, ResponsesToAnthropic, map[string]json.RawMessage{
			"tool_choice": []byte(`{"type":"none"}`),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tc.convert([]byte(tc.body), "model")
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			for key, want := range tc.want {
				var actual, expected any
				if err := json.Unmarshal(got[key], &actual); err != nil {
					t.Fatalf("missing %s: %s", key, out)
				}
				if err := json.Unmarshal(want, &expected); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(actual, expected) {
					t.Errorf("%s = %s, want %s", key, got[key], want)
				}
			}
		})
	}
}

func TestOpenAIControlRoundTrip(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_schema","json_schema":{"name":"result","description":"","strict":false,"schema":{"type":"object","properties":{"n":{"const":9007199254740993}}}}},"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"},"strict":false}}]}`)
	responses, err := ChatToResponses(body, "model")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := ResponsesToChat(responses, "model")
	if err != nil {
		t.Fatal(err)
	}
	for _, wire := range [][]byte{responses, chat} {
		if !strings.Contains(string(wire), `9007199254740993`) {
			t.Fatalf("schema number changed: %s", wire)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(wire, &fields); err != nil {
			t.Fatal(err)
		}
		if _, ok := fields["store"]; ok {
			t.Fatalf("absent store became explicit: %s", wire)
		}
		if strings.Count(string(wire), `"strict":false`) != 2 {
			t.Fatalf("explicit strict false lost: %s", wire)
		}
	}
	var before, after map[string]json.RawMessage
	if err := json.Unmarshal(body, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(chat, &after); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"response_format", "tools"} {
		var a, b any
		if err := json.Unmarshal(before[key], &a); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(after[key], &b); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s changed: %s", key, chat)
		}
	}
}

func TestTranslatedControlBoundaries(t *testing.T) {
	for _, route := range []struct {
		name, input string
		convert     func([]byte, string) ([]byte, error)
	}{
		{"chat-responses", `"messages":[{"role":"user","content":"hi"}]`, ChatToResponses},
		{"chat-messages", `"messages":[{"role":"user","content":"hi"}]`, ChatToAnthropic},
		{"responses-chat", `"input":"hi"`, ResponsesToChat},
		{"responses-messages", `"input":"hi"`, ResponsesToAnthropic},
	} {
		for _, fields := range []string{
			`"previous_response_id":"resp_prior"`, `"conversation":"conv_prior"`, `"n":2`, `"parallel_tool_calls":"false"`,
			`"tool_choice":"bogus"`, `"tool_choice":{}`, `"tool_choice":{"type":"function","name":"f","extra":true}`,
			`"audio":{}`, `"logit_bias":{"10":5}`, `"functions":[]`,
		} {
			t.Run(route.name+"/"+fields, func(t *testing.T) {
				if out, err := route.convert([]byte(`{`+route.input+`,`+fields+`}`), "model"); err == nil {
					t.Fatalf("unsupported controls accepted: %s", out)
				}
			})
		}
	}
	for _, tc := range []struct {
		body    string
		convert func([]byte, string) ([]byte, error)
	}{
		{`{"messages":[{"role":"user","content":"hi"}],"stop":"END"}`, ChatToResponses},
		{`{"messages":[{"role":"user","content":"hi"}],"temperature":1.1}`, ChatToAnthropic},
		{`{"input":"hi","store":false}`, ResponsesToAnthropic},
		{`{"input":"hi","reasoning":{"effort":"low","summary":"auto"}}`, ResponsesToChat},
		{`{"input":"hi","text":{"verbosity":"low"}}`, ResponsesToChat},
		{`{"input":"hi","tools":[{"type":"function","name":"f","strict":true}]}`, ResponsesToAnthropic},
		{`{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","strict":false}}]}`, ChatToAnthropic},
	} {
		if out, err := tc.convert([]byte(tc.body), "model"); err == nil {
			t.Errorf("unsupported control accepted: %s", out)
		}
	}
}
