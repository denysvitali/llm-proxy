package translate

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestChatResponsesMessageFidelity(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"system","content":" root \n"},
		{"role":"user","content":""},
		{"role":"developer","content":[{"type":"text","text":" a "},{"type":"text","text":""},{"type":"text","text":"b\n"}]},
		{"role":"assistant","content":" \n\t"},
		{"role":"user","content":[{"type":"text","text":"before"},{"type":"image_url","image_url":{"url":"https://example.invalid/image.png","detail":"low"}},{"type":"text","text":"after"}]},
		{"role":"system","content":"last"}
	]}`)
	converted, err := ChatToResponses(body, "model")
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Input []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(converted, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Input) != 6 || response.Input[1].Content[0]["text"] != "" {
		t.Fatalf("empty text or message lost: %s", converted)
	}
	if _, present := response.Input[4].Content[1]["text"]; present {
		t.Fatalf("image gained unrelated text field: %s", converted)
	}
	restored, err := ResponsesToChat(converted, "model")
	if err != nil {
		t.Fatal(err)
	}
	var before, after struct {
		Messages []any `json:"messages"`
	}
	if err := json.Unmarshal(body, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(restored, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Messages, after.Messages) {
		t.Fatalf("message round trip changed:\n%s\n%s", body, restored)
	}
}

func TestResponsesTypelessMessageFidelity(t *testing.T) {
	for _, text := range []string{"", " \n\t", " a b "} {
		encoded, _ := json.Marshal(text)
		for _, role := range []string{"system", "developer", "user", "assistant"} {
			body := []byte(`{"input":[{"role":"` + role + `","content":` + string(encoded) + `}]}`)
			got, err := ResponsesToChat(body, "model")
			if err != nil {
				t.Fatal(err)
			}
			var out openAIRequest
			if err := json.Unmarshal(got, &out); err != nil {
				t.Fatal(err)
			}
			if len(out.Messages) != 1 || out.Messages[0].Role != role || out.Messages[0].Content != text {
				t.Fatalf("role/text changed: %s", got)
			}
		}
	}
}

func TestChatResponsesRejectMalformedContentParts(t *testing.T) {
	for _, route := range []struct {
		name, textType, input string
		convert               func([]byte, string) ([]byte, error)
	}{
		{"chat", "text", `"messages":[{"role":"user","content":%s}]`, ChatToResponses},
		{"responses", "input_text", `"input":[{"role":"user","content":%s}]`, ResponsesToChat},
	} {
		for _, content := range []string{
			`[]`,
			`[{"type":"%s"}]`,
			`[{"type":"%s","text":null}]`,
			`[{"type":"%s","text":"","extra":true}]`,
			`[{"type":"%s","text":"","image_url":"ignored"}]`,
		} {
			t.Run(route.name+"/"+content, func(t *testing.T) {
				content = strings.ReplaceAll(content, "%s", route.textType)
				body := []byte(`{` + fmt.Sprintf(route.input, content) + `}`)
				if got, err := route.convert(body, "model"); err == nil {
					t.Fatalf("malformed content became a different request: %s", got)
				}
			})
		}
	}
}

func TestChatResponsesUnsupportedContent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		convert func([]byte, string) ([]byte, error)
		body    string
	}{
		{"chat audio", ChatToResponses, `{"messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"fixture","format":"wav"}}]}]}`},
		{"chat role", ChatToResponses, `{"messages":[{"role":"unknown","content":"hello"}]}`},
		{"responses file", ResponsesToChat, `{"input":[{"role":"user","content":[{"type":"input_file","file_id":"fixture"}]}]}`},
		{"responses assistant image", ResponsesToChat, `{"input":[{"role":"assistant","content":[{"type":"input_image","image_url":"https://example.invalid/a.png"}]}]}`},
		{"responses role", ResponsesToChat, `{"input":[{"type":"message","role":"unknown","content":"hello"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := tc.convert([]byte(tc.body), "model"); err == nil {
				t.Fatalf("silently accepted unsupported content: %s", got)
			}
		})
	}
}
