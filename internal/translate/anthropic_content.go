package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// MarshalJSON emits only fields belonging to a block's wire type. In particular,
// an empty text or tool result is still an explicit value, not a missing field.
func (b anthropicBlock) MarshalJSON() ([]byte, error) {
	switch b.Type {
	case "text":
		return json.Marshal(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{b.Type, b.Text})
	case "image":
		return json.Marshal(struct {
			Type   string           `json:"type"`
			Source *anthropicSource `json:"source"`
		}{b.Type, b.Source})
	case "tool_use":
		return json.Marshal(struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}{b.Type, b.ID, b.Name, b.Input})
	case "tool_result":
		return json.Marshal(struct {
			Type      string          `json:"type"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error,omitempty"`
		}{b.Type, b.ToolUseID, b.Content, b.IsError})
	default:
		type block anthropicBlock
		return json.Marshal(block(b))
	}
}

func (s anthropicSource) MarshalJSON() ([]byte, error) {
	if s.Type == "url" {
		return json.Marshal(struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		}{s.Type, s.URL})
	}
	return json.Marshal(struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	}{s.Type, s.MediaType, s.Data})
}

// openAIContentToAnthropic preserves supported content parts without flattening
// text or deleting unsupported parts. Missing/null assistant content is handled
// by the caller only when a tool call supplies the actual message content.
func openAIContentToAnthropic(raw json.RawMessage, responses, allowImages bool) ([]anthropicBlock, error) {
	if text, ok := plainString(raw); ok {
		return []anthropicBlock{{Type: "text", Text: text}}, nil
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil || parts == nil {
		return nil, fmt.Errorf("content must be a string or an array of supported parts")
	}
	blocks := make([]anthropicBlock, 0, len(parts))
	for i, rawPart := range parts {
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(rawPart, &header); err != nil {
			return nil, fmt.Errorf("content part %d must be an object", i)
		}
		textPart := header.Type == "text" && !responses || responses && (header.Type == "input_text" || header.Type == "output_text")
		if textPart {
			var part struct {
				Type string  `json:"type"`
				Text *string `json:"text"`
			}
			if err := decodeContentPart(rawPart, &part); err != nil || part.Text == nil {
				return nil, fmt.Errorf("text part %d requires string text and no unsupported fields", i)
			}
			blocks = append(blocks, anthropicBlock{Type: "text", Text: *part.Text})
			continue
		}
		imagePart := header.Type == "image_url" && !responses || header.Type == "input_image" && responses
		if !imagePart || !allowImages {
			return nil, fmt.Errorf("unsupported content type %q on this Messages role", header.Type)
		}
		var url, detail string
		if responses {
			var part struct {
				Type     string `json:"type"`
				ImageURL string `json:"image_url"`
				Detail   string `json:"detail"`
			}
			if err := decodeContentPart(rawPart, &part); err != nil {
				return nil, fmt.Errorf("unsupported image part %d: %w", i, err)
			}
			url, detail = part.ImageURL, part.Detail
		} else {
			var part struct {
				Type     string `json:"type"`
				ImageURL struct {
					URL    string `json:"url"`
					Detail string `json:"detail"`
				} `json:"image_url"`
			}
			if err := decodeContentPart(rawPart, &part); err != nil {
				return nil, fmt.Errorf("unsupported image part %d: %w", i, err)
			}
			url, detail = part.ImageURL.URL, part.ImageURL.Detail
		}
		if detail != "" && detail != "auto" {
			return nil, fmt.Errorf("image detail %q cannot be represented on the Messages wire", detail)
		}
		block := imageBlock(url)
		if block == nil {
			return nil, fmt.Errorf("image part %d requires a supported image URL", i)
		}
		blocks = append(blocks, *block)
	}
	return blocks, nil
}

func decodeContentPart(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func openAIToolResultToAnthropic(raw json.RawMessage, responses bool) (json.RawMessage, error) {
	if _, ok := plainString(raw); ok {
		return raw, nil
	}
	blocks, err := openAIContentToAnthropic(raw, responses, true)
	if err != nil {
		return nil, err
	}
	return json.Marshal(blocks)
}
