package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// UnmarshalJSON accepts both ordinary chat content and Mistral's typed
// thinking/text chunks. The same message shape is used by streamed deltas.
func (m *openAIMessageOut) UnmarshalJSON(data []byte) error {
	type alias openAIMessageOut
	var fields struct {
		*alias
		Content          json.RawMessage `json:"content"`
		ReasoningDetails json.RawMessage `json:"reasoning_details"`
	}
	*m = openAIMessageOut{}
	fields.alias = (*alias)(m)
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	content, reasoning, present, err := decodeChatContent(fields.Content)
	if err != nil {
		return err
	}
	m.Content = content
	m.ContentPresent = present
	m.ReasoningContent += reasoning
	if m.ReasoningContent == "" {
		m.ReasoningContent = unsignedReasoningDetails(fields.ReasoningDetails)
	}
	return nil
}

func (m *chatMessageOut) UnmarshalJSON(data []byte) error {
	type alias chatMessageOut
	var fields struct {
		*alias
		Content          json.RawMessage `json:"content"`
		ReasoningDetails json.RawMessage `json:"reasoning_details"`
	}
	*m = chatMessageOut{}
	fields.alias = (*alias)(m)
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	content, reasoning, present, err := decodeChatContent(fields.Content)
	if err != nil {
		return err
	}
	m.Content = content
	m.ContentPresent = present
	m.ReasoningContent += reasoning
	if m.ReasoningContent == "" {
		m.ReasoningContent = unsignedReasoningDetails(fields.ReasoningDetails)
	}
	return nil
}

func (d *chatDeltaOut) UnmarshalJSON(data []byte) error {
	type alias chatDeltaOut
	var fields struct {
		*alias
		Content json.RawMessage `json:"content"`
	}
	*d = chatDeltaOut{}
	fields.alias = (*alias)(d)
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	content, reasoning, present, err := decodeChatContent(fields.Content)
	if err != nil {
		return err
	}
	if present {
		d.Content = &content
	}
	d.ReasoningContent += reasoning
	return nil
}

// reasoningDetail is one OpenRouter-style reasoning_details entry, as sent by
// gateways such as Open Design in place of reasoning_content.
type reasoningDetail struct {
	Type    string `json:"type"`
	Summary string `json:"summary"`
	Text    string `json:"text"`
}

// plainReasoningDetails reports whether raw holds only plain-text
// reasoning_details entries (reasoning.summary / reasoning.text), which carry
// the same unsigned trace as reasoning_content. Encrypted or unknown entries
// make the details opaque state that cannot be translated.
func plainReasoningDetails(raw json.RawMessage) ([]reasoningDetail, bool) {
	var details []reasoningDetail
	if json.Unmarshal(raw, &details) != nil {
		return nil, false
	}
	for _, detail := range details {
		if detail.Type != "reasoning.summary" && detail.Type != "reasoning.text" {
			return nil, false
		}
	}
	return details, true
}

// unsignedReasoningDetails joins plain-text reasoning_details into the
// reasoning_content they stand for; anything else yields "" (the validator
// rejects it before decoding).
func unsignedReasoningDetails(raw json.RawMessage) string {
	details, ok := plainReasoningDetails(raw)
	if !ok {
		return ""
	}
	var reasoning string
	for _, detail := range details {
		reasoning += detail.Summary + detail.Text
	}
	return reasoning
}

// decodeChatContent distinguishes omitted/null content from an explicit empty
// string, which matters for streamed chat deltas using a *string field.
func decodeChatContent(raw json.RawMessage) (content, reasoning string, present bool, err error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", "", false, nil
	}
	switch raw[0] {
	case '"':
		err = json.Unmarshal(raw, &content)
		return content, "", true, err
	case '[':
		var chunks []json.RawMessage
		if err := json.Unmarshal(raw, &chunks); err != nil {
			return "", "", false, err
		}
		for _, rawChunk := range chunks {
			var chunk struct {
				Type     string            `json:"type"`
				Text     *string           `json:"text"`
				Thinking []json.RawMessage `json:"thinking"`
			}
			if err := decodeContentPart(rawChunk, &chunk); err != nil {
				return "", "", false, fmt.Errorf("unsupported chat content fields: %w", err)
			}
			switch chunk.Type {
			case "text":
				if chunk.Text == nil || chunk.Thinking != nil {
					return "", "", false, fmt.Errorf("chat text chunk requires string text")
				}
				content += *chunk.Text
			case "thinking":
				if chunk.Thinking == nil || chunk.Text != nil {
					return "", "", false, fmt.Errorf("chat thinking chunk requires a thinking array")
				}
				for _, rawThought := range chunk.Thinking {
					var thought struct {
						Type string  `json:"type"`
						Text *string `json:"text"`
					}
					if err := decodeContentPart(rawThought, &thought); err != nil || thought.Type != "text" || thought.Text == nil {
						return "", "", false, fmt.Errorf("unsupported or malformed chat thinking content")
					}
					reasoning += *thought.Text
				}
			default:
				return "", "", false, fmt.Errorf("unsupported chat content chunk %q", chunk.Type)
			}
		}
		return content, reasoning, true, nil
	default:
		return "", "", false, fmt.Errorf("unsupported chat content type %q", raw[0])
	}
}
