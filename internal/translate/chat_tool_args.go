package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Mistral can return function arguments as either a JSON object or an
// already-encoded string. Keep string fragments intact for streaming.
func decodeChatToolArguments(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	switch raw[0] {
	case '"':
		var arguments string
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return "", err
		}
		return arguments, nil
	case '{':
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			return "", err
		}
		return compact.String(), nil
	default:
		return "", fmt.Errorf("unsupported chat tool arguments type %q", raw[0])
	}
}

func (call *openAIToolCall) UnmarshalJSON(data []byte) error {
	type alias openAIToolCall
	var fields struct {
		*alias
		Function struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"function"`
	}
	*call = openAIToolCall{}
	fields.alias = (*alias)(call)
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	arguments, err := decodeChatToolArguments(fields.Function.Arguments)
	if err != nil {
		return err
	}
	call.Function.Name = fields.Function.Name
	call.Function.Arguments = arguments
	return nil
}

func (function *chatFuncOut) UnmarshalJSON(data []byte) error {
	type alias chatFuncOut
	var fields struct {
		*alias
		Arguments json.RawMessage `json:"arguments"`
	}
	*function = chatFuncOut{}
	fields.alias = (*alias)(function)
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	arguments, err := decodeChatToolArguments(fields.Arguments)
	if err != nil {
		return err
	}
	function.Arguments = arguments
	return nil
}
