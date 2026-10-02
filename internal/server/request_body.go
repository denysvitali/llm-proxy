package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// rewriteModel replaces only the top-level "model" string value of a JSON
// object, preserving every other byte exactly as the client sent it: key
// order, whitespace, indentation, string escapes and nested values are all
// left untouched.
func rewriteModel(body []byte, model string) ([]byte, error) {
	// Validate that the body is a JSON object before mutating anything.
	var probe interface{}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, err
	}
	if _, ok := probe.(map[string]interface{}); !ok {
		return nil, fmt.Errorf("body is not a JSON object")
	}

	quoted, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	return spliceModelValue(body, quoted)
}

// spliceModelValue walks body byte-by-byte, tracking JSON depth, to find the
// top-level "model" key and replace its string value with quoted. Everything
// outside that value is copied verbatim.
func spliceModelValue(body, quoted []byte) ([]byte, error) {
	n := len(body)
	depth := 0
	for i := 0; i < n; {
		switch body[i] {
		case '{':
			depth++
			i++
		case '}':
			depth--
			i++
		case '[':
			depth++
			i++
		case ']':
			depth--
			i++
		case '"':
			key, end, err := parseJSONString(body, i)
			if err != nil {
				return nil, err
			}
			// A string immediately followed by ':' is an object key.
			j := end
			for j < n && isJSONSpace(body[j]) {
				j++
			}
			if j < n && body[j] == ':' && depth == 1 && key == "model" {
				// Skip whitespace after ':' to reach the value.
				k := j + 1
				for k < n && isJSONSpace(body[k]) {
					k++
				}
				if k >= n || body[k] != '"' {
					return nil, fmt.Errorf(`"model" value is not a string`)
				}
				_, vend, err := parseJSONString(body, k)
				if err != nil {
					return nil, err
				}
				out := make([]byte, 0, len(body)-(vend-k)+len(quoted))
				out = append(out, body[:k]...)
				out = append(out, quoted...)
				out = append(out, body[vend:]...)
				return out, nil
			}
			i = end
		default:
			// Number, literal or whitespace: the validation pass already
			// guaranteed these are well-formed, so a byte-wise walk is safe.
			i++
		}
	}
	return nil, fmt.Errorf(`no top-level "model" key found`)
}

// parseJSONString parses the JSON string beginning at body[i] (which must be
// '"') and returns its decoded contents together with the exclusive end
// offset. It handles all JSON escapes, including \uXXXX surrogate pairs.
func parseJSONString(body []byte, i int) (string, int, error) {
	if i >= len(body) || body[i] != '"' {
		return "", 0, fmt.Errorf("expected string")
	}
	i++ // skip opening quote
	var sb strings.Builder
	for i < len(body) {
		c := body[i]
		switch c {
		case '"':
			return sb.String(), i + 1, nil
		case '\\':
			i++
			if i >= len(body) {
				return "", 0, fmt.Errorf("unterminated string escape")
			}
			switch body[i] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				sb.WriteByte(body[i])
			case 'u':
				if i+4 >= len(body) {
					return "", 0, fmt.Errorf("invalid unicode escape")
				}
				r := hexToRune(body[i+1 : i+5])
				if r < 0 {
					return "", 0, fmt.Errorf("invalid unicode escape")
				}
				i += 4
				// Decode an optional low surrogate to form a supplementary
				// code point (\uXXXX\uXXXX pair).
				if r >= 0xD800 && r <= 0xDBFF && i+6 < len(body) && body[i+1] == '\\' && body[i+2] == 'u' {
					if lo := hexToRune(body[i+3 : i+7]); lo >= 0xDC00 && lo <= 0xDFFF {
						r = 0x10000 + ((r - 0xD800) << 10) + (lo - 0xDC00)
						i += 6
					}
				}
				sb.WriteRune(r)
			default:
				return "", 0, fmt.Errorf("invalid escape")
			}
			i++
		default:
			sb.WriteByte(c)
			i++
		}
	}
	return "", 0, fmt.Errorf("unterminated string")
}

// hexToRune converts exactly four ASCII hex digits to a rune, or returns -1.
func hexToRune(h []byte) rune {
	if len(h) != 4 {
		return -1
	}
	var r rune
	for _, c := range h {
		r *= 16
		switch {
		case '0' <= c && c <= '9':
			r += rune(c - '0')
		case 'a' <= c && c <= 'f':
			r += rune(c - 'a' + 10)
		case 'A' <= c && c <= 'F':
			r += rune(c - 'A' + 10)
		default:
			return -1
		}
	}
	return r
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
