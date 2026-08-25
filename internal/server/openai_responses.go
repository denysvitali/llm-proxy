package server

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/sirupsen/logrus"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// handleResponses serves POST /v1/responses (OpenAI Responses API, e.g.
// Codex). The translation matrix picks how the request reaches its backend —
// natively when the backend speaks Responses, translated to chat or Anthropic
// Messages otherwise — and exchange relays the response back in Responses
// shape either way.
func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
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
	chain, found := s.resolveChain(r.Context(), envelope.Model)
	if !found {
		writeOpenAIModelNotFound(w, envelope.Model)
		return
	}

	log := s.log.WithFields(logrus.Fields{
		"request_id": RequestID(r.Context()),
		"model":      envelope.Model,
		"backend":    chain[0].backend.Name(),
	})
	env := translateEnv{
		kind:        backend.KindOpenAIResponses,
		body:        body,
		clientModel: envelope.Model,
		streaming:   envelope.Stream,
	}
	s.exchangeChain(w, r, log, chain, openAIResponsesDialect(), env, prepareResponsesRequest)
}

// prepareResponsesRequest encodes a Responses body for one route's backend:
// verbatim (model rewritten) when the backend speaks Responses natively,
// translated otherwise.
func prepareResponsesRequest(rt route, wire resolvedWire, env *translateEnv) ([]byte, error) {
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
