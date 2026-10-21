package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type instructionBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Anthropic has one system instruction tier, not OpenAI's system/developer
// hierarchy, so leading developer messages fold into the top-level system
// like system messages do (Codex sends its permissions and environment as
// developer messages on every turn). appendAnthropicInstruction handles those
// leading prompt messages; later ones keep their position as system messages
// inside messages (anthropicSystemMessage), as Claude Code itself sends them.
func appendAnthropicInstruction(current any, content json.RawMessage, textType string) (any, error) {
	var blocks []instructionBlock
	if text, ok := plainString(content); ok {
		if current == nil {
			return text, nil
		}
		blocks = []instructionBlock{{Type: "text", Text: text}}
	} else {
		var err error
		if blocks, err = instructionBlocks(content, textType); err != nil {
			return nil, err
		}
	}
	// Decode existing content into the same local shape without joining text or
	// adding separators. Empty text is retained as an explicit text block.
	var previous []instructionBlock
	if current != nil {
		if text, ok := current.(string); ok {
			previous = []instructionBlock{{Type: "text", Text: text}}
		} else if err := json.Unmarshal(mustMarshal(current), &previous); err != nil {
			return nil, err
		}
	}
	return append(previous, blocks...), nil
}

// anthropicSystemMessage renders a prompt-role message that follows
// conversation input as a positioned Messages system message.
func anthropicSystemMessage(content json.RawMessage, textType string) (AnthropicMessage, error) {
	if text, ok := plainString(content); ok {
		return AnthropicMessage{Role: "system", Content: mustMarshal(text)}, nil
	}
	blocks, err := instructionBlocks(content, textType)
	if err != nil {
		return AnthropicMessage{}, err
	}
	return AnthropicMessage{Role: "system", Content: mustMarshal(blocks)}, nil
}

func instructionBlocks(content json.RawMessage, textType string) ([]instructionBlock, error) {
	var parts []struct {
		Type string  `json:"type"`
		Text *string `json:"text"`
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parts); err != nil || len(parts) == 0 {
		return nil, fmt.Errorf("system content must be text or a non-empty array of text parts without unsupported fields")
	}
	blocks := make([]instructionBlock, 0, len(parts))
	for _, part := range parts {
		if part.Type != textType {
			return nil, fmt.Errorf("unsupported system content type %q", part.Type)
		}
		if part.Text == nil {
			return nil, fmt.Errorf("system text parts require a string text field")
		}
		blocks = append(blocks, instructionBlock{Type: "text", Text: *part.Text})
	}
	return blocks, nil
}
