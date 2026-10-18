package translate

import (
	"strings"
	"testing"
)

func TestGenerationControlsBoundaries(t *testing.T) {
	for _, tc := range []struct {
		body, dialect string
		valid         bool
	}{
		{`{"temperature":0,"top_p":0,"top_k":0,"max_tokens":1}`, "anthropic", true},
		{`{"temperature":1,"top_p":1}`, "anthropic", true},
		{`{"temperature":2,"max_completion_tokens":1}`, "openai-chat", true},
		{`{"temperature":null,"max_tokens":null}`, "openai-chat", true},
		{`{"temperature":2}`, "anthropic", false},
		{`{"temperature":null}`, "anthropic", false},
		{`{"temperature":-0.1}`, "openai-chat", false},
		{`{"temperature":1e999}`, "openai-chat", false},
		{`{"top_p":1.1}`, "openai-chat", false},
		{`{"max_tokens":0}`, "openai-chat", false},
		{`{"max_tokens":-1}`, "anthropic", false},
		{`{"max_output_tokens":1.5}`, "openai-responses", false},
		{`{"max_completion_tokens":"10"}`, "openai-chat", false},
		{`{"max_output_tokens":0,"top_k":-1}`, "openai-chat", true},
		{`{"max_tokens":0,"max_completion_tokens":0,"top_k":-1}`, "openai-responses", true},
		{`{"max_completion_tokens":0,"max_output_tokens":0}`, "anthropic", true},
		{`null`, "openai-chat", false},
		{`[]`, "anthropic", false},
		{`{}`, "unknown", false},
	} {
		err := validateGenerationControls([]byte(tc.body), tc.dialect)
		if (err == nil) != tc.valid {
			t.Errorf("validate(%s, %s) = %v; valid = %t", tc.body, tc.dialect, err, tc.valid)
		}
	}
}

func TestTranslatedRequestsRejectInvalidGenerationControls(t *testing.T) {
	for _, convert := range []struct {
		name string
		fn   func([]byte, string) ([]byte, error)
		body string
	}{
		{"chat-anthropic", ChatToAnthropic, `{"messages":[{"role":"user","content":"hello"}],"max_tokens":0}`},
		{"chat-responses", ChatToResponses, `{"messages":[{"role":"user","content":"hello"}],"max_tokens":0}`},
		{"responses-chat", ResponsesToChat, `{"input":"hello","max_output_tokens":0}`},
		{"responses-anthropic", ResponsesToAnthropic, `{"input":"hello","max_output_tokens":0}`},
	} {
		t.Run(convert.name, func(t *testing.T) {
			if _, err := convert.fn([]byte(convert.body), "model"); err == nil || !strings.Contains(err.Error(), "must be an integer") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if _, err := ParseRequest([]byte(`{"max_tokens":0}`)); err == nil {
		t.Fatal("accepted zero Anthropic max_tokens")
	}
}
