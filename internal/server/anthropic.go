package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/translate"
)

// handleMessages serves POST /v1/messages (Anthropic Messages API). Requests
// are forwarded natively when the routed backend speaks Anthropic, otherwise
// they are translated to the best wire format the backend supports and the
// response is translated back so clients always see pure Anthropic.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readBody(w, r)
	if !ok {
		return
	}

	var envelope struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "request body must be a JSON object")
		return
	}
	if strings.TrimSpace(envelope.Model) == "" {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}

	rt, ok := s.resolve(r.Context(), envelope.Model)
	if !ok {
		writeAnthropicError(w, http.StatusNotFound, "not_found_error",
			fmt.Sprintf("model %q has no available backend", envelope.Model))
		return
	}

	// Errored tool results arrive one turn after the call they answer;
	// counting them here is what makes the tool-call error rate meaningful.
	s.stats.recordInboundToolErrors(body, rt.backend.Name(), rt.model)

	log := s.log.WithFields(logrus.Fields{
		"request_id": RequestID(r.Context()),
		"model":      envelope.Model,
		"backend":    rt.backend.Name(),
	})

	env := translateEnv{
		kind:        backend.KindAnthropic,
		body:        body,
		model:       rt.model,
		clientModel: envelope.Model,
		streaming:   envelope.Stream,
	}
	wire, servable := resolveWire(env.kind, rt.backend, rt.model)
	if !servable {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "no translation path for backend")
		return
	}
	var payload []byte
	if wire.native {
		rewritten, err := rewriteModel(body, rt.model)
		if err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error",
				"request body is not valid JSON")
			return
		}
		payload = rewritten
	} else {
		request, err := translate.ParseRequest(body)
		if err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("invalid Anthropic Messages request: %v", err))
			return
		}
		env.thinking = wantsThinking(request)
		payload, err = wire.path.encode(env)
		if err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("translating request for backend %s failed: %v", rt.backend.Name(), err))
			return
		}
	}
	s.exchange(w, r, log, rt, anthropicDialect(func(w http.ResponseWriter, resp *backend.Response) {
		s.relayUpstreamError(w, log, resp)
	}), wire, payload, r.Header.Clone(), env)
}

// wantsThinking reports whether a successfully parsed Anthropic request asked
// for extended thinking; nil requests never do.
func wantsThinking(request *translate.Request) bool {
	if request == nil {
		return false
	}
	return request.WantsThinking()
}

// relayUpstreamError forwards a non-2xx upstream response to the client: the
// upstream body verbatim (capped at maxErrorRelay) when there is one, an
// Anthropic-shaped error derived from the status otherwise.
func (s *Server) relayUpstreamError(w http.ResponseWriter, log logrus.FieldLogger, resp *backend.Response) {
	data, _ := readAll(resp.Body, maxErrorRelay)
	log.WithFields(logrus.Fields{
		"upstream_status": resp.Status,
		"upstream_bytes":  len(data),
	}).Debug("upstream returned an error response")

	if len(data) > 0 {
		if contentType := resp.Header.Get("Content-Type"); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(resp.Status)
		_, _ = w.Write(data)
		return
	}
	writeAnthropicError(w, resp.Status, anthropicErrorType(resp.Status),
		fmt.Sprintf("upstream request failed with status %d", resp.Status))
}

// handleCountTokens serves POST /v1/messages/count_tokens with a cheap
// characters-over-three estimate; real tokenization is left to providers.
func (s *Server) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readBody(w, r)
	if !ok {
		return
	}
	tokens := (len(body) + 2) / 3
	if tokens < 1 {
		tokens = 1
	}
	w.Header().Add("Warning", `299 llm-proxy "token count is a conservative estimate"`)
	writeJSON(w, http.StatusOK, map[string]int{"input_tokens": tokens})
}
