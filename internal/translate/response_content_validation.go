package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
)

// Buffered output validation is separate from stream decoding: announcements may
// legitimately omit fields that must exist on a completed content block.
func validateResponsesOutput(body []byte, status string) error {
	var response struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}
	for i, raw := range response.Output {
		var item struct {
			ID               string            `json:"id"`
			Type             string            `json:"type"`
			Role             string            `json:"role"`
			Status           string            `json:"status"`
			Content          []json.RawMessage `json:"content"`
			Summary          []json.RawMessage `json:"summary"`
			CallID           string            `json:"call_id"`
			Namespace        string            `json:"namespace"`
			Name             string            `json:"name"`
			Arguments        *string           `json:"arguments"`
			EncryptedContent json.RawMessage   `json:"encrypted_content"`
			Phase            string            `json:"phase"`
		}
		if err := decodeContentPart(raw, &item); err != nil {
			return fmt.Errorf("unsupported upstream output item %d: %w", i, err)
		}
		if nonNullJSON(item.EncryptedContent) {
			return fmt.Errorf("opaque Responses reasoning requires a native route")
		}
		allowed := []string{"id", "type", "status"}
		switch item.Type {
		case "message":
			// phase ("commentary" or "final_answer", sent by the ChatGPT Codex
			// endpoint) labels the message's role in the turn; the text itself
			// is unchanged, so the label is dropped on translation.
			allowed = append(allowed, "role", "content", "phase")
		case "reasoning":
			allowed = append(allowed, "summary", "content", "encrypted_content")
		case "function_call":
			allowed = append(allowed, "call_id", "name", "namespace", "arguments")
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		for field := range fields {
			if !slices.Contains(allowed, field) {
				return fmt.Errorf("upstream %s output cannot represent field %q", item.Type, field)
			}
		}
		switch item.Status {
		case "", "completed":
		case "incomplete":
			if status != "incomplete" {
				return fmt.Errorf("incomplete output item requires an incomplete response")
			}
		default:
			return fmt.Errorf("upstream output item status %q is not terminal", item.Status)
		}
		switch item.Type {
		case "message":
			if item.Role != "" && item.Role != "assistant" {
				return fmt.Errorf("unsupported upstream output role %q", item.Role)
			}
			if item.Content == nil {
				return fmt.Errorf("upstream message output requires a content array")
			}
			for _, part := range item.Content {
				if err := validateResponsesOutputPart(part, false, false); err != nil {
					return err
				}
			}
		case "reasoning":
			for _, part := range item.Summary {
				if err := validateResponsesOutputPart(part, true, true); err != nil {
					return err
				}
			}
			for _, part := range item.Content {
				if err := validateResponsesOutputPart(part, true, false); err != nil {
					return err
				}
			}
		case "function_call":
			if item.Name == "" || item.Arguments == nil {
				return fmt.Errorf("upstream function call requires name and string arguments")
			}
			if item.Namespace != "" {
				return fmt.Errorf("namespaced Responses output requires a native route")
			}
		default:
			return fmt.Errorf("upstream Responses output type %q requires a native route", item.Type)
		}
	}
	return nil
}

func validateResponsesOutputPart(raw json.RawMessage, reasoning, summary bool) error {
	var part struct {
		Type        string            `json:"type"`
		Text        *string           `json:"text"`
		Refusal     *string           `json:"refusal"`
		Annotations []json.RawMessage `json:"annotations"`
		Logprobs    []json.RawMessage `json:"logprobs"`
	}
	if err := decodeContentPart(raw, &part); err != nil {
		return fmt.Errorf("unsupported upstream output content: %w", err)
	}
	if len(part.Annotations) > 0 || len(part.Logprobs) > 0 {
		return fmt.Errorf("output annotations and logprobs require a native route")
	}
	if reasoning {
		want := "reasoning_text"
		if summary {
			want = "summary_text"
		}
		if part.Type != want || part.Text == nil || part.Refusal != nil {
			return fmt.Errorf("upstream reasoning requires %s with string text", want)
		}
		return nil
	}
	switch part.Type {
	case "output_text":
		if part.Text != nil && part.Refusal == nil {
			return nil
		}
	case "refusal":
		if part.Refusal != nil && part.Text == nil {
			return nil
		}
	}
	return fmt.Errorf("unsupported or malformed upstream message content %q", part.Type)
}

func validateAnthropicOutput(body []byte) error {
	var response struct {
		Content []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}
	for _, raw := range response.Content {
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			return err
		}
		switch header.Type {
		case "text":
			var part struct {
				Type      string            `json:"type"`
				Text      *string           `json:"text"`
				Citations []json.RawMessage `json:"citations"`
			}
			if err := decodeContentPart(raw, &part); err != nil || part.Text == nil {
				return fmt.Errorf("upstream text block requires string text and supported fields")
			}
			if len(part.Citations) > 0 {
				return fmt.Errorf("messages citations require a native route")
			}
		case "thinking":
			var part struct {
				Type      string  `json:"type"`
				Thinking  *string `json:"thinking"`
				Signature string  `json:"signature"`
			}
			if err := decodeContentPart(raw, &part); err != nil || part.Thinking == nil {
				return fmt.Errorf("upstream thinking block requires string thinking and supported fields")
			}
		case "tool_use":
			var part struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			}
			if err := decodeContentPart(raw, &part); err != nil || part.Name == "" {
				return fmt.Errorf("upstream tool_use requires name and supported fields")
			}
			if _, err := compactJSON(part.Input); err != nil {
				return err
			}
		default:
			return fmt.Errorf("upstream Messages content type %q requires a native route", header.Type)
		}
	}
	return nil
}

func validateChatOutput(body []byte) error {
	var response struct {
		Choices []struct {
			Message map[string]json.RawMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}
	for _, choice := range response.Choices {
		for _, key := range []string{"audio", "function_call", "reasoning_details"} {
			if nonNullJSON(choice.Message[key]) {
				return fmt.Errorf("upstream Chat %s requires a native route", key)
			}
		}
		if raw := choice.Message["annotations"]; nonNullJSON(raw) {
			var annotations []json.RawMessage
			if err := json.Unmarshal(raw, &annotations); err != nil || len(annotations) != 0 {
				return fmt.Errorf("upstream Chat annotations require a native route")
			}
		}
	}
	return nil
}

func nonNullJSON(raw json.RawMessage) bool {
	return len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
