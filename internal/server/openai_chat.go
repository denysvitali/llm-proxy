package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/sirupsen/logrus"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// openAIEnvelope is the minimal request envelope shared by both OpenAI
// endpoints; everything else in the body is forwarded untouched.
type openAIEnvelope struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

func decodeOpenAIEnvelope(body []byte) (openAIEnvelope, error) {
	var env openAIEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return openAIEnvelope{}, err
	}
	return env, nil
}

// writeOpenAIModelNotFound answers the canonical OpenAI 404 for a model that
// no configured backend can serve.
func writeOpenAIModelNotFound(w http.ResponseWriter, model string) {
	writeJSON(w, http.StatusNotFound, openAIErrorBody{
		Error: openAIErrorObj{
			Message: fmt.Sprintf("model %q has no available backend", model),
			Type:    openAIErrorType(http.StatusNotFound),
			Code:    "model_not_found",
		},
	})
}

// copyUpstreamRequestID forwards the upstream x-request-id / request-id
// headers onto the client response when present.
func copyUpstreamRequestID(dst, src http.Header) {
	for _, name := range []string{"X-Request-Id", "Request-Id"} {
		if v := src.Get(name); v != "" {
			dst.Set(name, v)
		}
	}
}

// relayOpenAIUpstreamError answers a non-2xx upstream response: forward up to
// maxErrorRelay bytes of the upstream body (with its Content-Type) when there
// is one, otherwise synthesize an OpenAI error with the mapped type.
func relayOpenAIUpstreamError(w http.ResponseWriter, resp *backend.Response) {
	defer func() { _ = resp.Body.Close() }()
	data, _ := readAll(resp.Body, maxErrorRelay)
	copyUpstreamRequestID(w.Header(), resp.Header)
	if len(bytes.TrimSpace(data)) > 0 {
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(resp.Status)
		_, _ = w.Write(data)
		return
	}
	writeOpenAIError(w, resp.Status, openAIErrorType(resp.Status), "upstream request failed")
}

// handleChatCompletions serves POST /v1/chat/completions (OpenAI API). The
// translation matrix picks how the request reaches its backend — natively
// when the backend speaks chat, translated otherwise — and exchange relays
// the response back in chat-completions shape either way.
func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readBody(w, r)
	if !ok {
		return
	}
	envelope, err := decodeOpenAIEnvelope(body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			"request body is not valid JSON")
		return
	}
	rt, found := s.resolveChain(r.Context(), envelope.Model)
	if !found {
		writeOpenAIModelNotFound(w, envelope.Model)
		return
	}

	log := s.log.WithFields(logrus.Fields{
		"request_id": RequestID(r.Context()),
		"model":      envelope.Model,
		"backend":    rt[0].backend.Name(),
	})
	env := translateEnv{
		kind:        backend.KindOpenAIChat,
		body:        body,
		clientModel: envelope.Model,
		streaming:   envelope.Stream,
	}
	s.exchangeChain(w, r, log, rt, openAIDialect(), env, prepareChatRequest)
}

// prepareChatRequest encodes a chat-completions body for one route's backend:
// verbatim (model rewritten) when the backend speaks chat natively,
// translated otherwise.
func prepareChatRequest(rt route, wire resolvedWire, env *translateEnv) ([]byte, error) {
	if wire.native {
		rewritten, err := rewriteModel(env.body, rt.model)
		if err != nil {
			return nil, errors.New("request body is not valid JSON")
		}
		return rewritten, nil
	}
	translated, err := wire.path.encode(*env)
	if err != nil {
		return nil, fmt.Errorf("cannot translate request for backend %s: %v", rt.backend.Name(), err)
	}
	return translated, nil
}
