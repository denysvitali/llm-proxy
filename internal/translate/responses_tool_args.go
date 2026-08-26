package translate

import (
	"bytes"
	"encoding/json"
	"strings"
)

// NormalizeResponsesToolArguments converts integer-valued JSON floats inside
// Responses function-call argument strings (for example, 63889.0) to JSON
// integers. Some compatible model gateways use a floating-point encoder for
// all numbers, but Codex deserializes tool arguments against the declared MCP
// schema and rejects 63889.0 when the schema says i32.
//
// Only the Responses argument fields are inspected. This keeps ordinary text,
// metadata, and genuinely fractional tool arguments unchanged.
func NormalizeResponsesToolArguments(data []byte) []byte {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return data
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil || !normalizeResponsesToolArgumentValue(value, "") {
		return data
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return data
	}
	return encoded
}

func normalizeResponsesToolArgumentValue(value any, eventType string) bool {
	switch typed := value.(type) {
	case map[string]any:
		if nextType, ok := typed["type"].(string); ok {
			eventType = nextType
		}
		changed := false
		for key, child := range typed {
			if text, ok := child.(string); ok && normalizeResponsesToolArgumentField(key, eventType) {
				rewritten := coerceWholeNumberJSONTokens(text)
				if rewritten != text {
					typed[key] = rewritten
					changed = true
				}
				continue
			}
			if normalizeResponsesToolArgumentValue(child, eventType) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for _, child := range typed {
			if normalizeResponsesToolArgumentValue(child, eventType) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func normalizeResponsesToolArgumentField(key, eventType string) bool {
	if key == "arguments" {
		return true
	}
	return key == "delta" && strings.Contains(eventType, "function_call_arguments")
}

func coerceWholeNumberJSONTokens(value string) string {
	if !strings.Contains(value, ".") {
		return value
	}
	var out strings.Builder
	out.Grow(len(value))
	changed := false
	for i := 0; i < len(value); {
		if value[i] == '"' {
			start := i
			i++
			for i < len(value) {
				if value[i] == '\\' {
					i += 2
					continue
				}
				i++
				if value[i-1] == '"' {
					break
				}
			}
			out.WriteString(value[start:i])
			continue
		}
		start := i
		if value[i] == '-' || (value[i] >= '0' && value[i] <= '9') {
			if value[i] == '-' {
				i++
			}
			for i < len(value) && value[i] >= '0' && value[i] <= '9' {
				i++
			}
			if i < len(value) && value[i] == '.' {
				i++
				for i < len(value) && value[i] >= '0' && value[i] <= '9' {
					i++
				}
			}
			if i < len(value) && (value[i] == 'e' || value[i] == 'E') {
				i++
				if i < len(value) && (value[i] == '+' || value[i] == '-') {
					i++
				}
				for i < len(value) && value[i] >= '0' && value[i] <= '9' {
					i++
				}
			}
			token := value[start:i]
			rewritten := token
			if dot := strings.IndexByte(token, '.'); dot > 0 && !strings.ContainsAny(token, "eE") && strings.Trim(token[dot+1:], "0") == "" {
				rewritten = token[:dot]
			}
			out.WriteString(rewritten)
			changed = changed || rewritten != token
			continue
		}
		out.WriteByte(value[i])
		i++
	}
	if !changed {
		return value
	}
	return out.String()
}
