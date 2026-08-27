package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// NormalizeResponsesRequest adapts valid Responses input for strict compatible
// upstreams without changing the request's meaning. Prompt-role messages are
// folded into instructions, Codex-internal additional_tools items are dropped,
// and a null reasoning content field is removed while opaque provider state
// and unknown fields remain byte-for-byte values.
func NormalizeResponsesRequest(body []byte) ([]byte, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, err
	}
	rawInput := bytes.TrimSpace(request["input"])
	if len(rawInput) == 0 || rawInput[0] != '[' {
		return body, nil
	}

	var items []map[string]json.RawMessage
	if err := json.Unmarshal(rawInput, &items); err != nil {
		return nil, fmt.Errorf("decode input: %w", err)
	}
	changed := false
	var promptSections []string
	normalized := make([]map[string]json.RawMessage, 0, len(items))
	for index, item := range items {
		var header responsesInputItem
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("encode input item %d: %w", index, err)
		}
		if err := json.Unmarshal(encoded, &header); err != nil {
			return nil, fmt.Errorf("decode input item %d: %w", index, err)
		}
		if header.Type == "additional_tools" {
			changed = true
			continue
		}
		if header.Type == "message" && isPromptRole(header.Role) {
			if text := itemText(header); text != "" {
				promptSections = append(promptSections, text)
			}
			changed = true
			continue
		}
		if header.Type == "reasoning" && bytes.Equal(bytes.TrimSpace(item["content"]), []byte("null")) {
			delete(item, "content")
			changed = true
		}
		normalized = append(normalized, item)
	}
	if !changed {
		return body, nil
	}

	if len(promptSections) > 0 {
		instructions, err := responseInstructions(request["instructions"])
		if err != nil {
			return nil, err
		}
		for _, section := range promptSections {
			instructions = appendPrompt(instructions, section)
		}
		request["instructions"] = mustMarshal(instructions)
	}
	request["input"] = mustMarshal(normalized)
	return json.Marshal(request)
}

func responseInstructions(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}
	var instructions string
	if err := json.Unmarshal(raw, &instructions); err != nil {
		return "", fmt.Errorf("decode instructions: %w", err)
	}
	return instructions, nil
}
