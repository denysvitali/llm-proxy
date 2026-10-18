package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ValidateGenerationControls checks common numeric controls without changing
// provider-specific fields or decoding conversation contents.
func ValidateGenerationControls(body []byte, dialect string) error {
	return validateGenerationControls(body, dialect)
}

// ValidateMessageInput checks the required Messages/Chat history envelope.
// Responses input is optional (for example with provider-owned continuation),
// so native Responses requests retain upstream input validation.
func ValidateMessageInput(body []byte, dialect string) error {
	if dialect == "openai-responses" {
		return nil
	}
	if dialect != "anthropic" && dialect != "openai-chat" {
		return fmt.Errorf("unsupported request dialect %q", dialect)
	}
	var fields struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &fields); err != nil {
		return fmt.Errorf("messages must be a non-empty array")
	}
	if len(fields.Messages) == 0 {
		return fmt.Errorf("request contains no messages")
	}
	for _, message := range fields.Messages {
		message = bytes.TrimSpace(message)
		if len(message) == 0 || message[0] != '{' {
			return fmt.Errorf("each message must be an object")
		}
	}
	return nil
}

// validateGenerationControls checks values before optional fields are decoded
// into zero-valued Go fields. Null OpenAI controls mean the provider default.
func validateGenerationControls(body []byte, dialect string) error {
	anthropic := dialect == "anthropic"
	if !anthropic && dialect != "openai-chat" && dialect != "openai-responses" {
		return fmt.Errorf("unsupported request dialect %q", dialect)
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		return fmt.Errorf("request body must be a JSON object")
	}
	var fields struct {
		MaxTokens           json.RawMessage `json:"max_tokens"`
		MaxCompletionTokens json.RawMessage `json:"max_completion_tokens"`
		MaxOutputTokens     json.RawMessage `json:"max_output_tokens"`
		Temperature         json.RawMessage `json:"temperature"`
		TopP                json.RawMessage `json:"top_p"`
		TopK                json.RawMessage `json:"top_k"`
	}
	if err := json.Unmarshal(body, &fields); err != nil {
		return fmt.Errorf("request body must be a JSON object")
	}
	// Only standard controls for the inbound API belong to this validation.
	// Other names may be provider-specific extensions on native routes.
	switch dialect {
	case "anthropic":
		fields.MaxCompletionTokens, fields.MaxOutputTokens = nil, nil
	case "openai-chat":
		fields.MaxOutputTokens, fields.TopK = nil, nil
	case "openai-responses":
		fields.MaxTokens, fields.MaxCompletionTokens, fields.TopK = nil, nil, nil
	}
	for _, field := range []struct {
		name string
		raw  json.RawMessage
	}{
		{"max_tokens", fields.MaxTokens},
		{"max_completion_tokens", fields.MaxCompletionTokens},
		{"max_output_tokens", fields.MaxOutputTokens},
		{"top_k", fields.TopK},
	} {
		if len(field.raw) == 0 || !anthropic && string(field.raw) == "null" {
			continue
		}
		minimum := 1
		if field.name == "top_k" {
			minimum = 0
		}
		var value int
		if string(field.raw) == "null" || json.Unmarshal(field.raw, &value) != nil || value < minimum {
			return fmt.Errorf("%s must be an integer greater than or equal to %d", field.name, minimum)
		}
	}
	maxTemperature := 2.0
	if anthropic {
		maxTemperature = 1
	}
	for _, field := range []struct {
		name string
		raw  json.RawMessage
		max  float64
	}{
		{"temperature", fields.Temperature, maxTemperature},
		{"top_p", fields.TopP, 1},
	} {
		if len(field.raw) == 0 || !anthropic && string(field.raw) == "null" {
			continue
		}
		var value float64
		if string(field.raw) == "null" || json.Unmarshal(field.raw, &value) != nil || value < 0 || value > field.max {
			return fmt.Errorf("%s must be a number between 0 and %g", field.name, field.max)
		}
	}
	return nil
}
