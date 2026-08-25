package server

import (
	"encoding/json"
	"errors"
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

	chain, ok := s.resolveChain(r.Context(), envelope.Model)
	if !ok {
		writeAnthropicError(w, http.StatusNotFound, "not_found_error",
			fmt.Sprintf("model %q has no available backend", envelope.Model))
		return
	}

	// Errored tool results arrive one turn after the call they answer;
	// counting them here is what makes the tool-call error rate meaningful.
	s.stats.recordInboundToolErrors(body, chain[0].backend.Name(), chain[0].model)

	log := s.log.WithFields(logrus.Fields{
		"request_id": RequestID(r.Context()),
		"model":      envelope.Model,
		"backend":    chain[0].backend.Name(),
	})

	env := translateEnv{
		kind:        backend.KindAnthropic,
		body:        body,
		clientModel: envelope.Model,
		streaming:   envelope.Stream,
	}
	s.exchangeChain(w, r, log, chain, anthropicDialect(func(w http.ResponseWriter, resp *backend.Response) {
		s.relayUpstreamError(w, log, resp)
	}), env, prepareAnthropicRequest)
}

// prepareAnthropicRequest encodes an Anthropic Messages body for one route's
// backend: verbatim (model rewritten) when the backend speaks Anthropic
// natively, translated to its wire format otherwise.
func prepareAnthropicRequest(rt route, wire resolvedWire, env *translateEnv) ([]byte, error) {
	if wire.native {
		rewritten, err := rewriteModel(env.body, rt.model)
		if err != nil {
			return nil, errors.New("request body is not valid JSON")
		}
		return rewritten, nil
	}
	request, err := translate.ParseRequest(env.body)
	if err != nil {
		return nil, fmt.Errorf("invalid Anthropic Messages request: %v", err)
	}
	env.thinking = wantsThinking(request)
	payload, err := wire.path.encode(*env)
	if err != nil {
		return nil, fmt.Errorf("translating request for backend %s failed: %v", rt.backend.Name(), err)
	}
	return payload, nil
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
