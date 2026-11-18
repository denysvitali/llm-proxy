package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// Client dialects own the response shapes understood by each client API.
// The exchange layer supplies failures; these functions only format and
// deliver them, without deciding retry or fallback policy.

// clientDialect holds the inbound-API-specific answers: how errors are
// phrased when no upstream response exists, how a non-2xx upstream response
// is passed on (verbatim on a native wire, re-enveloped by errorBody on a
// translated one), and how a mid-stream break is surfaced as the protocol's
// in-band failure once content has already been forwarded.
type clientDialect struct {
	writeError    func(w http.ResponseWriter, status int, errType, message string)
	relayError    func(w http.ResponseWriter, resp *backend.Response)
	errorBody     func(status int, message string) any
	surfaceStream func(w http.ResponseWriter, message string)
}

func anthropicDialect() clientDialect {
	return clientDialect{
		writeError: writeAnthropicError,
		relayError: relayAnthropicUpstreamError,
		errorBody: func(status int, message string) any {
			return anthropicErrorBody{Type: "error", Error: anthropicErrorObj{Type: anthropicErrorType(status), Message: message}}
		},
		surfaceStream: emitAnthropicStreamFailure,
	}
}

func openAIErrorEnvelope(status int, message string) any {
	return openAIErrorBody{Error: openAIErrorObj{Message: message, Type: openAIErrorType(status)}}
}

// openAIDialect serves chat-completions clients; openAIResponsesDialect
// serves Responses clients. They share error shapes but differ in the
// in-band stream-failure event their SDKs understand.
func openAIDialect() clientDialect {
	return clientDialect{
		writeError:    writeOpenAIError,
		relayError:    relayOpenAIUpstreamError,
		errorBody:     openAIErrorEnvelope,
		surfaceStream: emitChatStreamFailure,
	}
}

func openAIResponsesDialect() clientDialect {
	return clientDialect{
		writeError:    writeOpenAIError,
		relayError:    relayOpenAIUpstreamError,
		errorBody:     openAIErrorEnvelope,
		surfaceStream: emitResponsesStreamFailure,
	}
}

// emitAnthropicStreamFailure ends an already-started Anthropic SSE stream
// with the protocol's error event, so the client sees a real failure it can
// replay instead of a truncation that looks finished.
func emitAnthropicStreamFailure(w http.ResponseWriter, message string) {
	payload, err := json.Marshal(map[string]any{
		"type":  "error",
		"error": map[string]any{"type": "api_error", "message": message},
	})
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", payload)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// emitChatStreamFailure ends an already-started chat-completions stream with
// the error-chunk shape OpenAI clients understand.
func emitChatStreamFailure(w http.ResponseWriter, message string) {
	payload, err := json.Marshal(map[string]any{
		"error": map[string]any{"message": message, "type": "api_error"},
	})
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// emitResponsesStreamFailure ends an already-started Responses stream with a
// response.failed event carrying the failure reason.
func emitResponsesStreamFailure(w http.ResponseWriter, message string) {
	payload, err := json.Marshal(map[string]any{
		"type": "response.failed",
		"response": map[string]any{
			"id":         "resp_llm-proxy",
			"object":     "response",
			"created_at": time.Now().Unix(),
			"status":     "failed",
			"error":      map[string]any{"code": "api_error", "message": message},
		},
	})
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: response.failed\ndata: %s\n\n", payload)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// relayTranslatedUpstreamError answers a non-2xx upstream reply that came
// back on a different wire than the client speaks: the upstream's own message
// is kept, but re-enveloped in the client's error shape so its SDK can parse
// it. It returns the bytes written, for the response shape trace.
func relayTranslatedUpstreamError(w http.ResponseWriter, dialect clientDialect, resp *backend.Response, upstreamBody []byte) []byte {
	message := upstreamErrorText(upstreamBody)
	if message == "" {
		message = fmt.Sprintf("upstream request failed with status %d", resp.Status)
	}
	data, err := json.Marshal(dialect.errorBody(resp.Status, message))
	if err != nil {
		return nil
	}
	copyUpstreamRetryAfter(w.Header(), resp.Header)
	copyUpstreamRequestID(w.Header(), resp.Header)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.Status)
	_, _ = w.Write(data)
	return data
}

// kindName labels an inbound API in client-facing error messages.
func kindName(kind backend.Kind) string {
	switch kind {
	case backend.KindAnthropic:
		return "Anthropic Messages"
	case backend.KindOpenAIChat:
		return "Chat Completions"
	case backend.KindOpenAIResponses:
		return "Responses"
	default:
		return string(kind)
	}
}
