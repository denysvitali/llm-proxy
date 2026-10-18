package translate

import (
	"encoding/json"
	"fmt"
)

// openAIToolOutput maps text-only tool results without inventing separators.
// Chat tool messages cannot represent Responses image/file result parts.
func openAIToolOutput(raw json.RawMessage, fromResponses bool) (json.RawMessage, error) {
	if _, ok := plainString(raw); ok {
		return raw, nil
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil || parts == nil {
		return nil, fmt.Errorf("tool output must be a string or an array of text parts")
	}
	out := make([]map[string]string, 0, len(parts))
	for _, rawPart := range parts {
		var part struct {
			Type string  `json:"type"`
			Text *string `json:"text"`
		}
		if err := decodeContentPart(rawPart, &part); err != nil || part.Text == nil {
			return nil, fmt.Errorf("tool output requires text parts with a string text field")
		}
		inputType, outputType := "text", "input_text"
		if fromResponses {
			inputType, outputType = "input_text", "text"
		}
		if part.Type != inputType {
			return nil, fmt.Errorf("unsupported tool output part %q", part.Type)
		}
		out = append(out, map[string]string{"type": outputType, "text": *part.Text})
	}
	return json.Marshal(out)
}
