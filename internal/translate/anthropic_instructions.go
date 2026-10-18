package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Anthropic has one top-level system instruction area, not an ordered
// system/developer message hierarchy. Only leading system messages map there.
func appendAnthropicInstruction(current any, role string, content json.RawMessage, textType string, conversationStarted bool) (any, error) {
	if role != "system" {
		return nil, fmt.Errorf("developer messages cannot preserve their authority on the Anthropic wire")
	}
	if conversationStarted {
		return nil, fmt.Errorf("system messages after conversation input cannot preserve their position on the Anthropic wire")
	}
	type textBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	var blocks []textBlock
	if text, ok := plainString(content); ok {
		if current == nil {
			return text, nil
		}
		blocks = []textBlock{{Type: "text", Text: text}}
	} else {
		var parts []struct {
			Type string  `json:"type"`
			Text *string `json:"text"`
		}
		decoder := json.NewDecoder(bytes.NewReader(content))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&parts); err != nil || len(parts) == 0 {
			return nil, fmt.Errorf("system content must be text or a non-empty array of text parts without unsupported fields")
		}
		for _, part := range parts {
			if part.Type != textType {
				return nil, fmt.Errorf("unsupported system content type %q", part.Type)
			}
			if part.Text == nil {
				return nil, fmt.Errorf("system text parts require a string text field")
			}
			blocks = append(blocks, textBlock{Type: "text", Text: *part.Text})
		}
	}
	// Decode existing content into the same local shape without joining text or
	// adding separators. Empty text is retained as an explicit text block.
	var previous []textBlock
	if current != nil {
		if text, ok := current.(string); ok {
			previous = []textBlock{{Type: "text", Text: text}}
		} else if err := json.Unmarshal(mustMarshal(current), &previous); err != nil {
			return nil, err
		}
	}
	return append(previous, blocks...), nil
}
