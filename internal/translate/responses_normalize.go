package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// NormalizeResponsesRequest removes an explicitly null reasoning content field
// for compatible upstreams that require it to be absent. Instruction roles and
// positions, tool extensions, and missing tool outputs remain upstream-owned.
// Requests needing no normalization retain their original bytes.
func NormalizeResponsesRequest(body []byte) ([]byte, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, err
	}
	rawInput := bytes.TrimSpace(request["input"])
	if len(rawInput) == 0 || rawInput[0] != '[' {
		return body, nil
	}

	var items []json.RawMessage
	if err := json.Unmarshal(rawInput, &items); err != nil {
		return nil, fmt.Errorf("decode input: %w", err)
	}
	changed := false
	for index, raw := range items {
		var header struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			return nil, fmt.Errorf("decode input item %d: %w", index, err)
		}
		if header.Type != "reasoning" || !bytes.Equal(bytes.TrimSpace(header.Content), []byte("null")) {
			continue
		}
		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, fmt.Errorf("decode reasoning item %d: %w", index, err)
		}
		delete(item, "content")
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		items[index] = encoded
		changed = true
	}
	if !changed {
		return body, nil
	}
	request["input"] = mustMarshal(items)
	return json.Marshal(request)
}
