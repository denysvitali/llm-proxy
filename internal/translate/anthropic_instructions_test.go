package translate

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestAnthropicInstructionFidelity(t *testing.T) {
	for _, tc := range []struct {
		name, field, textType string
		convert               func([]byte, string) ([]byte, error)
	}{
		{"chat", "messages", "text", ChatToAnthropic},
		{"responses", "input", "input_text", ResponsesToAnthropic},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, content := range []string{`""`, `" \n\t "`, `[{"type":"` + tc.textType + `","text":""},{"type":"` + tc.textType + `","text":" \n "}]`} {
				body := []byte(`{"model":"m","` + tc.field + `":[{"role":"system","content":` + content + `},{"role":"system","content":"last"},{"role":"user","content":"hi"}]}`)
				out, err := tc.convert(body, "upstream")
				if err != nil {
					t.Fatal(err)
				}
				got := decodeJSON(t, out)
				blocks := jarray(t, got["system"], "system")
				var want []map[string]any
				var text string
				if json.Unmarshal([]byte(content), &text) == nil {
					want = append(want, map[string]any{"type": "text", "text": text})
				} else {
					if err := json.Unmarshal([]byte(content), &want); err != nil {
						t.Fatal(err)
					}
					for _, block := range want {
						block["type"] = "text"
					}
				}
				want = append(want, map[string]any{"type": "text", "text": "last"})
				if len(blocks) != len(want) {
					t.Fatalf("system blocks = %v, want %v", blocks, want)
				}
				for i := range want {
					if !reflect.DeepEqual(blocks[i], want[i]) {
						t.Errorf("system block %d = %v, want %v", i, blocks[i], want[i])
					}
				}
			}
			out, err := tc.convert([]byte(`{"model":"m","`+tc.field+`":[{"role":"system","content":""},{"role":"user","content":"hi"}]}`), "upstream")
			if err != nil {
				t.Fatal(err)
			}
			if system, exists := decodeJSON(t, out)["system"]; !exists || system != "" {
				t.Fatalf("explicit empty system lost: %s", out)
			}
		})
	}
}

// Messages has one instruction tier: leading developer messages fold into the
// top-level system, and later prompt messages keep their position as system
// messages inside messages.
func TestAnthropicInstructionPlacement(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		convert     func([]byte, string) ([]byte, error)
	}{
		{"chat", "messages", ChatToAnthropic},
		{"responses", "input", ResponsesToAnthropic},
	} {
		for _, test := range []struct{ name, messages, want string }{
			{"developer", `{"role":"developer","content":"rules"},{"role":"user","content":"hi"}`, `"system":"rules"`},
			{"late system", `{"role":"user","content":"hi"},{"role":"system","content":"rules"}`, `{"role":"system","content":"rules"}`},
			{"late developer", `{"role":"user","content":"hi"},{"role":"developer","content":"rules"}`, `{"role":"system","content":"rules"}`},
		} {
			t.Run(tc.name+"/"+test.name, func(t *testing.T) {
				out, err := tc.convert([]byte(`{"model":"m","`+tc.field+`":[`+test.messages+`]}`), "upstream")
				if err != nil || !strings.Contains(string(out), test.want) {
					t.Fatalf("output = %s, error = %v; want %s", out, err, test.want)
				}
			})
		}
	}
}

func TestAnthropicInstructionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, field, textType string
		convert               func([]byte, string) ([]byte, error)
	}{
		{"chat", "messages", "text", ChatToAnthropic},
		{"responses", "input", "input_text", ResponsesToAnthropic},
	} {
		for _, test := range []struct{ name, messages, reason string }{
			{"empty array", `{"role":"system","content":[]},{"role":"user","content":"hi"}`, "non-empty"},
			{"missing text", `{"role":"system","content":[{"type":"` + tc.textType + `"}]},{"role":"user","content":"hi"}`, "string text"},
			{"null text", `{"role":"system","content":[{"type":"` + tc.textType + `","text":null}]},{"role":"user","content":"hi"}`, "string text"},
			{"unknown field", `{"role":"system","content":[{"type":"` + tc.textType + `","text":"rules","extension":true}]},{"role":"user","content":"hi"}`, "unsupported fields"},
			{"image", `{"role":"system","content":[{"type":"input_image","image_url":"https://example.com/image.png"}]},{"role":"user","content":"hi"}`, "unsupported"},
		} {
			t.Run(tc.name+"/"+test.name, func(t *testing.T) {
				out, err := tc.convert([]byte(`{"model":"m","`+tc.field+`":[`+test.messages+`]}`), "upstream")
				if err == nil || !strings.Contains(err.Error(), test.reason) {
					t.Fatalf("output = %s, error = %v; want %q", out, err, test.reason)
				}
			})
		}
	}
}
