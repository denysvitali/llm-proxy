package server

import (
	"encoding/json"
	"io"
	"net/http"
)

// maxErrorRelay caps how much of an upstream error body is forwarded.
const maxErrorRelay = 1 << 20

func readAll(r io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, limit+1))
}

// bodyTooLarge reports whether readAll hit the limit.
func bodyTooLarge(b []byte, limit int64) bool { return int64(len(b)) > limit }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type anthropicErrorBody struct {
	Type  string            `json:"type"`
	Error anthropicErrorObj `json:"error"`
}

type anthropicErrorObj struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type openAIErrorBody struct {
	Error openAIErrorObj `json:"error"`
}

type openAIErrorObj struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

// writeError answers with the error shape matching the request's API family,
// derived from the path (Anthropic under /v1/messages*, OpenAI elsewhere).
func writeError(w http.ResponseWriter, r *http.Request, status int, errType, message string) {
	if len(r.URL.Path) >= len("/v1/messages") && r.URL.Path[:len("/v1/messages")] == "/v1/messages" {
		writeAnthropicError(w, status, errType, message)
		return
	}
	writeOpenAIError(w, status, errType, message)
}

func writeAnthropicError(w http.ResponseWriter, status int, errType, message string) {
	writeJSON(w, status, anthropicErrorBody{
		Type:  "error",
		Error: anthropicErrorObj{Type: errType, Message: message},
	})
}

func writeOpenAIError(w http.ResponseWriter, status int, errType, message string) {
	writeJSON(w, status, openAIErrorBody{
		Error: openAIErrorObj{Message: message, Type: errType},
	})
}

// anthropicErrorType maps HTTP statuses to Anthropic error type strings.
func anthropicErrorType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestTimeout:
		return "timeout_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusUnauthorized, http.StatusForbidden:
		return "authentication_error"
	case 500, 502, 503, 504, http.StatusNotImplemented:
		return "api_error"
	case 529:
		return "overloaded_error"
	default:
		if status >= 500 {
			return "api_error"
		}
		return "invalid_request_error"
	}
}

// openAIErrorType maps HTTP statuses to OpenAI error type strings.
func openAIErrorType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "invalid_request_error"
	case http.StatusTooManyRequests:
		return "rate_limit_exceeded"
	default:
		if status >= 500 {
			return "api_error"
		}
		return "invalid_request_error"
	}
}
