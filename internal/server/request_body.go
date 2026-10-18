package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// requestEnvelope validates the routing fields and records the model's byte
// range. Nested values are skipped without allocating decoded history trees.
func requestEnvelope(body []byte) (openAIEnvelope, int, int, error) {
	var env openAIEnvelope
	if !json.Valid(body) || len(bytes.TrimSpace(body)) == 0 || bytes.TrimSpace(body)[0] != '{' {
		return env, 0, 0, fmt.Errorf("request body must be a JSON object")
	}
	start, end := 0, 0
	seenStream := false
	i := skipJSONSpace(body, 0) + 1
	for {
		i = skipJSONSpace(body, i)
		if body[i] == '}' {
			break
		}
		keyEnd := skipJSONString(body, i)
		var key string
		_ = json.Unmarshal(body[i:keyEnd], &key)
		i = skipJSONSpace(body, keyEnd) + 1 // colon
		i = skipJSONSpace(body, i)
		valueEnd := skipJSONValue(body, i)
		switch {
		case strings.EqualFold(key, "model"):
			if key != "model" || start != 0 {
				return env, 0, 0, fmt.Errorf("model must appear once with exact lowercase spelling")
			}
			if body[i] != '"' || json.Unmarshal(body[i:valueEnd], &env.Model) != nil {
				return env, 0, 0, fmt.Errorf("model must be a string")
			}
			start, end = i, valueEnd
		case strings.EqualFold(key, "stream"):
			if key != "stream" || seenStream {
				return env, 0, 0, fmt.Errorf("stream must appear once with exact lowercase spelling")
			}
			if !bytes.Equal(body[i:valueEnd], []byte("true")) && !bytes.Equal(body[i:valueEnd], []byte("false")) {
				return env, 0, 0, fmt.Errorf("stream must be a boolean")
			}
			env.Stream = body[i] == 't'
			seenStream = true
		}
		i = skipJSONSpace(body, valueEnd)
		if body[i] == '}' {
			break
		}
		i++ // comma
	}
	if strings.TrimSpace(env.Model) == "" {
		return env, 0, 0, fmt.Errorf("model is required")
	}
	return env, start, end, nil
}

// rewriteModel preserves every byte outside the validated top-level model.
func rewriteModel(body []byte, model string) ([]byte, error) {
	_, start, end, err := requestEnvelope(body)
	if err != nil {
		return nil, err
	}
	quoted, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(body)-(end-start)+len(quoted))
	out = append(out, body[:start]...)
	out = append(out, quoted...)
	out = append(out, body[end:]...)
	return out, nil
}

// These skippers operate only on a document already checked by json.Valid.
func skipJSONString(body []byte, i int) int {
	for i++; i < len(body); i++ {
		switch body[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(body)
}

func skipJSONValue(body []byte, i int) int {
	depth := 0
	for i < len(body) {
		switch body[i] {
		case '"':
			i = skipJSONString(body, i)
			if depth == 0 {
				return i
			}
			continue
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				return i
			}
			depth--
			if depth == 0 {
				return i + 1
			}
		case ',', ' ', '\t', '\r', '\n':
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return i
}

func skipJSONSpace(body []byte, i int) int {
	for i < len(body) && isJSONSpace(body[i]) {
		i++
	}
	return i
}

func isJSONSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// readBody enforces the configured maximum request body size.
func (s *Server) readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := readAll(r.Body, s.cfg.Server.MaxBodyBytes)
	if err != nil || bodyTooLarge(body, s.cfg.Server.MaxBodyBytes) {
		writeError(w, r, http.StatusRequestEntityTooLarge, "invalid_request_error", "request body too large")
		return nil, false
	}
	return body, true
}
