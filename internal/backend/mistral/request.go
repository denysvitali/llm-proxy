package mistral

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// Mistral's request schema uses max_tokens/random_seed, has no developer
// role or stream_options, and its tool IDs follow the nine-alphanumeric
// convention. Keep these adaptations local to this provider.
func normalizeRequest(body []byte) ([]byte, error) {
	if !json.Valid(body) {
		return nil, fmt.Errorf("invalid JSON")
	}
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, fmt.Errorf("request must be an object")
	}
	changed := false
	for from, to := range map[string]string{"max_completion_tokens": "max_tokens", "seed": "random_seed"} {
		if value, ok := payload[from]; ok {
			if _, exists := payload[to]; !exists {
				payload[to] = value
			}
			delete(payload, from)
			changed = true
		}
	}
	if _, ok := payload["stream_options"]; ok {
		delete(payload, "stream_options")
		changed = true
	}
	if raw, exists := payload["messages"]; exists {
		messages, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("messages must be an array")
		}
		seen := make(map[string]string)
		for _, rawMessage := range messages {
			message, ok := rawMessage.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("message must be an object")
			}
			if message["role"] == "developer" {
				message["role"] = "system"
				changed = true
			}
			modified, err := normalizeToolIDs(message, seen)
			if err != nil {
				return nil, err
			}
			changed = changed || modified
		}
	}
	if !changed {
		return body, nil
	}
	return json.Marshal(payload)
}

func normalizeToolIDs(message map[string]any, seen map[string]string) (bool, error) {
	changed, err := rewriteID(message, "tool_call_id", seen)
	if err != nil {
		return false, err
	}
	calls, _ := message["tool_calls"].([]any)
	for _, raw := range calls {
		call, ok := raw.(map[string]any)
		if !ok {
			return false, fmt.Errorf("tool call must be an object")
		}
		modified, err := rewriteID(call, "id", seen)
		if err != nil {
			return false, err
		}
		changed = changed || modified
	}
	return changed, nil
}

func rewriteID(object map[string]any, key string, seen map[string]string) (bool, error) {
	id, ok := object[key].(string)
	if !ok || id == "" {
		return false, nil
	}
	normalized := toolID(id)
	if original, exists := seen[normalized]; exists && original != id {
		return false, fmt.Errorf("tool call IDs collide after Mistral normalization")
	}
	seen[normalized] = id
	if normalized == id {
		return false, nil
	}
	object[key] = normalized
	return true, nil
}

// Hash incompatible IDs rather than truncating common call_/toolu_ prefixes.
// Matching calls and results get the same ID across requests, retries and
// concurrent sessions, without any shared mutable state.
func toolID(id string) string {
	valid := len(id) == 9
	for _, char := range id {
		valid = valid && (char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9')
	}
	if valid {
		return id
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	digest := sha256.Sum256([]byte(id))
	value := binary.BigEndian.Uint64(digest[:8])
	var result [9]byte
	for i := range result {
		result[i] = alphabet[value%62]
		value /= 62
	}
	return string(result[:])
}
