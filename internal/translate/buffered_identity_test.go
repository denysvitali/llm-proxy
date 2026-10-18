package translate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBufferedResponseIdentityAndModel(t *testing.T) {
	chat := `{"model":"provider","choices":[{"finish_reason":"stop","message":{"content":""}}]}`
	responses := `{"model":"provider","created_at":123,"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":""}]}]}`
	messages := `{"model":"provider","stop_reason":"end_turn","content":[{"type":"text","text":""}]}`
	for _, route := range []struct {
		name, body, prefix string
		convert            func([]byte, string) ([]byte, error)
	}{
		{"chat-responses", chat, "resp_", ResponsesFromChat},
		{"chat-messages", chat, "msg_", func(b []byte, m string) ([]byte, error) { return FromOpenAI(b, m, false) }},
		{"responses-chat", responses, "chatcmpl-", ChatFromResponses},
		{"responses-messages", responses, "msg_", func(b []byte, m string) ([]byte, error) { return FromResponses(b, m, false) }},
		{"messages-chat", messages, "chatcmpl-", ChatFromAnthropic},
		{"messages-responses", messages, "resp_", ResponsesFromAnthropic},
	} {
		t.Run(route.name, func(t *testing.T) {
			seen := map[string]bool{}
			for i := 0; i < 2; i++ {
				got, err := route.convert([]byte(route.body), "alias")
				if err != nil {
					t.Fatal(err)
				}
				var result struct {
					ID, Model string
					Created   int64
				}
				if err := json.Unmarshal(got, &result); err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(result.ID, route.prefix) || seen[result.ID] || result.Model != "provider" {
					t.Fatalf("invalid response identity/model: %s", got)
				}
				seen[result.ID] = true
				if route.name == "responses-chat" && result.Created != 123 {
					t.Fatalf("source timestamp changed: %s", got)
				}
			}
			body := []byte(`{"id":"upstream",` + route.body[1:])
			got, err := route.convert(body, "alias")
			if err != nil {
				t.Fatal(err)
			}
			var result struct{ ID string }
			if err := json.Unmarshal(got, &result); err != nil {
				t.Fatal(err)
			}
			want := "upstream"
			if route.prefix == "msg_" {
				want = "msg_upstream"
			}
			if result.ID != want {
				t.Fatalf("existing identity changed: %s", got)
			}
		})
	}
}
