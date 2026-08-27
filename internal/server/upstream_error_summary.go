package server

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// maxErrorMessageLen bounds one upstream error summary kept in stats and the
// recent-errors ring: long enough to hold a provider's sentence, short enough
// that a hostile body cannot bloat memory or the dashboard.
const maxErrorMessageLen = 240

// wireErrorObj unions the "error" objects Anthropic and OpenAI-shaped
// services attach to failed responses. Code accepts either spelling since
// OpenAI sends strings while some gateways send numbers.
type wireErrorObj struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

// upstreamErrorSummary extracts a short human-readable message from an
// upstream error body, recognizing the JSON shapes providers actually send:
// Anthropic {"error":{...}}, OpenAI chat/responses {"error":{...}} (code may
// be a string or number), and flat gateways {"message": ...}. Anything else —
// HTML interstitials, plain text, unknown envelopes — falls back to a bounded
// snippet of raw text so operators still see *something* of what came back.
func upstreamErrorSummary(body []byte) string {
	body = normalizeBodyBytes(body)
	if len(body) == 0 {
		return ""
	}
	var probe struct {
		Error   *wireErrorObj `json:"error"`
		Message string        `json:"message"`
	}
	if err := json.Unmarshal(body, &probe); err == nil {
		switch {
		case probe.Error != nil && probe.Error.Message != "":
			msg := probe.Error.Message
			if code := errorCode(probe.Error.Code); code != "" {
				msg = code + ": " + msg
			}
			return truncateMessage(msg)
		case probe.Error != nil && probe.Error.Type != "":
			return truncateMessage(probe.Error.Type)
		case probe.Message != "":
			return truncateMessage(probe.Message)
		}
	}
	text := firstLine(string(body))
	return truncateMessage(text)
}

// errorCode renders an OpenAI-style code, which may be a string, an integer,
// or absent.
func errorCode(code any) string {
	switch c := code.(type) {
	case string:
		return c
	case float64:
		if c == float64(int64(c)) {
			return strconv.FormatInt(int64(c), 10)
		}
		return ""
	default:
		return ""
	}
}

// normalizeBodyBytes trims a UTF-8/UTF-16 byte-order mark and surrounding
// whitespace so gateways that prefix BOMs do not defeat JSON parsing.
func normalizeBodyBytes(b []byte) []byte {
	b = bytes.TrimSpace(b)
	for len(b) > 2 {
		switch {
		case b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
			b = b[3:]
		case b[0] == 0xFF && b[1] == 0xFE, b[0] == 0xFE && b[1] == 0xFF:
			b = b[2:]
		default:
			return b
		}
	}
	return b
}

// firstLine returns the first non-empty line of a plain-text body.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// truncateMessage clamps a message to maxErrorMessageLen runes after folding
// whitespace, marking cut messages with an ellipsis.
func truncateMessage(msg string) string {
	msg = strings.Join(strings.Fields(msg), " ")
	runes := []rune(msg)
	if len(runes) <= maxErrorMessageLen {
		return msg
	}
	return string(runes[:maxErrorMessageLen-1]) + "…"
}
