package translate

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSystemMessageValidationErrors(t *testing.T) {
	const privateContent = "private-system-content-do-not-echo"
	for _, route := range []struct {
		name    string
		convert func(*Request, string) ([]byte, error)
	}{
		{"chat", ToOpenAI},
		{"responses", ToResponses},
	} {
		for _, tc := range []struct {
			name    string
			invalid string
			reason  string
		}{
			{"missing text", `{"type":"text"}`, "text block requires string text and no unsupported fields"},
			{"null text", `{"type":"text","text":null}`, "text block requires string text and no unsupported fields"},
			{"document", `{"type":"document","source":{"type":"text","data":"` + privateContent + `"}}`, `messages content type "document" cannot be represented on this OpenAI route`},
			{"image", `{"type":"image","source":{"type":"url","url":"https://example.invalid/` + privateContent + `"}}`, "system content requires text blocks"},
			{"unknown field", `{"type":"text","text":"` + privateContent + `","extension":"` + privateContent + `"}`, `text block requires string text and no unsupported fields (unknown field "extension")`},
		} {
			t.Run(route.name+"/"+tc.name, func(t *testing.T) {
				body := []byte(`{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"},{"role":"system","content":[{"type":"text","text":"` + privateContent + `"},` + tc.invalid + `]}]}`)
				request, err := ParseRequest(body)
				if err != nil {
					t.Fatal(err)
				}
				_, err = route.convert(request, "model")
				if err == nil {
					t.Fatal("invalid system message accepted")
				}
				if strings.Contains(err.Error(), privateContent) {
					t.Fatal("system message error exposed private content")
				}
				if want := "system message: " + tc.reason; err.Error() != want {
					t.Fatalf("error = %q, want %q", err, want)
				}
				if cause := errors.Unwrap(err); cause == nil || cause.Error() != tc.reason {
					t.Fatalf("underlying validation error = %v, want %q", cause, tc.reason)
				}
			})
		}
	}
}

func TestSystemMessageMissingContentErrors(t *testing.T) {
	for _, route := range []struct {
		name    string
		convert func(AnthropicMessage) error
	}{
		{"chat", func(message AnthropicMessage) error {
			_, err := translateMessage(message)
			return err
		}},
		{"responses", func(message AnthropicMessage) error {
			_, err := responsesItems(message)
			return err
		}},
	} {
		for _, tc := range []struct {
			name    string
			content json.RawMessage
			want    string
			wrapped bool
		}{
			{"missing", nil, "system message requires text content", false},
			{"null", json.RawMessage(`null`), "system message: messages content must be a string or a block array", true},
		} {
			t.Run(route.name+"/"+tc.name, func(t *testing.T) {
				err := route.convert(AnthropicMessage{Role: "system", Content: tc.content})
				if err == nil || err.Error() != tc.want {
					t.Fatalf("error = %v, want %q", err, tc.want)
				}
				if wrapped := errors.Unwrap(err) != nil; wrapped != tc.wrapped {
					t.Fatalf("error wrapped = %t, want %t", wrapped, tc.wrapped)
				}
			})
		}
	}
}
